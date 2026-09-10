package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/renewals"
)

func nativeRenewalFixture(t *testing.T, db *DB, s *Store, shop string, day time.Time, term int) (string, []RenewalTask) {
	t.Helper()
	a := createSeasonalTestAgreement(t, db, s, shop, []string{}, 5, 3)
	if _, err := db.Pool.Exec(context.Background(), `UPDATE dbo.agreements SET anniversary_date=CONVERT(date,@p2),plan_snapshot_term_months=@p3 WHERE id=CONVERT(uniqueidentifier,@p1)`, a, day.Format("2006-01-02"), term); err != nil {
		t.Fatal(err)
	}
	if err := s.GenerateRenewalTasks(context.Background(), shop, s.nowUTC()); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ListRenewalTasks(context.Background(), shop)
	if err != nil {
		t.Fatal(err)
	}
	var own []RenewalTask
	for _, task := range tasks {
		if task.AgreementID == a {
			own = append(own, task)
		}
	}
	return a, own
}
func nativeRenewalState(t *testing.T, ctx context.Context, db *DB, shop string) string {
	t.Helper()
	var state string
	err := db.Pool.QueryRow(ctx, `SELECT CONCAT((SELECT id,anniversary_date,visits_used,status,updated_at FROM dbo.agreements WHERE shop_id=CONVERT(uniqueidentifier,@p1) ORDER BY id FOR JSON PATH,INCLUDE_NULL_VALUES),(SELECT id,cycle_start_date,due_at,status,notes,updated_at FROM dbo.renewal_tasks WHERE shop_id=CONVERT(uniqueidentifier,@p1) ORDER BY id FOR JSON PATH,INCLUDE_NULL_VALUES),(SELECT event_type,occurred_at FROM dbo.pilot_activation_events WHERE shop_id=CONVERT(uniqueidentifier,@p1) ORDER BY event_type FOR JSON PATH,INCLUDE_NULL_VALUES))`, shop).Scan(&state)
	if err != nil {
		t.Fatal(err)
	}
	return state
}
func TestSQLServerRenewalFailuresRollbackWholeOutcome(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	for _, phase := range []string{"activation", "next-cycle"} {
		t.Run(phase, func(t *testing.T) {
			shop := createTestShop(t, ctx, db, "renewal-rollback-"+phase)
			_, tasks := nativeRenewalFixture(t, db, s, shop, now, 12)
			before := nativeRenewalState(t, ctx, db, shop)
			trigger := `CREATE TRIGGER dbo.fail_renewal_test ON dbo.pilot_activation_events AFTER INSERT AS BEGIN THROW 51000,'synthetic activation failure',1; END`
			if phase == "next-cycle" {
				trigger = `CREATE TRIGGER dbo.fail_renewal_test ON dbo.renewal_tasks AFTER INSERT AS BEGIN IF EXISTS(SELECT 1 FROM inserted WHERE milestone_days=14) THROW 51000,'synthetic late milestone failure',1; END`
			}
			if _, err := db.Pool.Exec(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "renewed", "kept only if committed"); err == nil {
				t.Fatal("failure reported success")
			}
			if got := nativeRenewalState(t, ctx, db, shop); got != before {
				t.Fatal("partial outcome, counter, timestamp or activation committed")
			}
			if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_renewal_test`); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "renewed", "saved once"); err != nil {
				t.Fatal(err)
			}
			final := nativeRenewalState(t, ctx, db, shop)
			if err := s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "renewed", "must not rewrite"); err != nil {
				t.Fatal(err)
			}
			if got := nativeRenewalState(t, ctx, db, shop); got != final {
				t.Fatal("retry rewrote final outcome")
			}
		})
	}
	// A late failure during first generation also leaves none of the five milestones.
	shop := createTestShop(t, ctx, db, "renewal-generation-rollback")
	a := createSeasonalTestAgreement(t, db, s, shop, []string{}, 5, 0)
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET anniversary_date=CONVERT(date,'2034-09-09') WHERE id=CONVERT(uniqueidentifier,@p1)`, a); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_renewal_generation ON dbo.renewal_tasks AFTER INSERT AS BEGIN IF EXISTS(SELECT 1 FROM inserted WHERE milestone_days=7) THROW 51000,'synthetic final milestone failure',1; END`); err != nil {
		t.Fatal(err)
	}
	if err := s.GenerateRenewalTasks(ctx, shop, now); err == nil {
		t.Fatal("generation succeeded")
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.renewal_tasks WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial milestones", n, err)
	}
}

func TestSQLServerRenewalRacesSerializeAndRevalidateParents(t *testing.T) {
	db := sqlServerTestDB(t)
	for _, changeParent := range []bool{false, true} {
		t.Run(fmt.Sprint(changeParent), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
			s := &Store{db: db, clock: fixedClock{now: now}}
			shop := createTestShop(t, ctx, db, "renewal-race-"+fmt.Sprint(changeParent))
			a, tasks := nativeRenewalFixture(t, db, s, shop, now, 12)
			var target string
			if changeParent {
				target = createSeasonalTestAgreement(t, db, s, shop, []string{}, 5, 3)
			}
			tx, err := db.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			var blocker, n int
			if err := tx.QueryRow(ctx, `SELECT @@SPID`).Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, a).Scan(&n); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 2)
			go func() { done <- s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "renewed", "first") }()
			waitForSeasonalBlockedSessions(t, ctx, db, blocker, 1)
			if changeParent {
				if _, err := tx.Exec(ctx, `UPDATE dbo.renewal_tasks SET agreement_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1)`, tasks[0].ID, target); err != nil {
					t.Fatal(err)
				}
			} else {
				go func() { done <- s.UpdateRenewalTask(ctx, shop, tasks[1].ID, "lost", "competing") }()
				waitForSeasonalBlockedSessions(t, ctx, db, blocker, 2)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if changeParent {
				if err := <-done; !errors.Is(err, renewals.ErrConflict) {
					t.Fatal("changed parent accepted", err)
				}
				for _, id := range []string{a, target} {
					got, err := s.GetAgreement(ctx, shop, id)
					if err != nil || got.VisitsUsed != 3 {
						t.Fatal("reparent changed agreement", err)
					}
				}
			} else {
				wins, conflicts := 0, 0
				for i := 0; i < 2; i++ {
					err := <-done
					if err == nil {
						wins++
					} else if errors.Is(err, renewals.ErrConflict) {
						conflicts++
					} else {
						t.Fatal(err)
					}
				}
				if wins != 1 || conflicts != 1 {
					t.Fatal("competing finals", wins, conflicts)
				}
				got, err := s.GetAgreement(ctx, shop, a)
				if err != nil {
					t.Fatal(err)
				}
				var distinct int
				if err := db.Pool.QueryRow(ctx, `SELECT COUNT(DISTINCT status) FROM dbo.renewal_tasks WHERE agreement_id=CONVERT(uniqueidentifier,@p1) AND cycle_start_date='2034-09-09'`, a).Scan(&distinct); err != nil || distinct != 1 {
					t.Fatal("split final cycle", err)
				}
				if got.Status == AgreementActive && got.VisitsUsed != 0 || got.Status == AgreementLapsed && got.VisitsUsed != 3 {
					t.Fatal("counter inconsistent with outcome")
				}
			}
		})
	}
}

func TestSQLServerRenewalDatesDemoAndSingleConnection(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	now := time.Date(2033, 4, 2, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, "renewal-month-normalization")
	a, tasks := nativeRenewalFixture(t, db, s, shop, time.Date(2033, 1, 31, 0, 0, 0, 0, time.UTC), 1)
	if len(tasks) == 0 {
		t.Fatal("no normalized cycle")
	}
	var cycle time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT TOP(1) cycle_start_date FROM dbo.renewal_tasks WHERE agreement_id=CONVERT(uniqueidentifier,@p1)`, a).Scan(&cycle); err != nil || cycle.Format("2006-01-02") != "2033-03-03" {
		t.Fatal("Go AddDate normalization changed", cycle, err)
	}
	// Explicit existing parity: selected old cycle governs rollover even after manual edits.
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET anniversary_date='2036-01-01',status=N'cancelled' WHERE id=CONVERT(uniqueidentifier,@p1)`, a); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "renewed", ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetAgreement(ctx, shop, a)
	if err != nil || got.AnniversaryDate.Format("2006-01-02") != "2033-04-03" || got.Status != AgreementActive {
		t.Fatal("old-cycle parity changed", err)
	}
	demo := createTestShop(t, ctx, db, "renewal-demo")
	_, demoTasks := nativeRenewalFixture(t, db, s, demo, now, 12)
	if len(demoTasks) != 5 {
		t.Fatal("demo fixture")
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.renewal_tasks WHERE shop_id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.shops SET kind=N'demo_sandbox',expires_at='2034-01-01' WHERE id=CONVERT(uniqueidentifier,@p1)`, demo); err != nil {
		t.Fatal(err)
	}
	// Hold three of four connections: all source cursors must close before nested work.
	for i := 0; i < 3; i++ {
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
	}
	if err := s.GenerateAllRenewalTasks(ctx, now); err != nil {
		t.Fatal("single-connection global generation", err)
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.renewal_tasks WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, demo).Scan(&n); err != nil || n != 0 {
		t.Fatal("global generated demo", err)
	}
	if err := s.GenerateRenewalTasks(ctx, demo, now); err != nil {
		t.Fatal("local demo generation", err)
	}
	if err := s.QueueRenewalDigests(ctx, now); err != nil {
		t.Fatal("single-connection digest", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.renewal_digest_outbox WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, demo).Scan(&n); err != nil || n != 0 {
		t.Fatal("demo digest", err)
	}
}
func TestSQLServerRenewalDateLimitsAndDashboardArithmetic(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	now := time.Date(9999, 12, 1, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, "renewal-date-limit")
	a, tasks := nativeRenewalFixture(t, db, s, shop, time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), 12)
	before := nativeRenewalState(t, ctx, db, shop)
	if err := s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "renewed", ""); !errors.Is(err, renewals.ErrInput) {
		t.Fatal("out of range rollover", err)
	}
	if nativeRenewalState(t, ctx, db, shop) != before {
		t.Fatal("date overflow partially changed outcome")
	}
	b := createSeasonalTestAgreement(t, db, s, shop, []string{}, 5, 0)
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET anniversary_date='9999-12-31',price_cents=2147483647,plan_snapshot_billing_interval=N'monthly' WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	stats, err := s.DashboardStats(ctx, shop)
	if err != nil || stats.MRRCents != 4294967294 || stats.ActiveCount != 2 || stats.RenewalsDueMonth != 2 || stats.RenewalsDue90 != 2 {
		t.Fatal("bigint/December dashboard", stats, err)
	}
	buckets, err := s.RenewalQueue(ctx, shop)
	if err != nil {
		t.Fatal(err)
	}
	for _, bucket := range buckets {
		want := 0
		if bucket.Days >= 30 {
			want = 2
		}
		if len(bucket.Items) != want {
			t.Fatal("overlapping buckets", bucket)
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET anniversary_date='0001-01-01' WHERE id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.agreements SET status=N'cancelled' WHERE id=CONVERT(uniqueidentifier,@p2)`, a, b); err != nil {
		t.Fatal(err)
	}
	if err := s.GenerateRenewalTasks(ctx, shop, time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, renewals.ErrInput) {
		t.Fatal("underflow not rejected", err)
	}
}

func TestSQLServerRenewalDigestsOrderLargeSnapshotsAndAllShopRollback(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, "renewal-large-digest")
	a := createSeasonalTestAgreement(t, db, s, shop, []string{}, 5, 0)
	expected := make([]string, 130)
	for n := 0; n < len(expected); n++ {
		expected[n] = fmt.Sprintf("%08x-0000-0000-0000-%012x", n, 1000-n)
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.renewal_tasks(id,shop_id,agreement_id,cycle_start_date,due_at,milestone_days) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),CONVERT(date,@p4),'2034-09-01',90)`, expected[n], shop, a, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n).Format("2006-01-02")); err != nil {
			t.Fatal(err)
		}
	}
	other := createTestShop(t, ctx, db, "renewal-digest-rollback")
	nativeRenewalFixture(t, db, s, other, now, 12)
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_digest ON dbo.renewal_digest_outbox AFTER INSERT AS BEGIN IF (SELECT COUNT(*) FROM dbo.renewal_digest_outbox)>1 THROW 51000,'synthetic second shop failure',1; END`); err != nil {
		t.Fatal(err)
	}
	if err := s.QueueRenewalDigests(ctx, now); err == nil {
		t.Fatal("digest failure succeeded")
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.renewal_digest_outbox`).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial shops committed", err)
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_digest`); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { <-gate; done <- s.QueueRenewalDigests(ctx, now) }()
	}
	close(gate)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal("concurrent dedupe", err)
		}
	}
	var payload string
	if err := db.Pool.QueryRow(ctx, `SELECT task_ids FROM dbo.renewal_digest_outbox WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := json.Unmarshal([]byte(payload), &ids); err != nil || strings.Join(ids, ",") != strings.Join(expected, ",") {
		t.Fatal("snapshot truncated or native UUID ordering leaked", err)
	}
	for _, status := range []string{"sent", "failed"} {
		if _, err := db.Pool.Exec(ctx, `UPDATE dbo.renewal_digest_outbox SET status=@p2 WHERE shop_id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.renewal_tasks SET status=N'lost' WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop, status); err != nil {
			t.Fatal(err)
		}
		if err := s.QueueRenewalDigests(ctx, now); err != nil {
			t.Fatal(err)
		}
		var after, gotStatus string
		if err := db.Pool.QueryRow(ctx, `SELECT task_ids,status FROM dbo.renewal_digest_outbox WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&after, &gotStatus); err != nil || after != payload || gotStatus != status {
			t.Fatal("rewrote sent/failed digest", err)
		}
	}
	if err := s.QueueRenewalDigests(ctx, now.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.renewal_digest_outbox WHERE digest_date='2034-09-10'`).Scan(&n); err != nil || n != 1 {
		t.Fatal("next day membership", n, err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT task_ids FROM dbo.renewal_digest_outbox WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&payload); !errors.Is(err, database.ErrNoRows) {
		t.Fatal("shop cascade", err)
	}
}

func TestSQLServerRenewalSharesCounterLockWithWork(t *testing.T) {
	db := sqlServerTestDB(t)
	for _, kind := range []string{"seasonal", "recurring"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
			s := &Store{db: db, clock: fixedClock{now: now}}
			shop := createTestShop(t, ctx, db, "renewal-work-"+kind)
			a, _, occurrences := recurringFixture(t, db, s, shop, 1, true)
			seasons, err := s.ListVisitDues(ctx, shop, false)
			if err != nil || len(seasons) != 1 {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET anniversary_date='2034-09-09' WHERE id=CONVERT(uniqueidentifier,@p1)`, a); err != nil {
				t.Fatal(err)
			}
			if err := s.GenerateRenewalTasks(ctx, shop, now); err != nil {
				t.Fatal(err)
			}
			tasks, err := s.ListRenewalTasks(ctx, shop)
			if err != nil || len(tasks) != 5 {
				t.Fatal(err)
			}
			tx, err := db.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			var blocker, n int
			if err := tx.QueryRow(ctx, `SELECT @@SPID`).Scan(&blocker); err != nil {
				t.Fatal(err)
			}
			if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, a).Scan(&n); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 2)
			go func() { done <- s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "renewed", "") }()
			waitForSeasonalBlockedSessions(t, ctx, db, blocker, 1)
			go func() {
				if kind == "seasonal" {
					done <- s.BulkUpdateVisitStatus(ctx, shop, []string{seasons[0].ID}, "completed")
				} else {
					done <- s.UpdateServiceOccurrence(ctx, shop, occurrences[0].ID, "completed", "")
				}
			}()
			waitForSeasonalBlockedSessions(t, ctx, db, blocker, 2)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := <-done; err != nil {
					t.Fatal("renewal/work transaction failed", err)
				}
			}
			got, err := s.GetAgreement(ctx, shop, a)
			if err != nil || got.AnniversaryDate.Format("2006-01-02") != "2035-09-09" || got.VisitsUsed < 0 || got.VisitsUsed > 1 {
				t.Fatal("invalid serialized renewal/work state", err)
			}
			var status string
			query := `SELECT status FROM dbo.visit_dues WHERE id=CONVERT(uniqueidentifier,@p1)`
			id := seasons[0].ID
			if kind == "recurring" {
				query = `SELECT status FROM dbo.service_occurrences WHERE id=CONVERT(uniqueidentifier,@p1)`
				id = occurrences[0].ID
			}
			if err := db.Pool.QueryRow(ctx, query, id).Scan(&status); err != nil || status != "completed" {
				t.Fatal("completion lost", err)
			}
			// Either completion precedes renewal (usage reset) or follows it (usage one).
			// Replaying either final action must preserve that committed serial order.
			before := nativeRenewalState(t, ctx, db, shop)
			if err := s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "renewed", ""); err != nil {
				t.Fatal(err)
			}
			if kind == "seasonal" {
				err = s.BulkUpdateVisitStatus(ctx, shop, []string{id}, "completed")
			} else {
				err = s.UpdateServiceOccurrence(ctx, shop, id, "completed", "")
			}
			if err != nil {
				t.Fatal(err)
			}
			if nativeRenewalState(t, ctx, db, shop) != before {
				t.Fatal("final replay changed agreement usage")
			}
		})
	}
}

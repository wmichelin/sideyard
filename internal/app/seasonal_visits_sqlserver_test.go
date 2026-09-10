package app

import (
	"context"
	"errors"
	"github.com/wmichelin/clubdue/internal/database"
	"strings"
	"testing"
	"time"
)

func TestSQLServerSeasonalGenerationAndOutcomesPreserveExistingBehavior(t *testing.T) {
	testSeasonalGenerationAndOutcomesPreserveExistingBehavior(t, sqlServerTestDB(t))
}

func TestSQLServerSeasonalSyncUsesOneAvailableConnectionAndExcludesGlobalDemos(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2033, 6, 1, 0, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "seasonal-single")
	createSeasonalTestAgreement(t, db, s, shop, []string{}, 2, 0)
	demo := createTestShop(t, ctx, db, "seasonal-single-demo")
	createSeasonalTestAgreement(t, db, s, demo, []string{}, 2, 0)
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.shops SET kind='demo_sandbox',expires_at=DATEADD(hour,4,SYSUTCDATETIME()) WHERE id=CONVERT(uniqueidentifier,@p1)`, demo); err != nil {
		t.Fatal(err)
	}
	// Reserve three of the native pool's four connections. All remaining work
	// must release each result before borrowing that last connection again.
	var held []database.Tx
	for i := 0; i < 3; i++ {
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, tx)
		defer tx.Rollback(ctx)
	}
	if err := s.SyncAllVisitDues(ctx); err != nil {
		t.Fatal("global sync exhausted pool", err)
	}
	var realCount, demoCount int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.visit_dues WHERE shop_id=CONVERT(uniqueidentifier,@p1)),(SELECT COUNT(*) FROM dbo.visit_dues WHERE shop_id=CONVERT(uniqueidentifier,@p2))`, shop, demo).Scan(&realCount, &demoCount); err != nil || realCount != 2 || demoCount != 0 {
		t.Fatal("global demo exclusion changed", err)
	}
	if rows, err := s.ListVisitDues(ctx, demo, false); err != nil || len(rows) != 2 {
		t.Fatal("local demo generation exhausted pool", err)
	}
	if count, err := s.CountVisitsOverdue(ctx, shop); err != nil || count != 1 {
		t.Fatal("one-connection count failed", err)
	}
	last, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(ctx, 25*time.Millisecond)
	defer stop()
	if err := s.SyncAllVisitDues(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting sync ignored cancellation", err)
	}
	if err := last.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncAllVisitDues(ctx); err != nil {
		t.Fatal("cancellation leaked connection", err)
	}
}

func TestSQLServerSeasonalCompletionRacesAndActivationFailureStayAtomic(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2033, 6, 1, 0, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "seasonal-race")
	agreement := createSeasonalTestAgreement(t, db, s, shop, []string{}, 1, 0)
	rows, err := s.ListVisitDues(ctx, shop, false)
	if err != nil || len(rows) != 2 {
		t.Fatal("fixture", err)
	}
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, v := range rows {
		go func(id string) { <-start; done <- s.BulkUpdateVisitStatus(ctx, shop, []string{id}, "completed") }(v.ID)
	}
	close(start)
	wins := 0
	for i := 0; i < 2; i++ {
		if err := <-done; err == nil {
			wins++
		}
	}
	got, err := s.GetAgreement(ctx, shop, agreement)
	if err != nil || wins != 1 || got.VisitsUsed != 1 {
		t.Fatal("racing completion exceeded entitlement", err)
	}
	// Reversed batches span two agreements and include duplicate IDs. Every
	// successful completion consumes one entitlement, even across racing retries.
	shop2 := createTestShop(t, ctx, db, "seasonal-batch")
	a := createSeasonalTestAgreement(t, db, s, shop2, []string{"spring"}, 1, 0)
	b := createSeasonalTestAgreement(t, db, s, shop2, []string{"fall"}, 1, 0)
	rows, err = s.ListVisitDues(ctx, shop2, false)
	if err != nil || len(rows) != 2 {
		t.Fatal("batch fixture", err)
	}
	ids := []string{rows[0].ID, rows[1].ID, rows[0].ID}
	// Force final activation to fail after both visits and counters have changed.
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_test_seasonal_activation ON dbo.pilot_activation_events AFTER INSERT AS BEGIN THROW 51000,'synthetic activation failure',1; END`); err != nil {
		t.Fatal(err)
	}
	if err := s.BulkUpdateVisitStatus(ctx, shop2, ids, "completed"); err == nil {
		t.Fatal("activation failure reported success")
	}
	for _, id := range []string{a, b} {
		got, err := s.GetAgreement(ctx, shop2, id)
		if err != nil || got.VisitsUsed != 0 {
			t.Fatal("activation failure left counter", err)
		}
	}
	rows, err = s.ListVisitDues(ctx, shop2, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rows {
		if v.Status != VisitDue || v.CompletedAt != nil {
			t.Fatal("activation failure left completed visit")
		}
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_test_seasonal_activation`); err != nil {
		t.Fatal(err)
	}
	start = make(chan struct{})
	done = make(chan error, 2)
	go func() { <-start; done <- s.BulkUpdateVisitStatus(ctx, shop2, ids, "completed") }()
	go func() { <-start; done <- s.BulkUpdateVisitStatus(ctx, shop2, []string{ids[1], ids[0]}, "completed") }()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal("reverse-order completion batch", err)
		}
	}
	for _, id := range []string{a, b} {
		got, err := s.GetAgreement(ctx, shop2, id)
		if err != nil || got.VisitsUsed != 1 {
			t.Fatal("racing retry incremented twice", err)
		}
	}
	var activated int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.pilot_activation_events WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND event_type='first_visit_action'`, shop2).Scan(&activated); err != nil || activated != 1 {
		t.Fatal("activation event duplicated/missing", err)
	}
}

// Use the disposable engine's lock state to establish the race ordering,
// rather than assuming a goroutine reached its blocking read after a sleep.
func waitForSeasonalBlockedSessions(t *testing.T, ctx context.Context, db *DB, blocker, count int) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked int
		if err := db.Pool.QueryRow(ctx, `WITH waiting AS (
 SELECT session_id FROM sys.dm_exec_requests WHERE blocking_session_id=@p1 AND database_id=DB_ID()
 UNION ALL
 SELECT r.session_id FROM sys.dm_exec_requests r JOIN waiting w ON r.blocking_session_id=w.session_id
 WHERE r.database_id=DB_ID()
) SELECT COUNT(*) FROM waiting OPTION(MAXRECURSION 16)`, blocker).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked >= count {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("expected blocked seasonal work", ctx.Err())
		}
	}
}

func TestSQLServerSeasonalSyncAndCompletionShareAgreementLockOrder(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2033, 6, 1, 0, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "seasonal-sync-race")
	agreement := createSeasonalTestAgreement(t, db, s, shop, []string{"spring"}, 2, 0)
	rows, err := s.ListVisitDues(ctx, shop, false)
	if err != nil || len(rows) != 1 {
		t.Fatal("fixture", err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var blocker, found int
	if err := tx.QueryRow(ctx, `SELECT @@SPID`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, agreement).Scan(&found); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	go func() { done <- s.SyncVisitDues(ctx, shop) }()
	waitForSeasonalBlockedSessions(t, ctx, db, blocker, 1)
	go func() { done <- s.BulkUpdateVisitStatus(ctx, shop, []string{rows[0].ID}, "completed") }()
	waitForSeasonalBlockedSessions(t, ctx, db, blocker, 2)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal("sync/completion lock order", err)
		}
	}
	got, err := s.GetAgreement(ctx, shop, agreement)
	if err != nil || got.VisitsUsed != 1 {
		t.Fatal("sync/completion counter", err)
	}
	rows, err = s.ListVisitDues(ctx, shop, false)
	if err != nil || len(rows) != 1 || rows[0].Status != VisitCompleted {
		t.Fatal("sync reset/duplicated completed work", err)
	}
}

func TestSQLServerSeasonalBulkRejectsChangedAgreementAfterDiscovery(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2033, 6, 1, 0, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "seasonal-reparent")
	original := createSeasonalTestAgreement(t, db, s, shop, []string{"spring"}, 2, 0)
	target := createSeasonalTestAgreement(t, db, s, shop, []string{"unknown"}, 2, 0)
	rows, err := s.ListVisitDues(ctx, shop, false)
	if err != nil || len(rows) != 1 {
		t.Fatal("fixture", err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var blocker, found int
	if err := tx.QueryRow(ctx, `SELECT @@SPID`).Scan(&blocker); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, original).Scan(&found); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.BulkUpdateVisitStatus(ctx, shop, []string{rows[0].ID}, "completed") }()
	waitForSeasonalBlockedSessions(t, ctx, db, blocker, 1)
	if _, err := tx.Exec(ctx, `UPDATE dbo.visit_dues SET agreement_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1)`, rows[0].ID, target); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("changed agreement was acquired out of order")
	}
	for _, id := range []string{original, target} {
		got, err := s.GetAgreement(ctx, shop, id)
		if err != nil || got.VisitsUsed != 0 {
			t.Fatal("reparent race incremented counter", err)
		}
	}
	var status string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM dbo.visit_dues WHERE id=CONVERT(uniqueidentifier,@p1)`, rows[0].ID).Scan(&status); err != nil || status != "due" {
		t.Fatal("reparent race changed visit", err)
	}
}

func TestSQLServerSeasonalTenantCascadesAndSignedYears(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2033, 6, 1, 0, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "seasonal-cascade")
	other := createTestShop(t, ctx, db, "seasonal-cascade-other")
	agreement := createSeasonalTestAgreement(t, db, s, shop, []string{"unknown"}, 2, 0)
	foreign := createSeasonalTestAgreement(t, db, s, other, []string{"unknown"}, 2, 0)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.visit_dues(shop_id,agreement_id,season,due_year) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),N'spring',2033)`, shop, foreign); err == nil {
		t.Fatal("foreign agreement accepted")
	}
	notes := strings.Repeat("🔧", 2500) + "  "
	for _, year := range []int{-2147483648, 2147483647} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.visit_dues(shop_id,agreement_id,season,due_year,notes) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),N'spring',@p3,@p4)`, shop, agreement, year, notes); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListVisitDues(ctx, shop, false)
	if err != nil || len(rows) != 2 || rows[0].DueYear != -2147483648 || rows[1].DueYear != 2147483647 || !rows[0].IsOverdue || rows[1].IsOverdue {
		t.Fatal("signed historical years changed", err)
	}
	for _, v := range rows {
		if v.Notes != notes {
			t.Fatal("historical notes truncated")
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.visit_dues SET shop_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1)`, rows[0].ID, other); err == nil {
		t.Fatal("foreign tenant update accepted")
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM dbo.agreements WHERE id=CONVERT(uniqueidentifier,@p1)`, agreement); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ListVisitDues(ctx, shop, false)
	if err != nil || len(rows) != 2 {
		t.Fatal("rolled back cascade lost visits")
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.visit_dues`).Scan(&count); err != nil || count != 0 {
		t.Fatal("shop cascade left visits", err)
	}
	if _, err := s.GetAgreement(ctx, other, foreign); err != nil {
		t.Fatal("shop cleanup crossed tenant", err)
	}
}

func TestSQLServerSeasonalLaterExhaustionAndForeignSelectionRollBackBatch(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2033, 6, 1, 0, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "seasonal-exhausted-batch")
	other := createTestShop(t, ctx, db, "seasonal-foreign-batch")
	a := createSeasonalTestAgreement(t, db, s, shop, []string{"spring"}, 1, 0)
	b := createSeasonalTestAgreement(t, db, s, shop, []string{"spring"}, 1, 0)
	createSeasonalTestAgreement(t, db, s, other, []string{"fall"}, 1, 0)
	rows, err := s.ListVisitDues(ctx, shop, false)
	if err != nil || len(rows) != 2 {
		t.Fatal("fixture", err)
	}
	foreign, err := s.ListVisitDues(ctx, other, false)
	if err != nil || len(foreign) != 1 {
		t.Fatal("foreign fixture", err)
	}
	ids := []string{rows[0].ID, rows[1].ID}
	if err := s.BulkUpdateVisitStatus(ctx, shop, append(append([]string{}, ids...), foreign[0].ID), "completed"); err == nil {
		t.Fatal("foreign visit accepted")
	}
	exhausted := a
	if b > a {
		exhausted = b
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET visits_used=1 WHERE id=CONVERT(uniqueidentifier,@p1)`, exhausted); err != nil {
		t.Fatal(err)
	}
	if err := s.BulkUpdateVisitStatus(ctx, shop, ids, "completed"); err == nil {
		t.Fatal("exhausted batch accepted")
	}
	rows, err = s.ListVisitDues(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rows {
		if v.Status != VisitDue || v.CompletedAt != nil {
			t.Fatal("later exhaustion retained partial completion")
		}
	}
	for _, id := range []string{a, b} {
		got, err := s.GetAgreement(ctx, shop, id)
		want := 0
		if id == exhausted {
			want = 1
		}
		if err != nil || got.VisitsUsed != want {
			t.Fatal("failed batch changed counter", err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET visits_used=0 WHERE id=CONVERT(uniqueidentifier,@p1)`, exhausted); err != nil {
		t.Fatal(err)
	}
	if err := s.BulkUpdateVisitStatus(ctx, shop, ids, "completed"); err != nil {
		t.Fatal("corrected batch could not retry", err)
	}
}

package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/servicework"
)

func recurringFixture(t *testing.T, db *DB, s *Store, shop string, entitled int, counts bool) (string, string, []ServiceOccurrence) {
	t.Helper()
	ctx := context.Background()
	agreement := createSeasonalTestAgreement(t, db, s, shop, []string{"fall"}, entitled, 0)
	id, err := s.CreateServiceSchedule(ctx, shop, ServiceScheduleInput{AgreementID: agreement, ServiceName: "Weekly service", CadenceUnit: "week", CadenceInterval: 1, StartsOn: s.nowUTC().AddDate(0, 0, -14), CountsTowardEntitlement: counts})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListServiceOccurrences(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	rows = slices.DeleteFunc(rows, func(v ServiceOccurrence) bool { return v.ScheduleID != id })
	return agreement, id, rows
}

func TestSQLServerRecurringEntitlementRaceAndBatchRollback(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}}
	shop := createTestShop(t, ctx, db, "recurring-race")
	agreement, _, rows := recurringFixture(t, db, s, shop, 1, true)
	seasons, err := s.ListVisitDues(ctx, shop, false)
	if err != nil || len(seasons) != 1 {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	done := make(chan error, 2)
	go func() { <-gate; done <- s.UpdateServiceOccurrence(ctx, shop, rows[0].ID, "completed", "first notes") }()
	go func() { <-gate; done <- s.BulkUpdateVisitStatus(ctx, shop, []string{seasons[0].ID}, "completed") }()
	close(gate)
	wins := 0
	for i := 0; i < 2; i++ {
		if <-done == nil {
			wins++
		}
	}
	a, err := s.GetAgreement(ctx, shop, agreement)
	if err != nil || a.VisitsUsed != 1 || wins != 1 {
		t.Fatal("shared entitlement race", wins, err)
	}
	// Non-counting completion and skipping don't consume agreement usage.
	_, _, noncount := recurringFixture(t, db, s, shop, 0, false)
	if err := s.UpdateServiceOccurrence(ctx, shop, noncount[0].ID, "completed", "first notes"); err != nil {
		t.Fatal(err)
	}
	s.clock = fixedClock{now: s.nowUTC().Add(time.Hour)}
	if err := s.UpdateServiceOccurrence(ctx, shop, noncount[0].ID, "completed", "replacement"); err != nil {
		t.Fatal(err)
	}
	var note string
	var completed time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT notes,completed_at FROM dbo.service_occurrences WHERE id=CONVERT(uniqueidentifier,@p1)`, noncount[0].ID).Scan(&note, &completed); err != nil || note != "first notes" || completed.Hour() != 12 {
		t.Fatal("retry changed history", err)
	}
	if err := s.UpdateServiceOccurrence(ctx, shop, noncount[0].ID, "skipped", ""); err == nil {
		t.Fatal("rewrote final")
	}
	if err := s.UpdateServiceOccurrence(ctx, shop, rows[1].ID, "skipped", strings.Repeat("é", 1000)); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServiceOccurrence(ctx, shop, rows[2].ID, "skipped", strings.Repeat("é", 1001)); err == nil {
		t.Fatal("byte limit ignored")
	}
	// Later exhaustion and a forced activation failure must undo earlier rows.
	batchShop := createTestShop(t, ctx, db, "recurring-batch")
	first, _, one := recurringFixture(t, db, s, batchShop, 1, true)
	second, _, two := recurringFixture(t, db, s, batchShop, 1, true)
	ids := []string{one[0].ID, two[0].ID, one[0].ID}
	agreements := []string{first, second}
	slices.Sort(agreements)
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET visits_entitled=0 WHERE id=CONVERT(uniqueidentifier,@p1)`, agreements[1]); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServiceOccurrences(ctx, batchShop, ids, "completed", ""); err == nil {
		t.Fatal("exhausted batch saved")
	}
	assertUnused := func() {
		t.Helper()
		for _, id := range agreements {
			a, err := s.GetAgreement(ctx, batchShop, id)
			if err != nil || a.VisitsUsed != 0 {
				t.Fatal("partial batch", err)
			}
		}
	}
	assertUnused()
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET visits_entitled=1 WHERE id=CONVERT(uniqueidentifier,@p1)`, agreements[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_recurring_activation ON dbo.pilot_activation_events AFTER INSERT AS BEGIN THROW 51000,'test failure',1; END`); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServiceOccurrences(ctx, batchShop, ids, "completed", ""); err == nil {
		t.Fatal("activation failure saved")
	}
	assertUnused()
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_recurring_activation`); err != nil {
		t.Fatal(err)
	}
	gate = make(chan struct{})
	done = make(chan error, 2)
	for _, batch := range [][]string{ids, {two[0].ID, one[0].ID}} {
		go func(batch []string) {
			<-gate
			done <- s.UpdateServiceOccurrences(ctx, batchShop, batch, "completed", "batch notes")
		}(batch)
	}
	close(gate)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal("opposite batch", err)
		}
	}
	for _, id := range agreements {
		a, err := s.GetAgreement(ctx, batchShop, id)
		if err != nil || a.VisitsUsed != 1 {
			t.Fatal("retry incremented", err)
		}
	}
	if err := s.UpdateServiceOccurrences(ctx, batchShop, []string{one[1].ID, rows[2].ID}, "skipped", ""); err == nil {
		t.Fatal("foreign batch accepted")
	}
	var status string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM dbo.service_occurrences WHERE id=CONVERT(uniqueidentifier,@p1)`, one[1].ID).Scan(&status); err != nil || status != "due" {
		t.Fatal("foreign batch changed own", err)
	}
	// Final retries reject paused schedules and inactive agreements.
	if err := s.SetServiceScheduleActive(ctx, batchShop, one[0].ScheduleID, false); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServiceOccurrence(ctx, batchShop, one[0].ID, "completed", ""); err == nil {
		t.Fatal("paused retry accepted")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET status=N'cancelled' WHERE id=CONVERT(uniqueidentifier,@p1)`, first); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServiceScheduleActive(ctx, batchShop, one[0].ScheduleID, true); err != nil {
		t.Fatal("inactive resume rejected", err)
	}
	if err := s.UpdateServiceOccurrence(ctx, batchShop, one[0].ID, "completed", ""); err == nil {
		t.Fatal("inactive retry accepted")
	}
}

func TestSQLServerRecurringCheckpointFailuresAndOneConnection(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, "recurring-checkpoint")
	a := createSeasonalTestAgreement(t, db, s, shop, []string{}, 5, 0)
	input := ServiceScheduleInput{AgreementID: a, ExternalID: "daily", ServiceName: "Daily", CadenceUnit: "day", CadenceInterval: 1, StartsOn: now.AddDate(-4, 0, 0)}
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_recurring_checkpoint ON dbo.service_schedules AFTER UPDATE AS BEGIN IF UPDATE(generated_through) THROW 51000,'test checkpoint failure',1; END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateServiceSchedule(ctx, shop, input); err == nil {
		t.Fatal("initial checkpoint failure saved")
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.service_schedules WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&count); err != nil || count != 0 {
		t.Fatal("initial rollback", err)
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_recurring_checkpoint`); err != nil {
		t.Fatal(err)
	}
	id, err := s.CreateServiceSchedule(ctx, shop, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.service_occurrences WHERE schedule_id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.service_schedules SET generated_through=DATEADD(day,-1,starts_on) WHERE id=CONVERT(uniqueidentifier,@p1)`, id); err != nil {
		t.Fatal(err)
	}
	var original time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT generated_through FROM dbo.service_schedules WHERE id=CONVERT(uniqueidentifier,@p1)`, id).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_recurring_checkpoint ON dbo.service_schedules AFTER UPDATE AS BEGIN IF UPDATE(generated_through) THROW 51000,'test checkpoint failure',1; END`); err != nil {
		t.Fatal(err)
	}
	if err := s.serviceWorkService().GenerateScheduleOccurrences(ctx, shop, id); err == nil {
		t.Fatal("generation failure saved")
	}
	var checkpoint time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT generated_through,(SELECT COUNT(*) FROM dbo.service_occurrences WHERE schedule_id=sc.id) FROM dbo.service_schedules sc WHERE id=CONVERT(uniqueidentifier,@p1)`, id).Scan(&checkpoint, &count); err != nil || count != 0 || !checkpoint.Equal(original) {
		t.Fatal("checkpoint partial commit", err)
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_recurring_checkpoint`); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 3; n++ {
		if err := s.serviceWorkService().GenerateScheduleOccurrences(ctx, shop, id); err != nil {
			t.Fatal(err)
		}
		if err := db.Pool.QueryRow(ctx, `SELECT generated_through,(SELECT COUNT(*) FROM dbo.service_occurrences WHERE schedule_id=sc.id) FROM dbo.service_schedules sc WHERE id=CONVERT(uniqueidentifier,@p1)`, id).Scan(&checkpoint, &count); err != nil || count != 500*n || !checkpoint.Equal(original.AddDate(0, 0, 500*n)) {
			t.Fatal("500 checkpoint", n, count, checkpoint, err)
		}
	}
	// A failed pause must preserve active state and every pending occurrence.
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_recurring_pause ON dbo.service_occurrences AFTER DELETE AS BEGIN THROW 51000,'test pause failure',1; END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetServiceScheduleActive(ctx, shop, id, false); err == nil {
		t.Fatal("pause failure saved")
	}
	var active bool
	if err := db.Pool.QueryRow(ctx, `SELECT active,(SELECT COUNT(*) FROM dbo.service_occurrences WHERE schedule_id=sc.id) FROM dbo.service_schedules sc WHERE id=CONVERT(uniqueidentifier,@p1)`, id).Scan(&active, &count); err != nil || !active || count != 1500 {
		t.Fatal("pause partial commit", err)
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_recurring_pause`); err != nil {
		t.Fatal(err)
	}
	demo := createTestShop(t, ctx, db, "recurring-global-demo")
	_, demoSchedule, _ := recurringFixture(t, db, s, demo, 2, false)
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.shops SET kind=N'demo_sandbox',expires_at=DATEADD(hour,4,SYSUTCDATETIME()) WHERE id=CONVERT(uniqueidentifier,@p1); DELETE FROM dbo.service_occurrences WHERE shop_id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.service_schedules SET generated_through=NULL WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, demo); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		held, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Rollback(ctx)
	}
	if err := s.GenerateAllServiceOccurrences(ctx); err != nil {
		t.Fatal("global exhausted pool", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.service_occurrences WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, demo).Scan(&count); err != nil || count != 0 {
		t.Fatal("global generated demo", err)
	}
	if _, err := s.ListServiceSchedules(ctx, shop, a); err != nil {
		t.Fatal("schedule list exhausted pool", err)
	}
	if _, err := s.ListServiceScheduleSummaries(ctx, shop); err != nil {
		t.Fatal("summary exhausted pool", err)
	}
	if _, err := s.CountServiceOccurrencesOverdue(ctx, shop); err != nil {
		t.Fatal("count exhausted pool", err)
	}
	if err := s.serviceWorkService().GenerateScheduleOccurrences(ctx, demo, demoSchedule); err != nil {
		t.Fatal("local demo exhausted pool", err)
	}
}

// Pause the repository between its count query and page query, after the first
// result closes. A separate connection commits a move inside that interval.
type calendarBarrierPool struct {
	database.Pool
	ready, release chan struct{}
}
type calendarBarrierTx struct {
	database.Tx
	ready, release chan struct{}
	queries        int
}

func (p calendarBarrierPool) BeginTx(ctx context.Context, options *sql.TxOptions) (database.Tx, error) {
	tx, err := p.Pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &calendarBarrierTx{Tx: tx, ready: p.ready, release: p.release}, nil
}
func (tx *calendarBarrierTx) Query(ctx context.Context, q string, args ...any) (database.Rows, error) {
	tx.queries++
	if tx.queries == 2 {
		close(tx.ready)
		select {
		case <-tx.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return tx.Tx.Query(ctx, q, args...)
}
func TestSQLServerRecurringCalendarSnapshotOrderingAndExtremeDates(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Date(9999, 12, 1, 0, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, "recurring-extreme")
	a := createSeasonalTestAgreement(t, db, s, shop, []string{}, 5, 0)
	// Long historical text and UTF-8 order must determine membership before paging.
	names := []string{"A", "a", "a ", "z", "Á", "ä", "\ue000", "\U00010000"}
	var all []servicework.CalendarVisit
	for i := 0; i < 58; i++ {
		name := fmt.Sprintf("%s%03d", names[i%len(names)], i)
		id, err := s.CreateServiceSchedule(ctx, shop, ServiceScheduleInput{AgreementID: a, ExternalID: strings.Repeat("界", 3000) + name, ServiceName: name, CadenceUnit: "week", CadenceInterval: 1, StartsOn: now})
		if err != nil {
			t.Fatal(err)
		}
		var visit string
		if err := db.Pool.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.service_occurrences WHERE schedule_id=CONVERT(uniqueidentifier,@p1) AND due_on=CONVERT(date,'9999-12-01')`, id).Scan(&visit); err != nil {
			t.Fatal(err)
		}
		all = append(all, servicework.CalendarVisit{ID: visit, ServiceName: name})
	}
	slices.SortFunc(all, func(a, b servicework.CalendarVisit) int {
		if n := strings.Compare(a.ServiceName, b.ServiceName); n != 0 {
			return n
		}
		return strings.Compare(a.ID, b.ID)
	})
	end := now.AddDate(0, 1, 0)
	first, err := s.ServiceCalendar(ctx, shop, now, end, now, 1)
	if err != nil || first.Total != 58 || len(first.Visits) != 50 {
		t.Fatal("December9999", err)
	}
	second, err := s.ServiceCalendar(ctx, shop, now, end, now, 9999)
	if err != nil || second.Page != 2 || len(second.Visits) != 8 {
		t.Fatal("page2", err)
	}
	for i, v := range append(first.Visits, second.Visits...) {
		if v.ID != all[i].ID {
			t.Fatal("wrong page membership", i, v.ServiceName, all[i].ServiceName)
		}
	}
	ready, release := make(chan struct{}), make(chan struct{})
	snapshot := &Store{db: &DB{Pool: calendarBarrierPool{Pool: db.Pool, ready: ready, release: release}}, clock: s.clock}
	result := make(chan servicework.Calendar, 1)
	failure := make(chan error, 1)
	go func() {
		got, err := snapshot.ServiceCalendar(ctx, shop, now, end, now, 1)
		result <- got
		failure <- err
	}()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := s.MoveServiceVisit(ctx, shop, all[0].ID, now.AddDate(0, 0, 1), 0); err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	got := <-result
	if err := <-failure; err != nil || got.Total != 58 || len(got.Visits) != 50 || got.Visits[0].ID != all[0].ID {
		t.Fatal("inconsistent snapshot", err)
	}
	fresh, err := s.ServiceCalendar(ctx, shop, now, end, now, 1)
	if err != nil || fresh.Total != 57 || fresh.Visits[0].ID == all[0].ID {
		t.Fatal("move missing after snapshot", err)
	}
	// The year-end checkpoint remains representable and generation is idempotent.
	if err := s.GenerateAllServiceOccurrences(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.service_schedules WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND generated_through=CONVERT(date,'9999-12-31')`, shop).Scan(&count); err != nil || count != 58 {
		t.Fatal("year-end cursor", err)
	}
	// Far-future ungenerated daily and weekly anchors must reject a move.
	s.clock = fixedClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	for _, unit := range []string{"day", "week"} {
		start := s.nowUTC()
		id, err := s.CreateServiceSchedule(ctx, shop, ServiceScheduleInput{AgreementID: a, ServiceName: unit, CadenceUnit: unit, CadenceInterval: 1, StartsOn: start})
		if err != nil {
			t.Fatal(err)
		}
		var visit string
		if err := db.Pool.QueryRow(ctx, `SELECT TOP(1) LOWER(CONVERT(char(36),id)) FROM dbo.service_occurrences WHERE schedule_id=CONVERT(uniqueidentifier,@p1) ORDER BY due_on`, id).Scan(&visit); err != nil {
			t.Fatal(err)
		}
		days := 1000000
		if unit == "week" {
			days = 2100000
		}
		if err := s.MoveServiceVisit(ctx, shop, visit, start.AddDate(0, 0, days), 0); !errors.Is(err, servicework.ErrDateConflict) {
			t.Fatal("far anchor missed", err)
		}
	}
}

func TestSQLServerRecurringMappingRacesAndMoveFinalization(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}}
	for _, mapping := range []string{"schedule", "occurrence"} {
		t.Run(mapping, func(t *testing.T) {
			shop := createTestShop(t, ctx, db, "recurring-parent-"+mapping)
			a, id, rows := recurringFixture(t, db, s, shop, 5, true)
			b, other, _ := recurringFixture(t, db, s, shop, 5, true)
			if mapping == "occurrence" {
				if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.service_occurrences WHERE schedule_id=CONVERT(uniqueidentifier,@p1)`, other); err != nil {
					t.Fatal(err)
				}
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
			if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, a).Scan(&found); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- s.UpdateServiceOccurrence(ctx, shop, rows[0].ID, "completed", "") }()
			waitForSeasonalBlockedSessions(t, ctx, db, blocker, 1)
			if mapping == "schedule" {
				_, err = tx.Exec(ctx, `UPDATE dbo.service_schedules SET agreement_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1)`, id, b)
			} else {
				_, err = tx.Exec(ctx, `UPDATE dbo.service_occurrences SET schedule_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1)`, rows[0].ID, other)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, database.ErrNoRows) {
				t.Fatal("changed mapping accepted", err)
			}
			for _, agreement := range []string{a, b} {
				got, err := s.GetAgreement(ctx, shop, agreement)
				if err != nil || got.VisitsUsed != 0 {
					t.Fatal("changed mapping consumed quota", err)
				}
			}
		})
	}
	// The first request waits under a real engine-observed lock before the second
	// operation starts, testing the common serialization point without sleeps.
	for _, action := range []string{"pause", "complete"} {
		t.Run(action, func(t *testing.T) {
			shop := createTestShop(t, ctx, db, "recurring-move-"+action)
			a, id, rows := recurringFixture(t, db, s, shop, 5, false)
			tx, err := db.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			var blocker, found int
			tx.QueryRow(ctx, `SELECT @@SPID`).Scan(&blocker)
			if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, a).Scan(&found); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 2)
			go func() { done <- s.MoveServiceVisit(ctx, shop, rows[2].ID, s.nowUTC().AddDate(0, 0, 1), 0) }()
			waitForSeasonalBlockedSessions(t, ctx, db, blocker, 1)
			go func() {
				if action == "pause" {
					done <- s.SetServiceScheduleActive(ctx, shop, id, false)
				} else {
					done <- s.UpdateServiceOccurrence(ctx, shop, rows[2].ID, "completed", "")
				}
			}()
			waitForSeasonalBlockedSessions(t, ctx, db, blocker, 2)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				err := <-done
				if err != nil && !errors.Is(err, servicework.ErrVisitUnavailable) {
					t.Fatal("serialized action failed", err)
				}
			}
			got, err := s.GetServiceVisitDate(ctx, shop, rows[2].ID)
			if action == "pause" {
				if !errors.Is(err, database.ErrNoRows) {
					t.Fatal("pause kept future work", err)
				}
			} else if err != nil || got.Actionable {
				t.Fatal("completed work actionable", err)
			}
		})
	}
}

func TestSQLServerRecurringTextRevisionsTenantsAndCascades(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, "recurring-integrity")
	a, id, rows := recurringFixture(t, db, s, shop, 5, false)
	other := createTestShop(t, ctx, db, "recurring-integrity-other")
	_, foreign, _ := recurringFixture(t, db, s, other, 5, false)
	text := "\u2060"
	input := ServiceScheduleInput{AgreementID: a, ExternalID: text, ServiceName: strings.Repeat("界", 3000), CadenceUnit: "month", CadenceInterval: 1, StartsOn: now}
	unicodeID, err := s.CreateServiceSchedule(ctx, shop, input)
	if err != nil {
		t.Fatal(err)
	}
	var external string
	if err := db.Pool.QueryRow(ctx, `SELECT external_id FROM dbo.service_schedules WHERE id=CONVERT(uniqueidentifier,@p1)`, unicodeID).Scan(&external); err != nil || external != text {
		t.Fatal("nonempty Unicode identity lost", err)
	}
	if _, err := s.CreateServiceSchedule(ctx, shop, input); err == nil {
		t.Fatal("duplicate Unicode identity accepted")
	}
	if err := s.UpdateServiceOccurrence(ctx, shop, rows[0].ID, "completed", text); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT notes FROM dbo.service_occurrences WHERE id=CONVERT(uniqueidentifier,@p1)`, rows[0].ID).Scan(&external); err != nil || external != text {
		t.Fatal("nonempty Unicode notes lost", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.service_occurrences SET schedule_revision=2147483647 WHERE id=CONVERT(uniqueidentifier,@p1)`, rows[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MoveServiceVisit(ctx, shop, rows[1].ID, now.AddDate(0, 0, 1), 2147483647); err == nil {
		t.Fatal("overflow accepted")
	}
	got, err := s.GetServiceVisitDate(ctx, shop, rows[1].ID)
	if err != nil || got.Revision != 2147483647 || !got.CurrentOn.Equal(rows[1].DueOn) {
		t.Fatal("overflow partially changed", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.service_occurrences SET schedule_revision=-2147483648 WHERE id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.service_schedules SET cadence_interval=2147483647 WHERE id=CONVERT(uniqueidentifier,@p2)`, rows[1].ID, id); err != nil {
		t.Fatal("historical signed/interval rejected", err)
	}
	got, err = s.GetServiceVisitDate(ctx, shop, rows[1].ID)
	if err != nil || got.Revision != -2147483648 {
		t.Fatal("signed revision lost", err)
	}
	for _, query := range []string{
		`UPDATE dbo.service_occurrences SET shop_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1)`,
		`UPDATE dbo.service_occurrences SET schedule_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1)`,
	} {
		foreignID := other
		if strings.Contains(query, "SET schedule_id") {
			foreignID = foreign
		}
		if _, err := db.Pool.Exec(ctx, query, rows[1].ID, foreignID); err == nil {
			t.Fatal("cross tenant update accepted")
		}
	}
	// Final history survives pause/resume and untouched retries preserve cursor/time.
	if err := s.SetServiceScheduleActive(ctx, shop, id, false); err != nil {
		t.Fatal(err)
	}
	var paused, checkpoint time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT paused_at,generated_through FROM dbo.service_schedules WHERE id=CONVERT(uniqueidentifier,@p1)`, id).Scan(&paused, &checkpoint); err != nil {
		t.Fatal(err)
	}
	s.clock = fixedClock{now: now.AddDate(0, 0, 10)}
	if err := s.SetServiceScheduleActive(ctx, shop, id, false); err != nil {
		t.Fatal(err)
	}
	var repeat, cursor time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT paused_at,generated_through FROM dbo.service_schedules WHERE id=CONVERT(uniqueidentifier,@p1)`, id).Scan(&repeat, &cursor); err != nil || !repeat.Equal(paused) || !cursor.Equal(checkpoint) {
		t.Fatal("repeated pause changed history", err)
	}
	if _, err := s.GetServiceVisitDate(ctx, shop, rows[0].ID); err != nil {
		t.Fatal("pause deleted final history", err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM dbo.agreements WHERE id=CONVERT(uniqueidentifier,@p1)`, a); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetServiceVisitDate(ctx, shop, rows[0].ID); err != nil {
		t.Fatal("rollback lost history", err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.service_schedules WHERE shop_id=CONVERT(uniqueidentifier,@p1))+(SELECT COUNT(*) FROM dbo.service_occurrences WHERE shop_id=CONVERT(uniqueidentifier,@p1))`, shop).Scan(&count); err != nil || count != 0 {
		t.Fatal("orphaned recurring rows", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.service_occurrences WHERE schedule_id=CONVERT(uniqueidentifier,@p1)`, foreign).Scan(&count); err != nil || count == 0 {
		t.Fatal("cascade crossed tenant", err)
	}
}

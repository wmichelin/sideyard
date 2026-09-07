package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRecurringDueQueueAndExportExcludeFutureWork(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2034, 3, 15, 23, 59, 59, 0, time.UTC)
	clock := &fixedClock{now: now}
	store := &Store{db: db, clock: clock}
	stamp := time.Now().UnixNano()
	shopID := createTestShop(t, ctx, db, fmt.Sprintf("due-queue-%d", stamp))
	otherShop := createTestShop(t, ctx, db, fmt.Sprintf("other-due-queue-%d", stamp))
	var source strings.Builder
	source.WriteString("external_id,customer_name,address_line1,city,state,postal_code,anniversary_date,service_name,cadence_unit,service_start_date\n")
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&source, "due-%d,Fictional Customer %02d,%d Test Way,Cary,NC,27511,2035-03-15,Weekly mowing,week,2034-03-15\n", i, i, i+1)
	}
	rows, err := parseImportCSV(strings.NewReader(source.String()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitImport(ctx, shopID, rows); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitImport(ctx, otherShop, rows[:1]); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListServiceOccurrences(ctx, shopID, true)
	if err != nil || len(due) != 30 {
		t.Fatalf("due queue = %d, %v; want 30 today", len(due), err)
	}
	all, err := store.ListServiceOccurrences(ctx, shopID, false)
	if err != nil || len(all) != 390 {
		t.Fatalf("all work = %d, %v; want 390", len(all), err)
	}
	ids := make([]string, 0, len(due))
	for _, work := range due {
		if work.StatusLabel != "Due today" || !work.Actionable || work.IsOverdue {
			t.Errorf("unexpected due work: %+v", work)
		}
		ids = append(ids, work.ID)
	}
	for _, filter := range []struct {
		dueOnly bool
		count   int
	}{{true, 30}, {false, 390}} {
		export, err := store.ServiceOccurrenceExportRows(ctx, shopID, filter.dueOnly)
		if err != nil || len(export) != filter.count {
			t.Fatalf("export due=%t: count=%d, err=%v", filter.dueOnly, len(export), err)
		}
		if filter.dueOnly {
			for _, row := range export {
				if !strings.Contains(row.Notes, "due 2034-03-15;") {
					t.Errorf("future work in due export: %s", row.Notes)
				}
			}
		}
	}
	if err := store.UpdateServiceOccurrences(ctx, shopID, ids, "completed", "today's work"); err != nil {
		t.Fatal(err)
	}
	due, err = store.ListServiceOccurrences(ctx, shopID, true)
	if err != nil || len(due) != 0 {
		t.Fatalf("completed due queue = %d, %v", len(due), err)
	}
	all, err = store.ListServiceOccurrences(ctx, shopID, false)
	if err != nil {
		t.Fatal(err)
	}
	completed, future := 0, 0
	for _, work := range all {
		switch work.StatusLabel {
		case "Completed":
			completed++
		case "Upcoming":
			future++
		default:
			t.Errorf("unexpected post-completion status %q", work.StatusLabel)
		}
	}
	if completed != 30 || future != 360 {
		t.Fatalf("completion touched future work: completed=%d future=%d", completed, future)
	}
	other, err := store.ListServiceOccurrences(ctx, otherShop, true)
	if err != nil || len(other) != 1 {
		t.Fatalf("other tenant changed: %d, %v", len(other), err)
	}
	clock.now = time.Date(2034, 3, 21, 23, 59, 59, 0, time.UTC)
	due, err = store.ListServiceOccurrences(ctx, shopID, true)
	if err != nil || len(due) != 0 {
		t.Fatalf("work appeared before its UTC date: %d, %v", len(due), err)
	}
	clock.now = clock.now.Add(time.Second)
	due, err = store.ListServiceOccurrences(ctx, shopID, true)
	if err != nil || len(due) != 30 {
		t.Fatalf("midnight due queue = %d, %v", len(due), err)
	}
	if err := store.SetServiceScheduleActive(ctx, shopID, due[0].ScheduleID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE agreements SET status = 'cancelled' WHERE shop_id = $1 AND id = $2`, shopID, due[1].AgreementID); err != nil {
		t.Fatal(err)
	}
	clock.now = clock.now.AddDate(0, 0, 1)
	due, err = store.ListServiceOccurrences(ctx, shopID, true)
	if err != nil || len(due) != 28 {
		t.Fatalf("overdue eligible queue = %d, %v", len(due), err)
	}
	for _, work := range due {
		if !work.IsOverdue || work.StatusLabel != "Overdue" {
			t.Errorf("expected overdue: %+v", work)
		}
	}
	export, err := store.ServiceOccurrenceExportRows(ctx, shopID, true)
	if err != nil || len(export) != 28 {
		t.Fatalf("eligible overdue export = %d, %v", len(export), err)
	}
}

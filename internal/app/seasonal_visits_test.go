package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func createSeasonalTestAgreement(t *testing.T, db *DB, s *Store, shop string, windows []string, entitled, used int) string {
	t.Helper()
	ctx := context.Background()
	customer := createTestCustomer(t, ctx, db, shop, "Seasonal customer")
	id, err := s.CreateAgreement(ctx, shop, CreateAgreementInput{CustomerID: customer, Status: AgreementActive, AnniversaryDate: time.Date(2040, 1, 1, 0, 0, 0, 0, time.UTC), VisitsEntitled: entitled, VisitsUsed: used})
	if err != nil {
		t.Fatal(err)
	}
	var value any = windows
	if db.Pool.Dialect() == database.SQLServer {
		if windows == nil {
			windows = []string{}
		}
		encoded, _ := json.Marshal(windows)
		value = string(encoded)
	}
	if _, err := db.Pool.Exec(ctx, database.Statement(db.Pool.Dialect(), `UPDATE agreements SET plan_snapshot_visit_windows=$2 WHERE id=$1`, `UPDATE dbo.agreements SET plan_snapshot_visit_windows=@p2 WHERE id=CONVERT(uniqueidentifier,@p1)`), id, value); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSeasonalGenerationAndOutcomesPreserveExistingBehavior(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testSeasonalGenerationAndOutcomesPreserveExistingBehavior(t, db)
}
func testSeasonalGenerationAndOutcomesPreserveExistingBehavior(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2033, 5, 31, 23, 59, 59, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, "seasonal-"+time.Now().Format("150405.000000000"))
	recognized := createSeasonalTestAgreement(t, db, s, shop, []string{" \tSpRiNg\u00a0", "fall", "FALL", "winter"}, 1, 0)
	empty := createSeasonalTestAgreement(t, db, s, shop, []string{}, 2, 0)
	ignored := createSeasonalTestAgreement(t, db, s, shop, []string{"winter", "", " "}, 2, 0)
	exhausted := createSeasonalTestAgreement(t, db, s, shop, []string{"fall"}, 1, 1)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SyncVisitDues(ctx, shop); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	rows, err := s.ListVisitDues(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	var selected []VisitDueRow
	for _, v := range rows {
		counts[v.AgreementID]++
		if v.DueYear != 2033 || v.IsOverdue || v.PlanName != "Custom plan" || v.PropertyAddress != "" {
			t.Fatal("generation changed year/terms/boundary")
		}
		if v.AgreementID == recognized {
			selected = append(selected, v)
		}
	}
	if counts[recognized] != 2 || counts[empty] != 2 || counts[ignored] != 0 || counts[exhausted] != 0 {
		t.Fatalf("window generation: %v", counts)
	}
	s.clock = fixedClock{now: now.Add(time.Nanosecond)}
	rows, err = s.ListVisitDues(ctx, shop, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rows {
		if v.IsOverdue != (v.Season == "spring") {
			t.Fatal("end-of-season boundary changed")
		}
	}
	// Generation eligibility does not hide or forbid existing inactive work.
	if _, err := db.Pool.Exec(ctx, database.Statement(db.Pool.Dialect(), `UPDATE agreements SET status='cancelled',plan_snapshot_visit_windows='{}' WHERE id=$1`, `UPDATE dbo.agreements SET status=N'cancelled',plan_snapshot_visit_windows=N'[]' WHERE id=CONVERT(uniqueidentifier,@p1)`), recognized); err != nil {
		t.Fatal(err)
	}
	first := selected[0].ID
	second := selected[1].ID
	if err := s.BulkUpdateVisitStatus(ctx, shop, []string{first, first}, "scheduled"); err != nil {
		t.Fatal(err)
	}
	handedAt := s.nowUTC()
	s.clock = fixedClock{now: now.Add(time.Hour)}
	if err := s.BulkUpdateVisitStatus(ctx, shop, []string{first}, "scheduled"); err != nil {
		t.Fatal(err)
	}
	if err := s.BulkUpdateVisitStatus(ctx, shop, []string{first}, "completed"); err != nil {
		t.Fatal(err)
	}
	completedAt := s.nowUTC()
	s.clock = fixedClock{now: now.Add(2 * time.Hour)}
	if err := s.BulkUpdateVisitStatus(ctx, shop, []string{first, first}, "completed"); err != nil {
		t.Fatal(err)
	}
	if err := s.BulkUpdateVisitStatus(ctx, shop, []string{second}, "completed"); err == nil {
		t.Fatal("entitlement exceeded")
	}
	if err := s.BulkUpdateVisitStatus(ctx, shop, []string{first}, "scheduled"); err == nil {
		t.Fatal("completed visit handed off again")
	}
	rows, err = s.ListVisitDues(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rows {
		if v.ID == first {
			if v.Status != VisitCompleted || v.IsOverdue || v.ScheduledAt == nil || !v.ScheduledAt.Truncate(time.Microsecond).Equal(handedAt.Truncate(time.Microsecond)) || v.CompletedAt == nil || !v.CompletedAt.Equal(completedAt) {
				t.Fatal("retry changed timestamps/state")
			}
		}
		if v.ID == second && v.Status != VisitDue {
			t.Fatal("rejected completion changed visit")
		}
	}
	agreement, err := s.GetAgreement(ctx, shop, recognized)
	if err != nil || agreement.VisitsUsed != 1 {
		t.Fatal("retry consumed entitlement twice")
	}
	// A failed mixed batch cannot retain its earlier handoff.
	var open string
	for _, v := range rows {
		if v.AgreementID == empty {
			open = v.ID
			break
		}
	}
	if err := s.BulkUpdateVisitStatus(ctx, shop, []string{open, "ffffffff-ffff-ffff-ffff-ffffffffffff"}, "scheduled"); err == nil {
		t.Fatal("missing visit accepted")
	}
	rows, err = s.ListVisitDues(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rows {
		if v.ID == open && (v.Status != VisitDue || v.ScheduledAt != nil) {
			t.Fatal("mixed batch retained partial handoff")
		}
	}
	if err := s.BulkUpdateVisitStatus(ctx, "invalid", nil, "unknown"); err != nil {
		t.Fatal("empty selection no-op changed")
	}
	if err := s.BulkUpdateVisitStatus(ctx, shop, []string{open + "suffix"}, "completed"); err == nil {
		t.Fatal("UUID alias accepted")
	}
	s.clock = fixedClock{now: time.Date(2034, 1, 1, 0, 0, 0, 0, time.UTC)}
	rows, err = s.ListVisitDues(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	byYear := map[int]int{}
	for _, v := range rows {
		byYear[v.DueYear]++
	}
	if byYear[2033] != 4 || byYear[2034] != 2 {
		t.Fatalf("year rollover deleted history/generated inactive work: %v", byYear)
	}
	exports, err := s.VisitExportRows(ctx, shop, false)
	if err != nil || len(exports) != 6 {
		t.Fatal("seasonal export changed", err)
	}
	for _, v := range exports {
		if v.ServiceType != "Custom plan" || !strings.HasPrefix(v.Notes, "agreement ") {
			t.Fatal("export contract identity changed")
		}
	}
}

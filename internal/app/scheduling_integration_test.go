package app

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/servicework"
)

func TestMoveServiceVisitPreservesCadenceAndSummaries(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testMoveServiceVisitPreservesCadenceAndSummaries(t, db)
}
func testMoveServiceVisitPreservesCadenceAndSummaries(t *testing.T, db *DB) {
	ctx := context.Background()
	now := time.Date(2034, 1, 31, 12, 0, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	for _, tc := range []struct {
		name, unit string
		interval   int
	}{{"weekly", "week", 1}, {"biweekly", "week", 2}, {"monthly", "month", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			shop := createTestShop(t, ctx, db, fmt.Sprintf("scheduling-%s-%d", tc.name, time.Now().UnixNano()))
			customer := createTestCustomer(t, ctx, db, shop, "Fictional Scheduling Customer")
			agreement, err := store.CreateAgreement(ctx, shop, CreateAgreementInput{CustomerID: customer, Status: AgreementActive, AnniversaryDate: now.AddDate(1, 0, 0), VisitsEntitled: 5})
			if err != nil {
				t.Fatal(err)
			}
			starts := time.Date(2033, 12, 31, 0, 0, 0, 0, time.UTC)
			schedule, err := store.CreateServiceSchedule(ctx, shop, ServiceScheduleInput{AgreementID: agreement, ServiceName: tc.name, CadenceUnit: tc.unit, CadenceInterval: tc.interval, StartsOn: starts})
			if err != nil {
				t.Fatal(err)
			}
			before, err := store.ListServiceOccurrences(ctx, shop, false)
			if err != nil || len(before) < 3 {
				t.Fatalf("fixture: %v %v", before, err)
			}
			target := before[0]
			newDate := time.Date(2034, 2, 2, 0, 0, 0, 0, time.UTC)
			if err := store.MoveServiceVisit(ctx, shop, target.ID, newDate, 0); err != nil {
				t.Fatal(err)
			}
			after, err := store.ListServiceOccurrences(ctx, shop, false)
			if err != nil || len(after) != len(before) {
				t.Fatalf("regeneration duplicated visit: %d %d %v", len(before), len(after), err)
			}
			byID := map[string]ServiceOccurrence{}
			for _, o := range after {
				byID[o.ID] = o
			}
			for _, o := range before {
				got := byID[o.ID]
				want := o.DueOn
				if o.ID == target.ID {
					want = newDate
				}
				if !got.DueOn.Equal(want) || !got.OriginalDueOn.Equal(o.OriginalDueOn) {
					t.Fatalf("date/identity changed: %+v", got)
				}
			}
			due, err := store.ListServiceOccurrences(ctx, shop, true)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range due {
				if o.ID == target.ID {
					t.Fatal("moved future visit still due")
				}
			}
			export, err := store.ServiceOccurrenceExportRows(ctx, shop, false)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, row := range export {
				if strings.Contains(row.Notes, "due 2034-02-02") {
					found = true
				}
			}
			if !found {
				t.Fatal("export ignored moved date")
			}
			summaries, err := store.ListServiceScheduleSummaries(ctx, shop)
			if err != nil || len(summaries) != 1 {
				t.Fatalf("summaries: %v %v", summaries, err)
			}
			if summaries[0].NextOccurrenceID != after[0].ID || summaries[0].LastCompletedLabel != "No completion recorded" {
				t.Fatalf("incorrect next/last: %+v", summaries[0])
			}
			if err := store.MoveServiceVisit(ctx, shop, target.ID, newDate.AddDate(0, 0, 1), 0); !errors.Is(err, servicework.ErrDateChanged) {
				t.Fatalf("stale date accepted: %v", err)
			}
			if err := store.MoveServiceVisit(ctx, shop, target.ID, before[len(before)-1].DueOn, 1); !errors.Is(err, servicework.ErrDateConflict) {
				t.Fatalf("collision accepted: %v", err)
			}
			// Returning to its original date is a supported adjustment, not a new obligation.
			if err := store.MoveServiceVisit(ctx, shop, target.ID, target.DueOn, 1); err != nil {
				t.Fatal(err)
			}
			visit, err := store.GetServiceVisitDate(ctx, shop, target.ID)
			if err != nil || visit.Revision != 2 || !visit.CurrentOn.Equal(target.DueOn) {
				t.Fatalf("restore original: %+v %v", visit, err)
			}
			if err := store.UpdateServiceOccurrence(ctx, shop, target.ID, "completed", ""); err != nil {
				t.Fatal(err)
			}
			if err := store.UpdateServiceOccurrence(ctx, shop, target.ID, "completed", ""); err != nil {
				t.Fatal(err)
			}
			if err := store.MoveServiceVisit(ctx, shop, target.ID, newDate, 2); !errors.Is(err, servicework.ErrVisitUnavailable) {
				t.Fatalf("completed move accepted: %v", err)
			}
			summaries, err = store.ListServiceScheduleSummaries(ctx, shop)
			if err != nil {
				t.Fatal(err)
			}
			if summaries[0].LastCompletedLabel != "Jan 31, 2034" {
				t.Fatalf("used due date instead of recorded completion: %+v", summaries[0])
			}
			var usage int
			if err := db.Pool.QueryRow(ctx, database.Statement(db.Pool.Dialect(), `SELECT visits_used FROM agreements WHERE id=$1`, `SELECT visits_used FROM dbo.agreements WHERE id=CONVERT(uniqueidentifier,@p1)`), agreement).Scan(&usage); err != nil || usage != 0 {
				t.Fatalf("move changed entitlement: %d %v", usage, err)
			}
			// Independent second service has no inherited completion history.
			_, err = store.CreateServiceSchedule(ctx, shop, ServiceScheduleInput{AgreementID: agreement, ServiceName: "Other service", CadenceUnit: "month", CadenceInterval: 1, StartsOn: now})
			if err != nil {
				t.Fatal(err)
			}
			summaries, err = store.ListServiceScheduleSummaries(ctx, shop)
			if err != nil {
				t.Fatal(err)
			}
			for _, summary := range summaries {
				if summary.ID != schedule && summary.LastCompletedLabel != "No completion recorded" {
					t.Fatal("completion leaked across services")
				}
			}
			foreign := createTestShop(t, ctx, db, fmt.Sprintf("foreign-scheduling-%d", time.Now().UnixNano()))
			if err := store.MoveServiceVisit(ctx, foreign, before[1].ID, newDate, 0); !errors.Is(err, servicework.ErrVisitUnavailable) {
				t.Fatalf("foreign move accepted: %v", err)
			}
			if err := store.MoveServiceVisit(ctx, shop, before[1].ID, starts.AddDate(0, 0, -1), 0); !errors.Is(err, servicework.ErrDateOutsideSchedule) {
				t.Fatalf("out of range date accepted: %v", err)
			}
		})
	}
}

func TestMoveServiceVisitPauseResetsFutureWork(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testMoveServiceVisitPauseResetsFutureWork(t, db)
}
func testMoveServiceVisitPauseResetsFutureWork(t *testing.T, db *DB) {
	ctx := context.Background()
	now := time.Date(2034, 1, 31, 12, 0, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, fmt.Sprintf("move-pause-%d", time.Now().UnixNano()))
	customer := createTestCustomer(t, ctx, db, shop, "Fictional Pause Customer")
	agreement, err := store.CreateAgreement(ctx, shop, CreateAgreementInput{CustomerID: customer, Status: AgreementActive, AnniversaryDate: now.AddDate(1, 0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := store.CreateServiceSchedule(ctx, shop, ServiceScheduleInput{AgreementID: agreement, ServiceName: "Weekly", CadenceUnit: "week", CadenceInterval: 1, StartsOn: now.AddDate(0, 0, -14)})
	if err != nil {
		t.Fatal(err)
	}
	work, err := store.ListServiceOccurrences(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	past, future := work[0], work[len(work)-1]
	if err := store.MoveServiceVisit(ctx, shop, past.ID, now.AddDate(0, 0, 2), 0); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveServiceVisit(ctx, shop, future.ID, now.AddDate(0, 0, -2), 0); err != nil {
		t.Fatal(err)
	}
	if err := store.SetServiceScheduleActive(ctx, shop, schedule, false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{past.ID, future.ID} {
		if _, err := store.GetServiceVisitDate(ctx, shop, id); err == nil {
			t.Fatal("pause kept reset future visit")
		}
	}
	summaries, err := store.ListServiceScheduleSummaries(ctx, shop)
	if err != nil || summaries[0].NextServiceLabel != "Paused" || summaries[0].NextOccurrenceID != "" {
		t.Fatalf("paused summary: %+v %v", summaries, err)
	}
	if err := store.SetServiceScheduleActive(ctx, shop, schedule, true); err != nil {
		t.Fatal(err)
	}
	work, err = store.ListServiceOccurrences(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range work {
		if !o.DueOn.Equal(o.OriginalDueOn) {
			t.Fatal("resume preserved canceled override")
		}
	}
}

func TestPublicDemoVisitDateUsesRealRoutesAndRejectsStaleChanges(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clock := fixedClock{now: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)}
	store := &Store{db: db, clock: clock}
	auth := newAuthWithClock(db, "date-test-secret", false, clock)
	config := securityTestConfig()
	config.EnablePublicDemo = true
	server := &Server{tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")), store: store, auth: auth, config: config, clock: clock, throttle: newAuthThrottle(config.RuntimeLimits)}
	handler := server.handler(fstest.MapFS{})
	demo, session := seedAuthenticatedPublicDemo(t, ctx, store, auth)
	request := func(method, path string, values url.Values) *httptest.ResponseRecorder {
		body := ""
		if values != nil {
			values.Set("csrf_token", "date-csrf")
			body = values.Encode()
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if method == http.MethodPost {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		addDemoCookies(r, session, "date-csrf")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	response := request(http.MethodGet, "/schedule", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Last completed") {
		t.Fatalf("schedule route: %d %s", response.Code, response.Body.String())
	}
	work, err := store.ListServiceOccurrences(ctx, demo.ShopID, true)
	if err != nil || len(work) == 0 {
		t.Fatal(err)
	}
	path := "/service-occurrences/" + work[0].ID + "/date"
	response = request(http.MethodGet, path, nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "This visit only") {
		t.Fatalf("date form: %d", response.Code)
	}
	values := url.Values{"scheduled_on": {"2026-09-09"}, "revision": {"0"}}
	response = request(http.MethodPost, path, values)
	if response.Code != 303 {
		t.Fatalf("demo move failed: %d %s", response.Code, response.Body.String())
	}
	response = request(http.MethodGet, response.Header().Get("Location"), nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Saved: this visit is planned for Sep 9, 2026") {
		t.Fatalf("save feedback: %d %s", response.Code, response.Body.String())
	}
	values.Set("scheduled_on", "2026-09-10")
	response = request(http.MethodPost, path, values)
	if response.Code != 409 || !strings.Contains(response.Body.String(), `value="2026-09-10"`) || !strings.Contains(response.Body.String(), `name="revision" value="1"`) {
		t.Fatalf("stale error lost chosen date: %d %s", response.Code, response.Body.String())
	}
	response = request(http.MethodPost, path+"?scheduled_on=2026-09-11", values)
	if response.Code != 403 {
		t.Fatalf("query injection not rejected: %d", response.Code)
	}
	other, _ := seedAuthenticatedPublicDemo(t, ctx, store, auth)
	foreign, err := store.ListServiceOccurrences(ctx, other.ShopID, true)
	if err != nil || len(foreign) == 0 {
		t.Fatal(err)
	}
	response = request(http.MethodPost, "/service-occurrences/"+foreign[0].ID+"/date", values)
	if response.Code != 404 {
		t.Fatalf("foreign date route exposed: %d", response.Code)
	}
}

func TestMoveServiceVisitImportAndConcurrentChanges(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testMoveServiceVisitImportAndConcurrentChanges(t, db)
}
func TestSQLServerMoveServiceVisitImportAndConcurrentChanges(t *testing.T) {
	testMoveServiceVisitImportAndConcurrentChanges(t, sqlServerTestDB(t))
}
func testMoveServiceVisitImportAndConcurrentChanges(t *testing.T, db *DB) {
	ctx := context.Background()
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, fmt.Sprintf("move-import-%d", time.Now().UnixNano()))
	rows, err := parseImportCSV(strings.NewReader("external_id,customer_name,address_line1,city,state,postal_code,anniversary_date,service_name,cadence_unit,service_start_date\nmove-1,Fictional Schedule Customer,1 Willow Lane,Columbus,OH,43215,2027-09-07,Weekly mowing,week,2026-08-10\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal(err)
	}
	work, err := store.ListServiceOccurrences(ctx, shop, false)
	if err != nil || len(work) < 3 {
		t.Fatalf("fixture %v %v", work, err)
	}
	target := work[0]
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, days := range []int{1, 2} {
		wg.Add(1)
		go func(days int) {
			defer wg.Done()
			<-start
			results <- store.MoveServiceVisit(ctx, shop, target.ID, now.AddDate(0, 0, days), 0)
		}(days)
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, servicework.ErrDateChanged) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent moves success=%d conflict=%d", succeeded, conflicted)
	}
	moved, err := store.GetServiceVisitDate(ctx, shop, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal(err)
	}
	preserved, err := store.GetServiceVisitDate(ctx, shop, target.ID)
	if err != nil || !preserved.CurrentOn.Equal(moved.CurrentOn) || preserved.Revision != 1 {
		t.Fatalf("unchanged import reset date: %+v %v", preserved, err)
	}
	// Reserve an as-yet unmaterialized Monday on this weekly series.
	if err := store.MoveServiceVisit(ctx, shop, target.ID, now.AddDate(0, 0, 182), 1); !errors.Is(err, servicework.ErrDateConflict) {
		t.Fatalf("future cadence collision: %v", err)
	}
	future := work[len(work)-1]
	if err := store.MoveServiceVisit(ctx, shop, future.ID, now.AddDate(0, 0, -2), 0); err != nil {
		t.Fatal(err)
	}
	rows[0].ServiceName = "Updated weekly mowing"
	if _, err := store.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{target.ID, future.ID} {
		if _, err := store.GetServiceVisitDate(ctx, shop, id); err == nil {
			t.Fatal("changed import retained reset future work")
		}
	}
	work, err = store.ListServiceOccurrences(ctx, shop, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range work {
		if !o.DueOn.Equal(o.OriginalDueOn) {
			t.Fatal("import regeneration retained deleted move")
		}
	}
	if err := store.UpdateServiceOccurrence(ctx, shop, work[0].ID, "skipped", ""); err != nil {
		t.Fatal(err)
	}
	summaries, err := store.ListServiceScheduleSummaries(ctx, shop)
	if err != nil || summaries[0].LastCompletedLabel != "No completion recorded" {
		t.Fatalf("skip counted as completion: %+v %v", summaries, err)
	}
	if err := store.MoveServiceVisit(ctx, shop, work[0].ID, now.AddDate(0, 0, 1), 0); !errors.Is(err, servicework.ErrVisitUnavailable) {
		t.Fatalf("skipped move allowed: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, database.Statement(db.Pool.Dialect(), `UPDATE agreements SET status='cancelled' WHERE shop_id=$1`, `UPDATE dbo.agreements SET status='cancelled' WHERE shop_id=CONVERT(uniqueidentifier,@p1)`), shop); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveServiceVisit(ctx, shop, work[1].ID, now.AddDate(0, 0, 1), 0); !errors.Is(err, servicework.ErrVisitUnavailable) {
		t.Fatalf("inactive plan move allowed: %v", err)
	}
}

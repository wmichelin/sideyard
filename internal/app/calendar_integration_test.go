package app

import (
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestCalendarCountsPagingAndTenantBoundaries(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, fmt.Sprintf("calendar-%d", time.Now().UnixNano()))
	other := createTestShop(t, ctx, db, fmt.Sprintf("foreign-calendar-%d", time.Now().UnixNano()))
	makeAgreement := func(shop, name string) string {
		customer := createTestCustomer(t, ctx, db, shop, name)
		id, err := store.CreateAgreement(ctx, shop, CreateAgreementInput{CustomerID: customer, Status: AgreementActive, AnniversaryDate: now.AddDate(1, 0, 0)})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	agreement := makeAgreement(shop, "Fictional Calendar Customer")
	foreignAgreement := makeAgreement(other, "Foreign Customer")
	for i := 0; i < 55; i++ {
		_, err := store.CreateServiceSchedule(ctx, shop, ServiceScheduleInput{AgreementID: agreement, ServiceName: fmt.Sprintf("Service %02d", i), CadenceUnit: "week", CadenceInterval: 1, StartsOn: now})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CreateServiceSchedule(ctx, other, ServiceScheduleInput{AgreementID: foreignAgreement, ServiceName: "Foreign service", CadenceUnit: "week", CadenceInterval: 1, StartsOn: now}); err != nil {
		t.Fatal(err)
	}
	month, end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	first, err := store.ServiceCalendar(ctx, shop, month, end, now, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 55 || len(first.Visits) != 50 || first.Counts["2026-09-07"] != 55 {
		t.Fatalf("first page: total%d visits%d", first.Total, len(first.Visits))
	}
	second, err := store.ServiceCalendar(ctx, shop, month, end, now, 999999)
	if err != nil {
		t.Fatal(err)
	}
	if second.Page != 2 || len(second.Visits) != 5 {
		t.Fatalf("last page clamp: %+v", second)
	}
	seen := map[string]bool{}
	for _, visit := range first.Visits {
		seen[visit.ID] = true
		if visit.CustomerName == "Foreign Customer" {
			t.Fatal("foreign visit leaked")
		}
	}
	for _, visit := range second.Visits {
		if seen[visit.ID] {
			t.Fatal("duplicate across pages")
		}
	}
	movedID := first.Visits[0].ID
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := store.MoveServiceVisit(ctx, shop, movedID, october, 0); err != nil {
		t.Fatal(err)
	}
	old, err := store.ServiceCalendar(ctx, shop, month, end, now, 1)
	if err != nil || old.Total != 54 {
		t.Fatalf("old count did not decrease: %d %v", old.Total, err)
	}
	newMonth, err := store.ServiceCalendar(ctx, shop, october, october.AddDate(0, 1, 0), october, 1)
	if err != nil || newMonth.Total != 1 || newMonth.Visits[0].ID != movedID || newMonth.Visits[0].OriginalDateLabel != "Sep 7, 2026" {
		t.Fatalf("cross-month move: %+v %v", newMonth, err)
	}
	if err := store.UpdateServiceOccurrence(ctx, shop, first.Visits[1].ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateServiceOccurrence(ctx, shop, first.Visits[2].ID, "skipped", ""); err != nil {
		t.Fatal(err)
	}
	visit, err := store.GetServiceVisitDate(ctx, shop, first.Visits[3].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetServiceScheduleActive(ctx, shop, visit.ScheduleID, false); err != nil {
		t.Fatal(err)
	}
	filtered, err := store.ServiceCalendar(ctx, shop, month, end, now, 1)
	if err != nil || filtered.Total != 51 {
		t.Fatalf("final/inactive filtering: %d %v", filtered.Total, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE agreements SET status='cancelled' WHERE id=$1`, agreement); err != nil {
		t.Fatal(err)
	}
	filtered, err = store.ServiceCalendar(ctx, shop, month, end, now, 1)
	if err != nil || filtered.Total != 0 || len(filtered.Visits) != 0 {
		t.Fatalf("inactive agreement included: %+v %v", filtered, err)
	}
}

func TestDashboardCalendarDemoReadsAndDateEditorReturn(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clock := fixedClock{now: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)}
	store := &Store{db: db, clock: clock}
	auth := newAuthWithClock(db, "calendar-demo-test-secret", false, clock)
	config := securityTestConfig()
	config.EnablePublicDemo = true
	server := &Server{tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")), store: store, auth: auth, config: config, clock: clock, throttle: newAuthThrottle(config.RuntimeLimits)}
	handler := server.handler(fstest.MapFS{})
	demo, session := seedAuthenticatedPublicDemo(t, ctx, store, auth)
	request := func(method, path string, values url.Values) *httptest.ResponseRecorder {
		body := ""
		if values != nil {
			values.Set("csrf_token", "calendar-csrf")
			body = values.Encode()
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if method == http.MethodPost {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		addDemoCookies(r, session, "calendar-csrf")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	var beforeGeneration string
	if err := db.Pool.QueryRow(ctx, `SELECT max(generated_through)::text FROM service_schedules WHERE shop_id=$1`, demo.ShopID).Scan(&beforeGeneration); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/dashboard", "/dashboard?month=2030-01&day=2030-01-02", "/dashboard?month=invalid&day=invalid", "/dashboard?month=9999-12&day=9999-12-31"} {
		response := request(http.MethodGet, path, nil)
		if response.Code != 200 || response.Body.Len() > 524288 || !strings.Contains(response.Body.String(), "Service calendar") {
			t.Fatalf("dashboard %s status%d bytes%d", path, response.Code, response.Body.Len())
		}
		assertPublicDemoResponseHeaders(t, response)
		if strings.Contains(path, "2030") && !strings.Contains(response.Body.String(), "beyond the planning window") {
			t.Fatal("future month claimed complete")
		}
	}
	var afterGeneration string
	if err := db.Pool.QueryRow(ctx, `SELECT max(generated_through)::text FROM service_schedules WHERE shop_id=$1`, demo.ShopID).Scan(&afterGeneration); err != nil || afterGeneration != beforeGeneration {
		t.Fatalf("calendar browsing expanded generation: %s→%s %v", beforeGeneration, afterGeneration, err)
	}
	// All reads must leave the sandbox's finite action budget untouched.
	var mutations int
	if err := db.Pool.QueryRow(ctx, `SELECT demo_mutations_used FROM shops WHERE id=$1`, demo.ShopID).Scan(&mutations); err != nil {
		t.Fatal(err)
	}
	if mutations != 0 {
		t.Fatalf("GET consumed actions: %d", mutations)
	}
	work, err := store.ListServiceOccurrences(ctx, demo.ShopID, true)
	if err != nil || len(work) == 0 {
		t.Fatal(err)
	}
	path := "/service-occurrences/" + work[0].ID + "/date"
	response := request(http.MethodGet, path+"?calendar_day=2026-09-07", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Back to calendar") {
		t.Fatalf("calendar editor link: %d", response.Code)
	}
	values := url.Values{"scheduled_on": {"2026-09-09"}, "revision": {"0"}, "calendar_day": {"2026-09-07"}}
	response = request(http.MethodPost, path, values)
	if response.Code != 303 {
		t.Fatalf("calendar date save: %d %s", response.Code, response.Body.String())
	}
	response = request(http.MethodGet, response.Header().Get("Location"), nil)
	if !strings.Contains(response.Body.String(), "View updated calendar") || !strings.Contains(response.Body.String(), "day=2026-09-09") {
		t.Fatal("save lost updated calendar context")
	}
	response = request(http.MethodGet, "/dashboard?day=2026-09-09", nil)
	if !strings.Contains(response.Body.String(), `data-calendar-visit="`+work[0].ID+`"`) {
		t.Fatal("moved visit missing from calendar")
	}
	values.Set("calendar_day", "https://external.invalid")
	response = request(http.MethodPost, path, values)
	if response.Code != 403 {
		t.Fatalf("unvalidated return context allowed: %d", response.Code)
	}
	farDate := time.Date(2030, 1, 2, 0, 0, 0, 0, time.UTC)
	// An open-ended service permits a manual date beyond the materialized window.
	scheduleID, err := store.CreateServiceSchedule(ctx, demo.ShopID, ServiceScheduleInput{AgreementID: work[0].AgreementID, ServiceName: "Open-ended weekly service", CadenceUnit: "week", CadenceInterval: 1, StartsOn: clock.now})
	if err != nil {
		t.Fatal(err)
	}
	all, err := store.ListServiceOccurrences(ctx, demo.ShopID, false)
	if err != nil {
		t.Fatal(err)
	}
	futureID := ""
	for _, visit := range all {
		if visit.ScheduleID == scheduleID {
			futureID = visit.ID
			break
		}
	}
	if futureID == "" {
		t.Fatal("open-ended fixture has no visit")
	}
	if err := store.MoveServiceVisit(ctx, demo.ShopID, futureID, farDate, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE customers SET name=$2 WHERE shop_id=$1 AND id=(SELECT customer_id FROM agreements WHERE shop_id=$1 AND id=$3)`, demo.ShopID, "Calendar <script>alert(1)</script>", work[0].AgreementID); err != nil {
		t.Fatal(err)
	}
	response = request(http.MethodGet, "/dashboard?day=2030-01-02", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `data-calendar-visit="`+futureID+`"`) || !strings.Contains(response.Body.String(), "beyond the planning window") {
		t.Fatal("known future move hidden beyond horizon")
	}
	if !strings.Contains(response.Body.String(), "Calendar &lt;script&gt;") || strings.Contains(response.Body.String(), "<script>alert(1)</script>") {
		t.Fatal("calendar customer content not escaped")
	}
}

package app

import (
	"context"
	"encoding/json"
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

func TestWorkActionsRejectPartialBatchesAndPreserveSelections(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clock := fixedClock{now: time.Date(2034, 9, 7, 12, 0, 0, 0, time.UTC)}
	store := &Store{db: db, clock: clock}
	auth := newAuthWithClock(db, "work-actions-test-secret", false, clock)
	config := securityTestConfig()
	config.RuntimeLimits.MaxRequestBodyBytes = 8 << 20
	server := &Server{tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")), store: store, auth: auth, config: config, clock: clock, throttle: newAuthThrottle(config.RuntimeLimits)}
	handler := server.handler(fstest.MapFS{})
	stamp := time.Now().UnixNano()
	shopID := createTestShop(t, ctx, db, fmt.Sprintf("work-actions-%d", stamp))
	otherShop := createTestShop(t, ctx, db, fmt.Sprintf("other-work-actions-%d", stamp))
	type fixture struct{ agreement, occurrence, visit string }
	makeFixture := func(shop string) fixture {
		customer := createTestCustomer(t, ctx, db, shop, "Fictional Work Customer")
		plan := createTestPlan(t, ctx, db, shop, "Work Plan")
		agreement, err := store.CreateAgreement(ctx, shop, CreateAgreementInput{CustomerID: customer, PlanTemplateID: plan, Status: AgreementActive, AnniversaryDate: clock.now.AddDate(1, 0, 0), VisitsEntitled: 5})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateServiceSchedule(ctx, shop, ServiceScheduleInput{AgreementID: agreement, ServiceName: "Weekly mowing", CadenceUnit: "week", CadenceInterval: 1, StartsOn: clock.now}); err != nil {
			t.Fatal(err)
		}
		work, err := store.ListServiceOccurrences(ctx, shop, true)
		if err != nil || len(work) != 1 {
			t.Fatalf("fixture work: %v %v", work, err)
		}
		visits, err := store.ListVisitDues(ctx, shop, true)
		if err != nil || len(visits) == 0 {
			t.Fatalf("fixture visits: %v %v", visits, err)
		}
		return fixture{agreement, work[0].ID, visits[0].ID}
	}
	own, foreign := makeFixture(shopID), makeFixture(otherShop)
	createTestUser(t, ctx, db, shopID, fmt.Sprintf("work-%d@example.test", stamp))
	var userID string
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM users WHERE shop_id=$1`, shopID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	session, err := auth.createSession(ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string, values url.Values, jsonResponse bool) *httptest.ResponseRecorder {
		values.Set("csrf_token", "work-actions-csrf")
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if jsonResponse {
			request.Header.Set("Accept", "application/json")
		}
		addDemoCookies(request, session, "work-actions-csrf")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	assertUnchanged := func() {
		t.Helper()
		for _, f := range []fixture{own, foreign} {
			var occurrence, visit string
			var used int
			if err := db.Pool.QueryRow(ctx, `SELECT o.status,v.status,a.visits_used FROM agreements a JOIN service_occurrences o ON o.id=$2 JOIN visit_dues v ON v.id=$3 WHERE a.id=$1`, f.agreement, f.occurrence, f.visit).Scan(&occurrence, &visit, &used); err != nil {
				t.Fatal(err)
			}
			if occurrence != "due" || visit != "due" || used != 0 {
				t.Fatalf("rejected batch changed data: %s %s used=%d", occurrence, visit, used)
			}
		}
	}
	missing := "ffffffff-ffff-ffff-ffff-ffffffffffff"
	for _, tc := range []struct {
		name   string
		values url.Values
	}{
		{"empty", url.Values{"action": {"completed"}}},
		{"invalid action", url.Values{"action": {"delete"}, "occurrence_id": {own.occurrence}}},
		{"wrong recurring action", url.Values{"action": {"scheduled"}, "occurrence_id": {own.occurrence}}},
		{"wrong seasonal action", url.Values{"action": {"skipped"}, "visit_id": {own.visit}}},
		{"mixed", url.Values{"action": {"completed"}, "occurrence_id": {own.occurrence}, "visit_id": {own.visit}}},
		{"missing recurring", url.Values{"action": {"completed"}, "occurrence_id": {own.occurrence, missing}}},
		{"foreign recurring", url.Values{"action": {"completed"}, "occurrence_id": {own.occurrence, foreign.occurrence}}},
		{"missing seasonal", url.Values{"action": {"completed"}, "visit_id": {own.visit, missing}}},
		{"foreign seasonal", url.Values{"action": {"completed"}, "visit_id": {own.visit, foreign.visit}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := post("/visits", tc.values, true)
			var payload map[string]any
			if response.Code != http.StatusBadRequest || json.Unmarshal(response.Body.Bytes(), &payload) != nil || payload["error"] == nil || payload["saved"] != nil {
				t.Fatalf("rejected response=%d %s", response.Code, response.Body.String())
			}
			assertUnchanged()
		})
	}
	response := post("/visits?occurrence_id="+own.occurrence, url.Values{"action": {"completed"}}, true)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("query parameter injected mutation: %d", response.Code)
	}
	assertUnchanged()
	response = post("/visits?filter=all", url.Values{"action": {"completed"}, "occurrence_id": {own.occurrence, missing}}, false)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `value="`+own.occurrence+`" class="service-check"`) || !strings.Contains(response.Body.String(), ` checked`) || !strings.Contains(response.Body.String(), `name="filter" value="all"`) || !strings.Contains(response.Body.String(), "Could not save this selection") {
		t.Fatalf("fallback lost view/selection: %d %s", response.Code, response.Body.String())
	}
	assertUnchanged()
	var actionEvents int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM pilot_activation_events WHERE shop_id=$1 AND event_type='first_visit_action'`, shopID).Scan(&actionEvents); err != nil {
		t.Fatal(err)
	}
	if actionEvents != 0 {
		t.Fatal("rejected selections recorded a successful visit action")
	}
	for attempt := 0; attempt < 2; attempt++ {
		response = post("/visits", url.Values{"action": {"completed"}, "occurrence_id": {own.occurrence, own.occurrence}}, true)
		var payload struct {
			Saved         bool
			Message, HTML string
		}
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil || !payload.Saved {
			t.Fatalf("JSON save: %d %s", response.Code, response.Body.String())
		}
		if payload.Message != "Saved: 1 recurring service marked completed." || strings.Contains(payload.HTML, `name="occurrence_id"`) || !strings.Contains(payload.HTML, `data-work-form="recurring"`) || strings.Contains(payload.HTML, `data-work-form="seasonal"`) {
			t.Fatalf("incorrect saved group/count: %+v", payload)
		}
	}
	response = post("/visits", url.Values{"action": {"scheduled"}, "visit_id": {own.visit}}, true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "marked handed off") {
		t.Fatalf("seasonal handoff: %d %s", response.Code, response.Body.String())
	}
	response = post("/visits", url.Values{"action": {"completed"}, "visit_id": {own.visit}, "filter": {"all&forged=1"}}, false)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/visits?filter=due&saved=seasonal-completed" {
		t.Fatalf("fallback redirect=%d %q", response.Code, response.Header().Get("Location"))
	}
	response = post("/visits", url.Values{"action": {"completed"}, "visit_id": {own.visit, own.visit}}, true)
	if response.Code != http.StatusOK {
		t.Fatalf("seasonal retry=%d %s", response.Code, response.Body.String())
	}
	var used int
	if err := db.Pool.QueryRow(ctx, `SELECT visits_used FROM agreements WHERE id=$1`, own.agreement).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if used != 1 {
		t.Fatalf("seasonal retry double-counted entitlement: %d", used)
	}
	response = post("/visits", url.Values{"action": {"scheduled"}, "visit_id": {own.visit}}, true)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("completed visit claimed handed off: %d", response.Code)
	}
	all, err := store.ListServiceOccurrences(ctx, shopID, false)
	if err != nil {
		t.Fatal(err)
	}
	var nextID string
	for _, work := range all {
		if work.Status == "due" {
			nextID = work.ID
			break
		}
	}
	if nextID == "" {
		t.Fatal("fixture has no future work")
	}
	// A failed post-commit render must never tell the caller the save failed.
	server.tmpl = template.Must(template.New("missing-work-form").Parse("unavailable"))
	response = post("/visits", url.Values{"action": {"skipped"}, "occurrence_id": {nextID}, "filter": {"all"}}, true)
	var refreshFailure struct {
		Saved         bool
		HTML, Message string
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &refreshFailure) != nil || !refreshFailure.Saved || refreshFailure.HTML != "" || !strings.Contains(refreshFailure.Message, "marked skipped") {
		t.Fatalf("post-commit refresh failure misreported: %d %s", response.Code, response.Body.String())
	}
	var status string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM service_occurrences WHERE id=$1`, nextID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" {
		t.Fatalf("confirmed save was not persisted: %s", status)
	}
}

func TestPublicDemoWorkActionsRefreshSavedGroupWithinLimits(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clock := fixedClock{now: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)}
	store := &Store{db: db, clock: clock}
	auth := newAuthWithClock(db, "demo-work-test-secret", false, clock)
	config := securityTestConfig()
	config.EnablePublicDemo = true
	config.RuntimeLimits.MaxRequestBodyBytes = 8 << 20
	server := &Server{tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")), store: store, auth: auth, config: config, clock: clock, throttle: newAuthThrottle(config.RuntimeLimits)}
	handler := server.handler(fstest.MapFS{})
	demo, session := seedAuthenticatedPublicDemo(t, ctx, store, auth)
	request := httptest.NewRequest(http.MethodGet, "/visits?filter=all", nil)
	addDemoCookies(request, session, "demo-work-csrf")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.Len() > 524288 {
		t.Fatalf("demo All status=%d bytes=%d", response.Code, response.Body.Len())
	}
	t.Logf("demo All response: %d bytes", response.Body.Len())
	work, err := store.ListServiceOccurrences(ctx, demo.ShopID, true)
	if err != nil || len(work) == 0 {
		t.Fatalf("demo due work: %d, %v", len(work), err)
	}
	values := url.Values{"csrf_token": {"demo-work-csrf"}, "occurrence_id": {work[0].ID}, "action": {"completed"}, "filter": {"due"}}
	request = httptest.NewRequest(http.MethodPost, "/visits?filter=due", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	addDemoCookies(request, session, "demo-work-csrf")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var payload struct {
		Saved bool
		HTML  string
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &payload) != nil || !payload.Saved || !strings.Contains(payload.HTML, `data-work-form="recurring"`) || !strings.Contains(payload.HTML, `value="demo-work-csrf"`) {
		t.Fatalf("demo enhanced save failed: %d %s", response.Code, response.Body.String())
	}
	after, err := store.ListServiceOccurrences(ctx, demo.ShopID, true)
	if err != nil || len(after) != len(work)-1 {
		t.Fatalf("demo completion: before=%d after=%d err=%v", len(work), len(after), err)
	}
}

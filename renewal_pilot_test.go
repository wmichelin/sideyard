package main

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestRenewalTasksKeepFutureDueTodayAndPastDueCyclesActionable(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}

	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopID := createTestShop(t, ctx, db, "pilot-renewals-"+stamp)
	planID := createTestPlan(t, ctx, db, shopID, "Pilot Renewal Plan")
	asOf := time.Now().UTC()
	cases := []struct {
		name        string
		anniversary time.Time
		wantState   string
	}{
		{name: "future", anniversary: asOf.AddDate(0, 0, 30), wantState: "Upcoming"},
		{name: "due today", anniversary: asOf, wantState: "Due today"},
		{name: "past due", anniversary: asOf.AddDate(0, 0, -30), wantState: "Past due"},
	}

	agreementIDs := make(map[string]string, len(cases))
	for _, tc := range cases {
		customerID := createTestCustomer(t, ctx, db, shopID, tc.name+" renewal customer")
		propertyID := createTestProperty(t, ctx, db, shopID, customerID, tc.name+" renewal street")
		if _, err := db.Pool.Exec(ctx, `UPDATE customers SET email = $2, phone = $3 WHERE id = $1`, customerID, tc.name+"@example.test", "555-0100"); err != nil {
			t.Fatal(err)
		}
		agreementID, err := store.CreateAgreement(ctx, shopID, CreateAgreementInput{
			CustomerID: customerID, PropertyID: propertyID, PlanTemplateID: planID, Status: AgreementActive,
			AnniversaryDate: tc.anniversary, VisitsEntitled: 2, PriceCents: 19900,
		})
		if err != nil {
			t.Fatalf("create %s agreement: %v", tc.name, err)
		}
		agreementIDs[tc.name] = agreementID
	}

	if err := store.GenerateRenewalTasks(ctx, shopID, asOf); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		var cycleStart time.Time
		if err := db.Pool.QueryRow(ctx, `SELECT cycle_start_date FROM renewal_tasks WHERE agreement_id = $1 AND milestone_days = 30`, agreementIDs[tc.name]).Scan(&cycleStart); err != nil {
			t.Fatalf("read %s cycle: %v", tc.name, err)
		}
		wantCycle := time.Date(tc.anniversary.Year(), tc.anniversary.Month(), tc.anniversary.Day(), 0, 0, 0, 0, time.UTC)
		if !cycleStart.Equal(wantCycle) {
			t.Fatalf("%s cycle start = %s, want %s", tc.name, cycleStart, wantCycle)
		}
	}

	tasks, err := store.ListRenewalTasks(ctx, shopID)
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]RenewalTask)
	for _, task := range tasks {
		if _, ok := seen[task.AgreementID]; !ok {
			seen[task.AgreementID] = task
		}
	}
	for _, tc := range cases {
		task, ok := seen[agreementIDs[tc.name]]
		if !ok {
			t.Fatalf("%s agreement did not remain actionable in renewal queue", tc.name)
		}
		if task.DueState != tc.wantState || task.CustomerEmail == "" || task.CustomerPhone == "" || task.PropertyAddress == "" {
			t.Fatalf("%s task = %+v, want state %q with contact and property", tc.name, task, tc.wantState)
		}
	}
}

func TestRenewalMutationErrorsRenderAndProductionRenewalHasNoPaymentCTA(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopID := createTestShop(t, ctx, db, "renewal-render-"+stamp)

	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store, tmpl: tmpl}
	form := url.Values{"task_id": {"missing-task"}, "status": {"open"}}
	request := httptest.NewRequest(http.MethodPost, "/renewals", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request = request.WithContext(contextWithUser(request.Context(), &User{ShopID: shopID}))
	response := httptest.NewRecorder()
	server.handleRenewals(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Could not save this renewal outcome") {
		t.Fatalf("renewal mutation response = %d %q", response.Code, response.Body.String())
	}

	var page bytes.Buffer
	if err := tmpl.ExecuteTemplate(&page, "renewal.html", map[string]any{
		"Renewal":  HomeownerRenewal{CustomerName: "Pilot Owner", ShopName: "Pilot HVAC", PlanName: "Annual Care"},
		"PriceFmt": "$199.00", "AnniversaryFmt": "Apr 15, 2034", "TestPaymentStub": false,
	}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page.String(), "Continue to test payment") || !strings.Contains(page.String(), "Contact Pilot HVAC to renew your plan") {
		t.Fatalf("production renewal page did not present the contact-only state: %q", page.String())
	}
}

func TestRenewalLinkCreationReturnsJSONForInPlaceSubmission(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	store := &Store{db: db}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopID := createTestShop(t, ctx, db, "renewal-link-json-"+stamp)
	customerID := createTestCustomer(t, ctx, db, shopID, "Renewal Link Customer")
	agreementID, err := store.CreateAgreement(ctx, shopID, CreateAgreementInput{
		CustomerID: customerID, Status: AgreementActive, AnniversaryDate: time.Now().UTC().AddDate(0, 1, 0),
		VisitsEntitled: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	auth := newAuth(db, "test-session-secret", false)
	server := &Server{store: store, auth: auth}
	request := httptest.NewRequest(http.MethodPost, "/agreements/"+agreementID+"/renewal-link", nil)
	request.Header.Set("Accept", "application/json")
	request.SetPathValue("id", agreementID)
	request = request.WithContext(contextWithUser(request.Context(), &User{ShopID: shopID}))
	response := httptest.NewRecorder()
	server.handleAgreementRenewalLink(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("JSON renewal link response = %d, want %d", response.Code, http.StatusCreated)
	}

	var payload struct {
		RenewalURL string `json:"renewal_url"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode JSON renewal link response: %v", err)
	}
	token := strings.TrimPrefix(payload.RenewalURL, "/renew/")
	if token == "" || token == payload.RenewalURL {
		t.Fatalf("renewal URL = %q, want /renew/<token>", payload.RenewalURL)
	}
	if _, err := store.GetHomeownerRenewal(ctx, hashToken(token, auth.sessionSecret)); err != nil {
		t.Fatalf("JSON response token did not resolve to a renewal: %v", err)
	}
}

func TestRenewalAndVisitTemplatesUseAccessiblePilotLanguage(t *testing.T) {
	renewals, err := templateFS.ReadFile("templates/renewals.html")
	if err != nil {
		t.Fatal(err)
	}
	agreements, err := templateFS.ReadFile("templates/agreements.html")
	if err != nil {
		t.Fatal(err)
	}
	visits, err := templateFS.ReadFile("templates/visits.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(renewals), `href="/customers/{{ .CustomerID }}"`) || !strings.Contains(string(renewals), `href="/agreements/{{ .AgreementID }}"`) {
		t.Fatal("renewal workbench lacks semantic customer/agreement links")
	}
	if strings.Contains(string(agreements), "onclick=") || !strings.Contains(string(agreements), `href="/agreements/{{ .ID }}"`) {
		t.Fatal("agreement list still relies on click-only rows")
	}
	if !strings.Contains(string(visits), "Mark handed off to Jobber") || visitStatusLabel(VisitScheduled) != "Handed off to Jobber" {
		t.Fatal("visit handoff language is not truthful")
	}
}

func TestHomeownerRenewalUsesScopedCardSpacing(t *testing.T) {
	renewal, err := templateFS.ReadFile("templates/renewal.html")
	if err != nil {
		t.Fatal(err)
	}
	agreementForm, err := templateFS.ReadFile("templates/agreement_form.html")
	if err != nil {
		t.Fatal(err)
	}
	styles, err := staticFS.ReadFile("static/style.css")
	if err != nil {
		t.Fatal(err)
	}
	renewalLinkScript, err := staticFS.ReadFile("static/renewal-link.js")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(renewal), `class="form-panel renewal-card"`) {
		t.Fatal("homeowner renewal template is missing its scoped card class")
	}
	if !strings.Contains(string(agreementForm), `class="renewal-link-actions"`) {
		t.Fatal("agreement editor homeowner renewal action is missing its scoped spacing class")
	}
	if !strings.Contains(string(agreementForm), `class="panel renewal-link-panel"`) {
		t.Fatal("agreement editor homeowner renewal panel is missing its scoped spacing class")
	}
	if !strings.Contains(string(agreementForm), "data-renewal-link-form") || !strings.Contains(string(agreementForm), "/static/renewal-link.js") {
		t.Fatal("agreement editor renewal link does not support in-place creation")
	}
	for _, want := range []string{"event.preventDefault()", "fetch(renewalLinkForm.action", "Accept: \"application/json\"", "credentials: \"same-origin\""} {
		if !strings.Contains(string(renewalLinkScript), want) {
			t.Fatalf("renewal link in-place submission is missing %q", want)
		}
	}
	for _, want := range []string{
		".renewal-card {",
		"max-width: 42rem;",
		"padding: 1.25rem;",
		".renewal-card .section-title {",
		"padding: 0 0 1rem;",
		".renewal-link-actions {",
		"margin-top: 1rem;",
		".renewal-link-panel .section-title {",
		"padding: 0 0 1rem;",
		"border-bottom: 1px solid var(--border);",
		".renewal-link-panel .empty-note {",
		"padding-top: 1.25rem;",
	} {
		if !strings.Contains(string(styles), want) {
			t.Fatalf("homeowner renewal spacing contract is missing %q", want)
		}
	}
}

package app

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

type contractFailingWritesPool struct {
	database.Pool
	err error
}

func (p contractFailingWritesPool) QueryRow(ctx context.Context, q string, args ...any) database.Row {
	if strings.HasPrefix(strings.TrimSpace(q), "SELECT") {
		return p.Pool.QueryRow(ctx, q, args...)
	}
	return unavailableAuthRow{p.err}
}
func (p contractFailingWritesPool) Exec(context.Context, string, ...any) (database.Result, error) {
	return nil, p.err
}
func (p contractFailingWritesPool) Begin(context.Context) (database.Tx, error) { return nil, p.err }

func TestContractReadFailuresStayDistinctFromMissingRecords(t *testing.T) {
	shop := "12345678-1234-1234-1234-123456789012"
	id := "12345678-1234-1234-1234-123456789013"
	for _, tc := range []struct {
		err    error
		status int
	}{{errors.New("internal contract dependency failure"), 503}, {database.ErrNoRows, 404}} {
		server := &Server{store: &Store{db: &DB{Pool: &unavailableAuthPool{err: tc.err}}}}
		for _, handle := range []http.HandlerFunc{server.handlePlanTemplateEdit, server.handleAgreementEdit, server.handleServiceScheduleCreate} {
			r := httptest.NewRequest("GET", "/test/"+id, nil)
			r.SetPathValue("id", id)
			r = r.WithContext(contextWithUser(r.Context(), &User{ShopID: shop, Role: "staff"}))
			w := httptest.NewRecorder()
			handle(w, r)
			if w.Code != tc.status || strings.Contains(w.Body.String(), tc.err.Error()) {
				t.Fatalf("read: %d %s", w.Code, w.Body.String())
			}
		}
	}
}

func TestContractFormsRejectInvalidNumbersAndUnconfirmedSaves(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testContractFormsRejectInvalidNumbersAndUnconfirmedSaves(t, db)
}
func testContractFormsRejectInvalidNumbersAndUnconfirmedSaves(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	store := &Store{db: db}
	shop := createTestShop(t, ctx, db, "contract-forms-"+time.Now().Format("150405.000000000"))
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	plan := createTestPlan(t, ctx, db, shop, "Original")
	in := CreateAgreementInput{CustomerID: customer, PlanTemplateID: plan, Status: AgreementActive, AnniversaryDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), PriceCents: 123, VisitsEntitled: 2}
	agreement, err := store.CreateAgreement(ctx, shop, in)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("internal contract write failure")
	server := &Server{store: &Store{db: &DB{Pool: contractFailingWritesPool{Pool: db.Pool, err: failure}}}, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	planForm := url.Values{"name": {"Edited"}, "description": {"Keep my description"}, "price_cents": {"987"}, "term_months": {"12"}, "visits_per_year": {"3"}}
	agreementForm := url.Values{"customer_id": {customer}, "plan_template_id": {plan}, "status": {"active"}, "anniversary_date": {"2027-01-01"}, "visits_entitled": {"3"}, "visits_used": {"1"}, "price_cents": {"987"}, "notes": {"Keep my notes"}}
	for _, tc := range []struct {
		path, id string
		handle   http.HandlerFunc
		form     url.Values
		numeric  []string
	}{
		{"/plan-templates", "", server.handlePlanTemplates, planForm, []string{"price_cents", "term_months", "visits_per_year"}},
		{"/plan-templates/" + plan, plan, server.handlePlanTemplateEdit, planForm, []string{"price_cents", "term_months", "visits_per_year"}},
		{"/agreements", "", server.handleAgreementCreate, agreementForm, []string{"price_cents", "visits_entitled", "visits_used"}},
		{"/agreements/" + agreement, agreement, server.handleAgreementUpdate, agreementForm, []string{"price_cents", "visits_entitled", "visits_used"}},
	} {
		request := func(form url.Values, status int) {
			t.Helper()
			r := httptest.NewRequest("POST", tc.path, strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", tc.id)
			r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "staff"}))
			w := httptest.NewRecorder()
			tc.handle(w, r)
			if w.Code != status || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), failure.Error()) {
				t.Fatalf("%s: %d %s", tc.path, w.Code, w.Body.String())
			}
		}
		request(tc.form, 503)
		for _, field := range tc.numeric {
			for _, bad := range []string{"not-a-number", "1.5", "2147483648", "-2147483649"} {
				form := url.Values{}
				for k, v := range tc.form {
					form[k] = append([]string(nil), v...)
				}
				form.Set(field, bad)
				request(form, 422)
			}
		}
	}
	got, err := store.GetAgreement(ctx, shop, agreement)
	if err != nil || got.PriceCents != 123 || got.VisitsEntitled != 2 || got.Notes != "" {
		t.Fatal("failed form changed agreement")
	}
	p, err := store.GetPlanTemplate(ctx, shop, plan)
	if err != nil || p.Name != "Original" || p.PriceCents != 0 {
		t.Fatal("failed form changed template")
	}

	// Ordinary creation validation keeps the owner's notes in the populated form.
	server.store = store
	for _, values := range []url.Values{
		{"customer_id": {customer}, "anniversary_date": {"invalid"}, "notes": {"Keep submitted notes"}},
		{"anniversary_date": {"2027-01-01"}, "notes": {"Keep submitted notes"}},
	} {
		r := httptest.NewRequest("POST", "/agreements", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "staff"}))
		w := httptest.NewRecorder()
		server.handleAgreementCreate(w, r)
		if w.Code != 422 || !strings.Contains(w.Body.String(), "Keep submitted notes") || !strings.HasPrefix(w.Result().Header.Get("Content-Type"), "text/html") {
			t.Fatalf("retained validation: %d %s", w.Code, w.Body.String())
		}
	}
	// New template optional number blanks still mean zero price/visits and 12 months.
	server.store = store
	r := httptest.NewRequest("POST", "/plan-templates", strings.NewReader(url.Values{"name": {"Blank defaults"}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "staff"}))
	w := httptest.NewRecorder()
	server.handlePlanTemplates(w, r)
	if w.Code != 303 {
		t.Fatalf("blank defaults: %d %s", w.Code, w.Body.String())
	}
	plans, err := store.ListPlanTemplates(ctx, shop)
	if err != nil || len(plans) != 2 {
		t.Fatal("blank plan missing")
	}
	for _, p := range plans {
		if p.Name == "Blank defaults" && (p.PriceCents != 0 || p.VisitsPerYear != 0 || p.TermMonths != 12) {
			t.Fatal("blank defaults changed")
		}
	}
}

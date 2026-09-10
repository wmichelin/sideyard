package app

import (
	"context"
	"errors"
	"github.com/wmichelin/clubdue/internal/database"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type unavailableRenewalQueuePool struct{ contractFailingWritesPool }

func (p unavailableRenewalQueuePool) Query(context.Context, string, ...any) (database.Rows, error) {
	return nil, p.err
}

func TestRenewalPublicHandlersDistinguishMissingAndUnavailable(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{database.ErrNoRows, 404}, {errors.New("private database hostname and failure"), 503}} {
		db := &DB{Pool: &unavailableAuthPool{err: tc.err}}
		s := &Server{store: &Store{db: db}, auth: newAuth(db, "synthetic-secret", false)}
		for _, handle := range []http.HandlerFunc{s.handleHomeownerRenewal, s.handleAgreementRenewalLink} {
			r := httptest.NewRequest("POST", "/test", nil)
			r.SetPathValue("token", "never-display-token")
			r.SetPathValue("id", "12345678-1234-1234-1234-123456789013")
			r = r.WithContext(contextWithUser(r.Context(), &User{ShopID: "12345678-1234-1234-1234-123456789012"}))
			w := httptest.NewRecorder()
			handle(w, r)
			if w.Code != tc.status || w.Header().Get("Cache-Control") != "private, no-store" || strings.Contains(w.Body.String(), tc.err.Error()) || strings.Contains(w.Body.String(), "never-display-token") {
				t.Fatalf("public error %d %s", w.Code, w.Body.String())
			}
		}
	}
}
func TestRenewalQueueRetainsDraftAndUnconfirmedFeedback(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testRenewalQueueRetainsDraftAndUnconfirmedFeedback(t, db)
}
func TestSQLServerRenewalQueueRetainsDraftAndUnconfirmedFeedback(t *testing.T) {
	testRenewalQueueRetainsDraftAndUnconfirmedFeedback(t, sqlServerTestDB(t))
}
func testRenewalQueueRetainsDraftAndUnconfirmedFeedback(t *testing.T, db *DB) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}
	store := &Store{db: db, clock: clock}
	shop := createTestShop(t, ctx, db, "renewal-feedback-"+time.Now().Format("150405.000000000"))
	customer := createTestCustomer(t, ctx, db, shop, "Owner")
	a, err := store.CreateAgreement(ctx, shop, CreateAgreementInput{CustomerID: customer, Status: AgreementActive, AnniversaryDate: clock.now, VisitsEntitled: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.GenerateRenewalTasks(ctx, shop, clock.now); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListRenewalTasks(ctx, shop)
	if err != nil || len(tasks) == 0 {
		t.Fatal(err)
	}
	failure := errors.New("private write failure")
	s := &Server{store: &Store{db: &DB{Pool: contractFailingWritesPool{Pool: db.Pool, err: failure}}, clock: clock}, clock: clock, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")), auth: newAuth(db, "synthetic-secret", false)}
	request := func(handle http.HandlerFunc, path string, form url.Values) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", a)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "staff"}))
		w := httptest.NewRecorder()
		handle(w, r)
		return w
	}
	form := url.Values{"task_id": {tasks[0].ID}, "status": {"contacted"}, "notes": {"Keep my call notes <private>"}}
	w := request(s.handleRenewals, "/renewals", form)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Could not confirm") || !strings.Contains(w.Body.String(), "Keep my call notes &lt;private&gt;") || !strings.Contains(w.Body.String(), `value="contacted" selected`) || strings.Contains(w.Body.String(), failure.Error()) {
		t.Fatalf("draft lost %d %s", w.Code, w.Body.String())
	}
	s.store = &Store{db: &DB{Pool: unavailableRenewalQueuePool{contractFailingWritesPool{Pool: db.Pool, err: failure}}}, clock: clock}
	w = request(s.handleRenewals, "/renewals", form)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Could not confirm") || !strings.Contains(w.Body.String(), "queue is temporarily unavailable") || strings.Contains(w.Body.String(), failure.Error()) {
		t.Fatalf("fallback lost save uncertainty %d %s", w.Code, w.Body.String())
	}
	s.store = store
	s.config.Environment = EnvironmentProduction
	w = request(s.handleAgreementRenewalLink, "/agreements/"+a+"/renewal-link", nil)
	if w.Code != 303 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("non-JS create", w.Code)
	}
	target, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", target.String(), nil)
	r.SetPathValue("id", a)
	r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "staff"}))
	w = httptest.NewRecorder()
	s.handleAgreementEdit(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Body.String(), target.Query().Get("renewal_token")) {
		t.Fatalf("bearer destination not private %d", w.Code)
	}
	token := target.Query().Get("renewal_token")
	r = httptest.NewRequest("GET", "/renew/"+token, nil)
	r.SetPathValue("token", token)
	w = httptest.NewRecorder()
	s.handleHomeownerRenewal(w, r)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(w.Body.String(), "Plan price") || strings.Contains(w.Body.String(), "Continue to test payment") {
		t.Fatal("public renewal", w.Code)
	}
	r = httptest.NewRequest("POST", "/renew/"+token+"/pay", nil)
	r.SetPathValue("token", token)
	w = httptest.NewRecorder()
	s.handleHomeownerRenewalPayment(w, r)
	if w.Code != 404 {
		t.Fatal("live payment unexpectedly enabled", w.Code)
	}
	got, err := store.GetAgreement(ctx, shop, a)
	if err != nil || got.VisitsUsed != 0 || !got.AnniversaryDate.Equal(time.Date(2034, 9, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("public page/payment changed agreement", err)
	}
}

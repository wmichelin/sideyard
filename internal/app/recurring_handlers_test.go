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
	"sync"
	"testing"
	"time"
)

type onceBeginFailingPool struct {
	database.Pool
	mu     sync.Mutex
	failed bool
	err    error
}

func (p *onceBeginFailingPool) Begin(ctx context.Context) (database.Tx, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.failed {
		p.failed = true
		return nil, p.err
	}
	return p.Pool.Begin(ctx)
}

func TestRecurringHandlersDistinguishMissingAndUnavailable(t *testing.T) {
	shop := "12345678-1234-1234-1234-123456789012"
	id := "12345678-1234-1234-1234-123456789013"
	for _, tc := range []struct {
		err    error
		status int
	}{{errors.New("internal recurring dependency"), 503}, {database.ErrNoRows, 404}} {
		server := &Server{store: &Store{db: &DB{Pool: &unavailableAuthPool{err: tc.err}}}}
		for _, route := range []struct {
			handle http.HandlerFunc
			method string
			form   url.Values
		}{{server.handleVisitDate, "GET", nil}, {server.handleServiceScheduleUpdate, "POST", url.Values{"action": {"pause"}}}} {
			r := httptest.NewRequest(route.method, "/test/"+id, strings.NewReader(route.form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", id)
			r = r.WithContext(contextWithUser(r.Context(), &User{ShopID: shop, Role: "staff"}))
			w := httptest.NewRecorder()
			route.handle(w, r)
			if w.Code != tc.status || strings.Contains(w.Body.String(), tc.err.Error()) {
				t.Fatalf("read/change status %d %s", w.Code, w.Body.String())
			}
		}
	}
}
func TestRecurringFormsPreserveInputAndUnconfirmedOutcomes(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testRecurringFormsPreserveInputAndUnconfirmedOutcomes(t, db)
}
func TestSQLServerRecurringFormsPreserveInputAndUnconfirmedOutcomes(t *testing.T) {
	testRecurringFormsPreserveInputAndUnconfirmedOutcomes(t, sqlServerTestDB(t))
}
func testRecurringFormsPreserveInputAndUnconfirmedOutcomes(t *testing.T, db *DB) {
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}
	store := &Store{db: db, clock: clock}
	shop := createTestShop(t, ctx, db, "recurring-form-"+time.Now().Format("150405.000000000"))
	a := createSeasonalTestAgreement(t, db, store, shop, []string{}, 2, 0)
	server := &Server{store: store, clock: clock, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	request := func(handle http.HandlerFunc, id, path string, form url.Values, json bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if json {
			r.Header.Set("Accept", "application/json")
		}
		r.SetPathValue("id", id)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "staff"}))
		w := httptest.NewRecorder()
		handle(w, r)
		return w
	}
	form := url.Values{"service_name": {"Keep service name"}, "cadence_unit": {"month"}, "cadence_interval": {"2147483648"}, "starts_on": {"2034-09-09"}, "ends_on": {"2035-09-09"}, "counts_toward_entitlement": {"1"}}
	w := request(server.handleServiceScheduleCreate, a, "/agreements/"+a+"/service-schedules", form, false)
	if w.Code != 422 || !strings.Contains(w.Body.String(), `value="Keep service name"`) || !strings.Contains(w.Body.String(), `value="2147483648"`) || !strings.Contains(w.Body.String(), `value="month" selected`) || !strings.Contains(w.Body.String(), `value="2035-09-09"`) {
		t.Fatalf("validation lost input %d %s", w.Code, w.Body.String())
	}
	form.Set("cadence_interval", "1")
	failure := errors.New("internal recurring write failure")
	server.store = &Store{db: &DB{Pool: contractFailingWritesPool{Pool: db.Pool, err: failure}}, clock: clock}
	w = request(server.handleServiceScheduleCreate, a, "/agreements/"+a+"/service-schedules", form, false)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Could not confirm") || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), failure.Error()) {
		t.Fatalf("unconfirmed create %d %s", w.Code, w.Body.String())
	}
	server.store = store
	_, id, rows := recurringFixture(t, db, store, shop, 2, false)
	server.store = &Store{db: &DB{Pool: contractFailingWritesPool{Pool: db.Pool, err: failure}}, clock: clock}
	w = request(server.handleServiceScheduleUpdate, id, "/service-schedules/"+id, url.Values{"action": {"pause"}}, false)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Could not confirm") {
		t.Fatalf("unconfirmed pause %d %s", w.Code, w.Body.String())
	}
	w = request(server.handleVisitDate, rows[0].ID, "/service-occurrences/"+rows[0].ID+"/date", url.Values{"scheduled_on": {"2034-09-10"}, "revision": {"0"}}, false)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `value="2034-09-10"`) || !strings.Contains(w.Body.String(), "Could not confirm") {
		t.Fatalf("unconfirmed move lost date %d %s", w.Code, w.Body.String())
	}
	w = request(server.handleVisits, "", "/visits", url.Values{"occurrence_id": {rows[0].ID}, "action": {"completed"}}, true)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Could not confirm") || strings.Contains(w.Body.String(), `"saved":true`) {
		t.Fatalf("unconfirmed completion %d %s", w.Code, w.Body.String())
	}
	w = request(server.handleVisits, "", "/visits", url.Values{"occurrence_id": {rows[0].ID}, "action": {"completed"}}, false)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Could not confirm") || strings.Contains(w.Body.String(), "selection is kept") {
		t.Fatalf("fallback lost honest save status %d %s", w.Code, w.Body.String())
	}

	server.store = store
	createSeasonalTestAgreement(t, db, store, shop, []string{"fall"}, 2, 0)
	if err := store.SyncVisitDues(ctx, shop); err != nil {
		t.Fatal(err)
	}
	seasonal, err := store.ListVisitDues(ctx, shop, false)
	if err != nil || len(seasonal) == 0 {
		t.Fatal("missing seasonal outage fixture", err)
	}
	server.store = &Store{db: &DB{Pool: &onceBeginFailingPool{Pool: db.Pool, err: failure}}, clock: clock}
	w = request(server.handleVisits, "", "/visits?filter=all", url.Values{"visit_id": {seasonal[0].ID}, "action": {"completed"}, "filter": {"all"}}, false)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Could not confirm") || !strings.Contains(w.Body.String(), `value="`+seasonal[0].ID+`"`) || !strings.Contains(w.Body.String(), "checked") || w.Header().Get("Location") != "" {
		t.Fatalf("seasonal outage lost status/selection %d %s", w.Code, w.Body.String())
	}

}

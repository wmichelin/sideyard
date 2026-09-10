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
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/jobs"
)

func TestCustomerAndNewJobReadsDistinguishMissingFromOutage(t *testing.T) {
	const id = "12345678-1234-1234-1234-123456789012"
	for _, failure := range []error{database.ErrNoRows, errors.New("synthetic private database failure")} {
		s := &Server{store: &Store{db: &DB{Pool: &unavailableAuthPool{err: failure}}}}
		for _, tc := range []struct {
			path    string
			handler http.HandlerFunc
		}{
			{"/customers/" + id, s.handleCustomerEdit},
			{"/jobs/new?customer_id=" + id, s.handleJobNew},
		} {
			r := httptest.NewRequest("GET", tc.path, nil)
			r.SetPathValue("id", id)
			r = r.WithContext(contextWithUser(context.Background(), &User{ShopID: id, Role: "owner"}))
			w := httptest.NewRecorder()
			tc.handler(w, r)
			want := 503
			if errors.Is(failure, database.ErrNoRows) {
				want = 404
			}
			if w.Code != want || strings.Contains(w.Body.String(), failure.Error()) {
				t.Fatalf("customer availability: got%d want%d", w.Code, want)
			}
		}
	}
}

func TestStandaloneJobsPreserveIdentityHistoryAndIsolation(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testStandaloneJobsPreserveIdentityHistoryAndIsolation(t, db)
}

func testStandaloneJobsPreserveIdentityHistoryAndIsolation(t *testing.T, db *DB) {
	ctx := context.Background()
	now := time.Date(2034, 6, 1, 12, 30, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	service := store.jobsService()
	stamp := fmt.Sprintf("%d", time.Now().UnixNano())
	shop := createTestShop(t, ctx, db, "jobs-"+stamp)
	other := createTestShop(t, ctx, db, "jobs-other-"+stamp)
	customer := createTestCustomer(t, ctx, db, shop, "One-time customer")
	customer2 := createTestCustomer(t, ctx, db, shop, "Other customer")
	foreign := createTestCustomer(t, ctx, db, other, "Foreign customer")
	property := createTestProperty(t, ctx, db, shop, customer, "12 First St")
	wrongProperty := createTestProperty(t, ctx, db, shop, customer2, "14 Other St")
	in := jobs.Input{CustomerID: customer, PropertyID: property, Kind: "survey", Title: "Inspect panel", Details: "Private access instructions"}
	key := strings.Repeat("a", 32)
	type result struct {
		id  string
		err error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() { id, err := service.Create(ctx, shop, key, "first-owner", in); results <- result{id, err} }()
	}
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.id != second.id {
		t.Fatalf("create retries: %+v %+v", first, second)
	}
	id := first.id
	r, err := service.Get(ctx, shop, id)
	if err != nil || r.Revision != 1 || r.Date != "" || r.Status != "planned" || r.Kind != "survey" {
		t.Fatalf("created record %+v %v", r, err)
	}
	history, err := service.History(ctx, shop, id)
	if err != nil || len(history) != 1 || history[0].Action != "created" || history[0].Author != "first-owner" {
		t.Fatal("initial history", history, err)
	}
	for _, bad := range []jobs.Input{{CustomerID: foreign, Title: "Foreign", Kind: "job"}, {CustomerID: customer, PropertyID: wrongProperty, Title: "Wrong property", Kind: "job"}} {
		if _, err := service.Create(ctx, shop, strings.Repeat("b", 32), "owner", bad); !errors.Is(err, jobs.ErrInput) {
			t.Fatal("bad parent", err)
		}
	}
	changed := in
	changed.Title = "Changed retry"
	if _, err := service.Create(ctx, shop, key, "first-owner", changed); !errors.Is(err, jobs.ErrSubmission) {
		t.Fatal("changed create retry", err)
	}
	if _, err := service.Get(ctx, other, id); !errors.Is(err, database.ErrNoRows) {
		t.Fatal("foreign read", err)
	}
	if err := service.Edit(ctx, other, id, "foreign", 1, in); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("foreign edit", err)
	}
	if err := service.SetOutcome(ctx, other, id, "foreign", "completed", 1); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("foreign outcome", err)
	}
	if h, err := service.History(ctx, other, id); err != nil || len(h) != 0 {
		t.Fatal("foreign history", err)
	}
	scheduled := in
	scheduled.Date = "2034-06-03"
	scheduled.Kind = "job"
	scheduled.Title = "Replace panel"
	if err := service.Edit(ctx, shop, id, "second-owner", 1, scheduled); err != nil {
		t.Fatal(err)
	}
	if err := service.Edit(ctx, shop, id, "stale-owner", 1, in); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("stale edit", err)
	}
	if err := service.SetOutcome(ctx, shop, id, "stale-owner", "completed", 1); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("stale completion", err)
	}
	retryID, err := service.Create(ctx, shop, key, "first-owner", in)
	if err != nil || retryID != id {
		t.Fatal("original create retry after edit", err)
	}
	if err := service.SetOutcome(ctx, shop, id, "worker-owner", "completed", 2); err != nil {
		t.Fatal(err)
	}
	if err := service.SetOutcome(ctx, shop, id, "worker-owner", "completed", 2); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("duplicate completion", err)
	}
	if err := service.Edit(ctx, shop, id, "owner", 3, in); !errors.Is(err, jobs.ErrConflict) {
		t.Fatal("closed edit", err)
	}
	r, err = service.Get(ctx, shop, id)
	if err != nil || r.CompletedAt == nil || !r.CompletedAt.Equal(now) {
		t.Fatal("completion time", r, err)
	}
	if err := service.SetOutcome(ctx, shop, id, "owner", "reopened", 3); err != nil {
		t.Fatal(err)
	}
	if err := service.SetOutcome(ctx, shop, id, "owner", "cancelled", 4); err != nil {
		t.Fatal(err)
	}
	history, err = service.History(ctx, shop, id)
	if err != nil || len(history) != 5 || history[2].Action != "completed" || history[4].Snapshot.Date != "" || history[3].Snapshot.Date != "2034-06-03" || history[4].Snapshot.Title != "Inspect panel" {
		t.Fatalf("history lost: %+v %v", history, err)
	}
	open, err := service.List(ctx, shop, "open")
	if err != nil || len(open) != 0 {
		t.Fatal("closed job in open view", err)
	}
	in.Title = "Independent job"
	otherID, err := service.Create(ctx, shop, strings.Repeat("c", 32), "owner", in)
	if err != nil || otherID == id {
		t.Fatal("second job", err)
	}
	if db.Pool.Dialect() == database.Postgres {
		for _, table := range []string{"agreements", "service_occurrences", "visit_dues"} {
			var count int
			if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE shop_id=$1", shop).Scan(&count); err != nil || count != 0 {
				t.Fatal("jobs created legacy work", table, count, err)
			}
		}
	}

	// Direct writes are constrained too; customer removal cannot silently erase jobs.
	if _, err := db.Pool.Exec(ctx, database.Statement(db.Pool.Dialect(), `UPDATE jobs SET property_id=$2 WHERE id=$1`, `UPDATE dbo.jobs SET property_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1)`), otherID, wrongProperty); err == nil {
		t.Fatal("database accepted other customer's property")
	}
	if err := store.DeleteCustomer(ctx, shop, customer); err == nil {
		t.Fatal("customer deletion lost jobs")
	}
	server := &Server{store: store, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	request := func(method, path, tenant, role string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.SetPathValue("id", otherID)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: tenant, Role: role, Email: "owner"}))
		w := httptest.NewRecorder()
		switch path {
		case "/jobs":
			if method == "POST" {
				server.handleJobNew(w, r)
			} else {
				server.handleJobs(w, r)
			}
		case "/jobs/new":
			server.handleJobNew(w, r)
		default:
			server.handleJob(w, r)
		}
		return w
	}
	for _, path := range []string{"/jobs", "/jobs/new", "/jobs/" + otherID} {
		for _, method := range []string{"GET", "POST"} {
			if w := request(method, path, shop, "staff", nil); w.Code != 403 {
				t.Fatal("staff route", path, w.Code)
			}
		}
	}
	if w := request("GET", "/jobs/"+otherID, other, "owner", nil); w.Code != 404 {
		t.Fatal("foreign route", w.Code)
	}
	form := url.Values{"action": {"edit"}, "revision": {"1"}, "kind": {"job"}, "title": {"Keep <script>draft</script>"}, "scheduled_on": {"bad-date"}, "details": {"Entered details"}}
	w := request("POST", "/jobs/"+otherID, shop, "owner", form)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "Keep &lt;script&gt;draft&lt;/script&gt;") || !strings.Contains(w.Body.String(), "Entered details") {
		t.Fatal("validation loses input", w.Code, w.Body.String())
	}
	form.Set("scheduled_on", "2034-06-02")
	w = request("POST", "/jobs/"+otherID, shop, "owner", form)
	if w.Code != 303 {
		t.Fatal("valid edit", w.Code, w.Body.String())
	}
	w = request("POST", "/jobs/"+otherID, shop, "owner", form)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Entered details") {
		t.Fatal("stale UI", w.Code)
	}
	w = request("GET", "/jobs/"+otherID, shop, "owner", nil)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("private job", w.Code)
	}
	// Demo expiration removes the shop and its records in one transaction.
	if _, err := db.Pool.Exec(ctx, database.Statement(db.Pool.Dialect(), `DELETE FROM shops WHERE id=$1`, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`), shop); err != nil {
		t.Fatal("shop cleanup with jobs", err)
	}
}

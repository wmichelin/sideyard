package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

type fakeInterestRecorder struct {
	inputs []interestSignup
	err    error
}

func (f *fakeInterestRecorder) SaveInterest(_ context.Context, in interestSignup) error {
	f.inputs = append(f.inputs, in)
	return f.err
}

func interestTestServer(recorder interestRecorder) *Server {
	config := securityTestConfig()
	config.Environment = EnvironmentProduction
	config.RuntimeLimits.MaxRequestBodyBytes = 8 << 20
	return &Server{config: config, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")), interest: recorder}
}

func interestRequest(values url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/interest", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test-csrf-token"})
	return r
}

func TestInterestSubmissionProtectsInputAndReportsPersistenceFailures(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		change                func(url.Values)
		storeError            error
		wantStatus, wantCalls int
	}{
		{"valid", func(v url.Values) {}, nil, 303, 1},
		{"missing name", func(v url.Values) { v.Set("name", " ") }, nil, 422, 0},
		{"invalid email", func(v url.Values) { v.Set("email", "not-an-email") }, nil, 422, 0},
		{"display name email", func(v url.Values) { v.Set("email", "Person <person@example.com>") }, nil, 422, 0},
		{"too long", func(v url.Values) { v.Set("message", strings.Repeat("x", 2001)) }, nil, 422, 0},
		{"null byte", func(v url.Values) { v.Set("name", "a\x00b") }, nil, 422, 0},
		{"missing csrf", func(v url.Values) { v.Del("csrf_token") }, nil, 403, 0},
		{"wrong csrf", func(v url.Values) { v.Set("csrf_token", "wrong") }, nil, 403, 0},
		{"honeypot", func(v url.Values) { v.Set("website", "spam.example") }, nil, 303, 0},
		{"oversize body", func(v url.Values) { v.Set("unknown", strings.Repeat("x", 17<<10)) }, nil, 413, 0},
		{"storage unavailable", func(v url.Values) {}, errors.New("database unavailable"), 503, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeInterestRecorder{err: tc.storeError}
			s := interestTestServer(fake)
			values := url.Values{"name": {" Alex "}, "email": {" ALEX@EXAMPLE.COM "}, "business": {" Alex's Plumbing "}, "trade": {"Plumbing"}, "csrf_token": {"test-csrf-token"}}
			tc.change(values)
			w := httptest.NewRecorder()
			s.handler(fstest.MapFS{}).ServeHTTP(w, interestRequest(values))
			if w.Code != tc.wantStatus || len(fake.inputs) != tc.wantCalls {
				t.Fatalf("status=%d calls=%d; want %d/%d", w.Code, len(fake.inputs), tc.wantStatus, tc.wantCalls)
			}
			if tc.wantStatus == 503 && (w.Header().Get("Location") != "" || !strings.Contains(w.Body.String(), "couldn’t") && !strings.Contains(w.Body.String(), "couldn&#39;t")) {
				t.Fatalf("failed persistence did not render retry message: %s", w.Body.String())
			}
			if tc.name == "valid" {
				if fake.inputs[0].Name != "Alex" || fake.inputs[0].Email != "alex@example.com" || fake.inputs[0].Business != "Alex's Plumbing" {
					t.Fatalf("input was not normalized: %+v", fake.inputs[0])
				}
				if w.Header().Get("Location") != "/interest/thanks" {
					t.Fatal("missing confirmation redirect")
				}
			}
		})
	}
}

func TestInterestLandingAndConfirmationArePublicButExportsAreNot(t *testing.T) {
	s := interestTestServer(nil)
	h := s.handler(fstest.MapFS{})
	for _, path := range []string{"/", "/interest/thanks"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Body.String(), "Sideyard") {
			t.Fatalf("public route %s: status %d", path, w.Code)
		}
		for _, c := range w.Result().Cookies() {
			if c.Name == sessionCookieName {
				t.Fatal("landing page created an authenticated session")
			}
		}
		if path == "/" && (!strings.Contains(w.Body.String(), `name="csrf_token"`) || !strings.Contains(w.Body.String(), `action="/interest#interest"`)) {
			t.Fatal("landing page is missing the protected interest form")
		}
	}
	for _, path := range []string{"/interest", "/interest.csv", "/export-interest"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 && w.Code != 405 {
			t.Fatalf("unexpected public interest read route %s: %d", path, w.Code)
		}
	}
	v := url.Values{"name": {"Alex"}, "email": {"alex@example.com"}, "csrf_token": {"test-csrf-token"}}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, interestRequest(v))
	if w.Code != 503 {
		t.Fatalf("nil store returned %d, want 503", w.Code)
	}
}

func TestInterestThrottleAndEscapedValidation(t *testing.T) {
	fake := &fakeInterestRecorder{}
	s := interestTestServer(fake)
	h := s.handler(fstest.MapFS{})
	values := url.Values{"name": {"<script>alert(1)</script>"}, "email": {"invalid"}, "csrf_token": {"test-csrf-token"}}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, interestRequest(values))
	if strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal("validation response did not escape retained input")
	}
	values.Set("name", "Alex")
	values.Set("email", "alex@example.com")
	for i := 1; i < 20; i++ {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, interestRequest(values))
		if w.Code != 303 {
			t.Fatalf("request %d: %d", i, w.Code)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, interestRequest(values))
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" || len(fake.inputs) != 19 {
		t.Fatal("interest write budget not enforced")
	}
}

func TestInterestPersistenceDeduplicatesWithoutOverwritingAndExportsPrivately(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	email := "sideyard-test-" + time.Now().UTC().Format("20060102150405.000000000") + "@example.test"
	t.Cleanup(func() {
		if _, err := db.Pool.Exec(ctx, `DELETE FROM interest_signups WHERE email=$1`, email); err != nil {
			t.Errorf("clean up synthetic signup: %v", err)
		}
	})
	store := &postgresInterestRecorder{db: db}
	in := interestSignup{Name: "=1+1", Email: email, Business: "A, B & C", Trade: "Plumbing", Message: "A multiline\nnote"}
	if err := store.SaveInterest(ctx, in); err != nil {
		t.Fatal(err)
	}
	in.Name = "Overwritten"
	if err := store.SaveInterest(ctx, in); err != nil {
		t.Fatal(err)
	}
	var name string
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*),min(name) FROM interest_signups WHERE email=$1`, email).Scan(&count, &name); err != nil {
		t.Fatal(err)
	}
	if count != 1 || name != "=1+1" {
		t.Fatalf("duplicate overwrote lead: count %d name %q", count, name)
	}
	var out bytes.Buffer
	if err := exportInterest(ctx, db, &out); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(&out).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if record[2] == email {
			if record[1] != "'=1+1" || record[3] != "A, B & C" || record[5] != "A multiline\nnote" {
				t.Fatalf("unsafe or damaged export: %#v", record)
			}
			return
		}
	}
	t.Fatal("persisted interest missing from export")
}

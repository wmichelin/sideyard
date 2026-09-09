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
	"testing/fstest"

	"github.com/wmichelin/clubdue/internal/database"
)

type unavailableAuthPool struct {
	database.Pool
	err     error
	queries int
}

func (p *unavailableAuthPool) Begin(context.Context) (database.Tx, error) { return nil, p.err }

func (*unavailableAuthPool) Dialect() database.Dialect { return database.Postgres }
func (p *unavailableAuthPool) QueryRow(context.Context, string, ...any) database.Row {
	p.queries++
	return unavailableAuthRow{p.err}
}

type unavailableAuthRow struct{ err error }

func (r unavailableAuthRow) Scan(...any) error { return r.err }

func unavailableAuthServer(t *testing.T, err error) (*Server, *unavailableAuthPool) {
	t.Helper()
	pool := &unavailableAuthPool{err: err}
	config := securityTestConfig()
	s := &Server{config: config, auth: newAuth(&DB{Pool: pool}, "synthetic-secret", false), throttle: newAuthThrottle(config.RuntimeLimits), tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	return s, pool
}

func TestAuthDependencyFailurePreservesLoginInputAndSession(t *testing.T) {
	failure := errors.New("synthetic dependency error with internal details")
	s, _ := unavailableAuthServer(t, failure)
	if _, err := s.auth.login(context.Background(), "owner@example.test", "password"); !errors.Is(err, failure) {
		t.Fatal("database failure became invalid credentials")
	}
	for i := 0; i < 4; i++ {
		form := url.Values{"email": {"owner@example.test"}, "password": {"never-render-this-password"}}
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "existing-session"})
		w := httptest.NewRecorder()
		s.handleLoginPost(w, r)
		body := w.Body.String()
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" || !strings.Contains(body, "owner@example.test") || !strings.Contains(body, "temporarily unavailable") {
			t.Fatalf("outage response: %d", w.Code)
		}
		if strings.Contains(body, failure.Error()) || strings.Contains(body, "never-render-this-password") || strings.Contains(body, "Invalid email or password") || w.Header().Get("Set-Cookie") != "" {
			t.Fatal("outage leaked details, misreported credentials or changed the session")
		}
		if !s.throttle.allowed(authThrottleKey(r, "login")) {
			t.Fatal("dependency outage consumed bad-password attempts")
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/customers", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "existing-session"})
	w := httptest.NewRecorder()
	s.requireAuth(func(http.ResponseWriter, *http.Request) { t.Fatal("unverified request reached mutation") })(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Location") != "" || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("outage redirected or signed out an existing session")
	}
}

func TestMissingAccountAndSessionKeepAuthenticationSemantics(t *testing.T) {
	s, _ := unavailableAuthServer(t, database.ErrNoRows)
	if _, err := s.auth.login(context.Background(), "owner@example.test", "password"); !errors.Is(err, errInvalidCredentials) {
		t.Fatal("missing account is not invalid credentials")
	}
	r := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "expired-or-missing"})
	w := httptest.NewRecorder()
	s.requireAuth(func(http.ResponseWriter, *http.Request) { t.Fatal("missing session authenticated") })(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatal("missing session stopped directing to sign-in")
	}
	for i := 0; i < 3; i++ {
		r = httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=owner%40example.test&password=incorrect"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		s.handleLoginPost(w, r)
		want := "Invalid email or password."
		if i == 2 {
			want = "Too many attempts."
		}
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			t.Fatal("invalid-password throttling changed")
		}
	}
}

func TestPublicPagesAndLivenessDoNotQueryDatabaseWithSessionCookie(t *testing.T) {
	s, pool := unavailableAuthServer(t, errors.New("database unavailable"))
	s.config.EnablePublicDemo = true
	handler := s.handler(fstest.MapFS{"test.css": {Data: []byte("body{}")}})
	for _, path := range []string{"/", "/login", "/signup", "/interest/thanks", "/livez", "/healthz", "/demo", "/static/test.css"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "existing-session"})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK || pool.queries != 0 {
			t.Fatalf("public route %s: status=%d queries=%d", path, w.Code, pool.queries)
		}
	}
}

func TestPublicFormAdmissionDoesNotAuthenticateBeforeCSRF(t *testing.T) {
	s, pool := unavailableAuthServer(t, errors.New("database unavailable"))
	s.config.EnablePublicDemo = true
	handler := s.securityHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid CSRF reached handler") }))
	for _, path := range []string{"/login", "/signup", "/interest", "/demo/session"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("csrf_token=wrong"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "existing-session"})
		r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "expected"})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || pool.queries != 0 {
			t.Fatalf("public form %s queried database before CSRF", path)
		}
	}
}

func TestDemoAdmissionFailsClosedOnDatabaseOutage(t *testing.T) {
	s, _ := unavailableAuthServer(t, errors.New("database unavailable"))
	s.config.EnablePublicDemo = true
	r := httptest.NewRequest(http.MethodPost, "/customers", strings.NewReader("csrf_token=expected"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "existing-session"})
	r.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "expected"})
	w := httptest.NewRecorder()
	s.securityHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unknown demo identity used larger admission limit") })).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatal("dependency failure did not stop demo admission")
	}
	w = httptest.NewRecorder()
	s.handlePublicDemoPost(w, r)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("outage replaced existing session with a new demo")
	}
}

func TestEnrollmentDependencyFailurePreservesInputWithoutThrottle(t *testing.T) {
	for _, cookie := range []string{"", "existing-session"} {
		s, _ := unavailableAuthServer(t, errors.New("synthetic private database details"))
		s.config.Environment = EnvironmentDevelopment
		for i := 0; i < 3; i++ {
			form := url.Values{"shop_name": {"Example trade"}, "email": {"owner@example.test"}, "password": {"valid-password"}}
			r := httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if cookie != "" {
				r.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
			}
			w := httptest.NewRecorder()
			s.handleSignupPost(w, r)
			body := w.Body.String()
			if w.Code != http.StatusServiceUnavailable || !strings.Contains(body, "Example trade") || !strings.Contains(body, "owner@example.test") || strings.Contains(body, "private database details") || w.Header().Get("Set-Cookie") != "" {
				t.Fatalf("enrollment outage: %d", w.Code)
			}
			if !s.throttle.allowed(authThrottleKey(r, "signup")) {
				t.Fatal("database failure consumed enrollment throttle")
			}
		}
	}
}

package app

import (
	"bytes"
	"html/template"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func securityTestConfig() Config {
	return Config{
		Environment: EnvironmentStaging,
		RuntimeLimits: RuntimeLimits{
			MaxRequestBodyBytes: 1024,
			MaxMultipartMemory:  128,
			AuthMaxAttempts:     2,
			AuthThrottleWindow:  time.Minute,
		},
	}
}

func TestEveryMutationRouteRejectsMissingCSRF(t *testing.T) {
	config := securityTestConfig()
	config.EnableDevRoutes = true
	config.EnablePublicDemo = true
	server := &Server{config: config, throttle: newAuthThrottle(config.RuntimeLimits)}
	handler := server.handler(fstest.MapFS{})

	paths := []string{
		"/login", "/signup", "/logout", "/renew/token", "/dev/seed", "/demo/session",
		"/agreements", "/agreements/id", "/agreements/id/renewal-link", "/agreements/id/service-schedules", "/service-schedules/id", "/renewals",
		"/customers", "/customers/id", "/customers/id/delete", "/customers/id/properties",
		"/properties/id/equipment", "/import", "/visits", "/plan-templates", "/plan-templates/id",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusForbidden {
				t.Fatalf("POST %s without CSRF = %d, want %d", path, response.Code, http.StatusForbidden)
			}
			if response.Header().Get("X-Request-ID") == "" {
				t.Fatalf("POST %s without CSRF did not receive a request ID", path)
			}
		})
	}
}

func TestPublicDemoGetDoesNotAuthenticateAndIsNotCacheable(t *testing.T) {
	tmpl := template.Must(template.ParseFS(templateFS, "templates/*.html"))
	server := &Server{config: securityTestConfig(), tmpl: tmpl}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/demo", nil)
	server.handlePublicDemoGet(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /demo = %d", response.Code)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			t.Fatal("GET /demo minted an authenticated session")
		}
	}
	if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") || response.Header().Get("X-Robots-Tag") == "" {
		t.Fatalf("demo headers = cache %q robots %q", response.Header().Get("Cache-Control"), response.Header().Get("X-Robots-Tag"))
	}
	if !strings.Contains(response.Body.String(), `action="/demo/session"`) || !strings.Contains(response.Body.String(), `name="csrf_token"`) {
		t.Fatal("demo entry lacks its CSRF-protected session form")
	}
}

func TestPublicDemoMutationAllowlistIsFinite(t *testing.T) {
	allowed := []struct{ path, field, value string }{
		{"/visits", "action", "completed"},
		{"/visits", "action", "skipped"},
		{"/renewals", "status", "contacted"},
		{"/service-schedules/existing", "action", "pause"},
		{"/service-schedules/existing", "action", "resume"},
	}
	for _, tc := range allowed {
		form := url.Values{tc.field: {tc.value}}
		request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if !publicDemoMutationAllowed(request) {
			t.Fatalf("expected finite demo action %q %s=%s to be allowed", tc.path, tc.field, tc.value)
		}
	}
	blocked := []struct{ path, field, value string }{
		{"/customers", "name", "unbounded"},
		{"/agreements", "status", "active"},
		{"/agreements/id/renewal-link", "", ""},
		{"/agreements/id/service-schedules", "service_name", "unbounded"},
		{"/plan-templates", "name", "unbounded"},
		{"/import", "step", "commit"},
		{"/renewals", "status", "renewed"},
		{"/service-schedules/existing", "action", "unexpected"},
	}
	for _, tc := range blocked {
		form := url.Values{}
		if tc.field != "" {
			form.Set(tc.field, tc.value)
		}
		request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if publicDemoMutationAllowed(request) {
			t.Fatalf("demo row-creating or setup action %q %s=%s was allowed", tc.path, tc.field, tc.value)
		}
	}
}

func TestPublicDemoFreeTextIsDiscardedBeforeMutation(t *testing.T) {
	form := url.Values{"notes": {strings.Repeat("x", 4096)}, "status": {"contacted"}}
	request := httptest.NewRequest(http.MethodPost, "/renewals", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := request.ParseForm(); err != nil {
		t.Fatal(err)
	}
	clearPublicDemoFreeText(request)
	if request.FormValue("notes") != "" || request.FormValue("status") != "contacted" {
		t.Fatalf("demo form was not narrowed safely: %+v", request.Form)
	}
}

func TestSecurityHandlerAllowsValidCSRFAndSetsHeaders(t *testing.T) {
	config := securityTestConfig()
	server := &Server{config: config}
	called := false
	handler := server.securityHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	invalidForm := url.Values{"csrf_token": {"wrong-token"}}
	invalidRequest := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(invalidForm.Encode()))
	invalidRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	invalidRequest.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "known-token"})
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusForbidden || called {
		t.Fatalf("invalid CSRF response = %d, called = %t", invalidResponse.Code, called)
	}

	form := url.Values{"csrf_token": {"known-token"}}
	request := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "known-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("valid CSRF response = %d, called = %t", response.Code, called)
	}
	if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "form-action 'self'") {
		t.Fatalf("Content-Security-Policy = %q", got)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

func TestSecurityHandlerRejectsOversizedRequest(t *testing.T) {
	config := securityTestConfig()
	config.RuntimeLimits.MaxRequestBodyBytes = 8
	server := &Server{config: config}
	handler := server.securityHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("oversized request reached handler")
	}))
	request := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader("csrf_token=too-large"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized request = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestSecurityHandlerRejectsOversizedMultipartRequest(t *testing.T) {
	config := securityTestConfig()
	config.RuntimeLimits.MaxRequestBodyBytes = 64
	server := &Server{config: config}
	handler := server.securityHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("oversized multipart request reached handler")
	}))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf_token", "known-token"); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("csv", strings.Repeat("x", 256)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/import", &body)
	request.ContentLength = -1
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "known-token"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized multipart request = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestAuthThrottleRecoversAfterWindow(t *testing.T) {
	now := time.Date(2026, 8, 29, 4, 0, 0, 0, time.UTC)
	throttle := newAuthThrottle(RuntimeLimits{AuthMaxAttempts: 2, AuthThrottleWindow: time.Minute})
	throttle.now = func() time.Time { return now }
	key := "login:127.0.0.1"
	throttle.failure(key)
	throttle.failure(key)
	if throttle.allowed(key) {
		t.Fatal("throttle allowed attempts at its limit")
	}
	now = now.Add(time.Minute)
	if !throttle.allowed(key) {
		t.Fatal("throttle did not recover after its window")
	}
}

func TestPublicDemoLimiterConsumesCapacityAtomically(t *testing.T) {
	now := time.Date(2026, 9, 1, 4, 0, 0, 0, time.UTC)
	limiter := newAuthThrottle(RuntimeLimits{AuthMaxAttempts: 2, AuthThrottleWindow: time.Minute})
	limiter.now = func() time.Time { return now }
	if !limiter.take("global") || !limiter.take("global") || limiter.take("global") {
		t.Fatal("demo limiter did not enforce its exact allocation capacity")
	}
	now = now.Add(time.Minute)
	if !limiter.take("global") {
		t.Fatal("demo limiter did not recover after its window")
	}
}

func TestSessionAndCSRFCookiesAreSecureOutsideDevelopment(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config Config
		secure bool
	}{
		{name: "development", config: Config{Environment: EnvironmentDevelopment}, secure: false},
		{name: "staging", config: securityTestConfig(), secure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := &Server{config: tc.config}
			csrfResponse := httptest.NewRecorder()
			if _, err := server.csrfToken(csrfResponse, httptest.NewRequest(http.MethodGet, "/login", nil)); err != nil {
				t.Fatal(err)
			}
			csrfCookie := csrfResponse.Result().Cookies()[0]
			if csrfCookie.Secure != tc.secure || !csrfCookie.HttpOnly {
				t.Fatalf("CSRF cookie secure=%t httpOnly=%t", csrfCookie.Secure, csrfCookie.HttpOnly)
			}

			auth := newAuth(nil, "test-session-secret", tc.secure)
			sessionResponse := httptest.NewRecorder()
			auth.setSessionCookie(sessionResponse, "session-token")
			sessionCookie := sessionResponse.Result().Cookies()[0]
			if sessionCookie.Secure != tc.secure || !sessionCookie.HttpOnly {
				t.Fatalf("session cookie secure=%t httpOnly=%t", sessionCookie.Secure, sessionCookie.HttpOnly)
			}
		})
	}
}

func TestEveryPOSTTemplateIncludesCSRFField(t *testing.T) {
	if _, err := template.ParseFS(templateFS, "templates/*.html"); err != nil {
		t.Fatalf("templates do not parse with CSRF field: %v", err)
	}
	files, err := fs.ReadDir(templateFS, "templates")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".html") {
			continue
		}
		contents, err := templateFS.ReadFile("templates/" + file.Name())
		if err != nil {
			t.Fatal(err)
		}
		postForms := strings.Count(string(contents), `method="post"`)
		if postForms == 0 {
			continue
		}
		csrfFields := strings.Count(string(contents), `{{ template "csrf_field"`)
		if csrfFields != postForms {
			t.Fatalf("%s has %d POST forms but %d CSRF fields", file.Name(), postForms, csrfFields)
		}
	}
}

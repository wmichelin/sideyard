package app

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestProductionPublicDemoRouteIsExplicitIsolatedAndWritable(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createdShopIDs := make([]string, 0, 3)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, shopID := range createdShopIDs {
			if _, cleanupErr := db.Pool.Exec(cleanupCtx, `DELETE FROM shops WHERE id = $1`, shopID); cleanupErr != nil {
				t.Errorf("clean up production demo test shop %s: %v", shopID, cleanupErr)
			}
		}
	}()

	now := time.Date(2035, time.September, 2, 12, 0, 0, 0, time.UTC)
	clock := fixedClock{now: now}
	store := &Store{db: db, clock: clock}
	auth := newAuthWithClock(db, "production-demo-session-secret", true, clock)
	tmpl := template.Must(template.ParseFS(templateFS, "templates/*.html"))
	config := securityTestConfig()
	config.Environment = EnvironmentProduction
	config.EnablePublicDemo = false

	stamp := time.Now().UTC().Format("20060102150405.000000000")
	realShopID := createTestShop(t, ctx, db, "production-demo-control-"+stamp)
	createdShopIDs = append(createdShopIDs, realShopID)
	realCustomerID := createTestCustomer(t, ctx, db, realShopID, "Existing Production Customer")
	createTestUser(t, ctx, db, realShopID, "production-demo-owner-"+stamp+"@example.test")
	var realUserID string
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM users WHERE shop_id = $1`, realShopID).Scan(&realUserID); err != nil {
		t.Fatal(err)
	}
	realSession, err := auth.createSession(ctx, realUserID)
	if err != nil {
		t.Fatal(err)
	}

	newHandler := func(current Config) http.Handler {
		server := &Server{
			auth: auth, store: store, config: current, tmpl: tmpl,
			throttle: newAuthThrottle(current.RuntimeLimits), clock: clock,
		}
		return server.handler(fstest.MapFS{})
	}
	disabledHandler := newHandler(config)
	disabledDemo := httptest.NewRecorder()
	disabledHandler.ServeHTTP(disabledDemo, httptest.NewRequest(http.MethodGet, "https://clubdue.example.test/demo", nil))
	if disabledDemo.Code != http.StatusNotFound {
		t.Fatalf("production demo without opt-in = %d, want %d", disabledDemo.Code, http.StatusNotFound)
	}
	disabledLogin := httptest.NewRecorder()
	disabledHandler.ServeHTTP(disabledLogin, httptest.NewRequest(http.MethodGet, "https://clubdue.example.test/login", nil))
	if strings.Contains(disabledLogin.Body.String(), "Explore a landscaping demo") {
		t.Fatal("production login exposed the demo CTA without opt-in")
	}

	config.EnablePublicDemo = true
	handler := newHandler(config)
	enabledLogin := httptest.NewRecorder()
	handler.ServeHTTP(enabledLogin, httptest.NewRequest(http.MethodGet, "https://clubdue.example.test/login", nil))
	if enabledLogin.Code != http.StatusOK || !strings.Contains(enabledLogin.Body.String(), "Explore a landscaping demo") {
		t.Fatalf("production demo CTA response = %d", enabledLogin.Code)
	}

	type demoSession struct {
		csrfToken, sessionToken, shopID string
	}
	startDemo := func() demoSession {
		t.Helper()
		entry := httptest.NewRecorder()
		handler.ServeHTTP(entry, httptest.NewRequest(http.MethodGet, "https://clubdue.example.test/demo", nil))
		if entry.Code != http.StatusOK || !strings.Contains(entry.Body.String(), `action="/demo/session"`) {
			t.Fatalf("production demo entry = %d %q", entry.Code, entry.Body.String())
		}
		assertPublicDemoResponseHeaders(t, entry)
		csrfToken := responseCookieValue(t, entry, csrfCookieName)
		for _, cookie := range entry.Result().Cookies() {
			if cookie.Name == sessionCookieName {
				t.Fatal("production demo GET created an authenticated session")
			}
		}

		form := url.Values{"csrf_token": {csrfToken}}
		request := httptest.NewRequest(http.MethodPost, "https://clubdue.example.test/demo/session", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrfToken})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard" {
			t.Fatalf("production demo start = %d %q", response.Code, response.Header().Get("Location"))
		}
		assertPublicDemoResponseHeaders(t, response)
		sessionToken := responseCookieValue(t, response, sessionCookieName)
		var sessionCookie *http.Cookie
		for _, cookie := range response.Result().Cookies() {
			if cookie.Name == sessionCookieName {
				sessionCookie = cookie
			}
		}
		if sessionCookie == nil || !sessionCookie.Secure || !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode || sessionCookie.Path != "/" || sessionCookie.MaxAge != int(publicDemoTTL.Seconds()) {
			t.Fatalf("production demo session cookie = %+v", sessionCookie)
		}

		userRequest := httptest.NewRequest(http.MethodGet, "https://clubdue.example.test/dashboard", nil)
		userRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
		user, err := auth.userFromRequest(ctx, userRequest)
		if err != nil || !user.IsDemo {
			t.Fatalf("production demo user = %+v, %v", user, err)
		}
		createdShopIDs = append(createdShopIDs, user.ShopID)
		return demoSession{csrfToken: csrfToken, sessionToken: sessionToken, shopID: user.ShopID}
	}

	first := startDemo()
	writeForm := url.Values{
		"csrf_token": {first.csrfToken},
		"name":       {"Production Demo Write"},
		"email":      {"production-demo-write@example.test"},
		"phone":      {"555-0199"},
	}
	writeRequest := httptest.NewRequest(http.MethodPost, "https://clubdue.example.test/customers", strings.NewReader(writeForm.Encode()))
	writeRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addDemoCookies(writeRequest, first.sessionToken, first.csrfToken)
	writeResponse := httptest.NewRecorder()
	handler.ServeHTTP(writeResponse, writeRequest)
	if writeResponse.Code != http.StatusSeeOther {
		t.Fatalf("production demo write = %d %q", writeResponse.Code, writeResponse.Body.String())
	}

	second := startDemo()
	if first.shopID == second.shopID {
		t.Fatal("production demo visitors shared a tenant")
	}
	for shopID, want := range map[string]int{first.shopID: 1, second.shopID: 0, realShopID: 0} {
		var got int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE shop_id = $1 AND name = 'Production Demo Write'`, shopID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("production demo write count for shop %s = %d, want %d", shopID, got, want)
		}
	}

	var firstDemoCustomerID string
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM customers WHERE shop_id = $1 AND name = 'Production Demo Write'`, first.shopID).Scan(&firstDemoCustomerID); err != nil {
		t.Fatal(err)
	}
	foreignRequest := httptest.NewRequest(http.MethodGet, "https://clubdue.example.test/customers/"+firstDemoCustomerID, nil)
	foreignRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: second.sessionToken})
	foreignResponse := httptest.NewRecorder()
	handler.ServeHTTP(foreignResponse, foreignRequest)
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("second production demo read first demo object = %d", foreignResponse.Code)
	}

	realDemoRequest := httptest.NewRequest(http.MethodGet, "https://clubdue.example.test/demo", nil)
	realDemoRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: realSession})
	realDemoResponse := httptest.NewRecorder()
	handler.ServeHTTP(realDemoResponse, realDemoRequest)
	if realDemoResponse.Code != http.StatusSeeOther || realDemoResponse.Header().Get("Location") != "/dashboard" {
		t.Fatalf("authenticated production owner demo route = %d %q", realDemoResponse.Code, realDemoResponse.Header().Get("Location"))
	}
	for _, cookie := range realDemoResponse.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			t.Fatal("authenticated production owner demo route replaced the owner session")
		}
	}
	realDashboardRequest := httptest.NewRequest(http.MethodGet, "https://clubdue.example.test/dashboard", nil)
	realDashboardRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: realSession})
	realDashboardResponse := httptest.NewRecorder()
	handler.ServeHTTP(realDashboardResponse, realDashboardRequest)
	if realDashboardResponse.Code != http.StatusOK || strings.Contains(realDashboardResponse.Body.String(), "Fictional public demo") {
		t.Fatalf("production owner dashboard after demo traffic = %d", realDashboardResponse.Code)
	}
	var realCustomerCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE shop_id = $1 AND id = $2`, realShopID, realCustomerID).Scan(&realCustomerCount); err != nil {
		t.Fatal(err)
	}
	if realCustomerCount != 1 {
		t.Fatalf("production demo traffic changed the real tenant: %d", realCustomerCount)
	}
}

func responseCookieValue(t *testing.T, response *httptest.ResponseRecorder, name string) string {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name && cookie.Value != "" {
			return cookie.Value
		}
	}
	t.Fatalf("response did not set %s", name)
	return ""
}

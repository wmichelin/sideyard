package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

var demoPreviewTokenPattern = regexp.MustCompile(`name="preview_token" value="([^"]+)"`)

func TestPublicDemoLogoutSurvivesExhaustedPreParseLimiter(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, time.September, 2, 23, 59, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	store := &Store{db: db, clock: clock}
	auth := newAuthWithClock(db, "demo-logout-session-secret", false, clock)
	config := securityTestConfig()
	config.EnablePublicDemo = true
	server := &Server{auth: auth, store: store, config: config, throttle: newAuthThrottle(config.RuntimeLimits), clock: clock}
	handler := server.handler(fstest.MapFS{})
	_, sessionToken := seedAuthenticatedPublicDemo(t, ctx, store, auth)
	csrfToken := "known-demo-logout-csrf"

	for attempt := range 20 {
		form := url.Values{"csrf_token": {"wrong"}}
		request := httptest.NewRequest(http.MethodPost, "/customers", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		addDemoCookies(request, sessionToken, csrfToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("limiter setup attempt %d = %d", attempt, response.Code)
		}
	}
	exhausted := httptest.NewRequest(http.MethodPost, "/customers", strings.NewReader("csrf_token=wrong"))
	exhausted.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addDemoCookies(exhausted, sessionToken, csrfToken)
	exhaustedResponse := httptest.NewRecorder()
	handler.ServeHTTP(exhaustedResponse, exhausted)
	if exhaustedResponse.Code != http.StatusTooManyRequests {
		t.Fatalf("exhausted demo mutation = %d, want %d", exhaustedResponse.Code, http.StatusTooManyRequests)
	}

	logoutForm := url.Values{"csrf_token": {csrfToken}}
	logout := httptest.NewRequest(http.MethodPost, "/logout", strings.NewReader(logoutForm.Encode()))
	logout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addDemoCookies(logout, sessionToken, csrfToken)
	logoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(logoutResponse, logout)
	if logoutResponse.Code != http.StatusSeeOther || logoutResponse.Header().Get("Location") != "/login" {
		t.Fatalf("logout after limiter exhaustion = %d %q", logoutResponse.Code, logoutResponse.Header().Get("Location"))
	}
	assertPublicDemoResponseHeaders(t, logoutResponse)

	replay := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	replay.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	replayResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayResponse, replay)
	if replayResponse.Code != http.StatusSeeOther || replayResponse.Header().Get("Location") != "/login" {
		t.Fatalf("replayed logged-out session = %d %q", replayResponse.Code, replayResponse.Header().Get("Location"))
	}
}

func TestPublicDemoWorstCaseDailyImportMaterializesWithinRequestBudget(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	demo, err := store.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	totalProjection := 0
	for batch := range publicDemoImportRowBudget / publicDemoMaxImportRows {
		var csv strings.Builder
		csv.WriteString("external_id,customer_name,address_line1,city,state,postal_code,anniversary_date,service_external_id,service_name,cadence_interval,cadence_unit,service_start_date\n")
		for offset := range publicDemoMaxImportRows {
			rowNumber := batch*publicDemoMaxImportRows + offset
			fmt.Fprintf(&csv, "max-%03d,Max Client %03d,%d Main St,Columbus,OH,43215,2030-01-01", rowNumber, rowNumber, rowNumber)
			if rowNumber < 165 {
				fmt.Fprintf(&csv, ",max-service-%03d,Daily route,1,day,2026-01-01\n", rowNumber)
			} else {
				csv.WriteString(",,,,,\n")
			}
		}
		raw := []byte(csv.String())
		if len(raw) > publicDemoMaxImportCSVBytes {
			t.Fatalf("worst-case batch %d = %d bytes", batch, len(raw))
		}
		rows, err := parseImportCSVWithLimits(bytes.NewReader(raw), publicDemoMaxImportRows, publicDemoMaxImportCellBytes)
		if err != nil {
			t.Fatal(err)
		}
		analysis, err := store.AnalyzeImport(ctx, demo.ShopID, rows)
		if err != nil || analysis.Summary.Errors != 0 {
			t.Fatalf("analyze worst-case demo import batch %d = %+v, %v", batch, analysis.Summary, err)
		}
		projected, err := projectedImportOccurrenceCount(rows, now)
		if err != nil {
			t.Fatal(err)
		}
		totalProjection += projected
		token := fmt.Sprintf("worst-case-token-%d", batch)
		if err := store.CreatePublicDemoImportPreview(
			ctx, demo.ShopID, token, raw, len(rows), projected, now,
		); err != nil {
			t.Fatal(err)
		}
		payload, err := store.ConsumePublicDemoImportPreview(ctx, demo.ShopID, token)
		if err != nil || payload.RowCount != len(rows) || payload.ProjectedOccurrences != projected {
			t.Fatalf("consume worst-case batch %d = %+v, %v", batch, payload, err)
		}
		if _, err := store.CommitImport(ctx, demo.ShopID, rows); err != nil {
			t.Fatal(err)
		}
	}
	if totalProjection != 165*121 || totalProjection > publicDemoOccurrenceBudget {
		t.Fatalf("worst-case projection = %d", totalProjection)
	}

	started := time.Now()
	if _, err := store.ListServiceOccurrences(ctx, demo.ShopID, false); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed >= 20*time.Second {
		t.Fatalf("worst-case demo service-work materialization took %s", elapsed)
	}
	var occurrences int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM service_occurrences o
		JOIN service_schedules sc ON sc.id = o.schedule_id AND sc.shop_id = o.shop_id
		WHERE o.shop_id = $1 AND sc.external_id LIKE 'max-service-%'`, demo.ShopID).Scan(&occurrences); err != nil {
		t.Fatal(err)
	}
	if occurrences != totalProjection {
		t.Fatalf("worst-case demo occurrences = %d, want %d", occurrences, totalProjection)
	}
}

func TestPublicDemoImportPreviewStoreEnforcesExpiryIsolationAndBudgets(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	first, err := store.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}

	exactLimit := bytes.Repeat([]byte("x"), publicDemoMaxImportCSVBytes)
	if err := store.CreatePublicDemoImportPreview(ctx, first.ShopID, "first-100", exactLimit, 100, 0, now); err != nil {
		t.Fatalf("create exact-limit preview: %v", err)
	}
	if _, err := store.ConsumePublicDemoImportPreview(ctx, second.ShopID, "first-100"); !errors.Is(err, errPublicDemoImportPreviewInvalid) {
		t.Fatalf("cross-tenant preview consume = %v", err)
	}
	payload, err := store.ConsumePublicDemoImportPreview(ctx, first.ShopID, "first-100")
	if err != nil || len(payload.CSV) != publicDemoMaxImportCSVBytes {
		t.Fatalf("consume exact-limit preview = %d bytes, %v", len(payload.CSV), err)
	}
	if _, err := store.ConsumePublicDemoImportPreview(ctx, first.ShopID, "first-100"); !errors.Is(err, errPublicDemoImportPreviewInvalid) {
		t.Fatalf("preview replay = %v", err)
	}
	if err := store.CreatePublicDemoImportPreview(
		ctx, first.ShopID, "too-large", bytes.Repeat([]byte("x"), publicDemoMaxImportCSVBytes+1), 1, 0, now,
	); !errors.Is(err, errPublicDemoImportPreviewInvalid) {
		t.Fatalf("oversized stored preview = %v", err)
	}

	for i := 2; i <= publicDemoImportRowBudget/publicDemoMaxImportRows; i++ {
		token := "budget-" + string(rune('0'+i))
		if err := store.CreatePublicDemoImportPreview(ctx, first.ShopID, token, []byte("x"), publicDemoMaxImportRows, 0, now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ConsumePublicDemoImportPreview(ctx, first.ShopID, token); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CreatePublicDemoImportPreview(ctx, first.ShopID, "budget-over", []byte("x"), 1, 0, now); !errors.Is(err, errPublicDemoImportRowBudgetReached) {
		t.Fatalf("preview beyond row budget = %v", err)
	}
	var rowsUsed int
	if err := db.Pool.QueryRow(ctx, `SELECT demo_import_rows_used FROM shops WHERE id = $1`, first.ShopID).Scan(&rowsUsed); err != nil {
		t.Fatal(err)
	}
	if rowsUsed != publicDemoImportRowBudget {
		t.Fatalf("demo import row budget = %d, want %d", rowsUsed, publicDemoImportRowBudget)
	}

	if err := store.CreatePublicDemoImportPreview(ctx, second.ShopID, "replaced", []byte("x"), 1, 0, now); err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePublicDemoImportPreview(ctx, second.ShopID, "replacement", []byte("y"), 1, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumePublicDemoImportPreview(ctx, second.ShopID, "replaced"); !errors.Is(err, errPublicDemoImportPreviewInvalid) {
		t.Fatalf("replaced preview = %v", err)
	}
	if _, err := store.ConsumePublicDemoImportPreview(ctx, second.ShopID, "replacement"); err != nil {
		t.Fatalf("replacement preview = %v", err)
	}
	for i := range 25 {
		oldToken := fmt.Sprintf("race-old-%d", i)
		newToken := fmt.Sprintf("race-new-%d", i)
		if err := store.CreatePublicDemoImportPreview(ctx, second.ShopID, oldToken, []byte("x"), 1, 0, now); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		results := make(chan error, 2)
		var requests sync.WaitGroup
		requests.Add(2)
		go func() {
			defer requests.Done()
			<-start
			results <- store.CreatePublicDemoImportPreview(ctx, second.ShopID, newToken, []byte("y"), 1, 0, now)
		}()
		go func() {
			defer requests.Done()
			<-start
			_, err := store.ConsumePublicDemoImportPreview(ctx, second.ShopID, oldToken)
			if errors.Is(err, errPublicDemoImportPreviewInvalid) {
				err = nil
			}
			results <- err
		}()
		close(start)
		requests.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatalf("concurrent preview create/consume %d: %v", i, err)
			}
		}
	}

	if err := store.CreatePublicDemoImportPreview(ctx, second.ShopID, "expiring", []byte("z"), 1, 0, now); err != nil {
		t.Fatal(err)
	}
	expiredStore := &Store{db: db, clock: fixedClock{now: now.Add(publicDemoImportPreviewTTL)}}
	if _, err := expiredStore.ConsumePublicDemoImportPreview(ctx, second.ShopID, "expiring"); !errors.Is(err, errPublicDemoImportPreviewInvalid) {
		t.Fatalf("expired preview = %v", err)
	}

	third, err := store.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePublicDemoImportPreview(ctx, third.ShopID, "occurrence-race", []byte("x"), 1, 10000, now); err != nil {
		t.Fatal(err)
	}
	consumeResults := make(chan error, 2)
	var consumers sync.WaitGroup
	for range 2 {
		consumers.Add(1)
		go func() {
			defer consumers.Done()
			_, err := store.ConsumePublicDemoImportPreview(ctx, third.ShopID, "occurrence-race")
			consumeResults <- err
		}()
	}
	consumers.Wait()
	close(consumeResults)
	succeeded, rejected := 0, 0
	for err := range consumeResults {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, errPublicDemoImportPreviewInvalid):
			rejected++
		default:
			t.Fatalf("concurrent occurrence claim: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent occurrence claims succeeded=%d rejected=%d", succeeded, rejected)
	}
	if err := store.CreatePublicDemoImportPreview(ctx, third.ShopID, "occurrence-exact", []byte("y"), 1, 10000, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumePublicDemoImportPreview(ctx, third.ShopID, "occurrence-exact"); err != nil {
		t.Fatal(err)
	}
	if err := store.CreatePublicDemoImportPreview(ctx, third.ShopID, "occurrence-over", []byte("z"), 1, 1, now); !errors.Is(err, errPublicDemoOccurrenceBudgetReached) {
		t.Fatalf("occurrence budget overflow = %v", err)
	}
	var occurrencesUsed int
	if err := db.Pool.QueryRow(ctx, `SELECT demo_import_occurrences_used FROM shops WHERE id = $1`, third.ShopID).Scan(&occurrencesUsed); err != nil {
		t.Fatal(err)
	}
	if occurrencesUsed != publicDemoOccurrenceBudget {
		t.Fatalf("demo occurrence budget = %d, want %d", occurrencesUsed, publicDemoOccurrenceBudget)
	}
}

func TestPublicDemoCSVPreviewCommitIsBoundedSingleUseAndIsolated(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, time.September, 2, 23, 59, 0, 0, time.UTC)
	clock := &fixedClock{now: now}
	store := &Store{db: db, clock: clock}
	auth := newAuthWithClock(db, "demo-import-session-secret", false, clock)
	config := securityTestConfig()
	config.EnablePublicDemo = true
	config.RuntimeLimits.MaxRequestBodyBytes = 8 << 20
	config.RuntimeLimits.MaxMultipartMemory = 1 << 20
	server := &Server{
		tmpl:     template.Must(template.ParseFS(templateFS, "templates/*.html")),
		auth:     auth,
		store:    store,
		config:   config,
		throttle: newAuthThrottle(config.RuntimeLimits),
		clock:    clock,
	}
	handler := server.handler(fstest.MapFS{})

	first, firstSession := seedAuthenticatedPublicDemo(t, ctx, store, auth)
	second, secondSession := seedAuthenticatedPublicDemo(t, ctx, store, auth)
	csrfToken := "known-demo-import-csrf"

	getRequest := httptest.NewRequest(http.MethodGet, "/import", nil)
	addDemoCookies(getRequest, firstSession, csrfToken)
	getResponse := httptest.NewRecorder()
	handler.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), "fictional landscaping sample CSV") {
		t.Fatalf("demo import page = %d %q", getResponse.Code, getResponse.Body.String())
	}
	assertPublicDemoResponseHeaders(t, getResponse)

	raw, err := staticFS.ReadFile("static/landscaping-demo-import.csv")
	if err != nil {
		t.Fatal(err)
	}
	domainTables := []string{"customers", "properties", "agreements", "service_schedules"}
	before := make(map[string]int, len(domainTables))
	for _, table := range domainTables {
		before[table] = countShopRows(t, ctx, db, table, first.ShopID)
	}
	previewRequest := newDemoImportPreviewRequest(t, raw, csrfToken)
	previewRequest.ContentLength = -1 // exercise the actual streaming boundary
	addDemoCookies(previewRequest, firstSession, csrfToken)
	previewResponse := httptest.NewRecorder()
	handler.ServeHTTP(previewResponse, previewRequest)
	if previewResponse.Code != http.StatusOK || !strings.Contains(previewResponse.Body.String(), "Nothing has been saved yet") {
		t.Fatalf("demo preview = %d %q", previewResponse.Code, previewResponse.Body.String())
	}
	assertPublicDemoResponseHeaders(t, previewResponse)
	for _, table := range domainTables {
		if got := countShopRows(t, ctx, db, table, first.ShopID); got != before[table] {
			t.Fatalf("preview persisted %s: before=%d after=%d", table, before[table], got)
		}
	}
	match := demoPreviewTokenPattern.FindStringSubmatch(previewResponse.Body.String())
	if len(match) != 2 || match[1] == "" || strings.Contains(previewResponse.Body.String(), "csv_data") {
		t.Fatalf("preview did not expose exactly one opaque server-side token: %q", previewResponse.Body.String())
	}
	previewToken := match[1]

	wrongTenantResponse := postDemoImportCommit(handler, secondSession, csrfToken, previewToken)
	if wrongTenantResponse.Code != http.StatusOK || !strings.Contains(wrongTenantResponse.Body.String(), "expired or was already used") {
		t.Fatalf("cross-tenant commit = %d %q", wrongTenantResponse.Code, wrongTenantResponse.Body.String())
	}
	assertPublicDemoResponseHeaders(t, wrongTenantResponse)
	if got := countShopRows(t, ctx, db, "customers", second.ShopID); got != 3 {
		t.Fatalf("cross-tenant commit changed second sandbox customers: %d", got)
	}

	clock.now = now.Add(2 * time.Minute)
	commitResponse := postDemoImportCommit(handler, firstSession, csrfToken, previewToken)
	if commitResponse.Code != http.StatusOK || !strings.Contains(commitResponse.Body.String(), "Import complete") {
		t.Fatalf("demo commit = %d %q", commitResponse.Code, commitResponse.Body.String())
	}
	assertPublicDemoResponseHeaders(t, commitResponse)
	if got := countShopRows(t, ctx, db, "customers", first.ShopID); got != before["customers"]+3 {
		t.Fatalf("demo commit customers = %d, want %d", got, before["customers"]+3)
	}
	for _, table := range []string{"properties", "agreements", "service_schedules"} {
		if got := countShopRows(t, ctx, db, table, first.ShopID); got != before[table]+3 {
			t.Fatalf("demo commit %s = %d, want %d", table, got, before[table]+3)
		}
	}
	if err := store.GenerateAllServiceOccurrences(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countDemoSampleOccurrences(t, ctx, db, first.ShopID); got != 0 {
		t.Fatalf("global maintenance generated %d imported demo occurrences", got)
	}
	if _, err := store.ListServiceOccurrences(ctx, first.ShopID, false); err != nil {
		t.Fatal(err)
	}
	if got := countDemoSampleOccurrences(t, ctx, db, first.ShopID); got == 0 {
		t.Fatal("tenant-local service-work view did not generate imported demo occurrences")
	}

	replayResponse := postDemoImportCommit(handler, firstSession, csrfToken, previewToken)
	if replayResponse.Code != http.StatusOK || !strings.Contains(replayResponse.Body.String(), "expired or was already used") {
		t.Fatalf("replayed commit = %d %q", replayResponse.Code, replayResponse.Body.String())
	}
	if got := countShopRows(t, ctx, db, "customers", first.ShopID); got != before["customers"]+3 {
		t.Fatalf("replayed commit changed customers: %d", got)
	}

	blockedMultipart := newDemoImportPreviewRequest(t, raw, csrfToken)
	blockedMultipart.URL.Path = "/customers"
	addDemoCookies(blockedMultipart, firstSession, csrfToken)
	blockedResponse := httptest.NewRecorder()
	handler.ServeHTTP(blockedResponse, blockedMultipart)
	if blockedResponse.Code != http.StatusForbidden {
		t.Fatalf("non-import demo multipart = %d, want %d", blockedResponse.Code, http.StatusForbidden)
	}

	oversizedRequest := newDemoImportPreviewRequest(
		t, bytes.Repeat([]byte("x"), publicDemoMaxImportRequestBytes), csrfToken,
	)
	oversizedRequest.ContentLength = -1
	addDemoCookies(oversizedRequest, firstSession, csrfToken)
	oversizedResponse := httptest.NewRecorder()
	handler.ServeHTTP(oversizedResponse, oversizedRequest)
	if oversizedResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked oversized demo import = %d, want %d", oversizedResponse.Code, http.StatusRequestEntityTooLarge)
	}
	assertPublicDemoResponseHeaders(t, oversizedResponse)
}

func seedAuthenticatedPublicDemo(t *testing.T, ctx context.Context, store *Store, auth *Auth) (PublicDemoResult, string) {
	t.Helper()
	result, err := store.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.createSessionUntil(ctx, result.UserID, result.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	return result, token
}

func newDemoImportPreviewRequest(t *testing.T, raw []byte, csrfToken string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("csrf_token", csrfToken); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("step", "dry_run"); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("csv", "fictional-landscaping.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/import", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func postDemoImportCommit(handler http.Handler, sessionToken, csrfToken, previewToken string) *httptest.ResponseRecorder {
	form := url.Values{
		"csrf_token":    {csrfToken},
		"step":          {"commit"},
		"preview_token": {previewToken},
	}
	request := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	addDemoCookies(request, sessionToken, csrfToken)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func addDemoCookies(request *http.Request, sessionToken, csrfToken string) {
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: sessionToken})
	request.AddCookie(&http.Cookie{Name: csrfCookieName, Value: csrfToken})
}

func assertPublicDemoResponseHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") || response.Header().Get("X-Robots-Tag") == "" {
		t.Fatalf("public demo response headers = cache %q robots %q", response.Header().Get("Cache-Control"), response.Header().Get("X-Robots-Tag"))
	}
}

func countShopRows(t *testing.T, ctx context.Context, db *DB, table, shopID string) int {
	t.Helper()
	allowed := map[string]bool{
		"agreements": true, "customers": true, "properties": true, "service_schedules": true,
	}
	if !allowed[table] {
		t.Fatalf("unsupported test table %q", table)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM "+table+" WHERE shop_id = $1", shopID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func countDemoSampleOccurrences(t *testing.T, ctx context.Context, db *DB, shopID string) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*)
		FROM service_occurrences o
		JOIN service_schedules sc ON sc.id = o.schedule_id AND sc.shop_id = o.shop_id
		WHERE o.shop_id = $1 AND sc.external_id LIKE 'demo-csv-service-%'`, shopID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

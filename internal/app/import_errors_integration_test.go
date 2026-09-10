package app

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"github.com/wmichelin/clubdue/internal/database"
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestImportPreviewShowsEveryRejectedRow(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testImportPreviewShowsEveryRejectedRow(t, db)
}
func TestSQLServerImportPreviewShowsEveryRejectedRow(t *testing.T) {
	testImportPreviewShowsEveryRejectedRow(t, sqlServerTestDB(t))
}
func testImportPreviewShowsEveryRejectedRow(t *testing.T, db *DB) {
	ctx := context.Background()
	var err error
	for _, demo := range []bool{false, true} {
		t.Run(fmt.Sprintf("demo=%t", demo), func(t *testing.T) {
			clock := fixedClock{now: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)}
			store := &Store{db: db, clock: clock}
			auth := newAuthWithClock(db, "import-errors-test-secret", false, clock)
			config := securityTestConfig()
			config.EnablePublicDemo = true
			config.RuntimeLimits.MaxRequestBodyBytes = 8 << 20
			config.RuntimeLimits.MaxMultipartMemory = 1 << 20
			server := &Server{tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")), auth: auth, store: store, config: config, throttle: newAuthThrottle(config.RuntimeLimits), clock: clock}
			handler := server.handler(fstest.MapFS{})
			var shopID, session string
			if demo {
				result, token := seedAuthenticatedPublicDemo(t, ctx, store, auth)
				shopID, session = result.ShopID, token
			} else {
				stamp := time.Now().UnixNano()
				shopID = createTestShop(t, ctx, db, fmt.Sprintf("import-errors-%d", stamp))
				createTestUser(t, ctx, db, shopID, fmt.Sprintf("import-errors-%d@example.test", stamp))
				var userID string
				if err := db.Pool.QueryRow(ctx, database.Statement(db.Pool.Dialect(), `SELECT id FROM users WHERE shop_id = $1`, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.users WHERE shop_id=CONVERT(uniqueidentifier,@p1)`), shopID).Scan(&userID); err != nil {
					t.Fatal(err)
				}
				session, err = auth.createSession(ctx, userID)
				if err != nil {
					t.Fatal(err)
				}
			}
			before := make(map[string]int)
			for _, table := range []string{"customers", "properties", "agreements", "service_schedules"} {
				before[table] = countShopRows(t, ctx, db, table, shopID)
			}
			for _, invalid := range []bool{true, false} {
				var raw bytes.Buffer
				writer := csv.NewWriter(&raw)
				if err := writer.Write([]string{"external_id", "customer_name", "address_line1", "city", "state", "postal_code", "anniversary_date", "plan_name"}); err != nil {
					t.Fatal(err)
				}
				for row := 1; row <= 100; row++ {
					date, plan := "2027-09-07", ""
					if invalid && (row == 21 || row == 73) {
						date = "bad-date"
					}
					if invalid && row == 100 {
						plan = "<script>alert(1)</script>"
					}
					if err := writer.Write([]string{fmt.Sprintf("review-%03d", row), fmt.Sprintf("Fictional Customer %03d", row), "1 Test Way", "Cary", "NC", "27511", date, plan}); err != nil {
						t.Fatal(err)
					}
				}
				writer.Flush()
				if err := writer.Error(); err != nil {
					t.Fatal(err)
				}
				request := newDemoImportPreviewRequest(t, raw.Bytes(), "import-errors-csrf")
				addDemoCookies(request, session, "import-errors-csrf")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				body := response.Body.String()
				if response.Code != http.StatusOK {
					t.Fatalf("preview status = %d: %s", response.Code, body)
				}
				if invalid {
					for _, want := range []string{"CSV row 22", "CSV row 74", "CSV row 101", "review-021", "review-073", "review-100", "Fictional Customer 100", "anniversary_date must be YYYY-MM-DD or M/D/YYYY", "unknown plan template: &lt;script&gt;alert(1)&lt;/script&gt;", "Fix these rows and upload the file again."} {
						if !strings.Contains(body, want) {
							t.Errorf("invalid preview missing %q", want)
						}
					}
					for _, forbidden := range []string{"Commit import", `name="csv_data"`, `name="preview_token"`, "<script>alert(1)</script>", "review-072", "Commit uses this exact protected preview"} {
						if strings.Contains(body, forbidden) {
							t.Errorf("invalid preview contains %q", forbidden)
						}
					}
				} else if !strings.Contains(body, "Commit import") {
					t.Error("valid 100-row preview has no commit control")
				}
				for table, count := range before {
					if got := countShopRows(t, ctx, db, table, shopID); got != count {
						t.Errorf("preview wrote %s: before=%d after=%d", table, count, got)
					}
				}
			}
		})
	}
}

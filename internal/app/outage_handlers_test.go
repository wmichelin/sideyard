package app

import (
	"bytes"
	"context"
	"errors"
	"html/template"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func TestCustomerFormsRetainDraftsOnWriteFailure(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testCustomerFormsRetainDraftsOnWriteFailure(t, db)
}
func TestSQLServerCustomerFormsRetainDraftsOnWriteFailure(t *testing.T) {
	testCustomerFormsRetainDraftsOnWriteFailure(t, sqlServerTestDB(t))
}

func testCustomerFormsRetainDraftsOnWriteFailure(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	base := &Store{db: db}
	shop := createTestShop(t, ctx, db, "outage-customer-"+time.Now().Format("150405.000000000"))
	customer := createTestCustomer(t, ctx, db, shop, "Original customer")
	property := createTestProperty(t, ctx, db, shop, customer, "Original address")
	failure := errors.New("private synthetic database failure")
	s := &Server{store: &Store{db: &DB{Pool: contractFailingWritesPool{Pool: db.Pool, err: failure}}}, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	u := &User{ShopID: shop, Role: "owner", Email: "owner@example.test"}

	request := func(path, id string, handle http.HandlerFunc, values url.Values, retained ...string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", id)
		r = r.WithContext(contextWithUser(ctx, u))
		w := httptest.NewRecorder()
		handle(w, r)
		body := w.Body.String()
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Location") != "" || !strings.Contains(body, "Could not confirm whether") || strings.Contains(body, failure.Error()) {
			t.Fatalf("%s: status=%d location=%q body=%s", path, w.Code, w.Header().Get("Location"), body)
		}
		for _, value := range retained {
			if !strings.Contains(body, value) {
				t.Fatalf("%s lost retained value %q", path, value)
			}
		}
	}

	request("/customers", "", s.handleCustomers, url.Values{"name": {"New & customer"}, "email": {"new@example.test"}, "phone": {"555-0100"}}, "New &amp; customer", "new@example.test", "555-0100")
	request("/customers/"+customer, customer, s.handleCustomerEdit, url.Values{"name": {"Edited & customer"}, "email": {"edited@example.test"}, "phone": {"555-0101"}}, "Edited &amp; customer", "edited@example.test", "555-0101")
	request("/customers/"+customer+"/properties", customer, s.handlePropertyCreate, url.Values{"address_line1": {"12 <Private> Rd"}, "address_line2": {"Unit & 2"}, "city": {"Columbus"}, "state": {"OH"}, "postal_code": {"43000"}}, "12 &lt;Private&gt; Rd", "Unit &amp; 2", "Columbus", "43000")
	request("/properties/"+property+"/equipment", property, s.handleEquipmentCreate, url.Values{"kind": {"Pump <A>"}, "make": {"Make & Co"}, "model": {"M-1"}, "serial_number": {"S-1"}}, "Pump &lt;A&gt;", "Make &amp; Co", "M-1", "S-1")
	request("/customers/"+customer+"/vehicles", customer, s.handleCustomerVehicle, url.Values{"name": {"Truck <A>"}, "make": {"Ford & Co"}, "model": {"F-1"}, "year": {"2024"}, "identifier": {"ID-1"}, "notes": {"Keep & inspect"}}, "Truck &lt;A&gt;", "Ford &amp; Co", "F-1", "2024", "ID-1", "Keep &amp; inspect")

	got, err := base.GetCustomer(ctx, shop, customer)
	if err != nil || got.Name != "Original customer" {
		t.Fatalf("failed drafts changed customer: %+v %v", got, err)
	}
}

type failingReadPool struct {
	database.Pool
	err       error
	failQuery bool
	failRow   bool
}

func (p failingReadPool) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	if p.failQuery {
		return nil, p.err
	}
	return p.Pool.Query(ctx, query, args...)
}

func (p failingReadPool) QueryRow(ctx context.Context, query string, args ...any) database.Row {
	if p.failRow {
		return unavailableAuthRow{p.err}
	}
	return p.Pool.QueryRow(ctx, query, args...)
}

func TestCustomerAndDashboardReadOutagesReturnUnavailable(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testCustomerAndDashboardReadOutagesReturnUnavailable(t, db)
}

func TestSQLServerCustomerAndDashboardReadOutagesReturnUnavailable(t *testing.T) {
	testCustomerAndDashboardReadOutagesReturnUnavailable(t, sqlServerTestDB(t))
}

func testCustomerAndDashboardReadOutagesReturnUnavailable(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	shop := createTestShop(t, ctx, db, "outage-reads-"+time.Now().Format("150405.000000000"))
	customer := createTestCustomer(t, ctx, db, shop, "Read customer")
	failure := errors.New("private read failure")
	u := &User{ShopID: shop, Role: "owner"}
	tmpl := template.Must(template.ParseFS(templateFS, "templates/*.html"))
	for _, tc := range []struct {
		name   string
		pool   database.Pool
		handle http.HandlerFunc
		path   string
		id     string
	}{
		{"dashboard", failingReadPool{Pool: db.Pool, err: failure, failRow: true}, nil, "/dashboard", ""},
		{"customer list", failingReadPool{Pool: db.Pool, err: failure, failQuery: true}, nil, "/customers", ""},
		{"customer record", failingReadPool{Pool: db.Pool, err: failure, failRow: true}, nil, "/customers/" + customer, customer},
		{"customer properties", failingReadPool{Pool: db.Pool, err: failure, failQuery: true}, nil, "/customers/" + customer, customer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{store: &Store{db: &DB{Pool: tc.pool}}, tmpl: tmpl, clock: fixedClock{now: time.Now()}}
			handle := s.handleDashboard
			switch tc.name {
			case "customer list":
				handle = s.handleCustomers
			case "customer record", "customer properties":
				handle = s.handleCustomerEdit
			}
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			r.SetPathValue("id", tc.id)
			r = r.WithContext(contextWithUser(ctx, u))
			w := httptest.NewRecorder()
			handle(w, r)
			if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), failure.Error()) {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestDemoAdmissionDistinguishesCapacityFromDependencyFailure(t *testing.T) {
	dependency := errors.New("private SQL 40613 detail")
	for _, tc := range []struct {
		name       string
		err        error
		status     int
		retryAfter string
		capacity   bool
	}{
		{"dependency", dependency, http.StatusServiceUnavailable, "15", false},
		{"capacity", errPublicDemoCapacityReached, http.StatusTooManyRequests, "300", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{auth: &Auth{}, store: &Store{}, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")), publicDemoSeed: func(context.Context) (PublicDemoResult, error) { return PublicDemoResult{}, tc.err }}
			w := httptest.NewRecorder()
			s.handlePublicDemoPost(w, httptest.NewRequest(http.MethodPost, "/demo/session", nil))
			body := w.Body.String()
			if w.Code != tc.status || w.Header().Get("Retry-After") != tc.retryAfter || strings.Contains(body, dependency.Error()) || strings.Contains(strings.ToLower(body), "capacity") != tc.capacity {
				t.Fatalf("status=%d retry=%q body=%s", w.Code, w.Header().Get("Retry-After"), body)
			}
			attempt := s.demoThrottle.attempts["public-demo:global"].failures
			if tc.capacity && attempt != 1 || !tc.capacity && attempt != 0 {
				t.Fatalf("limiter attempt=%d", attempt)
			}
		})
	}
}

func TestDemoSessionFailureRefundsAdmissionAndCleansUp(t *testing.T) {
	failure := errors.New("private session database detail")
	pool := &unavailableAuthPool{err: failure}
	cleaned := ""
	s := &Server{
		auth:  newAuth(&DB{Pool: pool}, "test-session-secret", false),
		store: &Store{},
		tmpl:  template.Must(template.ParseFS(templateFS, "templates/*.html")),
		publicDemoSeed: func(context.Context) (PublicDemoResult, error) {
			return PublicDemoResult{ShopID: "shop-to-clean", UserID: "12345678-1234-1234-1234-123456789012", ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		publicDemoCleanup: func(_ context.Context, shopID string) { cleaned = shopID },
	}
	w := httptest.NewRecorder()
	s.handlePublicDemoPost(w, httptest.NewRequest(http.MethodPost, "/demo/session", nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "15" || cleaned != "shop-to-clean" || strings.Contains(w.Body.String(), failure.Error()) {
		t.Fatalf("session failure status=%d retry=%q cleaned=%q body=%s", w.Code, w.Header().Get("Retry-After"), cleaned, w.Body.String())
	}
	if w.Header().Get("Set-Cookie") != "" || s.demoThrottle.attempts["public-demo:global"].failures != 0 {
		t.Fatal("session failure issued a cookie or consumed demo admission")
	}
}

type persistentOutagePool struct {
	database.Pool
	err error
}

func (p persistentOutagePool) Exec(context.Context, string, ...any) (database.Result, error) {
	return nil, p.err
}
func (p persistentOutagePool) Query(context.Context, string, ...any) (database.Rows, error) {
	return nil, p.err
}
func (p persistentOutagePool) QueryRow(context.Context, string, ...any) database.Row {
	return unavailableAuthRow{p.err}
}
func (p persistentOutagePool) Begin(context.Context) (database.Tx, error) { return nil, p.err }

func TestCustomerDraftsSurvivePersistentOutageWithoutLogLeaks(t *testing.T) {
	private := "private submitted alex@example.test at 12 Private Road"
	pool := persistentOutagePool{Pool: &unavailableAuthPool{err: errors.New(private)}, err: errors.New(private)}
	s := &Server{store: &Store{db: &DB{Pool: pool}}, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	u := &User{ShopID: "12345678-1234-1234-1234-123456789012", Role: "owner"}
	var logs bytes.Buffer
	priorWriter := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(priorWriter) })

	for _, tc := range []struct {
		path, id string
		handle   http.HandlerFunc
		values   url.Values
		retained []string
	}{
		{"/customers/12345678-1234-1234-1234-123456789013/properties", "12345678-1234-1234-1234-123456789013", s.handlePropertyCreate, url.Values{"address_line1": {"12 <Private> Road"}, "address_line2": {"Unit & 2"}, "city": {"Columbus"}, "state": {"OH"}, "postal_code": {"43000"}}, []string{"12 &lt;Private&gt; Road", "Unit &amp; 2", "Columbus", "43000"}},
		{"/customers/12345678-1234-1234-1234-123456789013/vehicles", "12345678-1234-1234-1234-123456789013", s.handleCustomerVehicle, url.Values{"name": {"Truck <A>"}, "make": {"Make & Co"}, "model": {"M-1"}, "year": {"2024"}, "identifier": {"ID-1"}, "notes": {"Keep & retry"}}, []string{"Truck &lt;A&gt;", "Make &amp; Co", "M-1", "2024", "ID-1", "Keep &amp; retry"}},
	} {
		r := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", tc.id)
		r = r.WithContext(contextWithUser(context.Background(), u))
		w := httptest.NewRecorder()
		tc.handle(w, r)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Location") != "" {
			t.Fatalf("%s status=%d location=%q body=%s", tc.path, w.Code, w.Header().Get("Location"), w.Body.String())
		}
		for _, value := range tc.retained {
			if !strings.Contains(w.Body.String(), value) {
				t.Fatalf("%s lost %q: %s", tc.path, value, w.Body.String())
			}
		}
	}
	if strings.Contains(logs.String(), private) || strings.Contains(logs.String(), "alex@example.test") || strings.Contains(logs.String(), "12 Private Road") {
		t.Fatalf("private provider detail reached logs: %s", logs.String())
	}
}

func TestCustomerDeleteSerializesConcurrentDependency(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testCustomerDeleteSerializesConcurrentDependency(t, db)
}

func TestSQLServerCustomerDeleteSerializesConcurrentDependency(t *testing.T) {
	testCustomerDeleteSerializesConcurrentDependency(t, sqlServerTestDB(t))
}

func testCustomerDeleteSerializesConcurrentDependency(t *testing.T, db *DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := &Store{db: db}
	shop := createTestShop(t, ctx, db, "delete-race-"+time.Now().Format("150405.000000000"))
	customer := createTestCustomer(t, ctx, db, shop, "Delete race")
	dependent, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer dependent.Rollback(ctx)
	var locked string
	if err := dependent.QueryRow(ctx, database.Statement(dependent.Dialect(), `SELECT id FROM customers WHERE shop_id=$1 AND id=$2 FOR KEY SHARE`, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.customers WITH(HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shop, customer).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	if _, err := dependent.Exec(ctx, database.Statement(dependent.Dialect(), `INSERT INTO jobs(shop_id,customer_id,submission_key,creation_fingerprint,kind,title,details) VALUES($1,$2,$3,$4,'job','Concurrent job','')`, `INSERT INTO dbo.jobs(shop_id,customer_id,submission_key,creation_fingerprint,kind,title,details) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,@p4,N'job',N'Concurrent job',N'')`), shop, customer, "12345678901234567890123456789012", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	deleted := make(chan error, 1)
	go func() {
		close(started)
		deleted <- store.DeleteCustomer(ctx, shop, customer)
	}()
	<-started
	select {
	case err := <-deleted:
		t.Fatalf("customer delete escaped uncommitted dependency: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := dependent.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; !errors.Is(err, errCustomerDeleteConflict) {
		t.Fatalf("delete result=%v, want protected dependency conflict", err)
	}
	if _, err := store.GetCustomer(ctx, shop, customer); err != nil {
		t.Fatalf("protected customer was deleted: %v", err)
	}
}

type pauseCustomerDeletePool struct {
	database.Pool
	reached chan struct{}
	release chan struct{}
}

func (p *pauseCustomerDeletePool) Begin(ctx context.Context) (database.Tx, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &pauseCustomerDeleteTx{Tx: tx, reached: p.reached, release: p.release}, nil
}

type pauseCustomerDeleteTx struct {
	database.Tx
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func (t *pauseCustomerDeleteTx) Exec(ctx context.Context, query string, args ...any) (database.Result, error) {
	lower := strings.ToLower(query)
	if strings.Contains(lower, "delete from") && strings.Contains(lower, "customers") {
		t.once.Do(func() { close(t.reached) })
		select {
		case <-t.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return t.Tx.Exec(ctx, query, args...)
}

func TestCustomerDeleteResolvesLaterDependency(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testCustomerDeleteResolvesLaterDependency(t, db)
}

func TestSQLServerCustomerDeleteResolvesLaterDependency(t *testing.T) {
	testCustomerDeleteResolvesLaterDependency(t, sqlServerTestDB(t))
}

func testCustomerDeleteResolvesLaterDependency(t *testing.T, db *DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shop := createTestShop(t, ctx, db, "delete-first-"+time.Now().Format("150405.000000000"))
	customer := createTestCustomer(t, ctx, db, shop, "Delete first")
	reached, release := make(chan struct{}), make(chan struct{})
	store := &Store{db: &DB{Pool: &pauseCustomerDeletePool{Pool: db.Pool, reached: reached, release: release}}}
	deleted := make(chan error, 1)
	go func() { deleted <- store.DeleteCustomer(ctx, shop, customer) }()
	<-reached
	dependent := make(chan error, 1)
	go func() {
		tx, err := db.Pool.Begin(ctx)
		if err == nil {
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, database.Statement(tx.Dialect(), `INSERT INTO jobs(shop_id,customer_id,submission_key,creation_fingerprint,kind,title,details) VALUES($1,$2,$3,$4,'job','Late job','')`, `INSERT INTO dbo.jobs(shop_id,customer_id,submission_key,creation_fingerprint,kind,title,details) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,@p4,N'job',N'Late job',N'')`), shop, customer, "21098765432109876543210987654321", strings.Repeat("b", 64))
			if err == nil {
				err = tx.Commit(ctx)
			}
		}
		dependent <- err
	}()
	var dependentErr error
	dependentFinished := false
	select {
	case dependentErr = <-dependent:
		dependentFinished = true
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	deleteErr := <-deleted
	if !dependentFinished {
		dependentErr = <-dependent
	}
	if dependentErr == nil {
		if !errors.Is(deleteErr, errCustomerDeleteConflict) {
			t.Fatalf("committed dependency produced delete result %v", deleteErr)
		}
		if _, err := (&Store{db: db}).GetCustomer(ctx, shop, customer); err != nil {
			t.Fatalf("conflicted delete removed customer: %v", err)
		}
		return
	}
	if deleteErr != nil {
		t.Fatalf("both concurrent operations failed: delete=%v dependency=%v", deleteErr, dependentErr)
	}
	if _, err := (&Store{db: db}).GetCustomer(ctx, shop, customer); !errors.Is(err, database.ErrNoRows) {
		t.Fatalf("customer survived successful delete: %v", err)
	}
}

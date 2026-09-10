package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

type schemaReadinessPool struct {
	database.Pool
	checks int
	pings  int
	err    error
}

func (p *schemaReadinessPool) CheckRuntime(ctx context.Context) error {
	p.checks++
	if _, ok := ctx.Deadline(); !ok {
		return errors.New("readiness schema check has no request deadline")
	}
	return p.err
}

func (p *schemaReadinessPool) Ping(context.Context) error {
	p.pings++
	return errors.New("Azure readiness used Ping")
}

func TestLivezStaysProcessOnlyAndReadyzRequiresDatabase(t *testing.T) {
	server := &Server{demo: true}
	liveRequest := httptest.NewRequest(http.MethodGet, "/livez", nil)
	liveResponse := httptest.NewRecorder()
	server.handleLivez(liveResponse, liveRequest)
	if liveResponse.Code != http.StatusOK || strings.TrimSpace(liveResponse.Body.String()) != "ok" {
		t.Fatalf("livez = %d %q", liveResponse.Code, liveResponse.Body.String())
	}

	readyRequest := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readyResponse := httptest.NewRecorder()
	server.handleReadyz(readyResponse, readyRequest)
	if readyResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz without database = %d, want %d", readyResponse.Code, http.StatusServiceUnavailable)
	}
}

func TestRollbackSafetyMarkerAttestsDemoSandboxAwareRelease(t *testing.T) {
	server := &Server{}
	response := httptest.NewRecorder()
	server.handleRollbackSafety(response, httptest.NewRequest(http.MethodGet, "/rollback-safetyz", nil))
	if response.Code != http.StatusOK || response.Body.String() != "demo-sandbox-v2\nenvironment=\nbackend=postgres\ngeneration=legacy\n" {
		t.Fatalf("rollback marker = %d %q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("rollback marker cache policy = %q", response.Header().Get("Cache-Control"))
	}
}

func TestHTTPServerUsesConfiguredTimeouts(t *testing.T) {
	config := Config{RuntimeLimits: RuntimeLimits{
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       4 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       6 * time.Second,
	}}
	server := newHTTPServer(config, http.NotFoundHandler())
	if server.ReadHeaderTimeout != 3*time.Second || server.ReadTimeout != 4*time.Second || server.WriteTimeout != 5*time.Second || server.IdleTimeout != 6*time.Second {
		t.Fatalf("server timeouts = %#v", server)
	}
}

func TestRequestDeadlineInterruptsBodyParsing(t *testing.T) {
	finished := make(chan time.Duration, 1)
	started := time.Now()
	handler := requestDeadlineHandler(50*time.Millisecond, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("request body parsing started without the application deadline")
		}
		err := r.ParseForm()
		if err == nil {
			t.Error("incomplete request body parsed successfully")
		}
		finished <- time.Since(started)
		writeRequestParseError(w, err)
	}))
	server := httptest.NewServer(handler)
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := connection.Write([]byte("POST / HTTP/1.1\r\nHost: " + address + "\r\nContent-Type: application/x-www-form-urlencoded\r\nContent-Length: 100\r\n\r\nx")); err != nil {
		t.Fatal(err)
	}
	_ = connection.SetReadDeadline(time.Now().Add(time.Second))
	request, _ := http.NewRequest(http.MethodPost, server.URL, nil)
	response, err := http.ReadResponse(bufio.NewReader(connection), request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusRequestTimeout {
		t.Fatalf("stalled body status = %d", response.StatusCode)
	}
	select {
	case elapsed := <-finished:
		if elapsed < 30*time.Millisecond || elapsed > 500*time.Millisecond {
			t.Fatalf("body deadline elapsed = %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("request body ignored the application deadline")
	}
}

func TestAzurePublicPagesStayLazyAndReadinessChecksSchema(t *testing.T) {
	config := Config{
		Environment: EnvironmentStaging, DatabaseBackend: "azure_sql", EnableAzureSQLHTTP: true,
		SessionSecret: "synthetic-session-secret", EnablePublicDemo: true,
		RuntimeLimits: RuntimeLimits{RequestTimeout: time.Second, MaxRequestBodyBytes: 8 << 20, MaxMultipartMemory: 1 << 20, AuthMaxAttempts: 5, AuthThrottleWindow: time.Minute},
	}
	pool := &schemaReadinessPool{}
	db := &DB{Pool: pool}
	server := &Server{
		tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html")), config: config,
		store: &Store{db: db}, auth: newAuth(db, config.SessionSecret, true),
		interest: &databaseInterestRecorder{db: db}, throttle: newAuthThrottle(config.RuntimeLimits),
	}
	static := fstest.MapFS{"asset.txt": {Data: []byte("asset")}}
	handler := server.handler(static)
	for _, path := range []string{"/", "/login", "/signup", "/interest/thanks", "/demo", "/livez", "/static/asset.txt"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "stale-session"})
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("lazy public path %s = %d", path, response.Code)
		}
	}
	if pool.checks != 0 || pool.pings != 0 {
		t.Fatalf("public pages contacted Azure SQL: checks=%d pings=%d", pool.checks, pool.pings)
	}
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK || pool.checks != 1 || pool.pings != 0 {
		t.Fatalf("Azure readiness = %d checks=%d pings=%d", ready.Code, pool.checks, pool.pings)
	}
	pool.err = errors.New("schema drift")
	ready = httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable || pool.checks != 2 || pool.pings != 0 {
		t.Fatalf("drifted Azure readiness = %d checks=%d pings=%d", ready.Code, pool.checks, pool.pings)
	}
}

func TestServeHTTPShutsDownAndClosesResources(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	closed := make(chan struct{})
	errCh := make(chan error, 1)
	go func() {
		errCh <- serveHTTP(ctx, Config{RuntimeLimits: RuntimeLimits{ShutdownTimeout: time.Second}}, http.NotFoundHandler(), listener, func() { close(closed) })
	}()
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("serveHTTP shutdown error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serveHTTP did not stop after cancellation")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("resource cleanup was not invoked")
	}
}

func TestServeHTTPForceClosesAfterShutdownDeadlineAndCleansOnce(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	cleaned := make(chan struct{}, 2)
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveHTTP(ctx, Config{RuntimeLimits: RuntimeLimits{ShutdownTimeout: 50 * time.Millisecond}}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(started)
			<-release
		}), listener, func() { cleaned <- struct{}{} })
	}()
	clientDone := make(chan error, 1)
	go func() {
		_, err := http.Get("http://" + listener.Addr().String())
		clientDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking request did not start")
	}
	cancel()
	select {
	case err := <-serverDone:
		if err == nil || !strings.Contains(err.Error(), "graceful shutdown") {
			t.Fatalf("forced shutdown error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown deadline did not force the server closed")
	}
	select {
	case <-cleaned:
	case <-time.After(time.Second):
		t.Fatal("forced shutdown did not clean resources")
	}
	select {
	case <-cleaned:
		t.Fatal("forced shutdown cleaned resources more than once")
	default:
	}
	select {
	case err := <-clientDone:
		if err == nil {
			t.Fatal("forced shutdown left the client connection open")
		}
	case <-time.After(time.Second):
		t.Fatal("forced shutdown did not release the client")
	}
	close(release)
}

func TestCompletionLogUsesRoutePatternWithoutSensitivePath(t *testing.T) {
	var output bytes.Buffer
	originalOutput := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(originalOutput)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /renew/{token}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	server := &Server{}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/renew/private-token-value", nil)
	server.observabilityHandler(mux).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("response=%d request_id=%q", response.Code, response.Header().Get("X-Request-ID"))
	}
	entry := output.String()
	if strings.Contains(entry, "private-token-value") || !strings.Contains(entry, "route=GET /renew/{token}") || !strings.Contains(entry, "request_id=") {
		t.Fatalf("unsafe or incomplete completion log: %q", entry)
	}
}

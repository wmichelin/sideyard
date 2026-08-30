package app

import (
	"bytes"
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

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

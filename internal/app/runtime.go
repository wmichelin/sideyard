package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"
)

func newHTTPServer(config Config, handler http.Handler) *http.Server {
	limits := config.RuntimeLimits
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: limits.ReadHeaderTimeout,
		ReadTimeout:       limits.ReadTimeout,
		WriteTimeout:      limits.WriteTimeout,
		IdleTimeout:       limits.IdleTimeout,
	}
}

// The request deadline is installed outside security and authentication so it
// covers body parsing, a cold database connection, domain work and rendering.
// Handlers retain control of their response; this middleware only cancels work.
func requestDeadlineHandler(timeout time.Duration, next http.Handler) http.Handler {
	if timeout <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		deadline := time.Now().Add(timeout)
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(deadline)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// serveHTTP drains in-flight HTTP work before the application releases its
// database pool. The caller owns the signal context and resource cleanup.
func serveHTTP(ctx context.Context, config Config, handler http.Handler, listener net.Listener, closeResources func()) error {
	if closeResources == nil {
		closeResources = func() {}
	}
	defer closeResources()

	server := newHTTPServer(config, handler)
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Print("shutdown requested")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.RuntimeLimits.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			<-errCh
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		log.Print("shutdown complete")
		return nil
	}
}

func runDailyRenewalGeneration(ctx context.Context, store *Store, interval time.Duration, now func() time.Time, wakeInterest func()) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runRenewalMaintenance(ctx, store, now, "scheduled")
			// Reconcile durable signup work in the same active database window.
			// This also catches handoffs interrupted during a rolling deployment.
			if wakeInterest != nil {
				wakeInterest()
			}
		}
	}
}

func runRenewalMaintenance(ctx context.Context, store *Store, now func() time.Time, phase string) {
	asOf := now()
	if err := store.CleanupExpiredPublicDemos(ctx); err != nil {
		log.Printf("demo maintenance failed phase=%s operation=cleanup_expired_sandboxes error=%v", phase, err)
	}
	if err := store.GenerateAllServiceOccurrences(ctx); err != nil {
		log.Printf("service maintenance failed phase=%s operation=generate_occurrences error=%v", phase, err)
	}
	if err := store.GenerateAllRenewalTasks(ctx, asOf); err != nil {
		log.Printf("renewal maintenance failed phase=%s operation=generate_tasks error=%v", phase, err)
	}
	if err := store.QueueRenewalDigests(ctx, asOf); err != nil {
		log.Printf("renewal maintenance failed phase=%s operation=queue_digests error=%v", phase, err)
	}
}

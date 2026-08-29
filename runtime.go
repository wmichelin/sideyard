package main

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

func runDailyRenewalGeneration(ctx context.Context, store *Store, interval time.Duration, now func() time.Time) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runRenewalMaintenance(ctx, store, now, "scheduled")
		}
	}
}

func runRenewalMaintenance(ctx context.Context, store *Store, now func() time.Time, phase string) {
	asOf := now()
	if err := store.GenerateAllRenewalTasks(ctx, asOf); err != nil {
		log.Printf("renewal maintenance failed phase=%s operation=generate_tasks error=%v", phase, err)
	}
	if err := store.QueueRenewalDigests(ctx, asOf); err != nil {
		log.Printf("renewal maintenance failed phase=%s operation=queue_digests error=%v", phase, err)
	}
}

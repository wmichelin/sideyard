package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/sqlserver"
	"github.com/wmichelin/clubdue/internal/testdb"
)

func sqlServerTestDB(t *testing.T) *DB {
	t.Helper()
	p := sqlserver.OpenConnector(testdb.SQLServer(t))
	t.Cleanup(p.Close)
	if err := p.MigrateSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &DB{Pool: p}
}

func TestSQLServerInterestNotificationRetriesAndConcurrency(t *testing.T) {
	t.Run("durable retry and acknowledgement", func(t *testing.T) { testInterestDeliveryRetry(t, sqlServerTestDB(t)) })
	t.Run("concurrent workers skip claimed signup", func(t *testing.T) { testInterestConcurrentWorkers(t, sqlServerTestDB(t)) })
}

func TestSQLServerInterestSaveIsAtomicAndDeduplicatesConcurrentSubmissions(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	var wakes atomic.Int32
	store := databaseInterestRecorder{db: db, wake: func() { wakes.Add(1) }}
	in := interestSignup{Name: strings.Repeat("🔧", 120), Email: "contractor@example.test", Business: strings.Repeat("🛠", 160), Trade: "Electrical", Message: strings.Repeat("💡", 2000)}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.SaveInterest(ctx, in); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if wakes.Load() != 12 {
		t.Fatal("committed submissions did not wake delivery")
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.interest_notifications`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate outbox rows %d %v", count, err)
	}
	var gotName, gotMessage string
	if err := db.Pool.QueryRow(ctx, `SELECT name,message FROM dbo.interest_signups WHERE email=@p1`, in.Email).Scan(&gotName, &gotMessage); err != nil || gotName != in.Name || gotMessage != in.Message {
		t.Fatalf("Unicode data changed: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.reject_interest_queue ON dbo.interest_notifications AFTER INSERT AS THROW 50001, 'synthetic outbox failure', 1;`); err != nil {
		t.Fatal(err)
	}
	in.Email = "another@example.test"
	if err := store.SaveInterest(ctx, in); err == nil {
		t.Fatal("queue failure reported saved signup")
	}
	if wakes.Load() != 12 {
		t.Fatal("failed save woke delivery before committing")
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.interest_signups WHERE email=@p1`, in.Email).Scan(&count); err != nil || count != 0 {
		t.Fatalf("signup committed without outbox %d %v", count, err)
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.reject_interest_queue`); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveInterest(ctx, in); err != nil {
		t.Fatalf("retry after outbox recovery: %v", err)
	}
	in.Email = "oversized@example.test"
	in.Message = strings.Repeat("💡", 1999) + "aa"
	if err := store.SaveInterest(ctx, in); err == nil {
		t.Fatal("message exceeded 2000 characters at the UTF-16 concatenation boundary")
	}
	in.Message = "valid"
	in.Name += "x"
	if err := store.SaveInterest(ctx, in); err == nil {
		t.Fatal("name exceeded existing 120-character limit")
	}
}

func TestSQLServerInterestWakeObservesCommittedSignup(t *testing.T) {
	db := sqlServerTestDB(t)
	in := interestSignup{Name: "Synthetic wake test", Email: "wake@example.test"}
	called := false
	store := databaseInterestRecorder{db: db, wake: func() {
		called = true
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.interest_signups s JOIN dbo.interest_notifications n ON n.signup_id=s.id WHERE s.email=@p1`, in.Email).Scan(&count); err != nil || count != 1 {
			t.Errorf("wake could not see committed signup/outbox: %d %v", count, err)
		}
	}}
	if err := store.SaveInterest(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("successful save did not wake worker")
	}
}

func TestSQLServerInterestFinalHTTPCommitSurvivesShutdownAndMissedWake(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	oldCtx, stopProcess := context.WithCancel(ctx)
	defer stopProcess()
	oldWake, notifyOld := newInterestNotificationSignal()
	newWake, reconcileNew := newInterestNotificationSignal()
	oldIdle, newIdle := make(chan struct{}, 2), make(chan struct{}, 2)
	observedWait := func(idle chan struct{}) func(context.Context, <-chan struct{}, *time.Duration) bool {
		return func(ctx context.Context, wake <-chan struct{}, delay *time.Duration) bool {
			if delay == nil {
				select {
				case idle <- struct{}{}:
				default:
				}
			}
			return waitForInterestNotification(ctx, wake, delay)
		}
	}
	stopOld := startInterestNotificationWorker(oldCtx, interestNotificationWorker{
		wake: oldWake, now: time.Now, wait: observedWait(oldIdle),
		queue: databaseInterestQueue{db: db, sender: interestSendFunc(func(ctx context.Context, _ interestNotification) (string, error) { <-ctx.Done(); return "", ctx.Err() })},
	})
	defer stopOld()
	var accepted atomic.Int32
	stopNew := startInterestNotificationWorker(ctx, interestNotificationWorker{
		wake: newWake, now: time.Now, wait: observedWait(newIdle),
		queue: databaseInterestQueue{db: db, sender: interestSendFunc(func(context.Context, interestNotification) (string, error) {
			accepted.Add(1)
			return "accepted-after-reconciliation", nil
		})},
	})
	defer stopNew()
	for _, idle := range []chan struct{}{oldIdle, newIdle} {
		select {
		case <-idle:
		case <-ctx.Done():
			t.Fatal("workers did not become idle")
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	recorder := databaseInterestRecorder{db: db, wake: notifyOld}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- serveHTTP(oldCtx, Config{RuntimeLimits: RuntimeLimits{ShutdownTimeout: 3 * time.Second}}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				w.WriteHeader(503)
				return
			}
			if err := recorder.SaveInterest(r.Context(), interestSignup{Name: "Last request", Email: "shutdown@example.test"}); err != nil {
				w.WriteHeader(503)
				return
			}
			w.WriteHeader(http.StatusCreated)
		}), listener, stopOld)
	}()
	response := make(chan int, 1)
	go func() {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+listener.Addr().String(), nil)
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			response <- 0
			return
		}
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
		response <- r.StatusCode
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("request did not start")
	}
	stopProcess() // HTTP drain starts before this last signup commits.
	unblock()
	select {
	case status := <-response:
		if status != http.StatusCreated {
			t.Fatalf("last signup status %d", status)
		}
	case <-ctx.Done():
		t.Fatal("request did not finish")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("shutdown did not complete")
	}
	var pending int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.interest_notifications WHERE sent_at IS NULL`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("last signup lost: %d %v", pending, err)
	}
	if accepted.Load() != 0 {
		t.Fatal("surviving idle instance unexpectedly received the stopped instance's wake")
	}
	reconcileNew() // The same wake used by daily maintenance recovers durable work.
	select {
	case <-newIdle:
	case <-ctx.Done():
		t.Fatal("reconciliation did not finish")
	}
	if accepted.Load() != 1 {
		t.Fatalf("reconciled delivery count %d", accepted.Load())
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.interest_notifications WHERE sent_at IS NOT NULL`).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("reconciled acknowledgement missing: %d %v", pending, err)
	}
}

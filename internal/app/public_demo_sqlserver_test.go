package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/wmichelin/clubdue/internal/database"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSQLServerPublicDemoSeedBaseAndHydration(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2035, 9, 2, 12, 0, 0, 0, time.UTC)}}
	started := time.Now()
	result, err := s.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native demo warm seed %s", time.Since(started))
	var email, normalized string
	if err := db.Pool.QueryRow(ctx, `SELECT email,email_normalized FROM dbo.users WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, result.ShopID).Scan(&email, &normalized); err != nil || normalized != strings.ToLower(email) {
		t.Fatal("demo email normalization", err)
	}
	for table, want := range map[string]int{"plan_templates": 6, "customers": 30, "properties": 31, "equipment": 8, "agreements": 26, "service_schedules": 22, "visit_dues": 8} {
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.`+table+` WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, result.ShopID).Scan(&count); err != nil || count != want {
			t.Fatal(table, count, want, err)
		}
	}
}

func TestSQLServerPublicDemoFailureCleanup(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2035, 9, 2, 12, 0, 0, 0, time.UTC)}}
	for _, table := range []string{"service_schedules", "service_occurrences"} {
		t.Run(table, func(t *testing.T) {
			if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_seed ON dbo.`+table+` AFTER INSERT AS BEGIN THROW 51000,'synthetic fixture failure',1; END`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SeedPublicDemo(ctx); err == nil {
				t.Fatal("partial fixture accepted")
			}
			var n int
			if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.shops`).Scan(&n); err != nil || n != 0 {
				t.Fatal("partial workspace survived", n, err)
			}
			if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_seed`); err != nil {
				t.Fatal(err)
			}
		})
	}
	// An invalid batch mapping must fail even when an INNER JOIN would produce no rows.
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, sqlServerPublicFixture, "00000000-0000-0000-0000-000000000000", "{}", s.nowUTC(), 6, 30, 31, 8, 26, 22); err == nil {
		t.Fatal("missing JSON mappings silently succeeded")
	}
}

type committedDemoFailurePool struct{ database.Pool }

func (p committedDemoFailurePool) Begin(ctx context.Context) (database.Tx, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return committedDemoFailureTx{tx}, nil
}

type committedDemoFailureTx struct{ database.Tx }

func (tx committedDemoFailureTx) Commit(ctx context.Context) error {
	if err := tx.Tx.Commit(ctx); err != nil {
		return err
	}
	return errors.New("synthetic lost commit acknowledgement")
}
func TestSQLServerPublicDemoAmbiguousBaseCommitCleansUp(t *testing.T) {
	db := sqlServerTestDB(t)
	s := &Store{db: &DB{Pool: committedDemoFailurePool{db.Pool}}, clock: fixedClock{now: time.Date(2035, 9, 2, 12, 0, 0, 0, time.UTC)}}
	if _, err := s.SeedPublicDemo(context.Background()); err == nil {
		t.Fatal("lost acknowledgement reported success")
	}
	var n int
	if err := db.Pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM dbo.shops`).Scan(&n); err != nil || n != 0 {
		t.Fatal("ambiguous base commit left new workspace", n, err)
	}
}
func TestSQLServerPublicDemoSessionFailureNeverIssuesCookie(t *testing.T) {
	db := sqlServerTestDB(t)
	clock := fixedClock{now: time.Date(2035, 9, 2, 12, 0, 0, 0, time.UTC)}
	s := &Server{store: &Store{db: db, clock: clock}, clock: clock, config: securityTestConfig(), auth: newAuthWithClock(&DB{Pool: committedDemoFailurePool{db.Pool}}, "synthetic-secret", false, clock)}
	r := httptest.NewRequest("POST", "/demo", nil)
	w := httptest.NewRecorder()
	s.handlePublicDemoPost(w, r)
	if w.Code != 503 || w.Header().Get("Set-Cookie") != "" {
		t.Fatal("ambiguous session issued access", w.Code)
	}
	var shops, sessions int
	if err := db.Pool.QueryRow(context.Background(), `SELECT (SELECT COUNT(*) FROM dbo.shops),(SELECT COUNT(*) FROM dbo.sessions)`).Scan(&shops, &sessions); err != nil || shops != 0 || sessions != 0 {
		t.Fatal("failed session left workspace/session", shops, sessions, err)
	}
}

type changingDemoClock struct {
	first time.Time
	calls int
}

func (c *changingDemoClock) Now() time.Time {
	c.calls++
	if c.calls == 1 {
		return c.first
	}
	return c.first.Add(2 * time.Minute)
}
func TestSQLServerPublicDemoKeepsOneTimeAcrossMidnight(t *testing.T) {
	db := sqlServerTestDB(t)
	clock := &changingDemoClock{first: time.Date(2034, 12, 31, 23, 59, 0, 0, time.UTC)}
	s := &Store{db: db, clock: clock}
	ctx := context.Background()
	demo, err := s.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if clock.calls != 1 {
		t.Fatal("hydration reread live clock", clock.calls)
	}
	var minYear, maxYear int
	if err := db.Pool.QueryRow(ctx, `SELECT MIN(due_year),MAX(due_year) FROM dbo.visit_dues WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, demo.ShopID).Scan(&minYear, &maxYear); err != nil || minYear != 2034 || maxYear != 2034 {
		t.Fatal("seed crossed work year", minYear, maxYear, err)
	}
}

func TestSQLServerPublicDemoAdmissionCancellationAndSingleConnection(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := &Store{db: db, clock: fixedClock{now: time.Date(2035, 9, 2, 12, 0, 0, 0, time.UTC)}}
	s.publicDemoSeedOnce.Do(func() { s.publicDemoSeedGate = make(chan struct{}, 1) })
	s.publicDemoSeedGate <- struct{}{}
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	if _, err := s.SeedPublicDemo(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("local gate ignored cancellation", err)
	}
	<-s.publicDemoSeedGate
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var result int
	if err := tx.QueryRow(ctx, `DECLARE @result int; EXEC @result=sys.sp_getapplock @Resource=N'clubdue-public-demo-sandboxes',@LockMode=N'Exclusive',@LockOwner=N'Transaction',@LockTimeout=0; SELECT @result;`).Scan(&result); err != nil || result < 0 {
		t.Fatal("lock fixture", err)
	}
	short, stop = context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	if _, err := s.SeedPublicDemo(short); err == nil || short.Err() == nil {
		t.Fatal("database gate ignored cancellation", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		held, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Rollback(ctx)
	}
	if _, err := s.SeedPublicDemo(ctx); err != nil {
		t.Fatal("cancelled seed leaked connection/one-connection seed failed", err)
	}
}

func TestSQLServerPublicDemoCrossStoreCapacityAndLegacySeed(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Date(2035, 9, 2, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	for i := 0; i < 99; i++ {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.shops(name,slug,kind,expires_at) VALUES(N'Capacity',@p1,'demo_sandbox',@p2)`, fmt.Sprint("capacity-", i), now.Add(publicDemoTTL)); err != nil {
			t.Fatal(err)
		}
	}
	gate := make(chan struct{})
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			other := &Store{db: db, clock: s.clock}
			<-gate
			_, err := other.SeedPublicDemo(ctx)
			done <- err
		}()
	}
	close(gate)
	wins := 0
	for i := 0; i < 2; i++ {
		err := <-done
		if err == nil {
			wins++
		} else if !strings.Contains(err.Error(), "capacity reached") {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("cross-store capacity", wins)
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.shops WHERE kind='demo_sandbox'`).Scan(&n); err != nil || n != 100 {
		t.Fatal("capacity exceeded", n, err)
	}
	first, err := s.SeedDemo(ctx)
	if err != nil {
		t.Fatal("development seed", err)
	}
	second, err := s.SeedDemo(ctx)
	if err != nil || first.ShopID != second.ShopID {
		t.Fatal("development retry", err)
	}
	if n := countShopRows(t, ctx, db, "agreements", first.ShopID); n != 3 {
		t.Fatal("development retry duplicated", n)
	}
}

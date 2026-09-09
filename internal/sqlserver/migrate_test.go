package sqlserver

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func fixtureMigrations() fstest.MapFS {
	return fstest.MapFS{
		"migrations/001_fixture.sql": {Data: []byte(`CREATE TABLE dbo.migration_test(id int PRIMARY KEY); INSERT INTO dbo.migration_test VALUES(1);`)},
		"migrations/002_fixture.sql": {Data: []byte(`ALTER TABLE dbo.migration_test ADD note nvarchar(100);`)},
	}
}

func TestSQLServerMigrationsRestartConcurrencyAndChecksums(t *testing.T) {
	p := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	source := fixtureMigrations()
	// A one-connection pool catches accidental db.BeginTx while the migrator
	// already holds its session-lock connection.
	p.db.SetMaxOpenConns(1)
	if err := p.Migrate(ctx, source); err != nil {
		t.Fatal(err)
	}
	assertNoConnections(t, p)
	p.db.SetMaxOpenConns(4)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Migrate(ctx, source); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	if err := p.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.schema_migrations`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("ledger count %d %v", count, err)
	}
	if err := p.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.migration_test`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("migration repeated %d %v", count, err)
	}
	changed := fixtureMigrations()
	changed["migrations/001_fixture.sql"].Data = append(changed["migrations/001_fixture.sql"].Data, []byte("\n-- changed")...)
	if err := p.Migrate(ctx, changed); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("changed migration accepted: %v", err)
	}
	assertNoConnections(t, p)
}

func TestSQLServerMigrationFailureRollsBackAndReleasesLock(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()
	source := fixtureMigrations()
	source["migrations/003_failure.sql"] = &fstest.MapFile{Data: []byte(`CREATE TABLE dbo.failed_migration(id int); THROW 50001, 'synthetic migration failure', 1;`)}
	if err := p.Migrate(ctx, source); err == nil {
		t.Fatal("broken migration succeeded")
	}
	var count int
	if err := p.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.schema_migrations WHERE version=3`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed migration ledger %d %v", count, err)
	}
	if err := p.QueryRow(ctx, `SELECT COUNT(*) FROM sys.tables WHERE name='failed_migration'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("DDL not rolled back %d %v", count, err)
	}
	assertNoConnections(t, p)
	if err := p.Migrate(ctx, fixtureMigrations()); err != nil {
		t.Fatalf("lock not released: %v", err)
	}
}

func TestSQLServerCancelledMigrationLockCanBeReacquired(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()
	conn, err := p.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var status int
	err = conn.QueryRowContext(ctx, `DECLARE @status int; EXEC @status=sys.sp_getapplock @Resource=N'sideyard-schema-migrations', @LockMode='Exclusive', @LockOwner='Session', @LockTimeout=0; SELECT @status;`).Scan(&status)
	if err != nil || status < 0 {
		t.Fatalf("hold migration lock %d %v", status, err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := p.Migrate(short, fixtureMigrations()); err == nil {
		t.Fatal("migration ignored cancelled lock wait")
	}
	conn.Close()
	if err := p.Migrate(ctx, fixtureMigrations()); err != nil {
		t.Fatalf("lock remained held: %v", err)
	}
	assertNoConnections(t, p)
}

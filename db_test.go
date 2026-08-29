package main

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrationChecksumsApplyAndSurviveRestart(t *testing.T) {
	ctx := context.Background()
	db := newMigrationTestDB(t, ctx)

	if err := db.migrate(ctx); err != nil {
		t.Fatalf("apply fresh migrations: %v", err)
	}
	assertMigrationChecksumsRecorded(t, ctx, db)

	if err := db.migrate(ctx); err != nil {
		t.Fatalf("restart migration check: %v", err)
	}
	assertMigrationChecksumsRecorded(t, ctx, db)
}

func TestMigrationLedgerBaselinesLegacyRows(t *testing.T) {
	ctx := context.Background()
	db := newMigrationTestDB(t, ctx)

	if _, err := db.Pool.Exec(ctx, `
		CREATE TABLE schema_migrations (
			version INT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		t.Fatalf("create legacy migration ledger: %v", err)
	}
	migrations := testMigrations(t, migrationFS)
	for _, migration := range migrations {
		if migration.version == 13 {
			continue
		}
		if _, err := db.Pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, migration.version); err != nil {
			t.Fatalf("record legacy migration %d: %v", migration.version, err)
		}
	}

	if err := db.migrate(ctx); err != nil {
		t.Fatalf("baseline legacy migration ledger: %v", err)
	}
	assertMigrationChecksumsRecorded(t, ctx, db)
}

func TestMigrationChecksumsRejectChangedAppliedMigration(t *testing.T) {
	ctx := context.Background()
	db := newMigrationTestDB(t, ctx)
	if err := db.migrate(ctx); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	changedSource := migrationTestSource(t, migrationFS)
	changed := changedSource["migrations/001_init.sql"]
	changed.Data = append(changed.Data, []byte("\n-- changed after application\n")...)
	changedSource["migrations/001_init.sql"] = changed

	err := db.migrateWithFS(ctx, changedSource)
	if err == nil || !strings.Contains(err.Error(), "migration 1 checksum does not match embedded migration") {
		t.Fatalf("changed applied migration error = %v", err)
	}
}

func newMigrationTestDB(t *testing.T, ctx context.Context) *DB {
	t.Helper()
	databaseURL := integrationDatabaseURL(t)

	adminConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse integration database URL: %v", err)
	}
	admin, err := pgxpool.NewWithConfig(ctx, adminConfig)
	if err != nil {
		t.Fatalf("connect integration database: %v", err)
	}

	schema := fmt.Sprintf("migration_test_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create migration test schema: %v", err)
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		admin.Close()
		t.Fatalf("parse schema-scoped database URL: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
		t.Fatalf("connect schema-scoped database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop migration test schema: %v", err)
		}
		admin.Close()
	})
	return &DB{Pool: pool}
}

func assertMigrationChecksumsRecorded(t *testing.T, ctx context.Context, db *DB) {
	t.Helper()
	migrations := testMigrations(t, migrationFS)
	var total, missing int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE checksum IS NULL)
		FROM schema_migrations`).Scan(&total, &missing); err != nil {
		t.Fatalf("read migration ledger: %v", err)
	}
	if total != len(migrations) || missing != 0 {
		t.Fatalf("migration ledger = total %d missing checksums %d, want total %d missing 0", total, missing, len(migrations))
	}
	for _, migration := range migrations {
		var checksum string
		if err := db.Pool.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version = $1`, migration.version).Scan(&checksum); err != nil {
			t.Fatalf("read checksum for migration %d: %v", migration.version, err)
		}
		if checksum != migration.checksum {
			t.Fatalf("migration %d checksum = %q, want %q", migration.version, checksum, migration.checksum)
		}
	}
}

func testMigrations(t *testing.T, source fs.FS) []migration {
	t.Helper()
	migrations, err := loadMigrations(source)
	if err != nil {
		t.Fatal(err)
	}
	return migrations
}

func migrationTestSource(t *testing.T, source fs.FS) fstest.MapFS {
	t.Helper()
	entries, err := fs.Glob(source, "migrations/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	result := make(fstest.MapFS, len(entries))
	for _, path := range entries {
		body, err := fs.ReadFile(source, path)
		if err != nil {
			t.Fatalf("read migration %q: %v", path, err)
		}
		result[path] = &fstest.MapFile{Data: body}
	}
	return result
}

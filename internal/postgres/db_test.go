package postgres

import (
	"context"
	"fmt"
	"io/fs"
	"os"
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

func TestOpenReadOnlyChecksSchemaAndCannotWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL := integrationDatabaseURL(t)
	writable, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	writable.Close()
	readOnly, err := OpenReadOnly(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()
	if err := readOnly.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := readOnly.Pool.Exec(ctx, `CREATE TEMP TABLE sideyard_read_only_probe(id int)`); err == nil {
		t.Fatal("read-only exporter connection accepted a write")
	}
}

// TestPrepareV29Fixture is invoked explicitly by the isolated cross-engine
// rehearsal. The dedicated database persists only until that script exits.
func TestPrepareV29Fixture(t *testing.T) {
	databaseURL := os.Getenv("POSTGRES_V29_TEST_URL")
	if databaseURL == "" {
		t.Skip("POSTGRES_V29_TEST_URL is required only by the release-profile rehearsal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	db := &DB{Pool: pool}
	if err := db.ensureMigrationLedger(ctx); err != nil {
		t.Fatal(err)
	}
	migrations, err := loadMigrations(migrationFS)
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:29] {
		if err := db.applyMigration(ctx, migration); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `
		INSERT INTO shops(id,name,slug) VALUES('20000000-0000-4000-8000-000000000001','Production v29 fixture','production-v29-fixture');
		INSERT INTO users(id,shop_id,email,password_hash,role) VALUES('20000000-0000-4000-8000-000000000002','20000000-0000-4000-8000-000000000001','owner@v29.example','$2a$10$fixture','owner');
		INSERT INTO customers(id,shop_id,name,email,phone) VALUES
		('20000000-0000-4000-8000-000000000011','20000000-0000-4000-8000-000000000001','Alpha & Sons',NULL,''),
		('20000000-0000-4000-8000-000000000012','20000000-0000-4000-8000-000000000001','Bravo <Home>','bravo@example.test',NULL),
		('20000000-0000-4000-8000-000000000013','20000000-0000-4000-8000-000000000001','Café 🚜','cafe@example.test',''),
		('20000000-0000-4000-8000-000000000014','20000000-0000-4000-8000-000000000001','Delta > Yard','delta@example.test','555-0100')`); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckSchemaThrough(ctx, 29); err != nil {
		t.Fatal(err)
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

func integrationDatabaseURL(t *testing.T) string {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL != "" {
		return databaseURL
	}
	if os.Getenv("CLUBDUE_REQUIRE_POSTGRES_INTEGRATION") == "1" {
		t.Fatal("DATABASE_URL is required when Postgres integration tests are required")
	}
	t.Skip("DATABASE_URL is required for Postgres integration tests")
	return ""
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

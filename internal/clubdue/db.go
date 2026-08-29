package clubdue

import (
	"context"
	"crypto/sha256"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type DB struct {
	Pool *pgxpool.Pool
}

func openDB(ctx context.Context, databaseURL string) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.MaxConns = 8
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	db := &DB{Pool: pool}
	if err := db.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() {
	if db != nil && db.Pool != nil {
		db.Pool.Close()
	}
}

func (db *DB) migrate(ctx context.Context) error {
	return db.migrateWithFS(ctx, migrationFS)
}

type migration struct {
	version  int
	body     []byte
	checksum string
}

func (db *DB) migrateWithFS(ctx context.Context, source fs.FS) error {
	if err := db.ensureMigrationLedger(ctx); err != nil {
		return err
	}

	migrations, err := loadMigrations(source)
	if err != nil {
		return err
	}

	for _, migration := range migrations {
		if err := db.applyMigration(ctx, migration); err != nil {
			return err
		}
	}
	return nil
}

func (db *DB) ensureMigrationLedger(ctx context.Context) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration ledger setup: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after a successful commit

	if _, err := tx.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			checksum TEXT
		)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}
	// Databases created before migration checksums used a two-column ledger.
	// The nullable column lets this release establish a trusted baseline without
	// rewriting migration history; see TestMigrationLedgerBaselinesLegacyRows.
	if _, err := tx.Exec(ctx, `ALTER TABLE schema_migrations ADD COLUMN IF NOT EXISTS checksum TEXT`); err != nil {
		return fmt.Errorf("add schema_migrations checksum: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration ledger setup: %w", err)
	}
	return nil
}

func loadMigrations(source fs.FS) ([]migration, error) {
	entries, err := fs.Glob(source, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(entries)

	migrations := make([]migration, 0, len(entries))
	versions := make(map[int]string, len(entries))
	for _, path := range entries {
		base := path[strings.LastIndex(path, "/")+1:]
		versionStr := strings.SplitN(base, "_", 2)[0]
		version, err := strconv.Atoi(versionStr)
		if err != nil {
			return nil, fmt.Errorf("migration filename %q: invalid version", base)
		}
		if previous, exists := versions[version]; exists {
			return nil, fmt.Errorf("migration filename %q: duplicate version with %q", base, previous)
		}
		versions[version] = base

		body, err := fs.ReadFile(source, path)
		if err != nil {
			return nil, fmt.Errorf("read migration %d: %w", version, err)
		}
		checksum := fmt.Sprintf("%x", sha256.Sum256(body))
		migrations = append(migrations, migration{version: version, body: body, checksum: checksum})
	}
	return migrations, nil
}

func (db *DB) applyMigration(ctx context.Context, migration migration) error {
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", migration.version, err)
	}
	defer tx.Rollback(ctx) // no-op after a successful commit

	var recordedChecksum *string
	err = tx.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version = $1 FOR UPDATE`, migration.version).Scan(&recordedChecksum)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, err := tx.Exec(ctx, string(migration.body)); err != nil {
			return fmt.Errorf("apply migration %d: %w", migration.version, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`, migration.version, migration.checksum,
		); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
	case err != nil:
		return fmt.Errorf("check migration %d: %w", migration.version, err)
	case recordedChecksum == nil:
		// Existing rows predate checksum enforcement. Recording the checksum in
		// this transaction protects future runs, but cannot prove whether the
		// historical file changed before this first checksum-aware startup.
		if _, err := tx.Exec(ctx,
			`UPDATE schema_migrations SET checksum = $2 WHERE version = $1 AND checksum IS NULL`, migration.version, migration.checksum,
		); err != nil {
			return fmt.Errorf("baseline migration %d checksum: %w", migration.version, err)
		}
	case *recordedChecksum != migration.checksum:
		return fmt.Errorf("migration %d checksum does not match embedded migration", migration.version)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %d: %w", migration.version, err)
	}
	return nil
}

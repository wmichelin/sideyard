package sqlserver

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

type migration struct {
	version        int
	body, checksum string
}

// Migrate applies trusted, native T-SQL files in one explicitly requested run.
// One reserved connection owns the session lock and every migration transaction;
// the runtime pool can therefore close all idle connections between requests.
func (p *Pool) Migrate(ctx context.Context, source fs.FS) error {
	migrations, err := loadMigrations(source)
	if err != nil {
		return err
	}
	conn, err := p.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire SQL Server migration connection: %w", err)
	}
	defer func() {
		// A zero-idle pool can hand a released connection directly to a queued
		// caller. Discard this session even if lock acquisition/unlock failed or
		// was cancelled, so a migration lock can never leak to that caller.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		_ = conn.Close()
	}()
	var status int
	err = conn.QueryRowContext(ctx, `DECLARE @status int;
		EXEC @status = sys.sp_getapplock @Resource = N'sideyard-schema-migrations',
		@LockMode = 'Exclusive', @LockOwner = 'Session', @LockTimeout = 60000;
		SELECT @status;`).Scan(&status)
	if err != nil {
		return fmt.Errorf("acquire SQL Server migration lock: %w", err)
	}
	if status < 0 {
		return fmt.Errorf("SQL Server migration lock rejected: status %d", status)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(cleanup, `EXEC sys.sp_releaseapplock @Resource=N'sideyard-schema-migrations', @LockOwner='Session'`)
		// The outer cleanup also discards the session if explicit unlock fails.
	}()
	if _, err := conn.ExecContext(ctx, `IF OBJECT_ID(N'dbo.schema_migrations', N'U') IS NULL
		CREATE TABLE dbo.schema_migrations (
			version int NOT NULL PRIMARY KEY,
			applied_at datetime2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
			checksum char(64) NOT NULL
		)`); err != nil {
		return fmt.Errorf("create SQL Server migration ledger: %w", err)
	}
	for _, m := range migrations {
		if err := applyMigration(ctx, conn, m); err != nil {
			return err
		}
	}
	return nil
}

func loadMigrations(source fs.FS) ([]migration, error) {
	paths, err := fs.Glob(source, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list SQL Server migrations: %w", err)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no SQL Server migrations")
	}
	seen := make(map[int]bool)
	var out []migration
	for _, path := range paths {
		file := strings.TrimPrefix(path, "migrations/")
		version, err := strconv.Atoi(strings.SplitN(file, "_", 2)[0])
		if err != nil || version < 1 || seen[version] {
			return nil, fmt.Errorf("invalid or duplicate SQL Server migration version: %s", file)
		}
		seen[version] = true
		body, err := fs.ReadFile(source, path)
		if err != nil {
			return nil, fmt.Errorf("read SQL Server migration %d: %w", version, err)
		}
		out = append(out, migration{version: version, body: string(body), checksum: fmt.Sprintf("%x", sha256.Sum256(body))})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func applyMigration(ctx context.Context, conn *sql.Conn, m migration) error {
	t, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQL Server migration %d: %w", m.version, err)
	}
	defer t.Rollback()
	var checksum string
	err = t.QueryRowContext(ctx, `SELECT checksum FROM dbo.schema_migrations WHERE version=@p1`, m.version).Scan(&checksum)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := t.ExecContext(ctx, m.body); err != nil {
			return fmt.Errorf("apply SQL Server migration %d: %w", m.version, err)
		}
		if _, err := t.ExecContext(ctx, `INSERT INTO dbo.schema_migrations(version,checksum) VALUES(@p1,@p2)`, m.version, m.checksum); err != nil {
			return fmt.Errorf("record SQL Server migration %d: %w", m.version, err)
		}
	case err != nil:
		return fmt.Errorf("read SQL Server migration %d: %w", m.version, err)
	case checksum != m.checksum:
		return fmt.Errorf("SQL Server migration %d checksum does not match embedded migration", m.version)
	}
	if err := t.Commit(); err != nil {
		return fmt.Errorf("commit SQL Server migration %d: %w", m.version, err)
	}
	return nil
}

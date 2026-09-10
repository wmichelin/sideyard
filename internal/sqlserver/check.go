package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
)

type contextRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// CheckRuntime verifies the effective application principal before checking
// schema compatibility. Readiness fails when a credential lacks application
// DML or has DDL/ledger-write authority, including an administrator credential.
func (p *Pool) CheckRuntime(ctx context.Context) error {
	if err := checkRuntimeIdentity(ctx, p.db, p.runtimeClientID); err != nil {
		return err
	}
	if err := checkRuntimePermissions(ctx, p.db); err != nil {
		return err
	}
	return p.CheckSchema(ctx)
}

func checkRuntimeIdentity(ctx context.Context, queryer contextRowQueryer, expectedClientID string) error {
	if !identifierPattern.MatchString(expectedClientID) {
		return fmt.Errorf("SQL Server runtime identity is not configured")
	}
	var name, principalType string
	var sidMatches int
	if err := queryer.QueryRowContext(ctx, `SELECT name,type,
		CASE WHEN sid=CONVERT(varbinary(16),CONVERT(uniqueidentifier,@p1)) THEN 1 ELSE 0 END
		FROM sys.database_principals WHERE name=USER_NAME()`, expectedClientID).Scan(&name, &principalType, &sidMatches); err != nil {
		return fmt.Errorf("check SQL Server runtime identity: %w", err)
	}
	if name != runtimePrincipalName || principalType != "E" || sidMatches != 1 {
		return fmt.Errorf("SQL Server runtime identity is incompatible")
	}
	return nil
}

func checkRuntimePermissions(ctx context.Context, queryer contextRowQueryer) error {
	var selectSchema, insertSchema, updateSchema, deleteSchema int
	var selectLedger, insertLedger, updateLedger, deleteLedger int
	var createTable, alterUsers, controlDatabase, alterSchema int
	err := queryer.QueryRowContext(ctx, `SELECT
		HAS_PERMS_BY_NAME(N'dbo',N'SCHEMA',N'SELECT'),
		HAS_PERMS_BY_NAME(N'dbo',N'SCHEMA',N'INSERT'),
		HAS_PERMS_BY_NAME(N'dbo',N'SCHEMA',N'UPDATE'),
		HAS_PERMS_BY_NAME(N'dbo',N'SCHEMA',N'DELETE'),
		HAS_PERMS_BY_NAME(N'dbo.schema_migrations',N'OBJECT',N'SELECT'),
		HAS_PERMS_BY_NAME(N'dbo.schema_migrations',N'OBJECT',N'INSERT'),
		HAS_PERMS_BY_NAME(N'dbo.schema_migrations',N'OBJECT',N'UPDATE'),
		HAS_PERMS_BY_NAME(N'dbo.schema_migrations',N'OBJECT',N'DELETE'),
		HAS_PERMS_BY_NAME(DB_NAME(),N'DATABASE',N'CREATE TABLE'),
		HAS_PERMS_BY_NAME(DB_NAME(),N'DATABASE',N'ALTER ANY USER'),
		HAS_PERMS_BY_NAME(DB_NAME(),N'DATABASE',N'CONTROL'),
		HAS_PERMS_BY_NAME(N'dbo',N'SCHEMA',N'ALTER')`).Scan(
		&selectSchema, &insertSchema, &updateSchema, &deleteSchema,
		&selectLedger, &insertLedger, &updateLedger, &deleteLedger,
		&createTable, &alterUsers, &controlDatabase, &alterSchema,
	)
	if err != nil {
		return fmt.Errorf("check SQL Server runtime permissions: %w", err)
	}
	if selectSchema != 1 || insertSchema != 1 || updateSchema != 1 || deleteSchema != 1 || selectLedger != 1 ||
		insertLedger != 0 || updateLedger != 0 || deleteLedger != 0 || createTable != 0 || alterUsers != 0 || controlDatabase != 0 || alterSchema != 0 {
		return fmt.Errorf("SQL Server runtime permissions are incompatible")
	}
	return nil
}

// CheckSchema is read-only: exact migration coverage/checksums plus the isolation
// settings required by native calendar snapshots and parent locking semantics.
func (p *Pool) CheckSchema(ctx context.Context) error {
	expected, err := loadMigrations(migrationFS)
	if err != nil {
		return err
	}
	rows, err := p.db.QueryContext(ctx, `SELECT version,checksum FROM dbo.schema_migrations ORDER BY version`)
	if err != nil {
		return fmt.Errorf("read SQL Server schema ledger: %w", err)
	}
	matched := 0
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return fmt.Errorf("read SQL Server schema entry: %w", err)
		}
		if matched >= len(expected) || version != expected[matched].version || checksum != expected[matched].checksum {
			rows.Close()
			return fmt.Errorf("SQL Server schema version %d is incompatible with this binary", version)
		}
		matched++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return fmt.Errorf("read SQL Server schema ledger: %w", err)
	}
	if matched != len(expected) {
		return fmt.Errorf("SQL Server schema is incomplete: %d of %d migrations", matched, len(expected))
	}
	rcsi, snapshot, err := p.isolationState(ctx)
	if err != nil {
		return err
	}
	if !rcsi || snapshot != 1 {
		return fmt.Errorf("SQL Server requires READ_COMMITTED_SNAPSHOT and ALLOW_SNAPSHOT_ISOLATION ON")
	}
	return nil
}
func (p *Pool) isolationState(ctx context.Context) (bool, int, error) {
	var rcsi bool
	var snapshot int
	err := p.db.QueryRowContext(ctx, `SELECT is_read_committed_snapshot_on,snapshot_isolation_state FROM sys.databases WHERE database_id=DB_ID()`).Scan(&rcsi, &snapshot)
	if err != nil {
		return false, 0, fmt.Errorf("read SQL Server isolation settings: %w", err)
	}
	return rcsi, snapshot, nil
}

// PrepareSchema is an explicit operator action. It does not terminate other
// sessions to enable isolation; an occupied database can wait until cancellation.
// Migrations retain their own checked session lock and per-file transactions.
func (p *Pool) PrepareSchema(ctx context.Context) error {
	rcsi, snapshot, err := p.isolationState(ctx)
	if err != nil {
		return err
	}
	if snapshot != 1 {
		if snapshot != 0 {
			return fmt.Errorf("SQL Server snapshot isolation is transitioning; retry after it settles")
		}
		if _, err := p.db.ExecContext(ctx, `ALTER DATABASE CURRENT SET ALLOW_SNAPSHOT_ISOLATION ON`); err != nil {
			return fmt.Errorf("enable SQL Server snapshot isolation: %w", err)
		}
	}
	if !rcsi {
		if _, err := p.db.ExecContext(ctx, `ALTER DATABASE CURRENT SET READ_COMMITTED_SNAPSHOT ON`); err != nil {
			return fmt.Errorf("enable SQL Server read committed snapshots: %w", err)
		}
	}
	if err := p.MigrateSchema(ctx); err != nil {
		return err
	}
	return p.CheckSchema(ctx)
}

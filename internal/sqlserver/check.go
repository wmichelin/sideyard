package sqlserver

import (
	"context"
	"fmt"
)

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

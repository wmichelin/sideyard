package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
)

type contextRowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (p *Pool) ExpectedMigrationChecksums(migrationCount int) ([]string, error) {
	migrations, err := loadMigrations(migrationFS)
	if err != nil {
		return nil, err
	}
	if migrationCount != len(migrations) {
		return nil, fmt.Errorf("SQL Server release migration count is invalid")
	}
	checksums := make([]string, migrationCount)
	for index := range checksums {
		checksums[index] = migrations[index].checksum
	}
	return checksums, nil
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

// CheckDataIntegrity verifies that application guards are present, enabled and
// trusted, then audits the relationships maintained by triggers. Restore runs
// this both before importing and before accepting reconciliation.
func (p *Pool) CheckDataIntegrity(ctx context.Context) error {
	expectedTriggers := []string{
		"agreements_validate_references", "asset_notes_validate_owner",
		"business_settings_validate_trades", "equipment_note_integrity",
		"plan_templates_integrity", "properties_address_revision",
		"properties_agreement_integrity", "property_photos_retire_object",
		"recurring_note_integrity", "renewal_digest_membership",
		"seasonal_note_integrity", "vehicle_note_integrity", "visit_notes_validate_owner",
	}
	rows, err := p.db.QueryContext(ctx, `SELECT tr.name,tr.is_disabled
		FROM sys.triggers tr
		JOIN sys.tables t ON t.object_id=tr.parent_id
		JOIN sys.schemas s ON s.schema_id=t.schema_id
		WHERE tr.parent_class=1 AND s.name=N'dbo' ORDER BY tr.name`)
	if err != nil {
		return fmt.Errorf("inspect SQL Server validation triggers: %w", err)
	}
	var actualTriggers []string
	for rows.Next() {
		var name string
		var disabled bool
		if err := rows.Scan(&name, &disabled); err != nil {
			rows.Close()
			return fmt.Errorf("inspect SQL Server validation trigger: %w", err)
		}
		if disabled {
			rows.Close()
			return fmt.Errorf("SQL Server validation trigger %s is disabled", name)
		}
		actualTriggers = append(actualTriggers, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("inspect SQL Server validation triggers: %w", err)
	}
	rows.Close()
	sort.Strings(expectedTriggers)
	if fmt.Sprint(actualTriggers) != fmt.Sprint(expectedTriggers) {
		return fmt.Errorf("SQL Server validation trigger inventory is incompatible")
	}
	var unsafeConstraints, disabledIndexes int
	if err := p.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM sys.check_constraints c JOIN sys.tables t ON t.object_id=c.parent_object_id JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name=N'dbo' AND (c.is_disabled=1 OR c.is_not_trusted=1))
		+(SELECT COUNT(*) FROM sys.foreign_keys f JOIN sys.tables t ON t.object_id=f.parent_object_id JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name=N'dbo' AND (f.is_disabled=1 OR f.is_not_trusted=1)),
		(SELECT COUNT(*) FROM sys.indexes i JOIN sys.tables t ON t.object_id=i.object_id JOIN sys.schemas s ON s.schema_id=t.schema_id WHERE s.name=N'dbo' AND i.is_disabled=1)`).Scan(&unsafeConstraints, &disabledIndexes); err != nil {
		return fmt.Errorf("inspect SQL Server constraint trust: %w", err)
	}
	if unsafeConstraints != 0 || disabledIndexes != 0 {
		return fmt.Errorf("SQL Server has disabled or untrusted data guards")
	}
	var triggerViolations int
	if err := p.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM dbo.asset_notes n WHERE
			(n.vehicle_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dbo.customer_vehicles v WHERE v.id=n.vehicle_id AND v.shop_id=n.shop_id)) OR
			(n.equipment_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dbo.equipment e WHERE e.id=n.equipment_id AND e.shop_id=n.shop_id)))
		+(SELECT COUNT(*) FROM dbo.visit_notes n WHERE
			(n.occurrence_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dbo.service_occurrences o WHERE o.id=n.occurrence_id AND o.shop_id=n.shop_id)) OR
			(n.seasonal_visit_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dbo.visit_dues v WHERE v.id=n.seasonal_visit_id AND v.shop_id=n.shop_id)))
		+(SELECT COUNT(*) FROM dbo.agreements a WHERE
			(a.property_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dbo.properties p WHERE p.id=a.property_id AND p.shop_id=a.shop_id AND p.customer_id=a.customer_id)) OR
			(a.plan_template_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dbo.plan_templates p WHERE p.id=a.plan_template_id AND p.shop_id=a.shop_id)))
		+(SELECT COUNT(*) FROM dbo.business_settings b WHERE
			EXISTS(SELECT 1 FROM OPENJSON(b.trades) j WHERE j.type<>1) OR NOT EXISTS(SELECT 1 FROM OPENJSON(b.trades) j WHERE j.value COLLATE Latin1_General_100_BIN2=b.primary_trade AND DATALENGTH(j.value)=DATALENGTH(b.primary_trade)))
		+(SELECT COUNT(*) FROM dbo.plan_templates p WHERE EXISTS(SELECT 1 FROM OPENJSON(p.visit_windows) j WHERE j.type<>1))
		+(SELECT COUNT(*) FROM dbo.agreements a WHERE EXISTS(SELECT 1 FROM OPENJSON(a.plan_snapshot_visit_windows) j WHERE j.type<>1))
		+(SELECT COUNT(*) FROM dbo.renewal_digest_outbox d WHERE NOT EXISTS(SELECT 1 FROM OPENJSON(d.task_ids)) OR EXISTS(
			SELECT 1 FROM OPENJSON(d.task_ids) j WHERE j.type<>0 AND (j.type<>1 OR DATALENGTH(j.value)<>72 OR TRY_CONVERT(uniqueidentifier,j.value) IS NULL OR LOWER(j.value) COLLATE Latin1_General_100_BIN2<>LOWER(CONVERT(char(36),TRY_CONVERT(uniqueidentifier,j.value))) COLLATE Latin1_General_100_BIN2)))`).Scan(&triggerViolations); err != nil {
		return fmt.Errorf("audit SQL Server trigger-maintained integrity: %w", err)
	}
	if triggerViolations != 0 {
		return fmt.Errorf("SQL Server data violates trigger-maintained integrity")
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

package sqlserver

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSQLServerPrepareAndReadOnlyCheckRejectSchemaDrift(t *testing.T) {
	p := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := p.CheckSchema(ctx); err == nil {
		t.Fatal("missing schema accepted")
	}
	var tables int
	if err := p.QueryRow(ctx, `SELECT COUNT(*) FROM sys.tables WHERE schema_id=SCHEMA_ID(N'dbo')`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("read-only check created schema", tables, err)
	}
	if err := p.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.PrepareSchema(ctx); err != nil {
		t.Fatal("repeated preparation", err)
	}
	if err := p.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var original string
	if err := p.QueryRow(ctx, `SELECT checksum FROM dbo.schema_migrations WHERE version=1`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `UPDATE dbo.schema_migrations SET checksum=@p1 WHERE version=1`, strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckSchema(ctx); err == nil {
		t.Fatal("changed checksum accepted")
	}
	var after string
	if err := p.QueryRow(ctx, `SELECT checksum FROM dbo.schema_migrations WHERE version=1`).Scan(&after); err != nil || after != strings.Repeat("f", 64) {
		t.Fatal("check rewrote drift", err)
	}
	if _, err := p.Exec(ctx, `UPDATE dbo.schema_migrations SET checksum=@p1 WHERE version=1; INSERT INTO dbo.schema_migrations(version,checksum) VALUES(999999,@p1)`, original); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckSchema(ctx); err == nil {
		t.Fatal("newer binary schema accepted")
	}
	if _, err := p.Exec(ctx, `DELETE FROM dbo.schema_migrations WHERE version=999999; DELETE FROM dbo.schema_migrations WHERE version=29`); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckSchema(ctx); err == nil {
		t.Fatal("missing migration accepted")
	}
}
func TestSQLServerCheckRequiresBothIsolationModes(t *testing.T) {
	p := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := p.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{"ALLOW_SNAPSHOT_ISOLATION", "READ_COMMITTED_SNAPSHOT"} {
		if _, err := p.Exec(ctx, `ALTER DATABASE CURRENT SET `+setting+` OFF`); err != nil {
			t.Fatal(err)
		}
		if err := p.CheckSchema(ctx); err == nil {
			t.Fatal("isolation drift accepted", setting)
		}
		if err := p.PrepareSchema(ctx); err != nil {
			t.Fatal("explicit repair failed", setting, err)
		}
	}
}

func TestSQLServerDataIntegrityRejectsDisabledGuards(t *testing.T) {
	p := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckDataIntegrity(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `DISABLE TRIGGER dbo.visit_notes_validate_owner ON dbo.visit_notes`); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckDataIntegrity(ctx); err == nil {
		t.Fatal("disabled validation trigger accepted")
	}
	if _, err := p.Exec(ctx, `ENABLE TRIGGER dbo.visit_notes_validate_owner ON dbo.visit_notes`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `
		INSERT INTO dbo.shops(id,name,slug) VALUES
		(CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000001'),N'First',N'integrity-first'),
		(CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000002'),N'Second',N'integrity-second');
		INSERT INTO dbo.customers(id,shop_id,name) VALUES
		(CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000003'),CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000001'),N'Owner');
		INSERT INTO dbo.customer_vehicles(id,shop_id,customer_id,name) VALUES
		(CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000004'),CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000001'),CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000003'),N'Truck');
		DISABLE TRIGGER dbo.asset_notes_validate_owner ON dbo.asset_notes;
		INSERT INTO dbo.asset_notes(shop_id,vehicle_id,submission_key,body,occurred_on,event_timezone,recorded_by)
		VALUES(CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000002'),CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000004'),N'12345678901234567890123456789012',N'Invalid owner','2035-09-02',N'America/New_York',N'Test');
		ENABLE TRIGGER dbo.asset_notes_validate_owner ON dbo.asset_notes;`); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckDataIntegrity(ctx); err == nil {
		t.Fatal("historical trigger-maintained violation accepted")
	}
	if _, err := p.Exec(ctx, `DELETE FROM dbo.asset_notes WHERE submission_key=N'12345678901234567890123456789012'; DELETE FROM dbo.shops WHERE id IN(CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000001'),CONVERT(uniqueidentifier,'30000000-0000-4000-8000-000000000002'))`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `ALTER TABLE dbo.visit_notes NOCHECK CONSTRAINT ALL`); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckDataIntegrity(ctx); err == nil {
		t.Fatal("disabled or untrusted constraints accepted")
	}
	if _, err := p.Exec(ctx, `ALTER TABLE dbo.visit_notes WITH CHECK CHECK CONSTRAINT ALL`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `ALTER INDEX shops_slug_key ON dbo.shops DISABLE`); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckDataIntegrity(ctx); err == nil {
		t.Fatal("disabled unique index accepted")
	}
	if _, err := p.Exec(ctx, `ALTER INDEX shops_slug_key ON dbo.shops REBUILD`); err != nil {
		t.Fatal(err)
	}
	if err := p.CheckDataIntegrity(ctx); err != nil {
		t.Fatal("re-enabled validation guards rejected", err)
	}
}

func TestSQLServerRuntimeRoleAllowsDomainWorkAndRejectsControl(t *testing.T) {
	p := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := p.PrepareSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.db.ExecContext(ctx, `
		DROP USER IF EXISTS sideyard_runtime_test;
		DROP ROLE IF EXISTS sideyard_runtime_test_role;
		CREATE ROLE sideyard_runtime_test_role;
		CREATE USER sideyard_runtime_test WITHOUT LOGIN;
		ALTER ROLE sideyard_runtime_test_role ADD MEMBER sideyard_runtime_test;
		GRANT SELECT,INSERT,UPDATE,DELETE ON SCHEMA::dbo TO sideyard_runtime_test_role;
		DENY INSERT,UPDATE,DELETE ON OBJECT::dbo.schema_migrations TO sideyard_runtime_test_role;`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = p.db.ExecContext(context.Background(), `DROP USER IF EXISTS sideyard_runtime_test; DROP ROLE IF EXISTS sideyard_runtime_test_role;`)
	})
	conn, err := p.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `EXECUTE AS USER=N'sideyard_runtime_test'`); err != nil {
		t.Fatal(err)
	}
	if err := checkRuntimePermissions(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if err := checkRuntimeIdentity(ctx, conn, "11111111-2222-3333-4444-555555555555"); err == nil {
		t.Fatal("same-permission non-Entra principal passed runtime identity check")
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO dbo.shops(name,slug) VALUES(N'Runtime permission test',N'runtime-permission-test'); UPDATE dbo.shops SET name=N'Runtime permission changed' WHERE slug=N'runtime-permission-test'; DELETE FROM dbo.shops WHERE slug=N'runtime-permission-test'`); err != nil {
		t.Fatal("runtime DML rejected", err)
	}
	if _, err := conn.ExecContext(ctx, `UPDATE dbo.schema_migrations SET checksum=checksum WHERE version=1`); err == nil {
		t.Fatal("runtime principal could update migration ledger")
	}
	if _, err := conn.ExecContext(ctx, `CREATE TABLE dbo.runtime_permission_escape(id int)`); err == nil {
		t.Fatal("runtime principal could create schema objects")
	}
	var lockStatus int
	if err := conn.QueryRowContext(ctx, `DECLARE @status int; EXEC @status=sys.sp_getapplock @Resource=N'sideyard-runtime-permission-test',@LockMode=N'Exclusive',@LockOwner=N'Session',@LockTimeout=1000; SELECT @status`).Scan(&lockStatus); err != nil || lockStatus < 0 {
		t.Fatal("runtime application lock rejected", lockStatus, err)
	}
	if _, err := conn.ExecContext(ctx, `EXEC sys.sp_releaseapplock @Resource=N'sideyard-runtime-permission-test',@LockOwner=N'Session'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `REVERT; SELECT 1;`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `GRANT CREATE TABLE TO sideyard_runtime_test; EXECUTE AS USER=N'sideyard_runtime_test'`); err != nil {
		t.Fatal(err)
	}
	if err := checkRuntimePermissions(ctx, conn); err == nil {
		t.Fatal("overprivileged runtime principal passed readiness")
	}
	if _, err := conn.ExecContext(ctx, `REVERT; SELECT 1;`); err != nil {
		t.Fatal(err)
	}
}

func TestSQLServerRuntimePrincipalBootstrapRejectsInvalidClientIdentity(t *testing.T) {
	p := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := p.PrepareRuntimePrincipal(ctx, "not-an-id"); err == nil {
		t.Fatal("invalid runtime client ID accepted")
	}
}

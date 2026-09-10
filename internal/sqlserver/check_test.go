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

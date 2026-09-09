// Package testdb owns disposable real-engine fixtures; it is imported only by tests.
package testdb

import (
	"context"
	"database/sql"
	"fmt"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
	"os"
	"regexp"
	"testing"
	"time"
)

// The required flag prevents a CI job from going green by skipping the engine.
func SQLServer(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SQLSERVER_TEST_DSN")
	if dsn == "" {
		if os.Getenv("CLUBDUE_REQUIRE_SQLSERVER_INTEGRATION") == "1" {
			t.Fatal("SQLSERVER_TEST_DSN is required")
		}
		t.Skip("set SQLSERVER_TEST_DSN for a disposable SQL Server instance")
	}
	cfg, err := msdsn.Parse(dsn)
	if err != nil {
		t.Fatal("invalid test SQL configuration")
	}
	// Only a deliberately configured disposable database is eligible for these
	// destructive local fixture tests. Never point them at an Azure endpoint.
	localHost := regexp.MustCompile(`^(localhost|127\.0\.0\.1|sideyard-sqlserver-test|sideyard-sqlserver-rehearsal-[0-9]+)$`)
	if !localHost.MatchString(cfg.Host) || cfg.Database != "sideyard_test" {
		t.Fatal("test DSN must name local sideyard_test")
	}
	admin, err := sql.Open("sqlserver", dsn)
	if err != nil {
		t.Fatal("initialize SQL test connection")
	}
	admin.SetMaxIdleConns(0)
	name := fmt.Sprintf("sideyard_test_%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE ["+name+"]"); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	if _, err := admin.ExecContext(ctx, "ALTER DATABASE ["+name+"] SET ALLOW_SNAPSHOT_ISOLATION ON; ALTER DATABASE ["+name+"] SET READ_COMMITTED_SNAPSHOT ON"); err != nil {
		t.Fatal(err)
	}
	cfg.Database = name
	// Build a native connector from the parsed test config; no query rewriting.
	p := sql.OpenDB(mssql.NewConnectorConfig(cfg))
	p.SetMaxIdleConns(0)
	t.Cleanup(func() {
		p.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(cleanup, "ALTER DATABASE ["+name+"] SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE ["+name+"]"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	return p
}

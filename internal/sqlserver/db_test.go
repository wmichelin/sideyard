package sqlserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/microsoft/go-mssqldb/msdsn"
	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/testdb"
)

type countingCredential struct{ calls int }

func (c *countingCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.calls++
	return azcore.AccessToken{}, errors.New("synthetic token failure")
}

func TestAzureConnectionConfigurationAndLazyConstruction(t *testing.T) {
	cred := &countingCredential{}
	p, err := NewAzure("sideyard-test.database.windows.net", "sideyard", cred)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if cred.calls != 0 || p.db.Stats().OpenConnections != 0 {
		t.Fatal("construction contacted Azure")
	}
	cfg, err := azureConfig("sideyard-test.database.windows.net", "sideyard")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Encryption != msdsn.EncryptionRequired || cfg.TLSConfig == nil || cfg.TLSConfig.InsecureSkipVerify || cfg.TLSConfig.MinVersion < tls.VersionTLS12 || cfg.User != "" || cfg.Password != "" || !cfg.DisableRetry {
		t.Fatal("Azure SQL must enforce TLS, Entra authentication and explicit retry ownership")
	}
	for _, tc := range []struct{ server, name string }{
		{"localhost", "sideyard"}, {"sideyard.database.windows.net.evil.test", "sideyard"},
		{"user:secret@sideyard.database.windows.net", "sideyard"},
		{"sideyard.database.windows.net;encrypt=false", "sideyard"},
		{"sideyard.database.windows.net", "sideyard;password=secret"},
		{"sideyard.database.windows.net", "master"},
		{"sideyard.database.windows.net", "MASTER"},
		{"sideyard.database.windows.net", "Master"},
	} {
		_, err := NewAzure(tc.server, tc.name, cred)
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe configuration accepted or echoed")
		}
	}
	if _, err := NewAzure("sideyard.database.windows.net", "sideyard", nil); err == nil {
		t.Fatal("missing credential accepted")
	}
}

func testPool(t *testing.T) *Pool {
	p := OpenConnector(testdb.SQLServer(t))
	t.Cleanup(p.Close)
	return p
}

func TestSQLServerComputedIdentityOptionsOnEveryConnection(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()
	if err := p.MigrateSchema(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		var options int
		if err := p.QueryRow(ctx, `SELECT @@OPTIONS`).Scan(&options); err != nil {
			t.Fatal(err)
		}
		// Required ANSI options, ARITHABORT and QUOTED_IDENTIFIER, with
		// NUMERIC_ROUNDABORT off. Each query uses a fresh zero-idle session.
		const on = 8 | 16 | 32 | 64 | 256 | 4096
		if options&on != on || options&8192 != 0 {
			t.Fatalf("computed-index session options = %d", options)
		}
		if _, err := p.Exec(ctx, `INSERT INTO dbo.shops(name,slug) VALUES (@p1,@p1)`, fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
		assertNoConnections(t, p)
	}
	// A reused driver session must reset changed options too. This test-only
	// override does not change the application's zero-idle connection policy.
	p.db.SetMaxIdleConns(1)
	if _, err := p.Exec(ctx, `SET NUMERIC_ROUNDABORT ON; SET ARITHABORT OFF;`); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO dbo.shops(name,slug) VALUES ('reused','reused')`); err != nil {
		t.Fatalf("session options not reset on reuse: %v", err)
	}
	p.db.SetMaxIdleConns(0)
	assertNoConnections(t, p)
}

type failedInitConnection struct {
	driver.Conn
	closed bool
}

func (c *failedInitConnection) Close() error { c.closed = true; return nil }

type failedInitConnector struct {
	driver.Connector
	conn driver.Conn
	err  error
}

func (c failedInitConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, c.err }

func TestSQLServerFailedSessionInitializationClosesUnownedConnection(t *testing.T) {
	c := &failedInitConnection{}
	want := errors.New("synthetic initialization failure")
	got, err := (cleanConnector{failedInitConnector{conn: c, err: want}}).Connect(context.Background())
	if got != nil || !errors.Is(err, want) || !c.closed {
		t.Fatal("failed initialization leaked a driver connection or its error")
	}
}

func assertNoConnections(t *testing.T, p *Pool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s := p.db.Stats()
		if s.OpenConnections == 0 && s.Idle == 0 && s.InUse == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("connections retained: %+v", p.db.Stats())
}

func TestSQLServerRowsValuesAndConnectionRelease(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()
	if err := p.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoConnections(t, p)
	_, err := p.Exec(ctx, `CREATE TABLE dbo.values_test (
		id uniqueidentifier PRIMARY KEY, image varbinary(max), measured decimal(12,2),
		day date, instant datetime2(6), label nvarchar(100), optional_day date, enabled bit)`)
	if err != nil {
		t.Fatal(err)
	}
	id := "01112233-4455-6677-8899-aabbccddeeff"
	image := []byte("1234567890123456") // 16-byte binary must not be mistaken for a UUID.
	instant := time.Date(2026, 9, 9, 12, 34, 56, 123456000, time.UTC)
	r, err := p.Exec(ctx, `INSERT INTO dbo.values_test VALUES (CONVERT(uniqueidentifier,@p1),@p2,CONVERT(decimal(12,2),@p3),CONVERT(date,@p4),@p5,@p6,NULL,@p7)`, id, image, "1234567890.12", "2026-09-09", instant, "Gate 🔑 — back yard", true)
	if err != nil || r.RowsAffected() != 1 {
		t.Fatalf("insert: %v %v", r, err)
	}
	var gotID, measured, label string
	var gotImage []byte
	var day, gotInstant time.Time
	var optional *time.Time
	var enabled bool
	err = p.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),id)),image,measured,day,instant,label,optional_day,enabled FROM dbo.values_test`).Scan(&gotID, &gotImage, &measured, &day, &gotInstant, &label, &optional, &enabled)
	if err != nil {
		t.Fatal(err)
	}
	if gotID != id || !bytes.Equal(image, gotImage) || measured != "1234567890.12" || day.Format("2006-01-02") != "2026-09-09" || !gotInstant.Equal(instant) || label != "Gate 🔑 — back yard" || optional != nil || !enabled {
		t.Fatalf("value round trip changed: %s %x %s %v %v %q %v %v", gotID, gotImage, measured, day, gotInstant, label, optional, enabled)
	}
	assertNoConnections(t, p)
	var n int
	if err := p.QueryRow(ctx, `SELECT 1 WHERE 1=0`).Scan(&n); err != database.ErrNoRows {
		t.Fatalf("missing row: %v", err)
	}
	if err := p.QueryRow(ctx, `SELECT missing_column FROM dbo.values_test`).Scan(&n); err == nil || errors.Is(err, database.ErrNoRows) {
		t.Fatalf("query error lost: %v", err)
	}
	r, err = p.Exec(ctx, `UPDATE dbo.values_test SET enabled=0 WHERE 1=0`)
	if err != nil || r.RowsAffected() != 0 {
		t.Fatalf("zero affected rows: %v %v", r, err)
	}
	rows, err := p.Query(ctx, `SELECT TOP (1000) object_id FROM sys.all_objects`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("missing fixture rows")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoConnections(t, p)
}

func TestSQLServerTransactionCancellationAndSnapshot(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()
	if _, err := p.Exec(ctx, `CREATE TABLE dbo.tx_test (id int PRIMARY KEY); INSERT INTO dbo.tx_test VALUES(1)`); err != nil {
		t.Fatal(err)
	}
	tx, err := p.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSnapshot})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var n int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.tx_test`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("initial snapshot %d %v", n, err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO dbo.tx_test VALUES(2)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.tx_test`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("snapshot changed %d %v", n, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoConnections(t, p)
	if tx, err := p.BeginTx(ctx, &sql.TxOptions{ReadOnly: true}); err == nil {
		tx.Rollback(ctx)
		t.Fatal("unsupported read-only transaction silently accepted")
	}
	for i := 0; i < 6; i++ {
		cancelled, cancel := context.WithCancel(ctx)
		tx, err := p.Begin(cancelled)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(cancelled, `INSERT INTO dbo.tx_test VALUES(@p1)`, 10+i); err != nil {
			t.Fatal(err)
		}
		cancel()
		if err := tx.Commit(cancelled); err == nil {
			t.Fatal("cancelled transaction committed")
		}
		if err := tx.Rollback(cancelled); err != nil {
			t.Fatal(err)
		}
		assertNoConnections(t, p)
	}
	if err := p.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.tx_test`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("cancelled writes persisted: %d %v", n, err)
	}
	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if _, err := p.Exec(short, `WAITFOR DELAY '00:00:05'`); err == nil {
		t.Fatal("query ignored cancellation")
	}
	assertNoConnections(t, p)
	if err := p.Ping(ctx); err != nil {
		t.Fatalf("did not recover after cancellation: %v", err)
	}
}

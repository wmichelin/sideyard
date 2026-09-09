// Package sqlserver owns SQL Server connections and explicit T-SQL migrations.
// The application does not select this backend until the domain port is ready.
package sqlserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/microsoft/go-mssqldb/msdsn"
	"github.com/wmichelin/clubdue/internal/database"
)

var azureHost = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.database\.windows\.net$`)
var databaseName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]{0,127}$`)

type Pool struct{ db *sql.DB }

// NewAzure constructs a lazy, Entra-only connection pool. It neither connects
// nor migrates. The caller owns a long-lived credential so reconnects reuse its
// token cache. SQL passwords and TLS bypass settings are deliberately absent.
func NewAzure(server, name string, credential azcore.TokenCredential) (*Pool, error) {
	if credential == nil {
		return nil, fmt.Errorf("Azure SQL requires an Entra credential")
	}
	cfg, err := azureConfig(server, name)
	if err != nil {
		return nil, err
	}
	connector, err := mssql.NewSecurityTokenConnector(cfg, func(ctx context.Context) (string, error) {
		token, err := credential.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"https://database.windows.net/.default"}})
		if err != nil {
			return "", fmt.Errorf("obtain Azure SQL token: %w", err)
		}
		return token.Token, nil
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Azure SQL connector")
	}
	return Wrap(sql.OpenDB(connector)), nil
}

func azureConfig(server, name string) (msdsn.Config, error) {
	if !azureHost.MatchString(server) || !databaseName.MatchString(name) || strings.EqualFold(name, "master") {
		// Do not include the supplied value; an invalid value may contain a secret.
		return msdsn.Config{}, fmt.Errorf("invalid Azure SQL server or database name")
	}
	return msdsn.Parse("server=" + server + ";database=" + name + ";encrypt=true;TrustServerCertificate=false;tlsmin=1.2;connection timeout=60;app name=Sideyard;disableretry=true")
}

// Wrap takes ownership of an injected native SQL Server database connection pool.
// It applies the same connection limits used by NewAzure.
func Wrap(db *sql.DB) *Pool {
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(0)
	db.SetConnMaxLifetime(30 * time.Minute)
	return &Pool{db: db}
}

func (*Pool) Dialect() database.Dialect        { return database.SQLServer }
func (p *Pool) Close()                         { _ = p.db.Close() }
func (p *Pool) Ping(ctx context.Context) error { return p.db.PingContext(ctx) }
func (p *Pool) Exec(ctx context.Context, query string, args ...any) (database.Result, error) {
	result, err := p.db.ExecContext(ctx, query, args...)
	return affected(result, err)
}
func (p *Pool) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	return p.db.QueryContext(ctx, query, args...)
}
func (p *Pool) QueryRow(ctx context.Context, query string, args ...any) database.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}
func (p *Pool) Begin(ctx context.Context) (database.Tx, error) { return p.BeginTx(ctx, nil) }
func (p *Pool) BeginTx(ctx context.Context, opts *sql.TxOptions) (database.Tx, error) {
	t, err := p.db.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return tx{t}, nil
}

type affectedRows int64

func (r affectedRows) RowsAffected() int64 { return int64(r) }
func affected(result sql.Result, err error) (database.Result, error) {
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("read affected row count: %w", err)
	}
	return affectedRows(n), nil
}

type tx struct{ tx *sql.Tx }

func (tx) Dialect() database.Dialect { return database.SQLServer }
func (t tx) Exec(ctx context.Context, query string, args ...any) (database.Result, error) {
	result, err := t.tx.ExecContext(ctx, query, args...)
	return affected(result, err)
}
func (t tx) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	return t.tx.QueryContext(ctx, query, args...)
}
func (t tx) QueryRow(ctx context.Context, query string, args ...any) database.Row {
	return t.tx.QueryRowContext(ctx, query, args...)
}
func (t tx) Commit(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		_ = t.tx.Rollback()
		return err
	}
	return t.tx.Commit()
}
func (t tx) Rollback(context.Context) error {
	// database/sql uses the begin context internally; cleanup must still happen
	// when the request's context is already cancelled.
	err := t.tx.Rollback()
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

var _ database.Pool = (*Pool)(nil)

package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wmichelin/clubdue/internal/database"
)

// Wrap exposes only the operations consumed by repositories. The native pool
// remains owned by DB for the existing PostgreSQL migration implementation.
func Wrap(pool *pgxpool.Pool) database.Pool { return &adapter{pool: pool} }

type adapter struct{ pool *pgxpool.Pool }

func (*adapter) Dialect() database.Dialect        { return database.Postgres }
func (p *adapter) Close()                         { p.pool.Close() }
func (p *adapter) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p *adapter) Exec(ctx context.Context, query string, args ...any) (database.Result, error) {
	return p.pool.Exec(ctx, query, args...)
}
func (p *adapter) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgRows{rows}, nil
}
func (p *adapter) QueryRow(ctx context.Context, query string, args ...any) database.Row {
	return pgRow{p.pool.QueryRow(ctx, query, args...)}
}
func (p *adapter) Begin(ctx context.Context) (database.Tx, error) { return p.BeginTx(ctx, nil) }
func (p *adapter) BeginTx(ctx context.Context, opts *sql.TxOptions) (database.Tx, error) {
	var native pgx.TxOptions
	if opts != nil {
		switch opts.Isolation {
		case sql.LevelDefault:
		case sql.LevelReadUncommitted:
			native.IsoLevel = pgx.ReadUncommitted
		case sql.LevelReadCommitted:
			native.IsoLevel = pgx.ReadCommitted
		case sql.LevelRepeatableRead:
			native.IsoLevel = pgx.RepeatableRead
		case sql.LevelSerializable:
			native.IsoLevel = pgx.Serializable
		default:
			return nil, fmt.Errorf("unsupported PostgreSQL transaction isolation: %s", opts.Isolation)
		}
		if opts.ReadOnly {
			native.AccessMode = pgx.ReadOnly
		}
	}
	tx, err := p.pool.BeginTx(ctx, native)
	if err != nil {
		return nil, err
	}
	return pgTx{tx}, nil
}

type pgRow struct{ pgx.Row }

func (r pgRow) Scan(dest ...any) error {
	err := r.Row.Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return database.ErrNoRows
	}
	return err
}

type pgRows struct{ pgx.Rows }

func (r pgRows) Close() error { r.Rows.Close(); return r.Rows.Err() }

type pgTx struct{ pgx.Tx }

func (pgTx) Dialect() database.Dialect { return database.Postgres }
func (t pgTx) Exec(ctx context.Context, query string, args ...any) (database.Result, error) {
	return t.Tx.Exec(ctx, query, args...)
}
func (t pgTx) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	rows, err := t.Tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgRows{rows}, nil
}
func (t pgTx) QueryRow(ctx context.Context, query string, args ...any) database.Row {
	return pgRow{t.Tx.QueryRow(ctx, query, args...)}
}
func (t pgTx) Rollback(ctx context.Context) error {
	err := t.Tx.Rollback(ctx)
	if errors.Is(err, pgx.ErrTxClosed) {
		return nil
	}
	return err
}

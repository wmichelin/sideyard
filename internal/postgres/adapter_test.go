package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wmichelin/clubdue/internal/database"
)

func TestPostgresAdapterPreservesRowsTransactionsAndErrors(t *testing.T) {
	ctx := context.Background()
	native := newMigrationTestDB(t, ctx)
	p := Wrap(native.Pool)
	var n int
	if err := p.QueryRow(ctx, `SELECT 1 WHERE false`).Scan(&n); err != database.ErrNoRows {
		t.Fatalf("missing row sentinel %v", err)
	}
	if _, err := p.Exec(ctx, `CREATE TABLE adapter_test(id int PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	r, err := p.Exec(ctx, `INSERT INTO adapter_test VALUES($1)`, 1)
	if err != nil || r.RowsAffected() != 1 {
		t.Fatalf("insert %v %v", r, err)
	}
	r, err = p.Exec(ctx, `UPDATE adapter_test SET id=2 WHERE id=3`)
	if err != nil || r.RowsAffected() != 0 {
		t.Fatalf("empty update %v %v", r, err)
	}
	_, err = p.Exec(ctx, `INSERT INTO adapter_test VALUES($1)`, 1)
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23505" {
		t.Fatalf("driver error lost: %v", err)
	}
	rows, err := p.Query(ctx, `SELECT generate_series(1,1000)`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("fixture empty")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	tx, err := p.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var isolation, readonly string
	if err := tx.QueryRow(ctx, `SHOW transaction_isolation`).Scan(&isolation); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SHOW transaction_read_only`).Scan(&readonly); err != nil {
		t.Fatal(err)
	}
	if isolation != "repeatable read" || readonly != "on" {
		t.Fatalf("transaction options changed: %s %s", isolation, readonly)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := p.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSnapshot}); err == nil {
		t.Fatal("unsupported isolation silently accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Begin(cancelled); err == nil {
		t.Fatal("cancelled begin succeeded")
	}
	if err := p.Ping(ctx); err != nil {
		t.Fatalf("connection failed after cancellation: %v", err)
	}
}

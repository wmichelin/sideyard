// Package database defines the connection capabilities consumed by application
// repositories. Statements remain native to their explicitly selected backend.
package database

import (
	"context"
	"database/sql"
)

type Dialect uint8

const (
	Postgres Dialect = iota
	SQLServer
)

var ErrNoRows = sql.ErrNoRows

// Statement selects explicitly authored native SQL. It never rewrites SQL or
// guesses parameter/result types. An unknown internal dialect fails closed.
func Statement(dialect Dialect, postgres, sqlserver string) string {
	switch dialect {
	case Postgres:
		return postgres
	case SQLServer:
		return sqlserver
	default:
		panic("unsupported database dialect")
	}
}

type Row interface{ Scan(...any) error }
type Rows interface {
	Row
	Next() bool
	Err() error
	Close() error
}
type Result interface{ RowsAffected() int64 }
type Queryer interface {
	Dialect() Dialect
	Exec(context.Context, string, ...any) (Result, error)
	Query(context.Context, string, ...any) (Rows, error)
	QueryRow(context.Context, string, ...any) Row
}
type Tx interface {
	Queryer
	// For database/sql, the context passed to Begin governs the transaction's
	// lifetime. Commit's context cannot replace that original deadline.
	Commit(context.Context) error
	Rollback(context.Context) error
}
type Pool interface {
	Queryer
	Begin(context.Context) (Tx, error)
	BeginTx(context.Context, *sql.TxOptions) (Tx, error)
	Ping(context.Context) error
	Close()
}

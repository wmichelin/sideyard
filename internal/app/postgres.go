package app

import (
	"context"

	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/postgres"
)

type DB struct{ Pool database.Pool }

func (db *DB) Close() {
	if db != nil && db.Pool != nil {
		db.Pool.Close()
	}
}

func openDB(ctx context.Context, databaseURL string) (*DB, error) {
	// SQL Server's runtime selection stays disabled until its native statements,
	// migrations and data conversion satisfy the application behavior suite.
	native, err := postgres.Open(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	return &DB{Pool: postgres.Wrap(native.Pool)}, nil
}

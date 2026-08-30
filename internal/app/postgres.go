package app

import (
	"context"

	"github.com/wmichelin/clubdue/internal/postgres"
)

// DB remains an application-local alias while domain packages are extracted.
// The concrete connection and migration boundary lives in internal/postgres.
type DB = postgres.DB

func openDB(ctx context.Context, databaseURL string) (*DB, error) {
	return postgres.Open(ctx, databaseURL)
}

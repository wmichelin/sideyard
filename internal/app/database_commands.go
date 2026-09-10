package app

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

// Operator commands have their own process-level cancellation and a bounded
// active window. No operator command is implicitly invoked by HTTP startup.
func databaseCommandContext() (context.Context, func()) {
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	ctx, cancel := context.WithTimeout(signalContext, 15*time.Minute)
	return ctx, func() { cancel(); stop() }
}
func runDatabaseCommand(c Config, command string) error {
	// A native schema check must never use PostgreSQL's migrating opener.
	if c.DatabaseBackend != "azure_sql" {
		return errors.New("migrate, check-db and maintenance currently require DATABASE_BACKEND=azure_sql")
	}
	if c.AzureSQL.AuthMode != "azure_cli" {
		return errors.New("native database commands require AZURE_SQL_AUTH_MODE=azure_cli")
	}
	log.Printf("database operator command=%s environment=%s server=%s database=%s", command, c.Environment, c.AzureSQL.Server, c.AzureSQL.Database)
	ctx, done := databaseCommandContext()
	defer done()
	db, err := openConfiguredDB(ctx, c)
	if err != nil {
		return err
	}
	defer db.Close()
	return executeDatabaseCommand(ctx, c, db, command)
}
func executeDatabaseCommand(ctx context.Context, c Config, db *DB, command string) error {
	if db.Pool.Dialect() != database.SQLServer {
		return errors.New("native database command requires SQL Server")
	}
	schema, ok := db.Pool.(interface {
		PrepareSchema(context.Context) error
		CheckSchema(context.Context) error
	})
	if !ok {
		return errors.New("native schema operator is unavailable")
	}
	switch command {
	case "migrate":
		if err := schema.PrepareSchema(ctx); err != nil {
			return err
		}
		log.Print("Azure SQL schema and isolation settings verified")
		return nil
	case "check-db":
		if err := schema.CheckSchema(ctx); err != nil {
			return err
		}
		log.Print("Azure SQL schema and isolation settings verified (read-only)")
		return nil
	case "maintenance":
		if err := schema.CheckSchema(ctx); err != nil {
			return err
		}
		store := &Store{db: db}
		if c.Environment == EnvironmentStaging {
			if _, err := store.RetireLegacySharedDemo(ctx); err != nil {
				return errors.New("retire legacy demo: " + err.Error())
			}
		}
		asOf := store.nowUTC()
		operations := []struct {
			name string
			run  func(context.Context) error
		}{
			{"sync seasonal visits", store.SyncAllVisitDues}, {"clean expired demos", store.CleanupExpiredPublicDemos}, {"generate recurring visits", store.GenerateAllServiceOccurrences},
			{"generate renewal tasks", func(ctx context.Context) error { return store.GenerateAllRenewalTasks(ctx, asOf) }},
			{"queue renewal digests", func(ctx context.Context) error { return store.QueueRenewalDigests(ctx, asOf) }},
		}
		for _, op := range operations {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := op.run(ctx); err != nil {
				return errors.New(op.name + ": " + err.Error())
			}
		}
		log.Print("Azure SQL database maintenance complete")
		return nil
	default:
		return errors.New("unsupported native database command")
	}
}

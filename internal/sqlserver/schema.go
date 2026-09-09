package sqlserver

import (
	"context"
	"embed"
)

// These are new native SQL Server migrations, not replacements for PostgreSQL's
// history. The application port is incremental and not selectable at runtime.
//
//go:embed migrations/*.sql
var migrationFS embed.FS

func (p *Pool) MigrateSchema(ctx context.Context) error { return p.Migrate(ctx, migrationFS) }

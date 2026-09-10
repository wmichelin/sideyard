package sqlserver

import (
	"context"
	"fmt"
	"regexp"
)

const runtimePrincipalName = "sideyard_staging_runtime"
const runtimeRoleName = "sideyard_app_runtime"

var identifierPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// PrepareRuntimePrincipal is an explicit staging administrator action. The
// external principal SID is the Entra application/client ID. A fixed contained
// alias avoids directory lookups and keeps all identifiers trusted constants.
func (p *Pool) PrepareRuntimePrincipal(ctx context.Context, clientID string) error {
	if !identifierPattern.MatchString(clientID) {
		return fmt.Errorf("runtime client ID must be a UUID")
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin runtime permission bootstrap: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `
		IF DATABASE_PRINCIPAL_ID(N'sideyard_staging_runtime') IS NULL
		BEGIN
			DECLARE @statement nvarchar(max)=N'CREATE USER [sideyard_staging_runtime] WITH SID = '
				+ CONVERT(nvarchar(34),CONVERT(varbinary(16),CONVERT(uniqueidentifier,@p1)),1)
				+ N', TYPE = E';
			EXEC sys.sp_executesql @statement;
		END;
		IF EXISTS (
			SELECT 1 FROM sys.database_principals
			WHERE name=N'sideyard_staging_runtime'
			  AND (type<>N'E' OR sid<>CONVERT(varbinary(16),CONVERT(uniqueidentifier,@p1)))
		) THROW 51000,'Sideyard runtime principal has an unexpected identity',1;
		IF DATABASE_PRINCIPAL_ID(N'sideyard_app_runtime') IS NULL
			EXEC(N'CREATE ROLE [sideyard_app_runtime]');
		IF IS_ROLEMEMBER(N'sideyard_app_runtime',N'sideyard_staging_runtime')<>1
			ALTER ROLE [sideyard_app_runtime] ADD MEMBER [sideyard_staging_runtime];
		GRANT SELECT,INSERT,UPDATE,DELETE ON SCHEMA::dbo TO [sideyard_app_runtime];
		DENY INSERT,UPDATE,DELETE ON OBJECT::dbo.schema_migrations TO [sideyard_app_runtime];`, clientID); err != nil {
		return fmt.Errorf("prepare SQL Server runtime permissions: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQL Server runtime permissions: %w", err)
	}
	return nil
}

package postgres

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wmichelin/clubdue/internal/transfer"
)

func TestLogicalExporterPermissions(t *testing.T) {
	if os.Getenv("CLUBDUE_REQUIRE_POSTGRES_EXPORTER_TEST") != "1" {
		t.Skip("dedicated PostgreSQL exporter rehearsal is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	adminURL := integrationDatabaseURL(t)
	admin, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	source, err := Open(ctx, adminURL)
	if err != nil {
		t.Fatal(err)
	}
	source.Close()
	testDatabase := fmt.Sprintf("sideyard_exporter_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, `CREATE DATABASE `+quotePostgresIdentifier(testDatabase)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1`, testDatabase)
		_, _ = admin.Exec(context.Background(), `DROP DATABASE IF EXISTS `+quotePostgresIdentifier(testDatabase))
		_, _ = admin.Exec(context.Background(), `DROP ROLE IF EXISTS unrelated_exporter_child`)
		_, _ = admin.Exec(context.Background(), `DROP ROLE IF EXISTS `+quotePostgresIdentifier(StagingExporterLogin))
		_, _ = admin.Exec(context.Background(), `DROP ROLE IF EXISTS `+quotePostgresIdentifier(LogicalExporterRole))
		rows, err := admin.Query(context.Background(), `SELECT datname FROM pg_database WHERE datallowconn`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var name string
				if rows.Scan(&name) == nil {
					_, _ = admin.Exec(context.Background(), `GRANT CONNECT,CREATE,TEMPORARY ON DATABASE `+quotePostgresIdentifier(name)+` TO PUBLIC`)
				}
			}
		}
	})

	testAdminURL := databaseURLForTest(t, adminURL, testDatabase, "", "")
	db, err := Open(ctx, testAdminURL)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	testAdmin, err := pgxpool.New(ctx, testAdminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer testAdmin.Close()
	profile := transfer.CurrentProfile
	if err := PrepareLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}
	if err := CheckLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}
	assertRoleCannotAuthenticate(t, ctx, databaseURLForTest(t, testAdminURL, testDatabase, StagingExporterLogin, "no-password"))

	password := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := ActivateLogicalExporter(ctx, testAdminURL, profile, password, func(time.Time) error { return nil }, func(time.Time) error { return nil }); err != nil {
		t.Fatal(err)
	}
	exporterURL := databaseURLForTest(t, testAdminURL, testDatabase, StagingExporterLogin, password)
	if err := CheckLogicalExporterSession(ctx, exporterURL, profile); err != nil {
		t.Fatal(err)
	}
	exporter, err := pgxpool.New(ctx, exporterURL)
	if err != nil {
		t.Fatal(err)
	}
	assertExporterStatementFails(t, ctx, exporter, `SELECT applied_at FROM public.schema_migrations`)
	assertExporterStatementFails(t, ctx, exporter, `INSERT INTO public.shops(id,name,slug,kind) VALUES ('11111111-1111-4111-8111-111111111111','x','x','customer')`)
	assertExporterStatementFails(t, ctx, exporter, `CREATE SCHEMA exporter_escape`)
	assertExporterStatementFails(t, ctx, exporter, `CREATE TABLE public.exporter_escape(id integer)`)
	assertExporterStatementFails(t, ctx, exporter, `CREATE TEMP TABLE exporter_temp(id integer)`)
	assertExporterStatementFails(t, ctx, exporter, `SET ROLE clubdue`)
	if err := DeactivateLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}
	if err := exporter.Ping(ctx); err == nil {
		t.Fatal("logical exporter session survived deactivation")
	}
	exporter.Close()
	assertRoleCannotAuthenticate(t, ctx, databaseURLForTest(t, adminURL, "postgres", StagingExporterLogin, password))

	freshOwner, err := pgxpool.New(ctx, testAdminURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := freshOwner.Ping(ctx); err != nil {
		freshOwner.Close()
		t.Fatalf("database owner lost source access: %v", err)
	}
	freshOwner.Close()

	if _, err := admin.Exec(ctx, `CREATE ROLE unrelated_exporter_child`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `GRANT sideyard_logical_exporter TO unrelated_exporter_child`); err != nil {
		t.Fatal(err)
	}
	assertExporterAuditFails(t, ctx, testAdminURL, profile, "memberships")
	if err := PrepareLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `DROP ROLE unrelated_exporter_child`); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.Exec(ctx, `CREATE ROLE unrelated_exporter_child`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `GRANT sideyard_staging_logical_exporter TO unrelated_exporter_child`); err != nil {
		t.Fatal(err)
	}
	assertExporterAuditFails(t, ctx, testAdminURL, profile, "memberships")
	if err := PrepareLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `DROP ROLE unrelated_exporter_child`); err != nil {
		t.Fatal(err)
	}

	if _, err := testAdmin.Exec(ctx, `ALTER TABLE public.shops ADD COLUMN private_exporter_probe text`); err != nil {
		t.Fatal(err)
	}
	assertExporterAuditFails(t, ctx, testAdminURL, profile, "source catalog is incompatible")
	if _, err := testAdmin.Exec(ctx, `ALTER TABLE public.shops DROP COLUMN private_exporter_probe`); err != nil {
		t.Fatal(err)
	}
	if _, err := testAdmin.Exec(ctx, `GRANT SELECT(applied_at) ON public.schema_migrations TO sideyard_logical_exporter`); err != nil {
		t.Fatal(err)
	}
	assertExporterAuditFails(t, ctx, testAdminURL, profile, "schema_migrations.applied_at")
	if err := PrepareLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}

	if _, err := testAdmin.Exec(ctx, `CREATE SCHEMA exporter_probe; CREATE VIEW exporter_probe.exporter_probe_view AS SELECT id FROM public.shops`); err != nil {
		t.Fatal(err)
	}
	if _, err := testAdmin.Exec(ctx, `GRANT SELECT ON exporter_probe.exporter_probe_view TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	assertExporterAuditFails(t, ctx, testAdminURL, profile, "exporter_probe_view")
	if _, err := testAdmin.Exec(ctx, `DROP SCHEMA exporter_probe CASCADE`); err != nil {
		t.Fatal(err)
	}

	if _, err := testAdmin.Exec(ctx, `CREATE SCHEMA exporter_private; CREATE TABLE exporter_private.secret(id integer); GRANT USAGE ON SCHEMA exporter_private TO PUBLIC; GRANT SELECT ON exporter_private.secret TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	assertExporterAuditFails(t, ctx, testAdminURL, profile, "schema privileges")
	if _, err := testAdmin.Exec(ctx, `DROP SCHEMA exporter_private CASCADE`); err != nil {
		t.Fatal(err)
	}

	if _, err := testAdmin.Exec(ctx, `CREATE SCHEMA exporter_zero; CREATE TABLE exporter_zero.empty(); GRANT SELECT,TRUNCATE ON exporter_zero.empty TO sideyard_logical_exporter`); err != nil {
		t.Fatal(err)
	}
	assertExporterAuditFails(t, ctx, testAdminURL, profile, "exporter_zero.empty")
	if err := PrepareLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := testAdmin.Exec(ctx, `DROP SCHEMA exporter_zero CASCADE`); err != nil {
		t.Fatal(err)
	}

	var largeObject uint32
	if err := testAdmin.QueryRow(ctx, `SELECT lo_from_bytea(0,decode('736563726574','hex'))`).Scan(&largeObject); err != nil {
		t.Fatal(err)
	}
	if _, err := testAdmin.Exec(ctx, fmt.Sprintf(`GRANT SELECT ON LARGE OBJECT %d TO sideyard_logical_exporter`, largeObject)); err != nil {
		t.Fatal(err)
	}
	assertExporterAuditFails(t, ctx, testAdminURL, profile, "large-object privileges")
	if err := PrepareLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}
	if _, err := testAdmin.Exec(ctx, `SELECT lo_unlink($1)`, largeObject); err != nil {
		t.Fatal(err)
	}

	if _, err := testAdmin.Exec(ctx, `GRANT CREATE ON DATABASE `+quotePostgresIdentifier(testDatabase)+` TO PUBLIC`); err != nil {
		t.Fatal(err)
	}
	assertExporterAuditFails(t, ctx, testAdminURL, profile, "database privileges")
	if err := PrepareLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}

	if _, err := testAdmin.Exec(ctx, `ALTER TABLE public.shops ENABLE ROW LEVEL SECURITY; CREATE POLICY exporter_hidden_rows ON public.shops USING (false)`); err != nil {
		t.Fatal(err)
	}
	assertExporterAuditFails(t, ctx, testAdminURL, profile, "row-level security")
	if _, err := testAdmin.Exec(ctx, `DROP POLICY exporter_hidden_rows ON public.shops; ALTER TABLE public.shops DISABLE ROW LEVEL SECURITY`); err != nil {
		t.Fatal(err)
	}
	if err := PrepareLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}
	if err := CheckLogicalExporter(ctx, testAdminURL, profile); err != nil {
		t.Fatal(err)
	}
}

func TestExporterCatalogIdentityRequiresExactCurrentProfile(t *testing.T) {
	profile := transfer.CurrentProfile
	profile.PostgresMigrations--
	if _, err := exporterCatalogIdentity(profile); err == nil {
		t.Fatal("exporter accepted a different migration profile")
	}

	profile = transfer.CurrentProfile
	profile.Catalog = append([]transfer.Table(nil), profile.Catalog...)
	profile.Catalog[0].Columns = append([]transfer.Column(nil), profile.Catalog[0].Columns...)
	profile.Catalog[0].Columns = append(profile.Catalog[0].Columns, transfer.Column{Name: "unexpected", Kind: transfer.Text})
	if _, err := exporterCatalogIdentity(profile); err == nil {
		t.Fatal("exporter accepted a different catalog under the current schema name")
	}
}

func assertRoleCannotAuthenticate(t *testing.T, ctx context.Context, databaseURL string) {
	t.Helper()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err == nil {
		t.Fatal("dormant logical exporter authenticated")
	}
}

func assertExporterAuditFails(t *testing.T, ctx context.Context, databaseURL string, profile transfer.Profile, want string) {
	t.Helper()
	if err := CheckLogicalExporter(ctx, databaseURL, profile); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("exporter audit error = %v, want %q", err, want)
	}
}

func databaseURLForTest(t *testing.T, source, database, username, password string) string {
	t.Helper()
	parsed, err := url.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Path = "/" + database
	if username != "" {
		parsed.User = url.UserPassword(username, password)
	}
	return parsed.String()
}

func assertExporterStatementFails(t *testing.T, ctx context.Context, pool *pgxpool.Pool, statement string) {
	t.Helper()
	if _, err := pool.Exec(ctx, statement); err == nil {
		t.Fatalf("logical exporter accepted %s", statement)
	}
}

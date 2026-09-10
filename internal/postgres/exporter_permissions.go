package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wmichelin/clubdue/internal/transfer"
)

const (
	LogicalExporterRole  = "sideyard_logical_exporter"
	StagingExporterLogin = "sideyard_staging_logical_exporter"
)

var exporterRoleSettings = []string{
	"default_transaction_read_only=on",
	"idle_in_transaction_session_timeout=60s",
	"lock_timeout=5s",
	"search_path=pg_catalog, public",
	"statement_timeout=15min",
}

// PrepareLogicalExporter creates a dormant staging login and binds its no-login
// grant role to the exact columns in one validated transfer profile. It closes
// PostgreSQL's broad PUBLIC defaults on this dedicated Sideyard cluster. No
// password is created; export activation requires a later reviewed lifecycle.
func PrepareLogicalExporter(ctx context.Context, databaseURL string, profile transfer.Profile) error {
	catalogIdentity, err := exporterCatalogIdentity(profile)
	if err != nil {
		return err
	}
	pool, database, administrator, err := openExporterAdministrator(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := checkDedicatedExporterCluster(ctx, pool, administrator); err != nil {
		return err
	}
	if err := checkExporterSourcePreflight(ctx, pool, profile); err != nil {
		return err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin logical exporter bootstrap: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, statement := range []string{
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='sideyard_logical_exporter') THEN CREATE ROLE sideyard_logical_exporter; END IF; END $$`,
		`DO $$ BEGIN IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname='sideyard_staging_logical_exporter') THEN CREATE ROLE sideyard_staging_logical_exporter; END IF; END $$`,
		`ALTER ROLE sideyard_logical_exporter NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS INHERIT CONNECTION LIMIT -1`,
		`ALTER ROLE sideyard_logical_exporter RESET ALL`,
		`ALTER ROLE sideyard_staging_logical_exporter NOLOGIN PASSWORD NULL NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS INHERIT CONNECTION LIMIT 1 VALID UNTIL 'infinity'`,
		`ALTER ROLE sideyard_staging_logical_exporter RESET ALL`,
	} {
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("prepare logical exporter roles: %w", err)
		}
	}
	for _, setting := range []string{
		`ALTER ROLE sideyard_staging_logical_exporter SET default_transaction_read_only TO 'on'`,
		`ALTER ROLE sideyard_staging_logical_exporter SET idle_in_transaction_session_timeout TO '60s'`,
		`ALTER ROLE sideyard_staging_logical_exporter SET lock_timeout TO '5s'`,
		`ALTER ROLE sideyard_staging_logical_exporter SET search_path TO pg_catalog, public`,
		`ALTER ROLE sideyard_staging_logical_exporter SET statement_timeout TO '15min'`,
	} {
		if _, err := tx.Exec(ctx, setting); err != nil {
			return fmt.Errorf("set logical exporter session boundary: %w", err)
		}
	}
	if err := removeExporterMembershipDrift(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `GRANT sideyard_logical_exporter TO sideyard_staging_logical_exporter`); err != nil {
		return fmt.Errorf("grant logical exporter role: %w", err)
	}

	databases, err := databaseNames(ctx, tx)
	if err != nil {
		return err
	}
	for _, name := range databases {
		if _, err := tx.Exec(ctx, "REVOKE CONNECT,CREATE,TEMPORARY ON DATABASE "+quotePostgresIdentifier(name)+" FROM PUBLIC,sideyard_logical_exporter,sideyard_staging_logical_exporter"); err != nil {
			return fmt.Errorf("restrict logical exporter database %s: %w", name, err)
		}
	}
	if _, err := tx.Exec(ctx, "GRANT CONNECT ON DATABASE "+quotePostgresIdentifier(database)+" TO sideyard_logical_exporter"); err != nil {
		return fmt.Errorf("grant logical exporter database access: %w", err)
	}

	schemas, err := userSchemaNames(ctx, tx)
	if err != nil {
		return err
	}
	for _, schema := range schemas {
		quoted := quotePostgresIdentifier(schema)
		for _, statement := range []string{
			"REVOKE ALL PRIVILEGES ON SCHEMA " + quoted + " FROM PUBLIC,sideyard_logical_exporter,sideyard_staging_logical_exporter",
			"REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA " + quoted + " FROM PUBLIC,sideyard_logical_exporter,sideyard_staging_logical_exporter",
			"REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA " + quoted + " FROM PUBLIC,sideyard_logical_exporter,sideyard_staging_logical_exporter",
			"REVOKE ALL PRIVILEGES ON ALL FUNCTIONS IN SCHEMA " + quoted + " FROM PUBLIC,sideyard_logical_exporter,sideyard_staging_logical_exporter",
		} {
			if _, err := tx.Exec(ctx, statement); err != nil {
				return fmt.Errorf("restrict logical exporter schema %s: %w", schema, err)
			}
		}
	}
	largeObjects, err := largeObjectIDs(ctx, tx)
	if err != nil {
		return err
	}
	for _, oid := range largeObjects {
		if _, err := tx.Exec(ctx, fmt.Sprintf("REVOKE ALL PRIVILEGES ON LARGE OBJECT %d FROM PUBLIC,sideyard_logical_exporter,sideyard_staging_logical_exporter", oid)); err != nil {
			return fmt.Errorf("restrict logical exporter large object %d: %w", oid, err)
		}
	}
	if _, err := tx.Exec(ctx, `GRANT USAGE ON SCHEMA public TO sideyard_logical_exporter`); err != nil {
		return fmt.Errorf("grant logical exporter schema access: %w", err)
	}
	for _, relation := range exporterRelations(profile) {
		columns := make([]string, len(relation.columns))
		for index, column := range relation.columns {
			columns[index] = quotePostgresIdentifier(column)
		}
		statement := `GRANT SELECT (` + strings.Join(columns, ",") + `) ON TABLE public.` + quotePostgresIdentifier(relation.name) + ` TO sideyard_logical_exporter`
		if _, err := tx.Exec(ctx, statement); err != nil {
			return fmt.Errorf("grant logical exporter columns on %s: %w", relation.name, err)
		}
	}
	if err := setRoleComment(ctx, tx, LogicalExporterRole, "Sideyard logical exporter "+catalogIdentity); err != nil {
		return err
	}
	if err := setRoleComment(ctx, tx, StagingExporterLogin, "Sideyard dormant staging logical exporter login"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit logical exporter bootstrap: %w", err)
	}
	return CheckLogicalExporter(ctx, databaseURL, profile)
}

// CheckLogicalExporter audits the dormant role from a superuser connection.
func CheckLogicalExporter(ctx context.Context, databaseURL string, profile transfer.Profile) error {
	catalogIdentity, err := exporterCatalogIdentity(profile)
	if err != nil {
		return err
	}
	admin, database, administrator, err := openExporterAdministrator(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer admin.Close()
	if err := checkDedicatedExporterCluster(ctx, admin, administrator); err != nil {
		return err
	}
	if err := checkExporterSourcePreflight(ctx, admin, profile); err != nil {
		return err
	}
	if err := checkExporterRoles(ctx, admin, catalogIdentity); err != nil {
		return err
	}
	return checkExporterPrivileges(ctx, admin, database, profile)
}

// CheckLogicalExporterSession is used by the disposable real-engine rehearsal
// and is the canary for a future short-lived export activation.
func CheckLogicalExporterSession(ctx context.Context, exporterURL string, profile transfer.Profile) error {
	if _, err := exporterCatalogIdentity(profile); err != nil {
		return err
	}
	exporter, err := pgxpool.New(ctx, exporterURL)
	if err != nil {
		return fmt.Errorf("connect with logical exporter credential: %w", err)
	}
	defer exporter.Close()
	var user, readOnly string
	if err := exporter.QueryRow(ctx, `SELECT current_user,current_setting('default_transaction_read_only')`).Scan(&user, &readOnly); err != nil {
		return fmt.Errorf("authenticate logical exporter: %w", err)
	}
	if user != StagingExporterLogin || readOnly != "on" {
		return fmt.Errorf("logical exporter authenticated outside its fixed boundary")
	}
	for _, relation := range exporterRelations(profile) {
		columns := make([]string, len(relation.columns))
		for index, column := range relation.columns {
			columns[index] = quotePostgresIdentifier(column)
		}
		if _, err := exporter.Exec(ctx, `SELECT `+strings.Join(columns, ",")+` FROM public.`+quotePostgresIdentifier(relation.name)+` LIMIT 0`); err != nil {
			return fmt.Errorf("logical exporter cannot read %s: %w", relation.name, err)
		}
	}
	return nil
}

type exporterRelation struct {
	name    string
	columns []string
}

func exporterRelations(profile transfer.Profile) []exporterRelation {
	relations := []exporterRelation{{name: "schema_migrations", columns: []string{"version", "checksum"}}}
	for _, table := range profile.Catalog {
		columns := make([]string, len(table.Columns))
		for index, column := range table.Columns {
			columns[index] = column.Name
		}
		relations = append(relations, exporterRelation{name: table.Name, columns: columns})
	}
	return relations
}

func exporterCatalogIdentity(profile transfer.Profile) (string, error) {
	if profile.LogicalSchema != transfer.CurrentProfile.LogicalSchema ||
		profile.PostgresMigrations != transfer.CurrentProfile.PostgresMigrations ||
		profile.SQLServerMigrations != transfer.CurrentProfile.SQLServerMigrations {
		return "", fmt.Errorf("logical exporter supports only the current staging profile")
	}
	digest, err := transfer.ProfileCatalogDigest(profile)
	if err != nil {
		return "", fmt.Errorf("logical exporter profile is invalid: %w", err)
	}
	currentDigest, err := transfer.ProfileCatalogDigest(transfer.CurrentProfile)
	if err != nil {
		return "", fmt.Errorf("current logical exporter profile is invalid: %w", err)
	}
	if digest != currentDigest {
		return "", fmt.Errorf("logical exporter supports only the current staging profile")
	}
	return profile.LogicalSchema + ":" + digest, nil
}

func checkExporterSourcePreflight(ctx context.Context, pool *pgxpool.Pool, profile transfer.Profile) error {
	if err := transfer.ValidateCatalog(ctx, Wrap(pool)); err != nil {
		return fmt.Errorf("logical exporter source catalog is incompatible: %w", err)
	}
	var unsafeRLS int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=ANY($1) AND (c.relrowsecurity OR c.relforcerowsecurity OR EXISTS (SELECT 1 FROM pg_policy p WHERE p.polrelid=c.oid))`, relationNames(profile)).Scan(&unsafeRLS); err != nil {
		return fmt.Errorf("inspect logical exporter source row security: %w", err)
	}
	if unsafeRLS != 0 {
		return fmt.Errorf("logical exporter catalog uses unsupported row-level security")
	}
	return nil
}

func openExporterAdministrator(ctx context.Context, databaseURL string) (*pgxpool.Pool, string, string, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, "", "", fmt.Errorf("connect PostgreSQL exporter administrator: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, "", "", fmt.Errorf("ping PostgreSQL exporter administrator: %w", err)
	}
	var database, user string
	var superuser bool
	if err := pool.QueryRow(ctx, `SELECT current_database(),current_user,r.rolsuper FROM pg_roles r WHERE r.rolname=current_user`).Scan(&database, &user, &superuser); err != nil {
		pool.Close()
		return nil, "", "", fmt.Errorf("inspect PostgreSQL exporter administrator: %w", err)
	}
	if database == "" || user == "" || !superuser {
		pool.Close()
		return nil, "", "", fmt.Errorf("logical exporter bootstrap requires the database superuser")
	}
	return pool, database, user, nil
}

// PUBLIC hardening is a permanent baseline on this dedicated cluster. Refuse
// to alter a cluster with another login or database owner.
func checkDedicatedExporterCluster(ctx context.Context, pool *pgxpool.Pool, administrator string) error {
	var otherLogins, otherOwners int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_roles WHERE rolcanlogin AND rolname NOT IN ($1,$2)`, administrator, StagingExporterLogin).Scan(&otherLogins); err != nil {
		return fmt.Errorf("inspect PostgreSQL cluster logins: %w", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datallowconn AND r.rolname<>$1`, administrator).Scan(&otherOwners); err != nil {
		return fmt.Errorf("inspect PostgreSQL database owners: %w", err)
	}
	if otherLogins != 0 || otherOwners != 0 {
		return fmt.Errorf("logical exporter requires a dedicated Sideyard PostgreSQL cluster")
	}
	return nil
}

func removeExporterMembershipDrift(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT parent.rolname,member.rolname FROM pg_auth_members m JOIN pg_roles member ON member.oid=m.member JOIN pg_roles parent ON parent.oid=m.roleid WHERE member.rolname IN ($1,$2) OR parent.rolname IN ($1,$2) ORDER BY parent.rolname,member.rolname`, LogicalExporterRole, StagingExporterLogin)
	if err != nil {
		return fmt.Errorf("inspect logical exporter memberships: %w", err)
	}
	var pairs [][2]string
	for rows.Next() {
		var pair [2]string
		if err := rows.Scan(&pair[0], &pair[1]); err != nil {
			rows.Close()
			return fmt.Errorf("inspect logical exporter memberships: %w", err)
		}
		pairs = append(pairs, pair)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("inspect logical exporter memberships: %w", err)
	}
	rows.Close()
	for _, pair := range pairs {
		if _, err := tx.Exec(ctx, "REVOKE "+quotePostgresIdentifier(pair[0])+" FROM "+quotePostgresIdentifier(pair[1])); err != nil {
			return fmt.Errorf("remove unexpected logical exporter membership: %w", err)
		}
	}
	return nil
}

func checkExporterRoles(ctx context.Context, admin *pgxpool.Pool, catalogIdentity string) error {
	rows, err := admin.Query(ctx, `SELECT r.rolname,r.rolsuper,r.rolinherit,r.rolcreaterole,r.rolcreatedb,r.rolcanlogin,r.rolreplication,r.rolconnlimit,r.rolbypassrls,COALESCE(r.rolconfig,ARRAY[]::text[]),COALESCE(shobj_description(r.oid,'pg_authid'),''),a.rolpassword IS NULL FROM pg_roles r JOIN pg_authid a ON a.oid=r.oid WHERE r.rolname IN ($1,$2) ORDER BY r.rolname`, LogicalExporterRole, StagingExporterLogin)
	if err != nil {
		return fmt.Errorf("inspect logical exporter roles: %w", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, comment string
		var superuser, inherit, createRole, createDB, login, replication, bypassRLS, passwordNull bool
		var connectionLimit int
		var settings []string
		if err := rows.Scan(&name, &superuser, &inherit, &createRole, &createDB, &login, &replication, &connectionLimit, &bypassRLS, &settings, &comment, &passwordNull); err != nil {
			return fmt.Errorf("inspect logical exporter roles: %w", err)
		}
		seen++
		if superuser || !inherit || createRole || createDB || login || replication || bypassRLS || !passwordNull {
			return fmt.Errorf("logical exporter role %s is not dormant and unprivileged", name)
		}
		sort.Strings(settings)
		if name == LogicalExporterRole {
			if connectionLimit != -1 || len(settings) != 0 || comment != "Sideyard logical exporter "+catalogIdentity {
				return fmt.Errorf("logical exporter grant role drifted")
			}
		} else {
			expectedSettings := append([]string(nil), exporterRoleSettings...)
			sort.Strings(expectedSettings)
			if connectionLimit != 1 || strings.Join(settings, "\n") != strings.Join(expectedSettings, "\n") || comment != "Sideyard dormant staging logical exporter login" {
				return fmt.Errorf("logical exporter login role drifted")
			}
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("inspect logical exporter roles: %w", err)
	}
	if seen != 2 {
		return fmt.Errorf("logical exporter roles are incomplete")
	}
	var total, expectedMemberships, adminOptions int
	if err := admin.QueryRow(ctx, `SELECT COUNT(*),COUNT(*) FILTER (WHERE parent.rolname=$1 AND member.rolname=$2),COUNT(*) FILTER (WHERE m.admin_option) FROM pg_auth_members m JOIN pg_roles member ON member.oid=m.member JOIN pg_roles parent ON parent.oid=m.roleid WHERE member.rolname IN ($1,$2) OR parent.rolname IN ($1,$2)`, LogicalExporterRole, StagingExporterLogin).Scan(&total, &expectedMemberships, &adminOptions); err != nil {
		return fmt.Errorf("inspect logical exporter membership closure: %w", err)
	}
	if total != 1 || expectedMemberships != 1 || adminOptions != 0 {
		return fmt.Errorf("logical exporter memberships drifted")
	}
	return nil
}

func checkExporterPrivileges(ctx context.Context, admin *pgxpool.Pool, database string, profile transfer.Profile) error {
	rows, err := admin.Query(ctx, `SELECT datname,has_database_privilege($1,datname,'CONNECT'),has_database_privilege($1,datname,'CREATE'),has_database_privilege($1,datname,'TEMP') FROM pg_database WHERE datallowconn ORDER BY datname`, StagingExporterLogin)
	if err != nil {
		return fmt.Errorf("inspect logical exporter database privileges: %w", err)
	}
	for rows.Next() {
		var name string
		var connect, create, temporary bool
		if err := rows.Scan(&name, &connect, &create, &temporary); err != nil {
			rows.Close()
			return fmt.Errorf("inspect logical exporter database privileges: %w", err)
		}
		if create || temporary || connect != (name == database) {
			rows.Close()
			return fmt.Errorf("logical exporter database privileges drifted")
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("inspect logical exporter database privileges: %w", err)
	}
	rows.Close()

	rows, err = admin.Query(ctx, `SELECT nspname,has_schema_privilege($1,nspname,'USAGE'),has_schema_privilege($1,nspname,'CREATE') FROM pg_namespace WHERE nspname<>'information_schema' AND nspname NOT LIKE 'pg_%' ORDER BY nspname`, StagingExporterLogin)
	if err != nil {
		return fmt.Errorf("inspect logical exporter schema privileges: %w", err)
	}
	for rows.Next() {
		var name string
		var usage, create bool
		if err := rows.Scan(&name, &usage, &create); err != nil {
			rows.Close()
			return fmt.Errorf("inspect logical exporter schema privileges: %w", err)
		}
		if create || usage != (name == "public") {
			rows.Close()
			return fmt.Errorf("logical exporter schema privileges drifted at %s", name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("inspect logical exporter schema privileges: %w", err)
	}
	rows.Close()

	expectedColumns := make(map[string]bool)
	expectedRelations := make(map[string]bool)
	for _, relation := range exporterRelations(profile) {
		expectedRelations["public."+relation.name] = true
		for _, column := range relation.columns {
			expectedColumns["public."+relation.name+"."+column] = true
		}
	}
	rows, err = admin.Query(ctx, `SELECT n.nspname,c.relname,c.relkind::text,has_table_privilege($1,c.oid,'SELECT'),has_table_privilege($1,c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname<>'information_schema' AND n.nspname NOT LIKE 'pg_%' AND c.relkind IN ('r','p','v','m','f') ORDER BY n.nspname,c.relname`, StagingExporterLogin)
	if err != nil {
		return fmt.Errorf("inspect logical exporter relation privileges: %w", err)
	}
	seenRelations := make(map[string]bool, len(expectedRelations))
	for rows.Next() {
		var schema, relation, kind string
		var tableSelect, tableWrite bool
		if err := rows.Scan(&schema, &relation, &kind, &tableSelect, &tableWrite); err != nil {
			rows.Close()
			return fmt.Errorf("inspect logical exporter relation privileges: %w", err)
		}
		key := schema + "." + relation
		if tableSelect || tableWrite {
			rows.Close()
			return fmt.Errorf("logical exporter relation privileges drifted at %s", key)
		}
		if expectedRelations[key] {
			seenRelations[key] = true
		}
		if expectedRelations[key] && kind != "r" && kind != "p" {
			rows.Close()
			return fmt.Errorf("logical exporter relation type drifted at %s", key)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("inspect logical exporter relation privileges: %w", err)
	}
	rows.Close()
	if len(seenRelations) != len(expectedRelations) {
		return fmt.Errorf("logical exporter relations are incomplete")
	}

	rows, err = admin.Query(ctx, `SELECT n.nspname,c.relname,a.attname,has_column_privilege($1,c.oid,a.attnum,'SELECT'),has_column_privilege($1,c.oid,a.attnum,'INSERT,UPDATE,REFERENCES') FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped WHERE n.nspname<>'information_schema' AND n.nspname NOT LIKE 'pg_%' AND c.relkind IN ('r','p','v','m','f') ORDER BY n.nspname,c.relname,a.attnum`, StagingExporterLogin)
	if err != nil {
		return fmt.Errorf("inspect logical exporter column privileges: %w", err)
	}
	seenColumns := make(map[string]bool, len(expectedColumns))
	for rows.Next() {
		var schema, relation, column string
		var columnSelect, columnWrite bool
		if err := rows.Scan(&schema, &relation, &column, &columnSelect, &columnWrite); err != nil {
			rows.Close()
			return fmt.Errorf("inspect logical exporter column privileges: %w", err)
		}
		key := schema + "." + relation + "." + column
		if columnWrite || columnSelect != expectedColumns[key] {
			rows.Close()
			return fmt.Errorf("logical exporter column privileges drifted at %s", key)
		}
		if expectedColumns[key] {
			seenColumns[key] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("inspect logical exporter column privileges: %w", err)
	}
	rows.Close()
	if len(seenColumns) != len(expectedColumns) {
		return fmt.Errorf("logical exporter column privileges are incomplete")
	}

	var unsafeRLS, sequencePrivileges, functionPrivileges, largeObjectPrivileges int
	if err := admin.QueryRow(ctx, `SELECT COUNT(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=ANY($1) AND (c.relrowsecurity OR c.relforcerowsecurity OR EXISTS (SELECT 1 FROM pg_policy p WHERE p.polrelid=c.oid))`, relationNames(profile)).Scan(&unsafeRLS); err != nil {
		return fmt.Errorf("inspect logical exporter row security: %w", err)
	}
	if err := admin.QueryRow(ctx, `SELECT COUNT(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname<>'information_schema' AND n.nspname NOT LIKE 'pg_%' AND c.relkind='S' AND has_sequence_privilege($1::text,format('%I.%I',n.nspname,c.relname),'USAGE,SELECT,UPDATE')`, StagingExporterLogin).Scan(&sequencePrivileges); err != nil {
		return fmt.Errorf("inspect logical exporter sequence privileges: %w", err)
	}
	if err := admin.QueryRow(ctx, `SELECT COUNT(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname<>'information_schema' AND n.nspname NOT LIKE 'pg_%' AND has_function_privilege($1::text,p.oid::regprocedure,'EXECUTE')`, StagingExporterLogin).Scan(&functionPrivileges); err != nil {
		return fmt.Errorf("inspect logical exporter function privileges: %w", err)
	}
	if err := admin.QueryRow(ctx, `SELECT COUNT(*) FROM pg_largeobject_metadata m WHERE m.lomowner IN (SELECT oid FROM pg_roles WHERE rolname IN ($1,$2)) OR EXISTS (SELECT 1 FROM aclexplode(m.lomacl) a WHERE a.grantee=0 OR a.grantee IN (SELECT oid FROM pg_roles WHERE rolname IN ($1,$2)))`, LogicalExporterRole, StagingExporterLogin).Scan(&largeObjectPrivileges); err != nil {
		return fmt.Errorf("inspect logical exporter large-object privileges: %w", err)
	}
	if unsafeRLS != 0 {
		return fmt.Errorf("logical exporter catalog uses unsupported row-level security")
	}
	if sequencePrivileges != 0 || functionPrivileges != 0 {
		return fmt.Errorf("logical exporter executable privileges drifted")
	}
	if largeObjectPrivileges != 0 {
		return fmt.Errorf("logical exporter large-object privileges drifted")
	}
	return nil
}

func relationNames(profile transfer.Profile) []string {
	result := make([]string, 0, len(profile.Catalog)+1)
	for _, relation := range exporterRelations(profile) {
		result = append(result, relation.name)
	}
	return result
}

func databaseNames(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT datname FROM pg_database WHERE datallowconn ORDER BY datname`)
	if err != nil {
		return nil, fmt.Errorf("list logical exporter databases: %w", err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("list logical exporter databases: %w", err)
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list logical exporter databases: %w", err)
	}
	return result, nil
}

func userSchemaNames(ctx context.Context, tx pgx.Tx) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname<>'information_schema' AND nspname NOT LIKE 'pg_%' ORDER BY nspname`)
	if err != nil {
		return nil, fmt.Errorf("list logical exporter schemas: %w", err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("list logical exporter schemas: %w", err)
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list logical exporter schemas: %w", err)
	}
	return result, nil
}

func largeObjectIDs(ctx context.Context, tx pgx.Tx) ([]uint32, error) {
	rows, err := tx.Query(ctx, `SELECT oid FROM pg_largeobject_metadata ORDER BY oid`)
	if err != nil {
		return nil, fmt.Errorf("list logical exporter large objects: %w", err)
	}
	defer rows.Close()
	var result []uint32
	for rows.Next() {
		var oid uint32
		if err := rows.Scan(&oid); err != nil {
			return nil, fmt.Errorf("list logical exporter large objects: %w", err)
		}
		result = append(result, oid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list logical exporter large objects: %w", err)
	}
	return result, nil
}

func setRoleComment(ctx context.Context, tx pgx.Tx, role, comment string) error {
	var statement string
	if err := tx.QueryRow(ctx, `SELECT format('COMMENT ON ROLE %I IS %L',$1::text,$2::text)`, role, comment).Scan(&statement); err != nil {
		return fmt.Errorf("prepare logical exporter role marker: %w", err)
	}
	if _, err := tx.Exec(ctx, statement); err != nil {
		return fmt.Errorf("set logical exporter role marker: %w", err)
	}
	return nil
}

func quotePostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

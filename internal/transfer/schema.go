package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/wmichelin/clubdue/internal/database"
)

type actualColumn struct {
	name     string
	kind     Kind
	nullable bool
}

var sqlDerivedColumns = map[string][]actualColumn{
	"users": {{name: "email_normalized", kind: Text, nullable: false}},
}

func CatalogDigest() string {
	return catalogDigest(Catalog)
}

func catalogDigest(catalog []Table) string {
	encoded, err := json.Marshal(catalog)
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func validateCatalogDefinition() error { return validateCatalog(Catalog) }

func validateCatalog(catalog []Table) error {
	if len(catalog) == 0 {
		return fmt.Errorf("logical catalog is empty")
	}
	tables := make(map[string]struct{}, len(catalog))
	for _, table := range catalog {
		if !identifierValue.MatchString(table.Name) {
			return fmt.Errorf("logical catalog has invalid table %q", table.Name)
		}
		if _, exists := tables[table.Name]; exists {
			return fmt.Errorf("logical catalog repeats table %s", table.Name)
		}
		tables[table.Name] = struct{}{}
		if len(table.Key) == 0 {
			return fmt.Errorf("logical table %s has no stable key", table.Name)
		}
		columns := make(map[string]Column, len(table.Columns))
		for _, column := range table.Columns {
			if !identifierValue.MatchString(column.Name) {
				return fmt.Errorf("logical table %s has invalid column %q", table.Name, column.Name)
			}
			if _, exists := columns[column.Name]; exists {
				return fmt.Errorf("logical table %s repeats column %s", table.Name, column.Name)
			}
			if !validKind(column.Kind) {
				return fmt.Errorf("logical table %s has unsupported kind %q", table.Name, column.Kind)
			}
			columns[column.Name] = column
		}
		for _, key := range table.Key {
			column, exists := columns[key]
			if !exists || column.Nullable {
				return fmt.Errorf("logical table %s has invalid key %s", table.Name, key)
			}
		}
		owners := 0
		if table.ShopColumn != "" {
			owners++
			column, exists := columns[table.ShopColumn]
			if !exists || column.Nullable || column.Kind != UUID {
				return fmt.Errorf("logical table %s has invalid shop column", table.Name)
			}
		}
		if table.ShopViaUser != "" {
			owners++
			column, exists := columns[table.ShopViaUser]
			if !exists || column.Nullable || column.Kind != UUID {
				return fmt.Errorf("logical table %s has invalid user ownership column", table.Name)
			}
			if _, usersSeen := tables["users"]; !usersSeen {
				return fmt.Errorf("logical table %s precedes users", table.Name)
			}
		}
		if owners > 1 {
			return fmt.Errorf("logical table %s has ambiguous shop ownership", table.Name)
		}
	}
	return nil
}

func validKind(kind Kind) bool {
	switch kind {
	case UUID, Text, TextArray, UUIDArray, JSON, Integer, Instant, Date, Decimal, Boolean, Bytes:
		return true
	default:
		return false
	}
}

// ValidateCatalog rejects a database with missing or unknown persisted
// application data. Engine ledgers and SQL Server computed columns are not
// transferable rows and are checked by each engine's migration verifier.
func ValidateCatalog(ctx context.Context, queryer database.Queryer) error {
	return validateCatalogProfile(ctx, queryer, CurrentProfile)
}

func validateCatalogProfile(ctx context.Context, queryer database.Queryer, profile Profile) error {
	if err := validateCatalog(profile.Catalog); err != nil {
		return err
	}
	actual, err := readActualCatalog(ctx, queryer)
	if err != nil {
		return err
	}
	expectedCatalog := profile.Catalog
	// Every SQL destination runs the current native schema. Older PostgreSQL
	// release profiles omit only rows that did not exist in that source release.
	if queryer.Dialect() == database.SQLServer {
		expectedCatalog = Catalog
	}
	if len(actual) != len(expectedCatalog) {
		return fmt.Errorf("logical catalog has %d tables; expected %d", len(actual), len(expectedCatalog))
	}
	for _, table := range expectedCatalog {
		columns, ok := actual[table.Name]
		if !ok {
			return fmt.Errorf("logical catalog is missing table %s", table.Name)
		}
		expected := make([]actualColumn, 0, len(table.Columns)+len(sqlDerivedColumns[table.Name]))
		for _, column := range table.Columns {
			kind := column.Kind
			if queryer.Dialect() == database.SQLServer && (kind == TextArray || kind == UUIDArray || kind == JSON) {
				kind = Text
			}
			expected = append(expected, actualColumn{name: column.Name, kind: kind, nullable: column.Nullable})
		}
		if queryer.Dialect() == database.SQLServer {
			expected = append(expected, sqlDerivedColumns[table.Name]...)
		}
		if len(columns) != len(expected) {
			return fmt.Errorf("logical table %s has %d persisted columns; expected %d", table.Name, len(columns), len(expected))
		}
		sort.Slice(columns, func(i, j int) bool { return columns[i].name < columns[j].name })
		sort.Slice(expected, func(i, j int) bool { return expected[i].name < expected[j].name })
		for i := range expected {
			if columns[i] != expected[i] {
				return fmt.Errorf("logical table %s column %d is incompatible", table.Name, i+1)
			}
		}
	}
	if queryer.Dialect() == database.SQLServer && len(profile.Catalog) != len(Catalog) {
		included := make(map[string]struct{}, len(profile.Catalog))
		for _, table := range profile.Catalog {
			included[table.Name] = struct{}{}
		}
		for _, table := range Catalog {
			if _, ok := included[table.Name]; ok {
				continue
			}
			var count int
			if err := queryer.QueryRow(ctx, "SELECT COUNT(*) FROM dbo."+quoteIdentifier(database.SQLServer, table.Name)).Scan(&count); err != nil {
				return fmt.Errorf("check excluded logical table %s: %w", table.Name, err)
			}
			if count != 0 {
				return fmt.Errorf("excluded logical table %s is not empty", table.Name)
			}
		}
	}
	return nil
}

func readActualCatalog(ctx context.Context, queryer database.Queryer) (map[string][]actualColumn, error) {
	result := make(map[string][]actualColumn)
	if queryer.Dialect() == database.Postgres {
		rows, err := queryer.Query(ctx, `SELECT table_name,column_name,udt_name,(is_nullable='YES')
			FROM information_schema.columns
			WHERE table_schema='public' AND table_name<>'schema_migrations' AND is_generated='NEVER'
			ORDER BY table_name,ordinal_position`)
		if err != nil {
			return nil, fmt.Errorf("inspect PostgreSQL logical catalog: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var table, column, native string
			var nullable bool
			if err := rows.Scan(&table, &column, &native, &nullable); err != nil {
				return nil, fmt.Errorf("inspect PostgreSQL logical catalog: %w", err)
			}
			kind, ok := postgresKind(native)
			if !ok {
				return nil, fmt.Errorf("unsupported PostgreSQL logical type on %s.%s", table, column)
			}
			result[table] = append(result[table], actualColumn{name: column, kind: kind, nullable: nullable})
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("inspect PostgreSQL logical catalog: %w", err)
		}
		return result, nil
	}
	if queryer.Dialect() != database.SQLServer {
		return nil, fmt.Errorf("unsupported logical catalog dialect")
	}
	rows, err := queryer.Query(ctx, `SELECT t.name,c.name,ty.name,c.is_nullable
		FROM sys.tables t
		JOIN sys.schemas s ON s.schema_id=t.schema_id
		JOIN sys.columns c ON c.object_id=t.object_id
		JOIN sys.types ty ON ty.user_type_id=c.user_type_id
		WHERE s.name=N'dbo' AND t.name<>N'schema_migrations' AND c.is_computed=0
		ORDER BY t.name,c.column_id`)
	if err != nil {
		return nil, fmt.Errorf("inspect SQL Server logical catalog: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, column, native string
		var nullable bool
		if err := rows.Scan(&table, &column, &native, &nullable); err != nil {
			return nil, fmt.Errorf("inspect SQL Server logical catalog: %w", err)
		}
		kind, ok := sqlServerKind(native)
		if !ok {
			return nil, fmt.Errorf("unsupported SQL Server logical type on %s.%s", table, column)
		}
		result[table] = append(result[table], actualColumn{name: column, kind: kind, nullable: nullable})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("inspect SQL Server logical catalog: %w", err)
	}
	return result, nil
}

func postgresKind(native string) (Kind, bool) {
	switch native {
	case "uuid":
		return UUID, true
	case "text", "varchar", "bpchar":
		return Text, true
	case "_text":
		return TextArray, true
	case "_uuid":
		return UUIDArray, true
	case "json", "jsonb":
		return JSON, true
	case "int2", "int4", "int8":
		return Integer, true
	case "timestamptz", "timestamp":
		return Instant, true
	case "date":
		return Date, true
	case "numeric":
		return Decimal, true
	case "bool":
		return Boolean, true
	case "bytea":
		return Bytes, true
	default:
		return "", false
	}
}

func sqlServerKind(native string) (Kind, bool) {
	switch strings.ToLower(native) {
	case "uniqueidentifier":
		return UUID, true
	case "nvarchar", "varchar", "nchar", "char":
		return Text, true
	case "tinyint", "smallint", "int", "bigint":
		return Integer, true
	case "datetime2":
		return Instant, true
	case "date":
		return Date, true
	case "decimal", "numeric":
		return Decimal, true
	case "bit":
		return Boolean, true
	case "binary", "varbinary":
		return Bytes, true
	default:
		return "", false
	}
}

func catalogTableNames() []string {
	names := make([]string, 0, len(Catalog))
	for _, table := range Catalog {
		names = append(names, table.Name)
	}
	sort.Strings(names)
	return names
}

package app

import "testing"

func TestPostgresExporterOperatorConfig(t *testing.T) {
	base := map[string]string{
		"APP_ENV":          "staging",
		"DATABASE_BACKEND": "postgres",
		"DATA_GENERATION":  "123e4567-e89b-42d3-a456-426614174000",
		"DATABASE_URL":     "postgres://clubdue:admin-secret@clubdue-staging-db:5432/clubdue?sslmode=disable",
	}
	if _, err := loadPostgresExporterOperatorConfig(func(key string) string { return base[key] }); err != nil {
		t.Fatal(err)
	}
	tests := map[string]map[string]string{
		"production":        {"APP_ENV": "production"},
		"azure backend":     {"DATABASE_BACKEND": "azure_sql"},
		"generation":        {"DATA_GENERATION": "wrong"},
		"host":              {"DATABASE_URL": "postgres://clubdue:x@clubdue-production-db:5432/clubdue?sslmode=disable"},
		"database override": {"DATABASE_URL": "postgres://clubdue:x@clubdue-staging-db:5432/clubdue?sslmode=disable&database=other"},
		"host override":     {"DATABASE_URL": "postgres://clubdue:x@clubdue-staging-db:5432/clubdue?sslmode=disable&host=evil"},
		"port override":     {"DATABASE_URL": "postgres://clubdue:x@clubdue-staging-db:5432/clubdue?sslmode=disable&port=7777"},
		"options override":  {"DATABASE_URL": "postgres://clubdue:x@clubdue-staging-db:5432/clubdue?sslmode=disable&options=-csearch_path%3Devil"},
		"duplicate mode":    {"DATABASE_URL": "postgres://clubdue:x@clubdue-staging-db:5432/clubdue?sslmode=disable&sslmode=require"},
	}
	for name, changes := range tests {
		t.Run(name, func(t *testing.T) {
			values := make(map[string]string, len(base))
			for key, value := range base {
				values[key] = value
			}
			for key, value := range changes {
				values[key] = value
			}
			if _, err := loadPostgresExporterOperatorConfig(func(key string) string { return values[key] }); err == nil {
				t.Fatal("unsafe exporter boundary was accepted")
			}
		})
	}
}

func TestPostgresLogicalExportConfig(t *testing.T) {
	base := map[string]string{
		"APP_ENV":                           "staging",
		"DATABASE_BACKEND":                  "postgres",
		"DATA_GENERATION":                   "123e4567-e89b-42d3-a456-426614174000",
		"DATABASE_URL":                      "postgres://clubdue:admin-secret@clubdue-staging-db:5432/clubdue?sslmode=disable",
		"SIDEYARD_SOURCE_REVISION":          "0123456789abcdef0123456789abcdef01234567",
		"SIDEYARD_SOURCE_ARTIFACT":          "ghcr.io/wmichelin/clubdue@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"SIDEYARD_PHOTO_ACCOUNT":            "sideyardstg1234",
		"SIDEYARD_PHOTO_RESOURCE_ID":        "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/sideyard-staging/providers/Microsoft.Storage/storageAccounts/sideyardstg1234",
		"SIDEYARD_LOGICAL_EXPORT_DIRECTORY": logicalExportDirectory,
	}
	get := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	if _, err := loadPostgresExporterOperatorConfigForCommand("export-postgres-logical", get(base)); err != nil {
		t.Fatal(err)
	}
	for name, changes := range map[string]map[string]string{
		"revision":       {"SIDEYARD_SOURCE_REVISION": "main"},
		"mutable image":  {"SIDEYARD_SOURCE_ARTIFACT": "ghcr.io/wmichelin/clubdue:latest"},
		"account":        {"SIDEYARD_PHOTO_ACCOUNT": "other"},
		"resource group": {"SIDEYARD_PHOTO_RESOURCE_ID": "/subscriptions/11111111-1111-1111-1111-111111111111/resourceGroups/sideyard-production/providers/Microsoft.Storage/storageAccounts/sideyardstg1234"},
		"spool":          {"SIDEYARD_LOGICAL_EXPORT_DIRECTORY": "/tmp"},
		"failpoint":      {"SIDEYARD_EXPORTER_FAILPOINT": "after-activation"},
	} {
		t.Run(name, func(t *testing.T) {
			values := make(map[string]string, len(base))
			for key, value := range base {
				values[key] = value
			}
			for key, value := range changes {
				values[key] = value
			}
			if _, err := loadPostgresExporterOperatorConfigForCommand("export-postgres-logical", get(values)); err == nil {
				t.Fatal("unsafe logical export identity was accepted")
			}
		})
	}
}

func TestPostgresExporterRestrictionIgnoresAuthoritySelector(t *testing.T) {
	values := map[string]string{
		"APP_ENV":          "staging",
		"DATABASE_BACKEND": "azure_sql",
		"DATA_GENERATION":  "broken",
		"DATABASE_URL":     "postgres://clubdue:admin-secret@clubdue-staging-db:5432/clubdue?sslmode=disable",
	}
	if _, err := loadPostgresExporterRestrictionConfig(func(key string) string { return values[key] }); err != nil {
		t.Fatal(err)
	}
	values["APP_ENV"] = "production"
	if _, err := loadPostgresExporterRestrictionConfig(func(key string) string { return values[key] }); err == nil {
		t.Fatal("production exporter restriction was accepted")
	}
}

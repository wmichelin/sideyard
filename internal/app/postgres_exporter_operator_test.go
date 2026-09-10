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

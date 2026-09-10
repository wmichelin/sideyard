package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/wmichelin/clubdue/internal/database"
)

func azureOperatorTestEnv() map[string]string {
	return map[string]string{"DATABASE_BACKEND": "azure_sql", "DATA_GENERATION": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "SCHEDULED_WORK_OWNER": "host", "INTEREST_WAKE_FILE": "/var/run/sideyard/interest.wake", "AZURE_SQL_SERVER": "sideyard-staging-test.database.windows.net", "AZURE_SQL_DATABASE": "sideyard", "AZURE_SQL_TENANT_ID": "11111111-1111-1111-1111-111111111111", "AZURE_SQL_CLIENT_ID": "22222222-2222-2222-2222-222222222222", "AZURE_SQL_RUNTIME_CLIENT_ID": "22222222-2222-2222-2222-222222222222", "AZURE_SQL_CLIENT_SECRET": "private-test-secret"}
}
func TestAzureSQLConfigurationIsExplicitAndNeverDemo(t *testing.T) {
	env := azureOperatorTestEnv()
	env["APP_ENV"] = "staging"
	env["PUBLIC_URL"] = "https://sideyard.example.test"
	env["SESSION_SECRET"] = strings.Repeat("a", 40)
	env["ENABLE_AZURE_SQL_HTTP"] = "1"
	config, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if config.DemoMode() || config.DatabaseBackend != "azure_sql" || !config.EnableAzureSQLHTTP || config.AzureSQL.AuthMode != "client_secret" {
		t.Fatal("Azure configuration fell through to demo or lost its explicit runtime gate")
	}
	if config.RuntimeLimits.RequestTimeout != 80*time.Second || config.RuntimeLimits.ReadTimeout != 90*time.Second || config.RuntimeLimits.WriteTimeout != 90*time.Second || config.RuntimeLimits.ShutdownTimeout != 100*time.Second {
		t.Fatalf("Azure HTTP limits = %+v", config.RuntimeLimits)
	}
	demoConfig, err := LoadConfig(func(string) string { return "" })
	if err != nil || !demoConfig.DemoMode() || demoConfig.DatabaseBackend != "postgres" || demoConfig.RuntimeLimits.RequestTimeout != 0 || demoConfig.RuntimeLimits.ReadTimeout != 15*time.Second || demoConfig.RuntimeLimits.WriteTimeout != 20*time.Second || demoConfig.RuntimeLimits.ShutdownTimeout != 10*time.Second {
		t.Fatal("development default changed", err)
	}
}

func TestAzureSQLHTTPRuntimeIsStagingOnlyAndExplicit(t *testing.T) {
	valid := azureOperatorTestEnv()
	valid["APP_ENV"] = "staging"
	valid["PUBLIC_URL"] = "https://sideyard.example.test"
	valid["SESSION_SECRET"] = strings.Repeat("a", 40)
	valid["ENABLE_AZURE_SQL_HTTP"] = "1"
	for name, change := range map[string]func(map[string]string){
		"missing opt-in":      func(values map[string]string) { delete(values, "ENABLE_AZURE_SQL_HTTP") },
		"missing host owner":  func(values map[string]string) { delete(values, "SCHEDULED_WORK_OWNER") },
		"missing generation":  func(values map[string]string) { delete(values, "DATA_GENERATION") },
		"missing wake file":   func(values map[string]string) { delete(values, "INTEREST_WAKE_FILE") },
		"wake path traversal": func(values map[string]string) { values["INTEREST_WAKE_FILE"] = "/var/run/sideyard/../unsafe" },
		"production":          func(values map[string]string) { values["APP_ENV"] = "production" },
		"wrong target": func(values map[string]string) {
			values["AZURE_SQL_SERVER"] = "sideyard-production-test.database.windows.net"
		},
		"CLI credential": func(values map[string]string) {
			values["AZURE_SQL_AUTH_MODE"] = "azure_cli"
			delete(values, "AZURE_SQL_CLIENT_ID")
			delete(values, "AZURE_SQL_CLIENT_SECRET")
		},
		"startup owner": func(values map[string]string) { values["BOOTSTRAP_EMAIL"] = "owner@example.test" },
		"different runtime identity": func(values map[string]string) {
			values["AZURE_SQL_RUNTIME_CLIENT_ID"] = "33333333-3333-3333-3333-333333333333"
		},
		"request reaches read limit": func(values map[string]string) {
			values["HTTP_REQUEST_TIMEOUT"] = "70s"
		},
		"shutdown does not outlive writes": func(values map[string]string) {
			values["HTTP_SHUTDOWN_TIMEOUT"] = "105s"
		},
	} {
		t.Run(name, func(t *testing.T) {
			env := make(map[string]string, len(valid))
			for key, value := range valid {
				env[key] = value
			}
			change(env)
			if _, err := LoadConfig(func(key string) string { return env[key] }); err == nil {
				t.Fatal("unsafe Azure SQL HTTP configuration accepted")
			}
		})
	}
	postgres := map[string]string{"ENABLE_AZURE_SQL_HTTP": "1"}
	if _, err := LoadConfig(func(key string) string { return postgres[key] }); err == nil {
		t.Fatal("Azure runtime opt-in accepted with PostgreSQL")
	}

	postgresHost := map[string]string{
		"APP_ENV": "production", "DATABASE_BACKEND": "postgres", "DATABASE_URL": "postgres://private",
		"SCHEDULED_WORK_OWNER": "host", "INTEREST_WAKE_FILE": "/var/run/sideyard/interest.wake",
	}
	if _, err := LoadConfig(func(key string) string { return postgresHost[key] }); err == nil {
		t.Fatal("PostgreSQL host ownership was accepted without a host executor")
	}
}

func TestDatabaseOperatorConfigNeedsOnlyExplicitNativeSettings(t *testing.T) {
	env := azureOperatorTestEnv()
	env["APP_ENV"] = "staging"
	env["AZURE_SQL_AUTH_MODE"] = "azure_cli"
	delete(env, "AZURE_SQL_CLIENT_ID")
	delete(env, "AZURE_SQL_CLIENT_SECRET")
	config, err := loadDatabaseOperatorConfig(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if config.Environment != EnvironmentStaging || config.DemoMode() {
		t.Fatal("operator configuration lost its target")
	}
	for _, change := range []func(map[string]string){
		func(values map[string]string) { delete(values, "APP_ENV") },
		func(values map[string]string) { values["APP_ENV"] = "development" },
		func(values map[string]string) { values["APP_ENV"] = "production" },
		func(values map[string]string) { values["DATABASE_BACKEND"] = "postgres" },
		func(values map[string]string) { values["DATABASE_URL"] = "postgres://private-secret" },
	} {
		invalid := azureOperatorTestEnv()
		invalid["APP_ENV"] = "staging"
		invalid["AZURE_SQL_SERVER"] = "sideyard-staging-test.database.windows.net"
		invalid["AZURE_SQL_AUTH_MODE"] = "azure_cli"
		delete(invalid, "AZURE_SQL_CLIENT_ID")
		delete(invalid, "AZURE_SQL_CLIENT_SECRET")
		change(invalid)
		_, err := loadDatabaseOperatorConfig(func(key string) string { return invalid[key] })
		if err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatal("ambiguous operator target accepted or leaked a URL", err)
		}
	}
	for _, change := range []func(map[string]string){
		func(values map[string]string) {
			values["AZURE_SQL_SERVER"] = "sideyard-production-test.database.windows.net"
		},
		func(values map[string]string) { values["AZURE_SQL_DATABASE"] = "another-database" },
		func(values map[string]string) {
			values["AZURE_SQL_AUTH_MODE"] = "client_secret"
			values["AZURE_SQL_CLIENT_ID"] = "22222222-2222-2222-2222-222222222222"
			values["AZURE_SQL_CLIENT_SECRET"] = "private-test-secret"
		},
	} {
		invalid := azureOperatorTestEnv()
		invalid["APP_ENV"] = "staging"
		invalid["AZURE_SQL_AUTH_MODE"] = "azure_cli"
		delete(invalid, "AZURE_SQL_CLIENT_ID")
		delete(invalid, "AZURE_SQL_CLIENT_SECRET")
		change(invalid)
		if _, err := loadDatabaseOperatorConfig(func(key string) string { return invalid[key] }); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal("ambiguous operator target accepted or leaked a credential", err)
		}
	}
}

func TestNativeDatabaseCommandsDoNotLoadImplicitDotEnv(t *testing.T) {
	directory := t.TempDir()
	contents := "APP_ENV=staging\nDATABASE_BACKEND=azure_sql\nAZURE_SQL_SERVER=sideyard-staging-private.database.windows.net\nAZURE_SQL_DATABASE=sideyard\nAZURE_SQL_TENANT_ID=11111111-1111-1111-1111-111111111111\nAZURE_SQL_AUTH_MODE=azure_cli\n"
	if err := os.WriteFile(filepath.Join(directory, ".env"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(workingDirectory) })
	for _, key := range []string{"APP_ENV", "DATABASE_BACKEND", "DATABASE_URL", "AZURE_SQL_SERVER", "AZURE_SQL_DATABASE", "AZURE_SQL_TENANT_ID", "AZURE_SQL_CLIENT_ID", "AZURE_SQL_CLIENT_SECRET", "AZURE_SQL_AUTH_MODE"} {
		t.Setenv(key, "")
	}
	if err := Run([]string{"check-db"}); err == nil || !strings.Contains(err.Error(), "APP_ENV=staging") {
		t.Fatal("native command loaded an implicit .env target", err)
	}
}
func TestAzureSQLConfigRejectsAmbiguityAndRedactsValues(t *testing.T) {
	cases := []struct{ key, value string }{{"DATABASE_BACKEND", "mysql"}, {"AZURE_SQL_SERVER", "private-secret;encrypt=false"}, {"AZURE_SQL_DATABASE", "master"}, {"AZURE_SQL_TENANT_ID", "private-secret"}, {"AZURE_SQL_CLIENT_ID", "private-secret"}, {"AZURE_SQL_CLIENT_SECRET", ""}, {"AZURE_SQL_AUTH_MODE", "unknown"}, {"AZURE_SQL_AUTH_MODE", "azure_cli"}}
	for _, tc := range cases {
		t.Run(tc.key+tc.value, func(t *testing.T) {
			env := azureOperatorTestEnv()
			env[tc.key] = tc.value
			_, err := LoadConfig(func(k string) string { return env[k] })
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
			if strings.Contains(err.Error(), "private-secret") || strings.Contains(err.Error(), "private-test-secret") {
				t.Fatal("configuration error leaked value")
			}
		})
	}
	env := azureOperatorTestEnv()
	env["APP_ENV"] = "staging"
	env["PUBLIC_URL"] = "https://example.test"
	env["SESSION_SECRET"] = strings.Repeat("a", 40)
	env["ENABLE_AZURE_SQL_HTTP"] = "1"
	c, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil || c.DemoMode() {
		t.Fatal("native operator required a PostgreSQL URL", err)
	}
}

func TestAzureSQLConfigRejectsPostgresURLAlongsideNativeBackend(t *testing.T) {
	env := azureOperatorTestEnv()
	env["DATABASE_URL"] = "postgres://private-secret"
	_, err := LoadConfig(func(key string) string { return env[key] })
	if err == nil || strings.Contains(err.Error(), "private-secret") {
		t.Fatal("ambiguous backend was accepted or leaked its URL", err)
	}
}

type unusedSQLCredential struct{ calls int }

func (c *unusedSQLCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.calls++
	panic("lazy construction requested a token")
}
func TestAzureSQLFactoryDoesNotConnectOrRequestToken(t *testing.T) {
	credential := &unusedSQLCredential{}
	db, err := openAzureDatabase(AzureSQLConfig{Server: "sideyard-test.database.windows.net", Database: "sideyard"}, credential)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Pool.Dialect() != database.SQLServer || credential.calls != 0 {
		t.Fatal("factory connected eagerly")
	}
}

type countingSQLCredential struct {
	calls int
	now   time.Time
}

func (c *countingSQLCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.calls++
	return azcore.AccessToken{Token: "test-token", ExpiresOn: c.now.Add(time.Hour)}, nil
}

func TestAzureSQLCredentialCachesOnlyUsableTokens(t *testing.T) {
	now := time.Date(2034, 9, 10, 12, 0, 0, 0, time.UTC)
	inner := &countingSQLCredential{now: now}
	credential := &cachedAzureSQLCredential{inner: inner, now: func() time.Time { return now }}
	for range 2 {
		if _, err := credential.GetToken(context.Background(), policy.TokenRequestOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("usable token requests = %d, want 1", inner.calls)
	}
	now = now.Add(56 * time.Minute)
	if _, err := credential.GetToken(context.Background(), policy.TokenRequestOptions{}); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 2 {
		t.Fatalf("expiring token requests = %d, want 2", inner.calls)
	}
}
func TestNativeOperatorRejectsPostgresBeforeOpeningIt(t *testing.T) {
	for _, command := range []string{"migrate", "check-db", "bootstrap-runtime", "maintenance"} {
		if err := runDatabaseCommand(Config{DatabaseURL: "private-invalid-url"}, command); err == nil || strings.Contains(err.Error(), "private-invalid-url") {
			t.Fatal("native command opened PostgreSQL", command, err)
		}
	}
	if err := runDatabaseCommand(Config{DatabaseBackend: "azure_sql", AzureSQL: AzureSQLConfig{AuthMode: "client_secret"}}, "check-db"); err == nil {
		t.Fatal("native command accepted application credentials")
	}
}

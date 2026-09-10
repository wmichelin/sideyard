package app

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/wmichelin/clubdue/internal/sqlserver"
)

type AzureSQLConfig struct{ Server, Database, TenantID, ClientID, ClientSecret, AuthMode string }

func loadDatabaseOperatorConfig(getenv func(string) string) (Config, error) {
	environment := Environment(strings.ToLower(strings.TrimSpace(getenv("APP_ENV"))))
	if environment != EnvironmentStaging {
		return Config{}, errors.New("native database commands are enabled for APP_ENV=staging only")
	}
	if strings.ToLower(strings.TrimSpace(getenv("DATABASE_BACKEND"))) != "azure_sql" {
		return Config{}, errors.New("native database commands require DATABASE_BACKEND=azure_sql")
	}
	if strings.TrimSpace(getenv("DATABASE_URL")) != "" {
		return Config{}, errors.New("DATABASE_URL must be unset when DATABASE_BACKEND=azure_sql")
	}
	azureSQL, err := loadAzureSQLConfig(getenv)
	if err != nil {
		return Config{}, err
	}
	if azureSQL.AuthMode != "azure_cli" {
		return Config{}, errors.New("native database commands require AZURE_SQL_AUTH_MODE=azure_cli")
	}
	expectedServerPrefix := "sideyard-" + string(environment) + "-"
	if !strings.HasPrefix(strings.ToLower(azureSQL.Server), expectedServerPrefix) || azureSQL.Database != "sideyard" {
		return Config{}, errors.New("Azure SQL operator target does not match APP_ENV")
	}
	runtimeClientID := strings.TrimSpace(getenv("AZURE_SQL_RUNTIME_CLIENT_ID"))
	if runtimeClientID != "" {
		if err := validateRecordIDs(runtimeClientID); err != nil {
			return Config{}, errors.New("AZURE_SQL_RUNTIME_CLIENT_ID must be a client identifier")
		}
	}
	return Config{Environment: environment, DatabaseBackend: "azure_sql", AzureSQL: azureSQL, AzureSQLRuntimeClientID: runtimeClientID}, nil
}

func loadAzureSQLConfig(getenv func(string) string) (AzureSQLConfig, error) {
	c := AzureSQLConfig{Server: strings.TrimSpace(getenv("AZURE_SQL_SERVER")), Database: strings.TrimSpace(getenv("AZURE_SQL_DATABASE")), TenantID: strings.TrimSpace(getenv("AZURE_SQL_TENANT_ID")), ClientID: strings.TrimSpace(getenv("AZURE_SQL_CLIENT_ID")), ClientSecret: getenv("AZURE_SQL_CLIENT_SECRET"), AuthMode: strings.ToLower(strings.TrimSpace(getenv("AZURE_SQL_AUTH_MODE")))}
	if err := sqlserver.ValidateAzureEndpoint(c.Server, c.Database); err != nil {
		return AzureSQLConfig{}, err
	}
	if err := validateRecordIDs(c.TenantID); err != nil {
		return AzureSQLConfig{}, errors.New("AZURE_SQL_TENANT_ID must be a tenant identifier")
	}
	if c.AuthMode == "" {
		c.AuthMode = "client_secret"
	}
	switch c.AuthMode {
	case "client_secret":
		if err := validateRecordIDs(c.ClientID); err != nil || c.ClientSecret == "" {
			return AzureSQLConfig{}, errors.New("Azure SQL requires AZURE_SQL_CLIENT_ID and AZURE_SQL_CLIENT_SECRET")
		}
	case "azure_cli":
		if c.ClientID != "" || c.ClientSecret != "" {
			return AzureSQLConfig{}, errors.New("Azure CLI authentication must not also configure SQL client credentials")
		}
	default:
		return AzureSQLConfig{}, errors.New("AZURE_SQL_AUTH_MODE must be client_secret or azure_cli")
	}
	return c, nil
}

// Azure CLI authentication is for explicitly invoked operator commands. HTTP
// runtime selection remains gated until lifecycle and cutover checks pass.
func openConfiguredDB(ctx context.Context, c Config) (*DB, error) {
	if c.DatabaseBackend == "" || c.DatabaseBackend == "postgres" {
		return openDB(ctx, c.DatabaseURL)
	}
	if c.DatabaseBackend != "azure_sql" {
		return nil, errors.New("unsupported database backend")
	}
	credential, err := newAzureSQLCredential(c.AzureSQL)
	if err != nil {
		return nil, err
	}
	return openAzureDatabase(c.AzureSQL, credential, c.AzureSQLRuntimeClientID)
}
func newAzureSQLCredential(c AzureSQLConfig) (azcore.TokenCredential, error) {
	switch c.AuthMode {
	case "azure_cli":
		credential, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{TenantID: c.TenantID})
		if err != nil {
			return nil, errors.New("initialize Azure SQL CLI credential")
		}
		return &cachedAzureSQLCredential{inner: credential, now: time.Now}, nil
	case "client_secret":
		client := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		credential, err := azidentity.NewClientSecretCredential(c.TenantID, c.ClientID, c.ClientSecret, &azidentity.ClientSecretCredentialOptions{ClientOptions: azcore.ClientOptions{Transport: client}})
		if err != nil {
			return nil, errors.New("initialize Azure SQL client credential")
		}
		return &cachedAzureSQLCredential{inner: credential, now: time.Now}, nil
	default:
		return nil, errors.New("unsupported Azure SQL authentication mode")
	}
}

// SQL Server opens a fresh physical connection for each zero-idle pool use.
// Keep one scoped access token in memory so an operator maintenance run does
// not launch Azure CLI or contact Entra once per statement. No token is written
// to disk or logged, and refresh starts five minutes before expiry.
type cachedAzureSQLCredential struct {
	inner azcore.TokenCredential
	now   func() time.Time
	mu    sync.Mutex
	token azcore.AccessToken
}

func (c *cachedAzureSQLCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token.Token != "" && c.token.ExpiresOn.After(c.now().Add(5*time.Minute)) {
		return c.token, nil
	}
	token, err := c.inner.GetToken(ctx, options)
	if err != nil {
		return azcore.AccessToken{}, err
	}
	c.token = token
	return token, nil
}
func openAzureDatabase(c AzureSQLConfig, credential azcore.TokenCredential, runtimeClientID ...string) (*DB, error) {
	pool, err := sqlserver.NewAzure(c.Server, c.Database, credential, runtimeClientID...)
	if err != nil {
		return nil, err
	}
	return &DB{Pool: pool}, nil
}

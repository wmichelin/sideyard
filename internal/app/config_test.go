package app

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	strongSecret := strings.Repeat("s", 32)
	validStaging := map[string]string{
		"APP_ENV":        "staging",
		"DATABASE_URL":   "postgres://clubdue:clubdue@db:5432/clubdue?sslmode=disable",
		"SESSION_SECRET": strongSecret,
		"PUBLIC_URL":     "https://staging.example.test",
	}
	cases := []struct {
		name           string
		env            map[string]string
		wantErr        string
		wantDemo       bool
		wantAddress    string
		wantPublicDemo bool
	}{
		{
			name:        "development defaults to local demo",
			env:         map[string]string{},
			wantDemo:    true,
			wantAddress: ":8080",
		},
		{
			name:        "staging requires explicit safe configuration",
			env:         validStaging,
			wantDemo:    false,
			wantAddress: ":8080",
		},
		{
			name: "staging may enable isolated public demos",
			env: map[string]string{
				"APP_ENV": "staging", "DATABASE_URL": validStaging["DATABASE_URL"],
				"SESSION_SECRET": strongSecret, "PUBLIC_URL": validStaging["PUBLIC_URL"],
				"ENABLE_PUBLIC_DEMO": "1",
			},
			wantAddress: ":8080", wantPublicDemo: true,
		},
		{
			name:    "production rejects missing database",
			env:     map[string]string{"APP_ENV": "production", "SESSION_SECRET": strongSecret, "PUBLIC_URL": "https://clubdue.example.test"},
			wantErr: "DATABASE_URL is required",
		},
		{
			name:    "production rejects short secret without leaking it",
			env:     map[string]string{"APP_ENV": "production", "DATABASE_URL": validStaging["DATABASE_URL"], "SESSION_SECRET": "do-not-leak-this-secret", "PUBLIC_URL": "https://clubdue.example.test"},
			wantErr: "SESSION_SECRET must be at least 32 characters",
		},
		{
			name:    "production rejects non HTTPS public URL",
			env:     map[string]string{"APP_ENV": "production", "DATABASE_URL": validStaging["DATABASE_URL"], "SESSION_SECRET": strongSecret, "PUBLIC_URL": "http://clubdue.example.test"},
			wantErr: "PUBLIC_URL must be an HTTPS URL",
		},
		{
			name:    "production rejects development routes",
			env:     map[string]string{"APP_ENV": "production", "DATABASE_URL": validStaging["DATABASE_URL"], "SESSION_SECRET": strongSecret, "PUBLIC_URL": "https://clubdue.example.test", "ENABLE_DEV_ROUTES": "true"},
			wantErr: "ENABLE_DEV_ROUTES is allowed only in development",
		},
		{
			name:    "staging rejects development routes",
			env:     map[string]string{"APP_ENV": "staging", "DATABASE_URL": validStaging["DATABASE_URL"], "SESSION_SECRET": strongSecret, "PUBLIC_URL": validStaging["PUBLIC_URL"], "ENABLE_DEV_ROUTES": "true"},
			wantErr: "ENABLE_DEV_ROUTES is allowed only in development",
		},
		{
			name: "production rejects public demo access",
			env: map[string]string{
				"APP_ENV": "production", "DATABASE_URL": validStaging["DATABASE_URL"],
				"SESSION_SECRET": strongSecret, "PUBLIC_URL": "https://clubdue.example.test",
				"ENABLE_PUBLIC_DEMO": "true",
			},
			wantErr: "ENABLE_PUBLIC_DEMO is allowed only in staging",
		},
		{
			name:    "development rejects public demo access",
			env:     map[string]string{"ENABLE_PUBLIC_DEMO": "true"},
			wantErr: "ENABLE_PUBLIC_DEMO is allowed only in staging",
		},
		{
			name:    "runtime limits must be positive",
			env:     map[string]string{"MAX_REQUEST_BODY_BYTES": "0"},
			wantErr: "MAX_REQUEST_BODY_BYTES must be a positive integer",
		},
		{
			name:    "multipart memory must fit within the request limit",
			env:     map[string]string{"MAX_REQUEST_BODY_BYTES": "1024", "MAX_MULTIPART_MEMORY_BYTES": "1025"},
			wantErr: "MAX_MULTIPART_MEMORY_BYTES must not exceed MAX_REQUEST_BODY_BYTES",
		},
		{
			name:    "shutdown timeout must be positive",
			env:     map[string]string{"HTTP_SHUTDOWN_TIMEOUT": "0s"},
			wantErr: "HTTP_SHUTDOWN_TIMEOUT must be a positive duration",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := LoadConfig(mapLookup(tc.env))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("LoadConfig error = %v, want %q", err, tc.wantErr)
				}
				if secret := tc.env["SESSION_SECRET"]; secret != "" && strings.Contains(err.Error(), secret) {
					t.Fatal("configuration error included the session secret")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.DemoMode() != tc.wantDemo || config.Address != tc.wantAddress {
				t.Fatalf("config = %+v, want demo=%t address=%q", config, tc.wantDemo, tc.wantAddress)
			}
			if config.EnablePublicDemo != tc.wantPublicDemo {
				t.Fatalf("public demo = %t, want %t", config.EnablePublicDemo, tc.wantPublicDemo)
			}
		})
	}
}

func TestSignupPoliciesByEnvironment(t *testing.T) {
	development := Config{Environment: EnvironmentDevelopment}
	if !development.SignupOpen() || !development.SignupAllowed("anyone@example.test") {
		t.Fatal("development signup should remain open")
	}
	staging := Config{Environment: EnvironmentStaging}
	if staging.SignupOpen() || staging.SignupAllowed("anyone@example.test") {
		t.Fatal("staging signup should be invitation-only by default")
	}
	staging.SignupAllowlist = map[string]struct{}{"invited@example.test": {}}
	if !staging.SignupOpen() || !staging.SignupAllowed(" INVITED@example.test ") || staging.SignupAllowed("other@example.test") {
		t.Fatal("staging signup allowlist was not enforced")
	}
}

func TestProductionSignupAndTestPaymentPolicies(t *testing.T) {
	config, err := LoadConfig(mapLookup(map[string]string{
		"APP_ENV":          "production",
		"DATABASE_URL":     "postgres://clubdue:clubdue@db:5432/clubdue?sslmode=disable",
		"SESSION_SECRET":   strings.Repeat("s", 32),
		"PUBLIC_URL":       "https://clubdue.example.test",
		"SIGNUP_ALLOWLIST": "approved@example.test, SECOND@example.test ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !config.SignupAllowed("SECOND@example.test") || config.SignupAllowed("unapproved@example.test") {
		t.Fatal("production signup allowlist was not enforced")
	}
	if config.TestPaymentStubEnabled() {
		t.Fatal("production must disable the test payment stub")
	}

	request := httptest.NewRequest(http.MethodPost, "/renew/test-token", nil)
	response := httptest.NewRecorder()
	(&Server{config: config}).handleHomeownerRenewalPayment(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("production renewal payment status = %d, want %d", response.Code, http.StatusNotFound)
	}

	form := url.Values{"shop_name": {"Denied Shop"}, "email": {"unapproved@example.test"}, "password": {"valid-password"}}
	request = httptest.NewRequest(http.MethodPost, "/signup", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response = httptest.NewRecorder()
	server := &Server{config: config, tmpl: template.Must(template.New("signup.html").Parse("{{ .Error }}"))}
	server.handleSignupPost(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "not approved") {
		t.Fatalf("unapproved signup response = %d %q", response.Code, response.Body.String())
	}
}

func TestValidateProvisionOwner(t *testing.T) {
	config := Config{ProvisionOwner: OwnerCredentials{Email: " OWNER@example.test ", Password: "recovery-password", ShopName: " Demo Shop "}}
	credentials, err := config.ValidateProvisionOwner()
	if err != nil {
		t.Fatal(err)
	}
	if credentials.Email != "owner@example.test" || credentials.ShopName != "Demo Shop" {
		t.Fatalf("credentials = %+v", credentials)
	}
}

func TestLoadConfigSupportsLegacyBootstrapShopName(t *testing.T) {
	config, err := LoadConfig(mapLookup(map[string]string{
		"BOOTSTRAP_EMAIL":    "owner@example.test",
		"BOOTSTRAP_PASSWORD": "bootstrap-password",
		"SHOP_NAME":          "Legacy Demo Shop",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if config.BootstrapOwner.ShopName != "Legacy Demo Shop" {
		t.Fatalf("bootstrap shop name = %q", config.BootstrapOwner.ShopName)
	}
}

func mapLookup(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

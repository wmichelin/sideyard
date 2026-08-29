package main

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Environment string

const (
	EnvironmentDevelopment Environment = "development"
	EnvironmentStaging     Environment = "staging"
	EnvironmentProduction  Environment = "production"
)

const developmentSessionSecret = "dev-insecure-change-me"

type RuntimeLimits struct {
	MaxRequestBodyBytes int64
	MaxMultipartMemory  int64
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	AuthMaxAttempts     int
	AuthThrottleWindow  time.Duration
}

type OwnerCredentials struct {
	Email    string
	Password string
	ShopName string
}

type Config struct {
	Environment     Environment
	Address         string
	DatabaseURL     string
	SessionSecret   string
	PublicURL       *url.URL
	EnableDevRoutes bool
	SignupAllowlist map[string]struct{}
	BootstrapOwner  OwnerCredentials
	ProvisionOwner  OwnerCredentials
	RuntimeLimits   RuntimeLimits
}

func LoadConfig(getenv func(string) string) (Config, error) {
	config := Config{
		Environment: Environment(strings.ToLower(strings.TrimSpace(getenv("APP_ENV")))),
		RuntimeLimits: RuntimeLimits{
			MaxRequestBodyBytes: 8 << 20,
			MaxMultipartMemory:  1 << 20,
			ReadHeaderTimeout:   10 * time.Second,
			ReadTimeout:         15 * time.Second,
			WriteTimeout:        20 * time.Second,
			IdleTimeout:         60 * time.Second,
			AuthMaxAttempts:     5,
			AuthThrottleWindow:  5 * time.Minute,
		},
	}
	if config.Environment == "" {
		config.Environment = EnvironmentDevelopment
	}
	if config.Environment != EnvironmentDevelopment && config.Environment != EnvironmentStaging && config.Environment != EnvironmentProduction {
		return Config{}, fmt.Errorf("APP_ENV must be development, staging, or production")
	}

	port := strings.TrimSpace(getenv("PORT"))
	if port == "" {
		port = "8080"
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return Config{}, fmt.Errorf("PORT must be a valid TCP port")
	}
	config.Address = ":" + port
	config.DatabaseURL = strings.TrimSpace(getenv("DATABASE_URL"))
	config.SessionSecret = getenv("SESSION_SECRET")
	if config.Environment == EnvironmentDevelopment && config.SessionSecret == "" {
		config.SessionSecret = developmentSessionSecret
	}
	config.EnableDevRoutes = parseBool(getenv("ENABLE_DEV_ROUTES"))
	config.SignupAllowlist = parseEmailAllowlist(getenv("SIGNUP_ALLOWLIST"))
	config.BootstrapOwner = ownerCredentialsFromEnv(getenv, "BOOTSTRAP")
	if config.BootstrapOwner.ShopName == "" {
		config.BootstrapOwner.ShopName = getenv("SHOP_NAME")
	}
	config.ProvisionOwner = ownerCredentialsFromEnv(getenv, "OWNER")

	publicURL, err := parsePublicURL(getenv("PUBLIC_URL"), config.Environment, port)
	if err != nil {
		return Config{}, err
	}
	config.PublicURL = publicURL

	if config.RuntimeLimits.MaxRequestBodyBytes, err = parsePositiveInt64(getenv("MAX_REQUEST_BODY_BYTES"), config.RuntimeLimits.MaxRequestBodyBytes); err != nil {
		return Config{}, fmt.Errorf("MAX_REQUEST_BODY_BYTES must be a positive integer")
	}
	if config.RuntimeLimits.MaxMultipartMemory, err = parsePositiveInt64(getenv("MAX_MULTIPART_MEMORY_BYTES"), config.RuntimeLimits.MaxMultipartMemory); err != nil {
		return Config{}, fmt.Errorf("MAX_MULTIPART_MEMORY_BYTES must be a positive integer")
	}
	if config.RuntimeLimits.MaxMultipartMemory > config.RuntimeLimits.MaxRequestBodyBytes {
		return Config{}, fmt.Errorf("MAX_MULTIPART_MEMORY_BYTES must not exceed MAX_REQUEST_BODY_BYTES")
	}
	if config.RuntimeLimits.ReadHeaderTimeout, err = parsePositiveDuration(getenv("HTTP_READ_HEADER_TIMEOUT"), config.RuntimeLimits.ReadHeaderTimeout); err != nil {
		return Config{}, fmt.Errorf("HTTP_READ_HEADER_TIMEOUT must be a positive duration")
	}
	if config.RuntimeLimits.ReadTimeout, err = parsePositiveDuration(getenv("HTTP_READ_TIMEOUT"), config.RuntimeLimits.ReadTimeout); err != nil {
		return Config{}, fmt.Errorf("HTTP_READ_TIMEOUT must be a positive duration")
	}
	if config.RuntimeLimits.WriteTimeout, err = parsePositiveDuration(getenv("HTTP_WRITE_TIMEOUT"), config.RuntimeLimits.WriteTimeout); err != nil {
		return Config{}, fmt.Errorf("HTTP_WRITE_TIMEOUT must be a positive duration")
	}
	if config.RuntimeLimits.IdleTimeout, err = parsePositiveDuration(getenv("HTTP_IDLE_TIMEOUT"), config.RuntimeLimits.IdleTimeout); err != nil {
		return Config{}, fmt.Errorf("HTTP_IDLE_TIMEOUT must be a positive duration")
	}
	if config.RuntimeLimits.AuthMaxAttempts, err = parsePositiveInt(getenv("AUTH_MAX_ATTEMPTS"), config.RuntimeLimits.AuthMaxAttempts); err != nil {
		return Config{}, fmt.Errorf("AUTH_MAX_ATTEMPTS must be a positive integer")
	}
	if config.RuntimeLimits.AuthThrottleWindow, err = parsePositiveDuration(getenv("AUTH_THROTTLE_WINDOW"), config.RuntimeLimits.AuthThrottleWindow); err != nil {
		return Config{}, fmt.Errorf("AUTH_THROTTLE_WINDOW must be a positive duration")
	}

	if config.Environment != EnvironmentDevelopment {
		if config.DatabaseURL == "" {
			return Config{}, fmt.Errorf("DATABASE_URL is required outside development")
		}
		if !isStrongSessionSecret(config.SessionSecret) {
			return Config{}, fmt.Errorf("SESSION_SECRET must be at least 32 characters outside development")
		}
		if config.PublicURL.Scheme != "https" || config.PublicURL.Host == "" {
			return Config{}, fmt.Errorf("PUBLIC_URL must be an HTTPS URL outside development")
		}
	}
	if config.Environment == EnvironmentProduction && config.EnableDevRoutes {
		return Config{}, fmt.Errorf("ENABLE_DEV_ROUTES is not allowed in production")
	}
	return config, nil
}

func (c Config) DemoMode() bool {
	return c.DatabaseURL == ""
}

func (c Config) TestPaymentStubEnabled() bool {
	return c.Environment != EnvironmentProduction
}

func (c Config) SignupAllowed(email string) bool {
	if c.Environment != EnvironmentProduction {
		return true
	}
	_, allowed := c.SignupAllowlist[normalizeEmail(email)]
	return allowed
}

func (c Config) ValidateProvisionOwner() (OwnerCredentials, error) {
	credentials := c.ProvisionOwner
	credentials.Email = normalizeEmail(credentials.Email)
	credentials.ShopName = strings.TrimSpace(credentials.ShopName)
	if credentials.Email == "" || credentials.Password == "" || credentials.ShopName == "" {
		return OwnerCredentials{}, fmt.Errorf("OWNER_EMAIL, OWNER_PASSWORD, and OWNER_SHOP_NAME are required for provision-owner")
	}
	if len(credentials.Password) < 8 {
		return OwnerCredentials{}, fmt.Errorf("OWNER_PASSWORD must be at least 8 characters")
	}
	return credentials, nil
}

func parsePublicURL(raw string, environment Environment, port string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" && environment == EnvironmentDevelopment {
		raw = "http://localhost:" + port
	}
	if raw == "" {
		return nil, fmt.Errorf("PUBLIC_URL is required outside development")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("PUBLIC_URL must be an absolute URL")
	}
	return parsed, nil
}

func parsePositiveInt64(raw string, fallback int64) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return value, nil
}

func parsePositiveInt(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return value, nil
}

func parsePositiveDuration(raw string, fallback time.Duration) (time.Duration, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid positive duration")
	}
	return value, nil
}

func ownerCredentialsFromEnv(getenv func(string) string, prefix string) OwnerCredentials {
	return OwnerCredentials{
		Email:    getenv(prefix + "_EMAIL"),
		Password: getenv(prefix + "_PASSWORD"),
		ShopName: getenv(prefix + "_SHOP_NAME"),
	}
}

func parseBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

func parseEmailAllowlist(raw string) map[string]struct{} {
	allowlist := make(map[string]struct{})
	for _, email := range strings.Split(raw, ",") {
		if normalized := normalizeEmail(email); normalized != "" {
			allowlist[normalized] = struct{}{}
		}
	}
	return allowlist
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func isStrongSessionSecret(secret string) bool {
	return len(secret) >= 32 && secret != developmentSessionSecret
}

package app

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	pgstore "github.com/wmichelin/clubdue/internal/postgres"
	"github.com/wmichelin/clubdue/internal/transfer"
)

var exporterGenerationPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

type postgresExporterOperatorConfig struct {
	adminURL   string
	generation string
}

func runPostgresExporterCommand(command string, getenv func(string) string) error {
	config, err := loadPostgresExporterOperatorConfig(getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	switch command {
	case "bootstrap-postgres-exporter":
		if err := pgstore.PrepareLogicalExporter(ctx, config.adminURL, transfer.CurrentProfile); err != nil {
			return err
		}
		fmt.Println("Dormant PostgreSQL staging logical exporter bootstrapped and verified")
	case "check-postgres-exporter":
		if err := pgstore.CheckLogicalExporter(ctx, config.adminURL, transfer.CurrentProfile); err != nil {
			return err
		}
		fmt.Println("Dormant PostgreSQL staging logical exporter verified")
	default:
		return fmt.Errorf("unsupported PostgreSQL exporter command")
	}
	return nil
}

func loadPostgresExporterOperatorConfig(getenv func(string) string) (postgresExporterOperatorConfig, error) {
	if getenv("APP_ENV") != "staging" || getenv("DATABASE_BACKEND") != "postgres" {
		return postgresExporterOperatorConfig{}, fmt.Errorf("PostgreSQL exporter commands require the staging PostgreSQL source")
	}
	generation := strings.ToLower(strings.TrimSpace(getenv("DATA_GENERATION")))
	if !exporterGenerationPattern.MatchString(generation) {
		return postgresExporterOperatorConfig{}, fmt.Errorf("PostgreSQL exporter commands require the authoritative data generation")
	}
	adminURL := getenv("DATABASE_URL")
	parsed, err := url.Parse(adminURL)
	if err != nil || parsed.Scheme != "postgres" || parsed.Hostname() != "clubdue-staging-db" || parsed.Port() != "5432" || parsed.Path != "/clubdue" || parsed.User == nil || parsed.User.Username() != "clubdue" || parsed.Fragment != "" {
		return postgresExporterOperatorConfig{}, fmt.Errorf("PostgreSQL exporter administrator target is invalid")
	}
	query := parsed.Query()
	if len(query) != 1 || len(query["sslmode"]) != 1 || query.Get("sslmode") != "disable" {
		return postgresExporterOperatorConfig{}, fmt.Errorf("PostgreSQL exporter administrator target is invalid")
	}
	return postgresExporterOperatorConfig{adminURL: adminURL, generation: generation}, nil
}

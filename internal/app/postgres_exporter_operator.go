package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	pgstore "github.com/wmichelin/clubdue/internal/postgres"
	"github.com/wmichelin/clubdue/internal/transfer"
)

var exporterGenerationPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var exporterRevisionPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
var exporterArtifactPattern = regexp.MustCompile(`^ghcr\.io/wmichelin/clubdue@sha256:[0-9a-f]{64}$`)
var exporterStorageAccountPattern = regexp.MustCompile(`^sideyard[a-z0-9]{1,16}$`)

const logicalExportDirectory = "/var/lib/sideyard-logical-export"

type postgresExporterOperatorConfig struct {
	adminURL        string
	generation      string
	revision        string
	artifact        string
	photoAccount    string
	photoResource   string
	outputDirectory string
	failpoint       string
}

func runPostgresExporterCommand(command string, getenv func(string) string) error {
	if command == "reconcile-postgres-exporter" {
		adminURL, err := loadPostgresExporterRestrictionConfig(getenv)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := pgstore.RestrictLogicalExporter(ctx, adminURL); err != nil {
			return err
		}
		fmt.Println("PostgreSQL staging logical exporter restricted")
		return nil
	}
	config, err := loadPostgresExporterOperatorConfigForCommand(command, getenv)
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
	case "export-postgres-logical":
		return exportPostgresLogical(ctx, config)
	default:
		return fmt.Errorf("unsupported PostgreSQL exporter command")
	}
	return nil
}

func loadPostgresExporterRestrictionConfig(getenv func(string) string) (string, error) {
	if getenv("APP_ENV") != "staging" {
		return "", fmt.Errorf("PostgreSQL exporter restriction requires staging")
	}
	return logicalExporterAdminURL(getenv)
}

func logicalExporterAdminURL(getenv func(string) string) (string, error) {
	adminURL := getenv("DATABASE_URL")
	if adminURL == "" {
		secretPath := getenv("SIDEYARD_DATABASE_PASSWORD_FILE")
		if secretPath != "/run/secrets/staging-db.env" {
			return "", fmt.Errorf("PostgreSQL exporter database credential path is invalid")
		}
		info, err := os.Lstat(secretPath)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("PostgreSQL exporter database credential is unsafe")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(owner.Uid) != os.Geteuid() {
			return "", fmt.Errorf("PostgreSQL exporter database credential has the wrong owner")
		}
		content, err := os.ReadFile(secretPath)
		if err != nil {
			return "", fmt.Errorf("read PostgreSQL exporter database credential: %w", err)
		}
		line := strings.TrimSuffix(string(content), "\n")
		if strings.Count(line, "\n") != 0 || !strings.HasPrefix(line, "POSTGRES_PASSWORD=") {
			return "", fmt.Errorf("PostgreSQL exporter database credential is invalid")
		}
		password := strings.TrimPrefix(line, "POSTGRES_PASSWORD=")
		if !regexp.MustCompile(`^[a-f0-9]{48}$`).MatchString(password) {
			return "", fmt.Errorf("PostgreSQL exporter database credential is invalid")
		}
		parsed := &url.URL{Scheme: "postgres", Host: "clubdue-staging-db:5432", Path: "/clubdue", User: url.UserPassword("clubdue", password)}
		parsed.RawQuery = "sslmode=disable"
		adminURL = parsed.String()
	}
	parsed, err := url.Parse(adminURL)
	if err != nil || parsed.Scheme != "postgres" || parsed.Hostname() != "clubdue-staging-db" || parsed.Port() != "5432" || parsed.Path != "/clubdue" || parsed.User == nil || parsed.User.Username() != "clubdue" || parsed.Fragment != "" {
		return "", fmt.Errorf("PostgreSQL exporter administrator target is invalid")
	}
	query := parsed.Query()
	if len(query) != 1 || len(query["sslmode"]) != 1 || query.Get("sslmode") != "disable" {
		return "", fmt.Errorf("PostgreSQL exporter administrator target is invalid")
	}
	return adminURL, nil
}

func loadPostgresExporterOperatorConfig(getenv func(string) string) (postgresExporterOperatorConfig, error) {
	return loadPostgresExporterOperatorConfigForCommand("", getenv)
}

func loadPostgresExporterOperatorConfigForCommand(command string, getenv func(string) string) (postgresExporterOperatorConfig, error) {
	if getenv("APP_ENV") != "staging" || getenv("DATABASE_BACKEND") != "postgres" {
		return postgresExporterOperatorConfig{}, fmt.Errorf("PostgreSQL exporter commands require the staging PostgreSQL source")
	}
	generation := strings.ToLower(strings.TrimSpace(getenv("DATA_GENERATION")))
	if !exporterGenerationPattern.MatchString(generation) {
		return postgresExporterOperatorConfig{}, fmt.Errorf("PostgreSQL exporter commands require the authoritative data generation")
	}
	adminURL, err := logicalExporterAdminURL(getenv)
	if err != nil {
		return postgresExporterOperatorConfig{}, err
	}
	config := postgresExporterOperatorConfig{adminURL: adminURL, generation: generation}
	if command == "export-postgres-logical" {
		config.revision = strings.ToLower(strings.TrimSpace(getenv("SIDEYARD_SOURCE_REVISION")))
		config.artifact = strings.ToLower(strings.TrimSpace(getenv("SIDEYARD_SOURCE_ARTIFACT")))
		config.photoAccount = strings.ToLower(strings.TrimSpace(getenv("SIDEYARD_PHOTO_ACCOUNT")))
		config.photoResource = strings.TrimSpace(getenv("SIDEYARD_PHOTO_RESOURCE_ID"))
		config.outputDirectory = getenv("SIDEYARD_LOGICAL_EXPORT_DIRECTORY")
		config.failpoint = getenv("SIDEYARD_EXPORTER_FAILPOINT")
		if config.outputDirectory == "" {
			config.outputDirectory = logicalExportDirectory
		}
		expectedResource := regexp.MustCompile(`^/subscriptions/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/resourceGroups/sideyard-staging/providers/Microsoft\.Storage/storageAccounts/` + regexp.QuoteMeta(config.photoAccount) + `$`)
		if !exporterRevisionPattern.MatchString(config.revision) || !exporterArtifactPattern.MatchString(config.artifact) ||
			!exporterStorageAccountPattern.MatchString(config.photoAccount) || !expectedResource.MatchString(config.photoResource) ||
			config.outputDirectory != logicalExportDirectory ||
			(config.failpoint != "" && (getenv("SIDEYARD_EXPORTER_REHEARSAL") != "1" ||
				(config.failpoint != "after-activation-wait" && config.failpoint != "after-activation-before-journal"))) {
			return postgresExporterOperatorConfig{}, fmt.Errorf("PostgreSQL logical export identity is invalid")
		}
	}
	return config, nil
}

type logicalExportResult struct {
	Path          string `json:"path"`
	ExportID      string `json:"export_id"`
	PackageSHA256 string `json:"package_sha256"`
	PackageBytes  int64  `json:"package_bytes"`
	RootSHA256    string `json:"root_sha256"`
}

func exportPostgresLogical(ctx context.Context, config postgresExporterOperatorConfig) (resultErr error) {
	if err := validatePrivateExportDirectory(config.outputDirectory); err != nil {
		return err
	}
	// Every attempt first repairs a process-killed prior activation.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := pgstore.DeactivateLogicalExporter(cleanupCtx, config.adminURL, transfer.CurrentProfile); err != nil {
		cancel()
		return err
	}
	cancel()
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("generate logical exporter credential: %w", err)
	}
	password := hex.EncodeToString(secret)
	for index := range secret {
		secret[index] = 0
	}
	_, err := pgstore.ActivateLogicalExporter(ctx, config.adminURL, transfer.CurrentProfile, password,
		func(deadline time.Time) error {
			return recordExporterPhase(config, "pre_activation", "activating", deadline)
		},
		func(deadline time.Time) error {
			if config.failpoint == "after-activation-before-journal" {
				for {
					time.Sleep(time.Hour)
				}
			}
			return recordExporterPhase(config, "activating", "activated", deadline)
		},
	)
	if err != nil {
		return err
	}
	active := true
	deactivate := func() error {
		if !active {
			return nil
		}
		deactivateCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		err := pgstore.DeactivateLogicalExporter(deactivateCtx, config.adminURL, transfer.CurrentProfile)
		if err == nil {
			active = false
		}
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, deactivate()) }()
	if config.failpoint == "after-activation-wait" {
		for {
			time.Sleep(time.Hour)
		}
	}

	parsed, _ := url.Parse(config.adminURL)
	parsed.User = url.UserPassword(pgstore.StagingExporterLogin, password)
	if err := pgstore.CheckLogicalExporterSession(ctx, parsed.String(), transfer.CurrentProfile); err != nil {
		password = ""
		return err
	}
	source, err := pgstore.OpenReadOnly(ctx, parsed.String())
	password = ""
	if err != nil {
		return err
	}
	defer source.Close()

	archive, err := os.CreateTemp(config.outputDirectory, ".database-*.ndjson")
	if err != nil {
		return fmt.Errorf("create logical archive: %w", err)
	}
	archivePath := archive.Name()
	defer os.Remove(archivePath)
	if err := archive.Chmod(0o600); err != nil {
		archive.Close()
		return fmt.Errorf("protect logical archive: %w", err)
	}
	footer, err := transfer.Export(ctx, pgstore.Wrap(source.Pool), archive, transfer.ExportOptions{
		Environment: "staging", Generation: config.generation,
		SourceIdentity: "postgres:sideyard_staging_logical_exporter@clubdue-staging-db:5432/clubdue#revision=" + config.revision,
		SourceArtifact: config.artifact, LogicalSchema: transfer.LogicalSchema,
	})
	if err != nil {
		archive.Close()
		return err
	}
	if err := archive.Sync(); err != nil {
		archive.Close()
		return fmt.Errorf("sync logical archive: %w", err)
	}
	source.Close()
	if err := deactivate(); err != nil {
		archive.Close()
		return err
	}
	if _, err := archive.Seek(0, 0); err != nil {
		archive.Close()
		return fmt.Errorf("rewind logical archive: %w", err)
	}
	packaged, err := os.CreateTemp(config.outputDirectory, ".recovery-*.tar")
	if err != nil {
		archive.Close()
		return fmt.Errorf("create logical package: %w", err)
	}
	packagePath := packaged.Name()
	defer os.Remove(packagePath)
	if err := packaged.Chmod(0o600); err != nil {
		packaged.Close()
		archive.Close()
		return fmt.Errorf("protect logical package: %w", err)
	}
	photoSource := transfer.AzurePhotoSource(config.photoAccount, config.photoResource)
	manifest, err := transfer.BuildLogicalPackage(archive, packaged, transfer.LogicalPackageOptions{ReleaseArtifact: config.artifact, PhotoSource: photoSource})
	archive.Close()
	if err != nil {
		packaged.Close()
		return err
	}
	if manifest.RootSHA256 != footer.RootSHA256 {
		packaged.Close()
		return fmt.Errorf("logical package root does not match its export")
	}
	if err := packaged.Sync(); err != nil {
		packaged.Close()
		return fmt.Errorf("sync logical package: %w", err)
	}
	if _, err := packaged.Seek(0, 0); err != nil {
		packaged.Close()
		return fmt.Errorf("rewind logical package: %w", err)
	}
	opened, err := transfer.OpenLogicalPackage(packaged, transfer.LogicalPackageOptions{ReleaseArtifact: config.artifact, PhotoSource: photoSource})
	if err != nil {
		packaged.Close()
		return fmt.Errorf("verify logical package: %w", err)
	}
	opened.Close()
	if _, err := packaged.Seek(0, 0); err != nil {
		packaged.Close()
		return fmt.Errorf("rewind verified logical package: %w", err)
	}
	packageHash := sha256.New()
	if _, err := io.Copy(packageHash, packaged); err != nil {
		packaged.Close()
		return fmt.Errorf("digest logical package: %w", err)
	}
	stat, err := packaged.Stat()
	if err != nil {
		packaged.Close()
		return fmt.Errorf("inspect logical package: %w", err)
	}
	if err := packaged.Close(); err != nil {
		return fmt.Errorf("close logical package: %w", err)
	}
	finalPath := filepath.Join(config.outputDirectory, "recovery-"+manifest.ExportID+".tar")
	if err := os.Link(packagePath, finalPath); err != nil {
		return fmt.Errorf("finalize logical package: %w", err)
	}
	if err := syncDirectory(config.outputDirectory); err != nil {
		os.Remove(finalPath)
		return err
	}
	result := logicalExportResult{Path: finalPath, ExportID: manifest.ExportID, PackageSHA256: hex.EncodeToString(packageHash.Sum(nil)), PackageBytes: stat.Size(), RootSHA256: manifest.RootSHA256}
	return json.NewEncoder(os.Stdout).Encode(result)
}

type exporterJournal struct {
	Format         int    `json:"format"`
	Phase          string `json:"phase"`
	Environment    string `json:"environment"`
	Generation     string `json:"generation"`
	Revision       string `json:"revision"`
	Image          string `json:"image"`
	Attempt        string `json:"attempt"`
	ServerDeadline string `json:"server_deadline,omitempty"`
}

func recordExporterPhase(config postgresExporterOperatorConfig, expectedPhase, nextPhase string, validUntil time.Time) error {
	target := filepath.Join(config.outputDirectory, "operator-journal.json")
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("logical export journal is unsafe or missing")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return fmt.Errorf("logical export journal has the wrong owner")
	}
	content, err := os.ReadFile(target)
	if err != nil {
		return fmt.Errorf("read logical export journal: %w", err)
	}
	var journal exporterJournal
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		journal.Format != 1 || journal.Phase != expectedPhase || journal.Environment != "staging" ||
		journal.Generation != config.generation || journal.Revision != config.revision || journal.Image != config.artifact ||
		!exporterGenerationPattern.MatchString(journal.Attempt) ||
		(expectedPhase == "pre_activation" && journal.ServerDeadline != "") ||
		(expectedPhase == "activating" && journal.ServerDeadline != validUntil.UTC().Format(time.RFC3339Nano)) {
		return fmt.Errorf("logical export journal does not match the activation")
	}
	journal.Phase = nextPhase
	journal.ServerDeadline = validUntil.UTC().Format(time.RFC3339Nano)
	temporary := target + ".new"
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create logical export journal update: %w", err)
	}
	writeErr := json.NewEncoder(file).Encode(journal)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(temporary)
		return fmt.Errorf("write logical export journal: %w", errors.Join(writeErr, closeErr))
	}
	if err := os.Rename(temporary, target); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("promote logical export journal: %w", err)
	}
	return syncDirectory(config.outputDirectory)
}

func validatePrivateExportDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("logical export directory must be a protected directory")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(owner.Uid) != os.Geteuid() {
		return fmt.Errorf("logical export directory must be owned by the operator")
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open logical export directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync logical export directory: %w", err)
	}
	return nil
}

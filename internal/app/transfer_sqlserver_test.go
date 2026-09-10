package app

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
	pgstore "github.com/wmichelin/clubdue/internal/postgres"
	"github.com/wmichelin/clubdue/internal/testdb"
	"github.com/wmichelin/clubdue/internal/transfer"
)

const transferTestGeneration = "123e4567-e89b-42d3-a456-426614174000"

func TestSQLServerLogicalRestoreRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	source := sqlServerTestDB(t)
	if err := testdb.SeedLogicalTransfer(ctx, source.Pool, true); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Pool.Exec(ctx, `
		INSERT INTO dbo.shops(id,name,slug) VALUES
		(CONVERT(uniqueidentifier,'10000000-0000-4000-8000-000000000001'),N'BMP',N'transfer-bmp'),
		(CONVERT(uniqueidentifier,'10000000-0000-4000-8000-000000000002'),N'Supplementary',N'transfer-supplementary');
		INSERT INTO dbo.demo_import_previews(token_hash,shop_id,csv_data,row_count,projected_occurrences,projection_date,expires_at)
		VALUES(@p1,CONVERT(uniqueidentifier,'10000000-0000-4000-8000-000000000001'),0x01,1,0,'2035-09-02','2035-09-03'),
		(@p2,CONVERT(uniqueidentifier,'10000000-0000-4000-8000-000000000002'),0x02,1,0,'2035-09-02','2035-09-03')`, "\uE000", "\U00010000"); err != nil {
		t.Fatal(err)
	}
	assertLogicalRoundTrip(t, ctx, source, "azure_sql", "staging", transfer.LogicalSchema, sqlServerTestDB(t))
}

func TestSQLServerLogicalRestoreFromPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	source, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	if err := testdb.SeedLogicalTransfer(ctx, source.Pool, true); err != nil {
		t.Fatal(err)
	}
	assertLogicalExportRejectsAlteredPostgresLedger(t, ctx, source)
	assertLogicalRoundTrip(t, ctx, source, "postgres", "staging", transfer.LogicalSchema, sqlServerTestDB(t))
}

func assertLogicalExportRejectsAlteredPostgresLedger(t *testing.T, ctx context.Context, source *DB) {
	t.Helper()
	var checksum string
	if err := source.Pool.QueryRow(ctx, `SELECT checksum FROM schema_migrations WHERE version=31`).Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Pool.Exec(ctx, `UPDATE schema_migrations SET checksum=$1 WHERE version=31`, strings.Repeat("0", 64)); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	_, exportErr := transfer.Export(ctx, source.Pool, &archive, transfer.ExportOptions{
		Environment: "staging", Generation: transferTestGeneration,
		SourceIdentity: "tampered-ledger-fixture", SourceArtifact: "test-binary",
	})
	if _, err := source.Pool.Exec(ctx, `UPDATE schema_migrations SET checksum=$1 WHERE version=31`, checksum); err != nil {
		t.Fatalf("restore PostgreSQL fixture migration checksum: %v", err)
	}
	if exportErr == nil || !strings.Contains(exportErr.Error(), "incompatible") {
		t.Fatalf("logical export error = %v, want embedded migration checksum rejection", exportErr)
	}
}

func TestSQLServerLogicalRestoreFromProductionV29(t *testing.T) {
	databaseURL := os.Getenv("POSTGRES_V29_TEST_URL")
	if databaseURL == "" {
		t.Skip("POSTGRES_V29_TEST_URL is required by the release-profile rehearsal")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	native, err := pgstore.OpenReadOnlyThrough(ctx, databaseURL, 29)
	if err != nil {
		t.Fatal(err)
	}
	source := &DB{Pool: pgstore.Wrap(native.Pool)}
	t.Cleanup(source.Close)
	store := &Store{db: source, clock: fixedClock{now: time.Date(2035, 9, 2, 12, 0, 0, 0, time.UTC)}}
	if _, err := store.SeedPublicDemo(ctx); err == nil {
		t.Fatal("read-only production fixture unexpectedly accepted seed writes")
	}
	assertLogicalRoundTrip(t, ctx, source, "postgres", "production", transfer.ProductionV29LogicalSchema, sqlServerTestDB(t))
}

func assertLogicalRoundTrip(t *testing.T, ctx context.Context, source *DB, sourceBackend, environment, logicalSchema string, target *DB) {
	t.Helper()
	sourceCounts := logicalProfileTableCounts(t, ctx, source, logicalSchema)
	assertLogicalProfileTablesNonEmpty(t, sourceCounts)
	var archive bytes.Buffer
	footer, err := transfer.Export(ctx, source.Pool, &archive, transfer.ExportOptions{
		Environment: environment, Generation: transferTestGeneration, LogicalSchema: logicalSchema,
		SourceIdentity: "disposable-" + sourceBackend, SourceArtifact: "test-binary",
		Now: func() time.Time { return time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := transfer.Inspect(bytes.NewReader(archive.Bytes()), 0)
	if err != nil {
		t.Fatal(err)
	}
	photoSource := transfer.AzurePhotoSource("sideyardfixture", "/subscriptions/11111111-1111-4111-8111-111111111111/resourceGroups/sideyard-staging/providers/Microsoft.Storage/storageAccounts/sideyardfixture")
	if logicalSchema == transfer.ProductionV29LogicalSchema {
		photoSource = transfer.InlinePhotoSource()
	}
	packageOptions := transfer.LogicalPackageOptions{ReleaseArtifact: "sha256:test-release", PhotoSource: photoSource}
	var logicalPackage bytes.Buffer
	manifest, err := transfer.BuildLogicalPackage(bytes.NewReader(archive.Bytes()), &logicalPackage, packageOptions)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := transfer.OpenLogicalPackage(bytes.NewReader(logicalPackage.Bytes()), packageOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	if manifest.ArchiveSHA256 != inspection.ArtifactSHA256 || manifest.RootSHA256 != footer.RootSHA256 {
		t.Fatal("logical package did not bind the exact archive and root")
	}
	if logicalSchema == transfer.ProductionV29LogicalSchema {
		if len(manifest.Assets) != 0 {
			t.Fatal("production v29 package unexpectedly requires Blob assets")
		}
	} else {
		assertLogicalPackageAssets(t, manifest.Assets)
	}
	badDigest := strings.Repeat("0", 64)
	if _, err := transfer.RestoreSQLServer(ctx, target.Pool, bytes.NewReader(archive.Bytes()), transfer.RestoreOptions{
		Environment: environment, Generation: transferTestGeneration,
		DestinationIdentity: "disposable-restore", ExpectedArtifactSHA256: badDigest, BatchSize: 2,
	}); err == nil {
		t.Fatal("logical restore accepted an unauthenticated artifact digest")
	}
	var checkpoints int
	if err := target.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM sideyard_transfer.restore_state`).Scan(&checkpoints); err != nil || checkpoints != 0 {
		t.Fatal("rejected artifact changed restore checkpoint", checkpoints, err)
	}
	failing := &restoreFailurePool{Pool: target.Pool, failCustomerInsert: 4, unknownCommit: true}
	if _, err := transfer.RestoreSQLServer(ctx, failing, bytes.NewReader(archive.Bytes()), transfer.RestoreOptions{
		Environment: environment, Generation: transferTestGeneration,
		DestinationIdentity: "disposable-restore", ExpectedArtifactSHA256: inspection.ArtifactSHA256, BatchSize: 2,
	}); err == nil {
		t.Fatal("injected mid-table restore failure was not reported")
	}
	result, err := transfer.RestoreSQLServer(ctx, target.Pool, bytes.NewReader(archive.Bytes()), transfer.RestoreOptions{
		Environment: environment, Generation: transferTestGeneration,
		DestinationIdentity: "disposable-restore", ExpectedArtifactSHA256: inspection.ArtifactSHA256, BatchSize: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Verified || result.Inspection.Footer.RootSHA256 != footer.RootSHA256 {
		t.Fatal("logical restore did not verify the source root")
	}
	targetCounts := logicalProfileTableCounts(t, ctx, target, logicalSchema)
	for table, sourceCount := range sourceCounts {
		if targetCounts[table] != sourceCount {
			t.Fatalf("logical table %s restored %d rows, want %d", table, targetCounts[table], sourceCount)
		}
	}
	if !result.Resumed || !failing.returnedUnknownCommit {
		t.Fatal("logical restore did not resume a partial table or reconcile an unknown commit")
	}
	resumed, err := transfer.RestoreSQLServer(ctx, target.Pool, bytes.NewReader(archive.Bytes()), transfer.RestoreOptions{
		Environment: environment, Generation: transferTestGeneration,
		DestinationIdentity: "disposable-restore", ExpectedArtifactSHA256: inspection.ArtifactSHA256, BatchSize: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resumed.Resumed || !resumed.Verified {
		t.Fatal("completed logical restore was not safely resumable")
	}
}

func assertLogicalPackageAssets(t *testing.T, assets []transfer.PhotoAsset) {
	t.Helper()
	if len(assets) != 6 {
		t.Fatalf("logical package has %d assets, want 6", len(assets))
	}
	type expectedAsset struct {
		photo, hash, mime, version string
		size                       int64
		required, retired, purged  bool
	}
	expected := map[byte]expectedAsset{
		'a': {"f1000000-0000-4000-8000-000000000033", "039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81", "image/png", "", 3, false, false, false},
		'b': {"f1000000-0000-4000-8000-000000000034", "d798d1fac6bd4bb1c11f50312760351013379a0ab6f0a8c0af8a506b96b2525a", "image/jpeg", "version-retired", 2, true, true, false},
		'c': {"f1000000-0000-4000-8000-000000000035", "5a8814ae66ff07179d2c22381da6221f6fe754e6175c47d7d87846080f0a9715", "image/png", "version-purged", 2, false, true, true},
		'd': {"f1000000-0000-4000-8000-000000000016", "5240672d7b51756b829ad0ef8d9468b7a078afa2f410484fd3892dab47becb72", "image/png", "version-1", 3, true, false, false},
		'e': {"f1000000-0000-4000-8000-000000000036", "787c798e39a5bc1910355bae6d0cd87a36b2e10fd0202a83e3bb6b005da83472", "image/png", "", 3, false, false, false},
		'f': {"f1000000-0000-4000-8000-000000000037", "5a8814ae66ff07179d2c22381da6221f6fe754e6175c47d7d87846080f0a9715", "image/png", "version-linked-purged", 2, false, true, true},
	}
	for _, asset := range assets {
		want, ok := expected[asset.ObjectKey[0]]
		version := ""
		if asset.VersionID != nil {
			version = *asset.VersionID
		}
		if !ok || asset.ObjectKey != strings.Repeat(string(asset.ObjectKey[0]), 64) || asset.ShopID != "f1000000-0000-4000-8000-000000000001" ||
			asset.PhotoID != want.photo || asset.ContentSHA256 != want.hash || asset.ContentType != want.mime || version != want.version ||
			asset.ByteLength != want.size || asset.Required != want.required || (asset.RetiredAt != nil) != want.retired || (asset.PurgedAt != nil) != want.purged {
			t.Fatalf("logical package asset = %+v, want %+v", asset, want)
		}
	}
}

func logicalProfileTableCounts(t *testing.T, ctx context.Context, source *DB, logicalSchema string) map[string]int {
	t.Helper()
	catalog := transfer.CurrentProfile.Catalog
	if logicalSchema == transfer.ProductionV29LogicalSchema {
		catalog = transfer.ProductionV29Profile.Catalog
	}
	counts := make(map[string]int, len(catalog))
	for _, table := range catalog {
		name := `"` + table.Name + `"`
		if source.Pool.Dialect() == database.SQLServer {
			name = `dbo.[` + table.Name + `]`
		}
		var count int
		if err := source.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM `+name).Scan(&count); err != nil {
			t.Fatalf("count logical fixture table %s: %v", table.Name, err)
		}
		counts[table.Name] = count
	}
	return counts
}

func assertLogicalProfileTablesNonEmpty(t *testing.T, counts map[string]int) {
	t.Helper()
	var missing []string
	for table, count := range counts {
		if count == 0 {
			missing = append(missing, table)
		}
	}
	if len(missing) != 0 {
		slices.Sort(missing)
		t.Fatalf("logical fixture has empty profile tables: %s", strings.Join(missing, ", "))
	}
}

type restoreFailurePool struct {
	database.Pool
	failCustomerInsert    int
	unknownCommit         bool
	returnedUnknownCommit bool
}

func (pool *restoreFailurePool) CheckSchema(ctx context.Context) error {
	return pool.Pool.(interface{ CheckSchema(context.Context) error }).CheckSchema(ctx)
}

func (pool *restoreFailurePool) CheckDataIntegrity(ctx context.Context) error {
	return pool.Pool.(interface{ CheckDataIntegrity(context.Context) error }).CheckDataIntegrity(ctx)
}

func (pool *restoreFailurePool) Begin(ctx context.Context) (database.Tx, error) {
	return pool.BeginTx(ctx, nil)
}

func (pool *restoreFailurePool) BeginTx(ctx context.Context, options *sql.TxOptions) (database.Tx, error) {
	tx, err := pool.Pool.BeginTx(ctx, options)
	if err != nil {
		return nil, err
	}
	return &restoreFailureTx{Tx: tx, pool: pool}, nil
}

type restoreFailureTx struct {
	database.Tx
	pool       *restoreFailurePool
	checkpoint bool
}

func (tx *restoreFailureTx) Exec(ctx context.Context, query string, arguments ...any) (database.Result, error) {
	if strings.HasPrefix(query, "INSERT INTO dbo.[customers]") {
		tx.pool.failCustomerInsert--
		if tx.pool.failCustomerInsert == 0 {
			return nil, errors.New("synthetic mid-table restore failure")
		}
	}
	if strings.Contains(query, "UPDATE sideyard_transfer.restore_state") {
		tx.checkpoint = true
	}
	return tx.Tx.Exec(ctx, query, arguments...)
}

func (tx *restoreFailureTx) Commit(ctx context.Context) error {
	err := tx.Tx.Commit(ctx)
	if err == nil && tx.checkpoint && tx.pool.unknownCommit {
		tx.pool.unknownCommit = false
		tx.pool.returnedUnknownCommit = true
		return errors.New("synthetic lost commit acknowledgement")
	}
	return err
}

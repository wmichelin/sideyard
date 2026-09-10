package transfer

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"time"
)

const (
	LogicalPackageFormat    = 1
	DefaultMaxManifestBytes = 16 * 1024 * 1024
	// Leave encryption, authentication and transport framing headroom beneath
	// the existing 256 MiB Blob object boundary.
	DefaultMaxPackageBytes = 250 * 1024 * 1024
)

var storageAccountValue = regexp.MustCompile(`^sideyard[a-z0-9]{1,16}$`)
var storageResourceValue = regexp.MustCompile(`^/subscriptions/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/resourceGroups/sideyard-(staging|production)/providers/Microsoft\.Storage/storageAccounts/(sideyard[a-z0-9]{1,16})$`)

type PhotoSource struct {
	Kind       string `json:"kind"`
	Account    string `json:"account"`
	Container  string `json:"container"`
	ResourceID string `json:"resource_id"`
}

func InlinePhotoSource() PhotoSource { return PhotoSource{Kind: "inline"} }

func AzurePhotoSource(account, resourceID string) PhotoSource {
	return PhotoSource{Kind: "azure_blob", Account: account, Container: "photos", ResourceID: resourceID}
}

type LogicalPackageOptions struct {
	ReleaseArtifact string
	PhotoSource     PhotoSource
	MaxArchiveBytes int64
	MaxPackageBytes int64
}

type PhotoAsset struct {
	ObjectKey     string  `json:"object_key"`
	ShopID        string  `json:"shop_id"`
	PhotoID       string  `json:"photo_id"`
	ContentSHA256 string  `json:"content_sha256"`
	ContentType   string  `json:"content_type"`
	ByteLength    int64   `json:"byte_length"`
	VersionID     *string `json:"version_id"`
	CreatedAt     string  `json:"created_at"`
	RetiredAt     *string `json:"retired_at"`
	PurgedAt      *string `json:"purged_at"`
	Required      bool    `json:"required"`
}

type RecoverySnapshot struct {
	Inspection Inspection
	Assets     []PhotoAsset
}

type LogicalPackageManifest struct {
	Type                string        `json:"type"`
	Format              int           `json:"format"`
	Environment         string        `json:"environment"`
	Generation          string        `json:"generation"`
	SourceBackend       string        `json:"source_backend"`
	SourceIdentity      string        `json:"source_identity"`
	SourceArtifact      string        `json:"source_artifact"`
	ReleaseArtifact     string        `json:"release_artifact"`
	LogicalSchema       string        `json:"logical_schema"`
	CatalogSHA256       string        `json:"catalog_sha256"`
	MigrationLedger     []LedgerEntry `json:"migration_ledger"`
	ExportID            string        `json:"export_id"`
	SnapshotStartedAt   string        `json:"snapshot_started_at"`
	SnapshotCompletedAt string        `json:"snapshot_completed_at"`
	ArchiveSHA256       string        `json:"archive_sha256"`
	ArchiveBytes        int64         `json:"archive_bytes"`
	RootSHA256          string        `json:"root_sha256"`
	PhotoSource         PhotoSource   `json:"photo_source"`
	Assets              []PhotoAsset  `json:"assets"`
}

type LogicalPackage struct {
	Manifest   LogicalPackageManifest
	Inspection Inspection
	archive    *os.File
}

func (p *LogicalPackage) Close() error {
	if p == nil || p.archive == nil {
		return nil
	}
	return p.archive.Close()
}

func (p *LogicalPackage) ArchiveReader() (io.Reader, error) {
	if p == nil || p.archive == nil {
		return nil, fmt.Errorf("logical package is closed")
	}
	if _, err := p.archive.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind logical package archive: %w", err)
	}
	return io.LimitReader(p.archive, p.Inspection.Bytes), nil
}

type recoveryPhoto struct {
	id, shopID, contentHash, contentType string
	imageHex, deletedAt                  *string
	objectKey, objectVersion             *string
	pending                              bool
}

type recoveryVisitor struct {
	photos map[string]recoveryPhoto
	assets []PhotoAsset
}

func (v *recoveryVisitor) Begin(Header) error                      { return nil }
func (v *recoveryVisitor) BeginTable(int, Table) error             { return nil }
func (v *recoveryVisitor) EndTable(int, Table, TableSummary) error { return nil }
func (v *recoveryVisitor) End(Footer) error                        { return v.validate() }

func (v *recoveryVisitor) VisitRow(_ int, table Table, _ int64, row Row) error {
	switch table.Name {
	case "property_photos":
		photo := recoveryPhoto{
			id: valueAt(table, row.Values, "id"), shopID: valueAt(table, row.Values, "shop_id"),
			contentHash: valueAt(table, row.Values, "content_hash"), contentType: valueAt(table, row.Values, "content_type"),
			imageHex: optionalValueAt(table, row.Values, "image_data"), deletedAt: optionalValueAt(table, row.Values, "deleted_at"),
			objectKey: optionalValueAt(table, row.Values, "object_key"), objectVersion: optionalValueAt(table, row.Values, "object_version"),
			pending: valueAt(table, row.Values, "object_pending") == "1",
		}
		if len(photo.contentHash) != 64 || !hexValue.MatchString(photo.contentHash) || (photo.contentType != "image/jpeg" && photo.contentType != "image/png") {
			return fmt.Errorf("logical photo %s has an invalid digest or content type", photo.id)
		}
		if photo.objectKey != nil && (len(*photo.objectKey) != 64 || !hexValue.MatchString(*photo.objectKey)) {
			return fmt.Errorf("logical photo %s has an invalid object key", photo.id)
		}
		if photo.objectVersion != nil && (*photo.objectVersion == "" || len(*photo.objectVersion) > 512) {
			return fmt.Errorf("logical photo %s has an invalid object version", photo.id)
		}
		if photo.deletedAt == nil {
			if photo.imageHex == nil || *photo.imageHex == "" {
				return fmt.Errorf("active logical photo %s has no inline recovery bytes", photo.id)
			}
			content, err := hex.DecodeString(*photo.imageHex)
			digest := sha256.Sum256(content)
			if err != nil || len(content) == 0 || hex.EncodeToString(digest[:]) != photo.contentHash {
				return fmt.Errorf("active logical photo %s inline recovery bytes do not match its digest", photo.id)
			}
		}
		v.photos[photo.id] = photo
	case "property_photo_objects":
		length, err := strconv.ParseInt(valueAt(table, row.Values, "byte_length"), 10, 64)
		if err != nil || length < 1 || length > 4*1024*1024 {
			return fmt.Errorf("logical photo object has an invalid byte length")
		}
		version := optionalValueAt(table, row.Values, "version_id")
		purged := optionalValueAt(table, row.Values, "purged_at")
		asset := PhotoAsset{
			ObjectKey: valueAt(table, row.Values, "object_key"), ShopID: valueAt(table, row.Values, "shop_id"),
			PhotoID: valueAt(table, row.Values, "photo_id"), ContentSHA256: valueAt(table, row.Values, "content_hash"),
			ContentType: valueAt(table, row.Values, "content_type"), ByteLength: length, VersionID: version,
			CreatedAt: valueAt(table, row.Values, "created_at"), RetiredAt: optionalValueAt(table, row.Values, "retired_at"),
			PurgedAt: purged, Required: version != nil && purged == nil,
		}
		if len(asset.ObjectKey) != 64 || !hexValue.MatchString(asset.ObjectKey) || len(asset.ContentSHA256) != 64 || !hexValue.MatchString(asset.ContentSHA256) {
			return fmt.Errorf("logical photo object has an invalid key or digest")
		}
		if asset.ContentType != "image/jpeg" && asset.ContentType != "image/png" {
			return fmt.Errorf("logical photo object has an invalid content type")
		}
		if version != nil && (*version == "" || len(*version) > 512) {
			return fmt.Errorf("logical photo object has an invalid version")
		}
		v.assets = append(v.assets, asset)
	}
	return nil
}

func (v *recoveryVisitor) validate() error {
	assets := make(map[string]PhotoAsset, len(v.assets))
	photoAssets := make(map[string]string, len(v.assets))
	for _, asset := range v.assets {
		if _, exists := assets[asset.ObjectKey]; exists {
			return fmt.Errorf("logical photo inventory repeats object %s", asset.ObjectKey)
		}
		if previous, exists := photoAssets[asset.PhotoID]; exists {
			return fmt.Errorf("logical photo inventory repeats photo %s in objects %s and %s", asset.PhotoID, previous, asset.ObjectKey)
		}
		if asset.PurgedAt != nil && (asset.RetiredAt == nil || asset.VersionID == nil || *asset.PurgedAt < *asset.RetiredAt) {
			return fmt.Errorf("logical photo object %s has an invalid purge state", asset.ObjectKey)
		}
		assets[asset.ObjectKey] = asset
		photoAssets[asset.PhotoID] = asset.ObjectKey
	}
	for _, photo := range v.photos {
		if photo.objectVersion != nil && photo.objectKey == nil {
			return fmt.Errorf("logical photo %s has a version without an object key", photo.id)
		}
		if photo.pending && photo.objectVersion != nil {
			return fmt.Errorf("logical photo %s is pending with a published version", photo.id)
		}
		if photo.objectKey == nil {
			continue
		}
		asset, exists := assets[*photo.objectKey]
		if !exists || asset.PhotoID != photo.id || asset.ShopID != photo.shopID || asset.ContentSHA256 != photo.contentHash || asset.ContentType != photo.contentType {
			return fmt.Errorf("logical photo %s does not match its object inventory", photo.id)
		}
		if photo.deletedAt == nil {
			content, _ := hex.DecodeString(*photo.imageHex)
			if asset.ByteLength != int64(len(content)) || asset.PurgedAt != nil {
				return fmt.Errorf("active logical photo %s has an unusable object inventory", photo.id)
			}
		}
		if photo.objectVersion != nil && (asset.VersionID == nil || *asset.VersionID != *photo.objectVersion) {
			return fmt.Errorf("logical photo %s does not match its exact object version", photo.id)
		}
		if photo.deletedAt == nil && photo.objectVersion != nil && !asset.Required {
			return fmt.Errorf("active logical photo %s references a purged object version", photo.id)
		}
	}
	return nil
}

func optionalValueAt(table Table, values []*string, column string) *string {
	for i := range table.Columns {
		if table.Columns[i].Name == column {
			if values[i] == nil {
				return nil
			}
			value := *values[i]
			return &value
		}
	}
	return nil
}

func InspectRecoveryArchive(input io.Reader, maxBytes int64) (RecoverySnapshot, error) {
	visitor := &recoveryVisitor{photos: make(map[string]recoveryPhoto)}
	inspection, err := walkArchive(input, maxBytes, visitor)
	if err != nil {
		return RecoverySnapshot{}, err
	}
	if inspection.Header.LogicalSchema == ProductionV29LogicalSchema && len(visitor.assets) != 0 {
		return RecoverySnapshot{}, fmt.Errorf("production v29 archive unexpectedly references Blob assets")
	}
	if visitor.assets == nil {
		visitor.assets = []PhotoAsset{}
	}
	return RecoverySnapshot{Inspection: inspection, Assets: visitor.assets}, nil
}

func BuildLogicalPackage(archive io.ReadSeeker, output io.Writer, options LogicalPackageOptions) (LogicalPackageManifest, error) {
	maxArchiveBytes, maxPackageBytes, err := validateLogicalPackageOptions(options)
	if err != nil {
		return LogicalPackageManifest{}, err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return LogicalPackageManifest{}, fmt.Errorf("rewind logical package source: %w", err)
	}
	snapshot, err := InspectRecoveryArchive(archive, maxArchiveBytes)
	if err != nil {
		return LogicalPackageManifest{}, err
	}
	if err := validatePhotoSource(options.PhotoSource, snapshot); err != nil {
		return LogicalPackageManifest{}, err
	}
	manifest := manifestFor(snapshot, options)
	manifestBytes, _ := json.Marshal(manifest)
	if len(manifestBytes) > DefaultMaxManifestBytes {
		return LogicalPackageManifest{}, fmt.Errorf("logical package manifest exceeds %d bytes", DefaultMaxManifestBytes)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return LogicalPackageManifest{}, fmt.Errorf("rewind logical package source: %w", err)
	}
	bounded := &countWriter{w: output, limit: maxPackageBytes}
	writer := tar.NewWriter(bounded)
	archiveHasher := sha256.New()
	for _, member := range []struct {
		name string
		size int64
		body io.Reader
	}{{"manifest.json", int64(len(manifestBytes)), bytes.NewReader(manifestBytes)}, {"database.ndjson", snapshot.Inspection.Bytes, io.TeeReader(io.LimitReader(archive, snapshot.Inspection.Bytes), archiveHasher)}} {
		header := &tar.Header{Name: member.name, Mode: 0o600, Size: member.size, Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatUSTAR}
		if err := writer.WriteHeader(header); err != nil {
			return LogicalPackageManifest{}, fmt.Errorf("write logical package %s header: %w", member.name, err)
		}
		written, err := io.Copy(writer, member.body)
		if err != nil || written != member.size {
			return LogicalPackageManifest{}, fmt.Errorf("write logical package %s: copied %d of %d bytes: %w", member.name, written, member.size, err)
		}
	}
	if hex.EncodeToString(archiveHasher.Sum(nil)) != snapshot.Inspection.ArtifactSHA256 {
		return LogicalPackageManifest{}, fmt.Errorf("logical archive changed while packaging")
	}
	if err := writer.Close(); err != nil {
		return LogicalPackageManifest{}, fmt.Errorf("finish logical package: %w", err)
	}
	return manifest, nil
}

func OpenLogicalPackage(input io.Reader, options LogicalPackageOptions) (*LogicalPackage, error) {
	maxArchiveBytes, maxPackageBytes, err := validateLogicalPackageOptions(options)
	if err != nil {
		return nil, err
	}
	spooled, err := spoolArchive(input, maxPackageBytes)
	if err != nil {
		return nil, err
	}
	defer spooled.Close()
	stat, err := spooled.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect logical package: %w", err)
	}
	reader := tar.NewReader(spooled)
	manifestHeader, err := reader.Next()
	if err != nil || !validPackageHeader(manifestHeader, "manifest.json", DefaultMaxManifestBytes) {
		return nil, fmt.Errorf("logical package manifest member is invalid")
	}
	manifestBytes, err := io.ReadAll(reader)
	if err != nil || int64(len(manifestBytes)) != manifestHeader.Size {
		return nil, fmt.Errorf("read logical package manifest")
	}
	var manifest LogicalPackageManifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode logical package manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("logical package manifest has an invalid suffix")
	}
	canonical, _ := json.Marshal(manifest)
	if !reflect.DeepEqual(canonical, manifestBytes) {
		return nil, fmt.Errorf("logical package manifest is not canonical")
	}
	if manifest.Type != "logical-package" || manifest.Format != LogicalPackageFormat || manifest.ReleaseArtifact != options.ReleaseArtifact || manifest.PhotoSource != options.PhotoSource {
		return nil, fmt.Errorf("logical package manifest identity is invalid")
	}
	archiveHeader, err := reader.Next()
	if err != nil || !validPackageHeader(archiveHeader, "database.ndjson", maxArchiveBytes) {
		return nil, fmt.Errorf("logical package database member is invalid")
	}
	archive, err := spoolArchive(io.LimitReader(reader, archiveHeader.Size), archiveHeader.Size)
	if err != nil {
		return nil, err
	}
	if _, err := reader.Next(); err != io.EOF {
		archive.Close()
		return nil, fmt.Errorf("logical package has unexpected members")
	}
	expectedSize := tarPackageSize(manifestHeader.Size, archiveHeader.Size)
	if stat.Size() != expectedSize {
		archive.Close()
		return nil, fmt.Errorf("logical package has trailing or missing bytes")
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		archive.Close()
		return nil, fmt.Errorf("rewind logical package archive: %w", err)
	}
	snapshot, err := InspectRecoveryArchive(archive, archiveHeader.Size)
	if err != nil {
		archive.Close()
		return nil, err
	}
	if err := validatePhotoSource(manifest.PhotoSource, snapshot); err != nil {
		archive.Close()
		return nil, err
	}
	expected := manifestFor(snapshot, options)
	if !reflect.DeepEqual(manifest, expected) {
		archive.Close()
		return nil, fmt.Errorf("logical package manifest does not match its archive")
	}
	return &LogicalPackage{Manifest: manifest, Inspection: snapshot.Inspection, archive: archive}, nil
}

func manifestFor(snapshot RecoverySnapshot, options LogicalPackageOptions) LogicalPackageManifest {
	header, footer := snapshot.Inspection.Header, snapshot.Inspection.Footer
	return LogicalPackageManifest{
		Type: "logical-package", Format: LogicalPackageFormat, Environment: header.Environment, Generation: header.Generation,
		SourceBackend: header.SourceBackend, SourceIdentity: header.SourceIdentity, SourceArtifact: header.SourceArtifact,
		ReleaseArtifact: options.ReleaseArtifact, LogicalSchema: header.LogicalSchema, CatalogSHA256: header.CatalogSHA256,
		MigrationLedger: append([]LedgerEntry(nil), header.MigrationLedger...),
		ExportID:        header.ExportID, SnapshotStartedAt: header.SnapshotStartedAt, SnapshotCompletedAt: footer.SnapshotCompletedAt,
		ArchiveSHA256: snapshot.Inspection.ArtifactSHA256, ArchiveBytes: snapshot.Inspection.Bytes,
		RootSHA256: footer.RootSHA256, PhotoSource: options.PhotoSource, Assets: snapshot.Assets,
	}
}

func validateLogicalPackageOptions(options LogicalPackageOptions) (int64, int64, error) {
	if options.ReleaseArtifact == "" || len(options.ReleaseArtifact) > 512 {
		return 0, 0, fmt.Errorf("logical package requires a bounded release artifact")
	}
	maxArchiveBytes := options.MaxArchiveBytes
	if maxArchiveBytes == 0 {
		maxArchiveBytes = DefaultMaxArchiveBytes
	}
	maxPackageBytes := options.MaxPackageBytes
	if maxPackageBytes == 0 {
		maxPackageBytes = DefaultMaxPackageBytes
	}
	if maxArchiveBytes < 1 || maxArchiveBytes > DefaultMaxArchiveBytes || maxPackageBytes < 1 || maxPackageBytes > DefaultMaxPackageBytes {
		return 0, 0, fmt.Errorf("logical package size limits are invalid")
	}
	return maxArchiveBytes, maxPackageBytes, nil
}

func validatePhotoSource(source PhotoSource, snapshot RecoverySnapshot) error {
	switch source.Kind {
	case "inline":
		if source.Account != "" || source.Container != "" || source.ResourceID != "" || len(snapshot.Assets) != 0 {
			return fmt.Errorf("inline logical photo source cannot contain Blob inventory")
		}
	case "azure_blob":
		parts := storageResourceValue.FindStringSubmatch(source.ResourceID)
		if !storageAccountValue.MatchString(source.Account) || source.Container != "photos" || len(parts) != 3 || parts[1] != snapshot.Inspection.Header.Environment || parts[2] != source.Account || snapshot.Inspection.Header.LogicalSchema == ProductionV29LogicalSchema {
			return fmt.Errorf("logical package has an invalid Azure photo source")
		}
	default:
		return fmt.Errorf("logical package has an invalid photo source")
	}
	return nil
}

func validPackageHeader(header *tar.Header, name string, maximum int64) bool {
	return header != nil && header.Name == name && header.Typeflag == tar.TypeReg && header.Mode == 0o600 &&
		header.Size >= 1 && header.Size <= maximum && header.ModTime.Equal(time.Unix(0, 0).UTC())
}

func tarPackageSize(sizes ...int64) int64 {
	result := int64(1024)
	for _, size := range sizes {
		result += 512 + ((size + 511) / 512 * 512)
	}
	return result
}

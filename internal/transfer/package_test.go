package transfer

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLogicalPackageRoundTripBindsExactArchive(t *testing.T) {
	archive := emptyArchive(t)
	options := LogicalPackageOptions{ReleaseArtifact: "sha256:release", PhotoSource: InlinePhotoSource()}
	var encoded bytes.Buffer
	manifest, err := BuildLogicalPackage(bytes.NewReader(archive), &encoded, options)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ArchiveBytes != int64(len(archive)) || manifest.ArchiveSHA256 == "" || manifest.Assets == nil {
		t.Fatalf("logical package manifest does not bind its archive: %+v", manifest)
	}
	opened, err := OpenLogicalPackage(bytes.NewReader(encoded.Bytes()), options)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	reader, err := opened.ArchiveReader()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := io.ReadAll(reader)
	if err != nil || !bytes.Equal(restored, archive) || !reflect.DeepEqual(opened.Manifest, manifest) {
		t.Fatalf("logical package changed its archive or manifest: bytes=%v manifest=%v err=%v", bytes.Equal(restored, archive), reflect.DeepEqual(opened.Manifest, manifest), err)
	}
	changed := options
	changed.ReleaseArtifact = "sha256:other-release"
	if _, err := OpenLogicalPackage(bytes.NewReader(encoded.Bytes()), changed); err == nil {
		t.Fatal("logical package accepted a different release artifact")
	}
}

func TestLogicalPackageRejectsMalformedContainerBeforeUse(t *testing.T) {
	archive := emptyArchive(t)
	options := LogicalPackageOptions{ReleaseArtifact: "sha256:release", PhotoSource: InlinePhotoSource()}
	var encoded bytes.Buffer
	if _, err := BuildLogicalPackage(bytes.NewReader(archive), &encoded, options); err != nil {
		t.Fatal(err)
	}
	valid := encoded.Bytes()
	mutatedArchive := append([]byte(nil), valid...)
	needle := []byte(`"source_identity":"fixture"`)
	index := bytes.LastIndex(mutatedArchive, needle)
	if index < 0 {
		t.Fatal("fixture archive identity not found")
	}
	mutatedArchive[index+len(needle)-2] ^= 1
	for name, input := range map[string][]byte{
		"truncated":       valid[:len(valid)-1],
		"trailing":        append(append([]byte(nil), valid...), 0),
		"changed archive": mutatedArchive,
	} {
		t.Run(name, func(t *testing.T) {
			if opened, err := OpenLogicalPackage(bytes.NewReader(input), options); err == nil {
				opened.Close()
				t.Fatal("malformed logical package was accepted")
			}
		})
	}
}

func TestLogicalPackageRequiresFixedMembersAndMetadata(t *testing.T) {
	archive := emptyArchive(t)
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	header := &tar.Header{Name: "database.ndjson", Mode: 0o600, Size: int64(len(archive)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatUSTAR}
	if err := writer.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(archive); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	options := LogicalPackageOptions{ReleaseArtifact: "sha256:release", PhotoSource: InlinePhotoSource()}
	if opened, err := OpenLogicalPackage(bytes.NewReader(output.Bytes()), options); err == nil {
		opened.Close()
		t.Fatal("logical package accepted members in the wrong order")
	}
	options.ReleaseArtifact = strings.Repeat("x", 513)
	if _, err := BuildLogicalPackage(bytes.NewReader(archive), io.Discard, options); err == nil {
		t.Fatal("logical package accepted an unbounded release artifact")
	}
	var valid bytes.Buffer
	options.ReleaseArtifact = "sha256:release"
	if _, err := BuildLogicalPackage(bytes.NewReader(archive), &valid, options); err != nil {
		t.Fatal(err)
	}
	manifest, database := readPackageMembers(t, valid.Bytes())
	for name, first := range map[string]tarMember{
		"mode": {name: "manifest.json", mode: 0o644, modified: time.Unix(0, 0).UTC(), data: manifest},
		"time": {name: "manifest.json", mode: 0o600, modified: time.Unix(1, 0).UTC(), data: manifest},
		"type": {name: "manifest.json", mode: 0o600, modified: time.Unix(0, 0).UTC(), kind: tar.TypeSymlink},
	} {
		t.Run(name, func(t *testing.T) {
			members := []tarMember{first, {name: "database.ndjson", mode: 0o600, modified: time.Unix(0, 0).UTC(), data: database}}
			if opened, err := OpenLogicalPackage(bytes.NewReader(writeRawPackage(t, members)), options); err == nil {
				opened.Close()
				t.Fatal("logical package accepted invalid member metadata")
			}
		})
	}
}

func TestLogicalPackageSizeLimitsAreSymmetricAndAbsolute(t *testing.T) {
	archive := emptyArchive(t)
	options := LogicalPackageOptions{ReleaseArtifact: "sha256:release", PhotoSource: InlinePhotoSource()}
	var encoded bytes.Buffer
	if _, err := BuildLogicalPackage(bytes.NewReader(archive), &encoded, options); err != nil {
		t.Fatal(err)
	}
	for name, changed := range map[string]LogicalPackageOptions{
		"archive above ceiling": {ReleaseArtifact: options.ReleaseArtifact, PhotoSource: options.PhotoSource, MaxArchiveBytes: DefaultMaxArchiveBytes + 1},
		"package above ceiling": {ReleaseArtifact: options.ReleaseArtifact, PhotoSource: options.PhotoSource, MaxPackageBytes: DefaultMaxPackageBytes + 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildLogicalPackage(bytes.NewReader(archive), io.Discard, changed); err == nil {
				t.Fatal("build accepted an above-ceiling size limit")
			}
			if _, err := OpenLogicalPackage(bytes.NewReader(encoded.Bytes()), changed); err == nil {
				t.Fatal("open accepted an above-ceiling size limit")
			}
		})
	}
	exact := options
	exact.MaxPackageBytes = int64(encoded.Len())
	if opened, err := OpenLogicalPackage(bytes.NewReader(encoded.Bytes()), exact); err != nil {
		t.Fatal(err)
	} else {
		opened.Close()
	}
	oneShort := exact
	oneShort.MaxPackageBytes--
	if _, err := OpenLogicalPackage(bytes.NewReader(encoded.Bytes()), oneShort); err == nil {
		t.Fatal("open accepted a package one byte above its configured limit")
	}
	if _, err := BuildLogicalPackage(bytes.NewReader(archive), io.Discard, oneShort); err == nil {
		t.Fatal("build accepted a package one byte above its configured limit")
	}
}

func TestRecoveryAssetRelationships(t *testing.T) {
	active := recoveryPhoto{
		id: "10000000-0000-4000-8000-000000000001", shopID: "10000000-0000-4000-8000-000000000002",
		contentHash: "039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81", contentType: "image/png",
		imageHex: pointer("010203"), objectKey: pointer(strings.Repeat("a", 64)), objectVersion: pointer("version-1"),
	}
	matching := PhotoAsset{
		ObjectKey: *active.objectKey, ShopID: active.shopID, PhotoID: active.id, ContentSHA256: active.contentHash,
		ContentType: active.contentType, ByteLength: 3, VersionID: pointer("version-1"), CreatedAt: "2035-09-02T12:00:00.000000Z", Required: true,
	}
	valid := &recoveryVisitor{photos: map[string]recoveryPhoto{active.id: active}, assets: []PhotoAsset{matching}}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	legacy := active
	legacy.objectVersion = nil
	legacy.pending = false
	legacyAsset := matching
	legacyAsset.VersionID = nil
	legacyAsset.Required = false
	if err := (&recoveryVisitor{photos: map[string]recoveryPhoto{legacy.id: legacy}, assets: []PhotoAsset{legacyAsset}}).validate(); err != nil {
		t.Fatalf("valid interrupted legacy backfill was rejected: %v", err)
	}
	deleted := active
	deleted.deletedAt = pointer("2035-09-03T12:00:00.000000Z")
	deleted.imageHex = nil
	purged := matching
	purged.RetiredAt = pointer("2035-09-03T12:00:00.000000Z")
	purged.PurgedAt = pointer("2035-09-10T12:00:00.000000Z")
	purged.Required = false
	if err := (&recoveryVisitor{photos: map[string]recoveryPhoto{deleted.id: deleted}, assets: []PhotoAsset{purged}}).validate(); err != nil {
		t.Fatalf("valid linked purged tombstone was rejected: %v", err)
	}

	for name, fixture := range map[string]*recoveryVisitor{
		"version without key": {photos: map[string]recoveryPhoto{active.id: func() recoveryPhoto { p := active; p.objectKey = nil; return p }()}},
		"pending published":   {photos: map[string]recoveryPhoto{active.id: func() recoveryPhoto { p := active; p.pending = true; return p }()}, assets: []PhotoAsset{matching}},
		"missing inventory":   {photos: map[string]recoveryPhoto{active.id: active}},
		"wrong shop":          {photos: map[string]recoveryPhoto{active.id: active}, assets: []PhotoAsset{func() PhotoAsset { a := matching; a.ShopID = "10000000-0000-4000-8000-000000000003"; return a }()}},
		"wrong photo":         {photos: map[string]recoveryPhoto{active.id: active}, assets: []PhotoAsset{func() PhotoAsset { a := matching; a.PhotoID = "10000000-0000-4000-8000-000000000003"; return a }()}},
		"wrong hash":          {photos: map[string]recoveryPhoto{active.id: active}, assets: []PhotoAsset{func() PhotoAsset { a := matching; a.ContentSHA256 = strings.Repeat("b", 64); return a }()}},
		"wrong type":          {photos: map[string]recoveryPhoto{active.id: active}, assets: []PhotoAsset{func() PhotoAsset { a := matching; a.ContentType = "image/jpeg"; return a }()}},
		"wrong size":          {photos: map[string]recoveryPhoto{active.id: active}, assets: []PhotoAsset{func() PhotoAsset { a := matching; a.ByteLength = 2; return a }()}},
		"wrong version":       {photos: map[string]recoveryPhoto{active.id: active}, assets: []PhotoAsset{func() PhotoAsset { a := matching; a.VersionID = pointer("other"); return a }()}},
		"active purged":       {photos: map[string]recoveryPhoto{active.id: active}, assets: []PhotoAsset{purged}},
		"invalid purge": {assets: []PhotoAsset{func() PhotoAsset {
			a := matching
			a.PurgedAt = pointer("2035-09-10T12:00:00.000000Z")
			a.Required = false
			return a
		}()}},
		"duplicate photo": {assets: []PhotoAsset{matching, func() PhotoAsset { a := matching; a.ObjectKey = strings.Repeat("b", 64); return a }()}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := fixture.validate(); err == nil {
				t.Fatal("invalid recovery relationship was accepted")
			}
		})
	}
}

func TestRecoveryPhotoInlineBytesFailClosed(t *testing.T) {
	table := profileTable(t, CurrentProfile, "property_photos")
	base := make([]*string, len(table.Columns))
	setValue(table, base, "id", "10000000-0000-4000-8000-000000000001")
	setValue(table, base, "shop_id", "10000000-0000-4000-8000-000000000002")
	setValue(table, base, "content_hash", "039058c6f2c0cb492c533b0a4d14ef77cc0f78abccced5287d84a1a2011cfb81")
	setValue(table, base, "content_type", "image/png")
	setValue(table, base, "image_data", "010203")
	setValue(table, base, "object_pending", "0")
	for name, mutate := range map[string]func([]*string){
		"missing":      func(values []*string) { setNull(table, values, "image_data") },
		"wrong digest": func(values []*string) { setValue(table, values, "content_hash", strings.Repeat("a", 64)) },
	} {
		t.Run(name, func(t *testing.T) {
			values := append([]*string(nil), base...)
			mutate(values)
			visitor := &recoveryVisitor{photos: make(map[string]recoveryPhoto)}
			if err := visitor.VisitRow(0, table, 1, Row{Type: "row", Values: values}); err == nil {
				t.Fatal("invalid active inline recovery bytes were accepted")
			}
		})
	}
}

func TestLogicalPackageManifestTamperingFailsClosed(t *testing.T) {
	archive := emptyArchive(t)
	options := LogicalPackageOptions{ReleaseArtifact: "sha256:release", PhotoSource: InlinePhotoSource()}
	var encoded bytes.Buffer
	if _, err := BuildLogicalPackage(bytes.NewReader(archive), &encoded, options); err != nil {
		t.Fatal(err)
	}
	manifest, database := readPackageMembers(t, encoded.Bytes())
	var parsed LogicalPackageManifest
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		t.Fatal(err)
	}
	changedEnvironment := parsed
	changedEnvironment.Environment = "production"
	changedRoot := parsed
	changedRoot.RootSHA256 = strings.Repeat("0", 64)
	changedAssets := parsed
	changedAssets.Assets = []PhotoAsset{{ObjectKey: strings.Repeat("a", 64)}}
	unknown := append(append([]byte(nil), manifest[:len(manifest)-1]...), []byte(`,"unknown":true}`)...)
	for name, changed := range map[string][]byte{
		"unknown field": unknown,
		"noncanonical":  append([]byte(" "), manifest...),
		"environment":   packageManifest(t, changedEnvironment),
		"root":          packageManifest(t, changedRoot),
		"assets":        packageManifest(t, changedAssets),
	} {
		t.Run(name, func(t *testing.T) {
			candidate := writePackageMembers(t, changed, database, nil)
			if opened, err := OpenLogicalPackage(bytes.NewReader(candidate), options); err == nil {
				opened.Close()
				t.Fatal("tampered logical package manifest was accepted")
			}
		})
	}
	extra := []tarMember{{name: "extra", mode: 0o600, modified: time.Unix(0, 0).UTC(), data: []byte("x")}}
	if opened, err := OpenLogicalPackage(bytes.NewReader(writePackageMembers(t, manifest, database, extra)), options); err == nil {
		opened.Close()
		t.Fatal("logical package accepted an extra member")
	}
}

type tarMember struct {
	name     string
	mode     int64
	modified time.Time
	kind     byte
	data     []byte
}

func readPackageMembers(t *testing.T, input []byte) ([]byte, []byte) {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(input))
	var result [][]byte
	for range 2 {
		if _, err := reader.Next(); err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, data)
	}
	return result[0], result[1]
}

func writePackageMembers(t *testing.T, manifest, database []byte, extra []tarMember) []byte {
	t.Helper()
	members := []tarMember{
		{name: "manifest.json", mode: 0o600, modified: time.Unix(0, 0).UTC(), data: manifest},
		{name: "database.ndjson", mode: 0o600, modified: time.Unix(0, 0).UTC(), data: database},
	}
	members = append(members, extra...)
	return writeRawPackage(t, members)
}

func writeRawPackage(t *testing.T, members []tarMember) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	for _, member := range members {
		kind := member.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: member.name, Mode: member.mode, Size: int64(len(member.data)), Typeflag: kind, ModTime: member.modified, Format: tar.FormatUSTAR}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(member.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func packageManifest(t *testing.T, manifest LogicalPackageManifest) []byte {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func profileTable(t *testing.T, profile Profile, name string) Table {
	t.Helper()
	for _, table := range profile.Catalog {
		if table.Name == name {
			return table
		}
	}
	t.Fatalf("profile has no %s table", name)
	return Table{}
}

func setValue(table Table, values []*string, column, value string) {
	for index := range table.Columns {
		if table.Columns[index].Name == column {
			values[index] = pointer(value)
			return
		}
	}
}

func setNull(table Table, values []*string, column string) {
	for index := range table.Columns {
		if table.Columns[index].Name == column {
			values[index] = nil
			return
		}
	}
}

func pointer(value string) *string { return &value }

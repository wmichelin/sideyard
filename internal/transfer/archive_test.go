package transfer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

func TestCatalogDefinitionAndDigest(t *testing.T) {
	if err := validateCatalogDefinition(); err != nil {
		t.Fatal(err)
	}
	if got := len(Catalog); got != 32 {
		t.Fatalf("catalog has %d tables, want 32", got)
	}
	if got := len(ProductionV29Profile.Catalog); got != 29 {
		t.Fatalf("production v29 catalog has %d tables, want 29", got)
	}
	if err := validateCatalog(ProductionV29Profile.Catalog); err != nil {
		t.Fatal(err)
	}
	const expected = "acc7ad3e5ea72c5c1c572a7763c4f119e04c30e7e134fea4755a860a39171b63"
	if got := CatalogDigest(); got != expected {
		t.Fatalf("catalog digest = %s, update expected only with a reviewed schema change", got)
	}
	const productionExpected = "8e063835784b66a22bdd0ebd6590472bf7cc35c0acd16e50d1f676c884e2d3f2"
	if got := catalogDigest(ProductionV29Profile.Catalog); got != productionExpected {
		t.Fatalf("production v29 catalog digest = %s, update expected only with a reviewed release-profile change", got)
	}
	var photos *Table
	for index := range ProductionV29Profile.Catalog {
		if ProductionV29Profile.Catalog[index].Name == "property_photos" {
			photos = &ProductionV29Profile.Catalog[index]
			break
		}
	}
	if photos == nil || len(photos.Columns) != 12 {
		t.Fatalf("production v29 photo profile = %#v, want property_photos with 12 columns", photos)
	}
}

func TestExportProfileRequiresEnvironmentReleaseMatch(t *testing.T) {
	for name, test := range map[string]struct {
		options ExportOptions
		want    string
	}{
		"implicit production": {options: ExportOptions{Environment: "production"}, want: "explicit release profile"},
		"current production":  {options: ExportOptions{Environment: "production", LogicalSchema: LogicalSchema}, want: "does not match environment"},
		"v29 staging":         {options: ExportOptions{Environment: "staging", LogicalSchema: ProductionV29LogicalSchema}, want: "does not match environment"},
		"unknown":             {options: ExportOptions{Environment: "staging", LogicalSchema: "unknown"}, want: "unknown logical release profile"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := exportProfile(test.options); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("exportProfile error = %v, want %q", err, test.want)
			}
		})
	}
	for name, options := range map[string]ExportOptions{
		"current staging": {Environment: "staging"},
		"v29 production":  {Environment: "production", LogicalSchema: ProductionV29LogicalSchema},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := exportProfile(options); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestVerifyLedgerRequiresExactEmbeddedRelease(t *testing.T) {
	expected := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
	valid := []LedgerEntry{{Version: 1, Checksum: expected[0]}, {Version: 2, Checksum: expected[1]}}
	if err := verifyLedger(valid, expected); err != nil {
		t.Fatal(err)
	}
	for name, actual := range map[string][]LedgerEntry{
		"changed checksum": {{Version: 1, Checksum: strings.Repeat("c", 64)}, valid[1]},
		"changed version":  {valid[0], {Version: 3, Checksum: valid[1].Checksum}},
		"missing entry":    {valid[0]},
	} {
		t.Run(name, func(t *testing.T) {
			if err := verifyLedger(actual, expected); err == nil {
				t.Fatal("incompatible migration ledger was accepted")
			}
		})
	}
}

func TestCanonicalValue(t *testing.T) {
	valid := []struct {
		kind Kind
		in   string
		want string
	}{
		{Text, " snowman ☃ &<>  ", " snowman ☃ &<>  "},
		{UUID, "123E4567-E89B-12D3-A456-426614174000", "123e4567-e89b-12d3-a456-426614174000"},
		{Integer, "+0042", "42"},
		{Boolean, "0", "0"},
		{Instant, "2026-09-10T14:03:02.1234-04:00", "2026-09-10T18:03:02.123400Z"},
		{Date, "2024-02-29", "2024-02-29"},
		{Decimal, "0001.230", "1.230"},
		{Decimal, "-0000.000", "0.000"},
		{Bytes, "00A1ff", "00a1ff"},
		{JSON, `{ "b": [true,null], "a": 1 }`, `{"a":1,"b":[true,null]}`},
		{TextArray, `["",null,"x","x"]`, `["",null,"x","x"]`},
		{UUIDArray, `["123E4567-E89B-12D3-A456-426614174000",null]`, `["123e4567-e89b-12d3-a456-426614174000",null]`},
	}
	for _, test := range valid {
		got, err := canonicalValue(test.kind, test.in)
		if err != nil || got != test.want {
			t.Errorf("canonicalValue(%s, %q) = %q, %v; want %q", test.kind, test.in, got, err, test.want)
		}
	}
	invalid := []struct {
		kind Kind
		in   string
	}{
		{Text, "bad\x00text"}, {UUID, "nope"}, {Integer, "1.2"},
		{Boolean, "true"}, {Instant, "2026-09-10T18:03:02.1234567Z"},
		{Date, "2026-02-30"}, {Decimal, "1.23"}, {Bytes, "abc"},
		{JSON, "{} trailing"}, {TextArray, "{}"}, {TextArray, `[1]`},
		{UUIDArray, `["nope"]`},
	}
	for _, test := range invalid {
		if got, err := canonicalValue(test.kind, test.in); err == nil {
			t.Errorf("canonicalValue(%s, %q) = %q, want error", test.kind, test.in, got)
		}
	}
}

func TestDigestRowBindsSchemaNullAndEmpty(t *testing.T) {
	table := Table{Name: "x", Columns: []Column{{Name: "value", Kind: Text, Nullable: true}}}
	empty := ""
	nullDigest := digestRow(table, []*string{nil})
	emptyDigest := digestRow(table, []*string{&empty})
	if nullDigest == emptyDigest {
		t.Fatal("null and empty have the same digest")
	}
	changed := table
	changed.Columns = []Column{{Name: "renamed", Kind: Text, Nullable: true}}
	if digestRow(changed, []*string{nil}) == nullDigest {
		t.Fatal("schema change did not change row digest")
	}
	if _, err := hex.DecodeString(CatalogDigest()); err != nil {
		t.Fatal(err)
	}
}

func TestReadRecordBoundaries(t *testing.T) {
	line, err := ReadRecord(bufio.NewReaderSize(strings.NewReader("one\n"), 2))
	if err != nil || string(line) != "one\n" {
		t.Fatalf("ReadRecord = %q, %v", line, err)
	}
	if _, err := ReadRecord(bufio.NewReader(strings.NewReader("truncated"))); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("truncated record error = %v", err)
	}
	if _, err := ReadRecord(bufio.NewReader(bytes.NewReader(nil))); err != io.EOF {
		t.Fatalf("empty archive error = %v", err)
	}
	const maxRecord = 12 * 1024 * 1024
	oversized := io.MultiReader(io.LimitReader(zeroReader{}, maxRecord+1), strings.NewReader("\n"))
	if _, err := ReadRecord(bufio.NewReaderSize(oversized, 1024)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized record error = %v", err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(value []byte) (int, error) {
	for index := range value {
		value[index] = 'x'
	}
	return len(value), nil
}

func TestCountWriterRejectsPartialOverflow(t *testing.T) {
	var output bytes.Buffer
	writer := &countWriter{w: &output, limit: 3}
	if _, err := writer.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("d")); err == nil {
		t.Fatal("overflow succeeded")
	}
	if output.String() != "abc" {
		t.Fatalf("output = %q", output.String())
	}
	if got := sha256.Sum256(output.Bytes()); got == ([sha256.Size]byte{}) {
		t.Fatal("unexpected zero digest")
	}
}

func TestInspectAcceptsOnlyCompleteCanonicalArchive(t *testing.T) {
	archive := emptyArchive(t)
	inspection, err := Inspect(bytes.NewReader(archive), 0)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Bytes != int64(len(archive)) || inspection.Footer.Tables != len(Catalog) {
		t.Fatal("inspection metadata does not cover the archive")
	}
	for name, mutation := range map[string][]byte{
		"unknown field": bytes.Replace(archive, []byte(`"format":1`), []byte(`"format":1,"unknown":true`), 1),
		"bad root":      bytes.Replace(archive, []byte(inspection.Footer.RootSHA256), []byte(strings.Repeat("0", 64)), 1),
		"truncated":     archive[:len(archive)-1],
		"extra record":  append(append([]byte(nil), archive...), []byte("{}\n")...),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Inspect(bytes.NewReader(mutation), 0); err == nil {
				t.Fatal("malformed archive passed inspection")
			}
		})
	}
}

func emptyArchive(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	when := formatInstant(time.Date(2026, 9, 10, 18, 0, 0, 0, time.UTC))
	ledger := make([]LedgerEntry, CurrentProfile.PostgresMigrations)
	for index := range ledger {
		ledger[index] = LedgerEntry{Version: index + 1, Checksum: strings.Repeat("a", 64)}
	}
	header := Header{
		Type: "header", Format: FormatVersion, LogicalSchema: LogicalSchema,
		CatalogSHA256: CatalogDigest(), Environment: "staging",
		Generation: "123e4567-e89b-42d3-a456-426614174000", SourceBackend: "postgres",
		SourceIdentity: "fixture", SourceArtifact: "fixture", ExportID: "223e4567-e89b-42d3-a456-426614174000",
		SnapshotStartedAt: when, MigrationLedger: ledger,
	}
	if err := encoder.Encode(header); err != nil {
		t.Fatal(err)
	}
	root := sha256.New()
	_, _ = io.WriteString(root, header.CatalogSHA256+"\n")
	for _, table := range Catalog {
		if err := encoder.Encode(TableStart{Type: "table", Table: table.Name, Key: table.Key, Columns: table.Columns}); err != nil {
			t.Fatal(err)
		}
		summary := TableSummary{Type: "table_end", Table: table.Name, SHA256: hex.EncodeToString(sha256.New().Sum(nil))}
		if err := encoder.Encode(summary); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(summary)
		_, _ = root.Write(encoded)
		_, _ = root.Write([]byte{'\n'})
	}
	if err := encoder.Encode(Footer{Type: "footer", Tables: len(Catalog), RootSHA256: hex.EncodeToString(root.Sum(nil)), SnapshotCompletedAt: when}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

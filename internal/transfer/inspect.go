package transfer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"strings"
	"time"
)

type Inspection struct {
	Header         Header
	Footer         Footer
	Profile        Profile
	ArtifactSHA256 string
	Bytes          int64
}

type archiveVisitor interface {
	Begin(Header) error
	BeginTable(int, Table) error
	VisitRow(int, Table, int64, Row) error
	EndTable(int, Table, TableSummary) error
	End(Footer) error
}

// Inspect validates the complete archive, including its fixed schema, canonical
// values, row order, table/shop digests, root digest and final newline.
func Inspect(input io.Reader, maxBytes int64) (Inspection, error) {
	return walkArchive(input, maxBytes, nil)
}

func walkArchive(input io.Reader, maxBytes int64, visitor archiveVisitor) (Inspection, error) {
	if maxBytes == 0 {
		maxBytes = DefaultMaxArchiveBytes
	}
	if maxBytes < 0 {
		return Inspection{}, fmt.Errorf("logical archive size limit is invalid")
	}
	reader := bufio.NewReaderSize(input, 64*1024)
	hasher := sha256.New()
	var consumed int64
	read := func() ([]byte, error) {
		line, err := ReadRecord(reader)
		if err != nil {
			return nil, err
		}
		if int64(len(line)) > maxBytes-consumed {
			return nil, fmt.Errorf("logical archive exceeds %d bytes", maxBytes)
		}
		consumed += int64(len(line))
		_, _ = hasher.Write(line)
		return line, nil
	}

	line, err := read()
	if err != nil {
		return Inspection{}, fmt.Errorf("read logical archive header: %w", err)
	}
	var header Header
	if err := decodeExact(line, "header", &header); err != nil {
		return Inspection{}, err
	}
	if err := validateHeader(header); err != nil {
		return Inspection{}, err
	}
	profile, _ := profileBySchema(header.LogicalSchema)
	if visitor != nil {
		if err := visitor.Begin(header); err != nil {
			return Inspection{}, err
		}
	}

	root := sha256.New()
	_, _ = io.WriteString(root, header.CatalogSHA256+"\n")
	userShops := make(map[string]string)
	for tableIndex, table := range profile.Catalog {
		line, err = read()
		if err != nil {
			return Inspection{}, fmt.Errorf("read logical table %s: %w", table.Name, err)
		}
		var start TableStart
		if err := decodeExact(line, "table", &start); err != nil {
			return Inspection{}, err
		}
		expectedStart := TableStart{Type: "table", Table: table.Name, Key: table.Key, Columns: table.Columns}
		if !reflect.DeepEqual(start, expectedStart) {
			return Inspection{}, fmt.Errorf("logical table contract for %s is incompatible", table.Name)
		}
		if visitor != nil {
			if err := visitor.BeginTable(tableIndex, table); err != nil {
				return Inspection{}, err
			}
		}
		digest := newDigestRows()
		shops := make(map[string]*digestRows)
		var previousKey []string
		for {
			line, err = read()
			if err != nil {
				return Inspection{}, fmt.Errorf("read logical table %s body: %w", table.Name, err)
			}
			recordType, err := recordType(line)
			if err != nil {
				return Inspection{}, err
			}
			if recordType == "table_end" {
				var summary TableSummary
				if err := decodeExact(line, "table_end", &summary); err != nil {
					return Inspection{}, err
				}
				expected := tableSummary(table, digest, shops)
				if !reflect.DeepEqual(summary, expected) {
					return Inspection{}, fmt.Errorf("logical summary for %s does not match its rows", table.Name)
				}
				rootLine, _ := json.Marshal(summary)
				_, _ = root.Write(rootLine)
				_, _ = root.Write([]byte{'\n'})
				if visitor != nil {
					if err := visitor.EndTable(tableIndex, table, summary); err != nil {
						return Inspection{}, err
					}
				}
				break
			}
			if recordType != "row" {
				return Inspection{}, fmt.Errorf("logical table %s contains unexpected %s record", table.Name, recordType)
			}
			var row Row
			if err := decodeExact(line, "row", &row); err != nil {
				return Inspection{}, err
			}
			if len(row.Values) != len(table.Columns) {
				return Inspection{}, fmt.Errorf("logical row for %s has %d values; expected %d", table.Name, len(row.Values), len(table.Columns))
			}
			for index, value := range row.Values {
				if value == nil {
					if !table.Columns[index].Nullable {
						return Inspection{}, fmt.Errorf("logical row for %s has null %s", table.Name, table.Columns[index].Name)
					}
					continue
				}
				canonical, err := canonicalValue(table.Columns[index].Kind, *value)
				if err != nil || canonical != *value {
					return Inspection{}, fmt.Errorf("logical row for %s has noncanonical %s", table.Name, table.Columns[index].Name)
				}
			}
			key := rowKey(table, row.Values)
			if previousKey != nil && compareTableKey(table, previousKey, key) >= 0 {
				return Inspection{}, fmt.Errorf("logical rows for %s are not in strict key order", table.Name)
			}
			previousKey = key
			rowDigest := digestRow(table, row.Values)
			digest.add(rowDigest)
			shopID, err := rowShop(table, row.Values, userShops)
			if err != nil {
				return Inspection{}, err
			}
			if shopID != "" {
				shopDigest := shops[shopID]
				if shopDigest == nil {
					shopDigest = newDigestRows()
					shops[shopID] = shopDigest
				}
				shopDigest.add(rowDigest)
			}
			if table.Name == "users" {
				userShops[valueAt(table, row.Values, "id")] = valueAt(table, row.Values, "shop_id")
			}
			if visitor != nil {
				if err := visitor.VisitRow(tableIndex, table, digest.rows, row); err != nil {
					return Inspection{}, err
				}
			}
		}
	}

	line, err = read()
	if err != nil {
		return Inspection{}, fmt.Errorf("read logical archive footer: %w", err)
	}
	var footer Footer
	if err := decodeExact(line, "footer", &footer); err != nil {
		return Inspection{}, err
	}
	if footer.Tables != len(profile.Catalog) || footer.RootSHA256 != hex.EncodeToString(root.Sum(nil)) || !hexValue.MatchString(footer.RootSHA256) {
		return Inspection{}, fmt.Errorf("logical archive root does not match its tables")
	}
	completed, err := time.Parse(time.RFC3339Nano, footer.SnapshotCompletedAt)
	if err != nil || formatInstant(completed) != footer.SnapshotCompletedAt {
		return Inspection{}, fmt.Errorf("logical archive has invalid completion time")
	}
	started, _ := time.Parse(time.RFC3339Nano, header.SnapshotStartedAt)
	if completed.Before(started) {
		return Inspection{}, fmt.Errorf("logical archive completes before its snapshot starts")
	}
	if _, err := ReadRecord(reader); err != io.EOF {
		if err == nil {
			return Inspection{}, fmt.Errorf("logical archive has records after its footer")
		}
		return Inspection{}, fmt.Errorf("read after logical archive footer: %w", err)
	}
	if visitor != nil {
		if err := visitor.End(footer); err != nil {
			return Inspection{}, err
		}
	}
	return Inspection{Header: header, Footer: footer, Profile: profile, ArtifactSHA256: hex.EncodeToString(hasher.Sum(nil)), Bytes: consumed}, nil
}

func validateHeader(header Header) error {
	profile, ok := profileBySchema(header.LogicalSchema)
	if header.Format != FormatVersion || !ok || header.CatalogSHA256 != catalogDigest(profile.Catalog) {
		return fmt.Errorf("logical archive schema is incompatible")
	}
	if header.Environment != "staging" && header.Environment != "production" {
		return fmt.Errorf("logical archive environment is invalid")
	}
	if (header.Environment == "production") != (profile.LogicalSchema == ProductionV29LogicalSchema) {
		return fmt.Errorf("logical archive release profile does not match environment")
	}
	if !uuidValue.MatchString(header.Generation) || !uuidValue.MatchString(header.ExportID) {
		return fmt.Errorf("logical archive identity is invalid")
	}
	if header.SourceBackend != "postgres" && header.SourceBackend != "azure_sql" {
		return fmt.Errorf("logical archive backend is invalid")
	}
	if header.SourceIdentity == "" || len(header.SourceIdentity) > 512 || header.SourceArtifact == "" || len(header.SourceArtifact) > 512 {
		return fmt.Errorf("logical archive source is invalid")
	}
	started, err := time.Parse(time.RFC3339Nano, header.SnapshotStartedAt)
	if err != nil || formatInstant(started) != header.SnapshotStartedAt {
		return fmt.Errorf("logical archive has invalid snapshot time")
	}
	if len(header.MigrationLedger) == 0 {
		return fmt.Errorf("logical archive migration ledger is empty")
	}
	previous := 0
	expectedMigrations := profile.PostgresMigrations
	if header.SourceBackend == "azure_sql" {
		expectedMigrations = profile.SQLServerMigrations
	}
	for _, entry := range header.MigrationLedger {
		if entry.Version != previous+1 || len(entry.Checksum) != 64 || !hexValue.MatchString(entry.Checksum) {
			return fmt.Errorf("logical archive migration ledger is invalid")
		}
		previous = entry.Version
	}
	if len(header.MigrationLedger) != expectedMigrations {
		return fmt.Errorf("logical archive migration ledger does not match release profile")
	}
	return nil
}

func recordType(line []byte) (string, error) {
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil || envelope.Type == "" {
		return "", fmt.Errorf("logical archive contains invalid JSON record")
	}
	return envelope.Type, nil
}

func decodeExact(line []byte, expectedType string, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode logical %s record: %w", expectedType, err)
	}
	if record, err := recordType(line); err != nil || record != expectedType {
		return fmt.Errorf("expected logical %s record", expectedType)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("logical %s record has invalid suffix", expectedType)
	}
	canonical, err := canonicalRecord(destination)
	if err != nil || !bytes.Equal(line, canonical) {
		return fmt.Errorf("logical %s record is not canonical", expectedType)
	}
	return nil
}

func canonicalRecord(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func rowKey(table Table, values []*string) []string {
	key := make([]string, len(table.Key))
	for index, column := range table.Key {
		key[index] = valueAt(table, values, column)
	}
	return key
}

func compareTableKey(table Table, left, right []string) int {
	for index := range left {
		kind := Text
		for _, column := range table.Columns {
			if column.Name == table.Key[index] {
				kind = column.Kind
				break
			}
		}
		comparison := strings.Compare(left[index], right[index])
		if kind == Integer {
			leftNumber := new(big.Int)
			rightNumber := new(big.Int)
			leftNumber.SetString(left[index], 10)
			rightNumber.SetString(right[index], 10)
			comparison = leftNumber.Cmp(rightNumber)
		}
		if comparison != 0 {
			return comparison
		}
	}
	return 0
}

func tableSummary(table Table, digest *digestRows, shops map[string]*digestRows) TableSummary {
	var byShop []ShopSummary
	for shopID, shopDigest := range shops {
		byShop = append(byShop, ShopSummary{ShopID: shopID, Rows: shopDigest.rows, SHA256: shopDigest.sum()})
	}
	sortShopSummaries(byShop)
	return TableSummary{Type: "table_end", Table: table.Name, Rows: digest.rows, SHA256: digest.sum(), ByShop: byShop}
}

func sortShopSummaries(summaries []ShopSummary) {
	for index := 1; index < len(summaries); index++ {
		for cursor := index; cursor > 0 && summaries[cursor].ShopID < summaries[cursor-1].ShopID; cursor-- {
			summaries[cursor], summaries[cursor-1] = summaries[cursor-1], summaries[cursor]
		}
	}
}

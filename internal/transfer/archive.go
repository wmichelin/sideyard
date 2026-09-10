package transfer

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wmichelin/clubdue/internal/database"
)

const DefaultMaxArchiveBytes int64 = 240 * 1024 * 1024

var (
	uuidValue       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hexValue        = regexp.MustCompile(`^(?:[0-9a-f]{2})*$`)
	identifierValue = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	digitsValue     = regexp.MustCompile(`^[0-9]+$`)
)

type ExportOptions struct {
	Environment    string
	Generation     string
	SourceIdentity string
	SourceArtifact string
	LogicalSchema  string
	Now            func() time.Time
	MaxBytes       int64
}

type LedgerEntry struct {
	Version  int    `json:"version"`
	Checksum string `json:"checksum"`
}

type Header struct {
	Type              string        `json:"type"`
	Format            int           `json:"format"`
	LogicalSchema     string        `json:"logical_schema"`
	CatalogSHA256     string        `json:"catalog_sha256"`
	Environment       string        `json:"environment"`
	Generation        string        `json:"generation"`
	SourceBackend     string        `json:"source_backend"`
	SourceIdentity    string        `json:"source_identity"`
	SourceArtifact    string        `json:"source_artifact"`
	ExportID          string        `json:"export_id"`
	SnapshotStartedAt string        `json:"snapshot_started_at"`
	MigrationLedger   []LedgerEntry `json:"migration_ledger"`
}

type TableStart struct {
	Type    string   `json:"type"`
	Table   string   `json:"table"`
	Key     []string `json:"key"`
	Columns []Column `json:"columns"`
}

type Row struct {
	Type   string    `json:"type"`
	Values []*string `json:"values"`
}

type ShopSummary struct {
	ShopID string `json:"shop_id"`
	Rows   int64  `json:"rows"`
	SHA256 string `json:"sha256"`
}

type TableSummary struct {
	Type   string        `json:"type"`
	Table  string        `json:"table"`
	Rows   int64         `json:"rows"`
	SHA256 string        `json:"sha256"`
	ByShop []ShopSummary `json:"by_shop,omitempty"`
}

type Footer struct {
	Type                string `json:"type"`
	Tables              int    `json:"tables"`
	RootSHA256          string `json:"root_sha256"`
	SnapshotCompletedAt string `json:"snapshot_completed_at"`
}

type countWriter struct {
	w       io.Writer
	written int64
	limit   int64
}

func (w *countWriter) Write(value []byte) (int, error) {
	if int64(len(value)) > w.limit-w.written {
		return 0, fmt.Errorf("logical archive exceeds %d bytes", w.limit)
	}
	n, err := w.w.Write(value)
	w.written += int64(n)
	return n, err
}

type digestRows struct {
	rows int64
	hash hash.Hash
}

func newDigestRows() *digestRows { return &digestRows{hash: sha256.New()} }
func (d *digestRows) add(rowDigest [sha256.Size]byte) {
	d.rows++
	_, _ = d.hash.Write(rowDigest[:])
}
func (d *digestRows) sum() string { return hex.EncodeToString(d.hash.Sum(nil)) }

// Export writes a bounded newline-delimited logical archive from one database
// snapshot. It never writes through the supplied transaction.
func Export(ctx context.Context, pool database.Pool, output io.Writer, options ExportOptions) (Footer, error) {
	profile, err := exportProfile(options)
	if err != nil {
		return Footer{}, err
	}
	if err := validateExportOptions(pool.Dialect(), options); err != nil {
		return Footer{}, err
	}
	migrationCount := profile.PostgresMigrations
	if pool.Dialect() == database.SQLServer {
		migrationCount = profile.SQLServerMigrations
	}
	ledgerProvider, ok := pool.(interface {
		ExpectedMigrationChecksums(int) ([]string, error)
	})
	if !ok {
		return Footer{}, fmt.Errorf("logical export requires an embedded migration ledger")
	}
	expectedLedger, err := ledgerProvider.ExpectedMigrationChecksums(migrationCount)
	if err != nil {
		return Footer{}, err
	}
	switch pool.Dialect() {
	case database.Postgres:
		checker, ok := pool.(interface {
			CheckSchemaThrough(context.Context, int) error
		})
		if !ok {
			return Footer{}, fmt.Errorf("logical PostgreSQL export requires exact release-schema verification")
		}
		if err := checker.CheckSchemaThrough(ctx, profile.PostgresMigrations); err != nil {
			return Footer{}, err
		}
	case database.SQLServer:
		checker, ok := pool.(interface {
			CheckSchema(context.Context) error
			CheckDataIntegrity(context.Context) error
		})
		if !ok {
			return Footer{}, fmt.Errorf("logical SQL Server export requires schema and integrity verification")
		}
		if err := checker.CheckSchema(ctx); err != nil {
			return Footer{}, err
		}
		if err := checker.CheckDataIntegrity(ctx); err != nil {
			return Footer{}, err
		}
	default:
		return Footer{}, fmt.Errorf("logical export requires a supported database")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	maxBytes := options.MaxBytes
	if maxBytes == 0 {
		maxBytes = DefaultMaxArchiveBytes
	}
	started := now().UTC().Truncate(time.Microsecond)
	txOptions := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	if pool.Dialect() == database.SQLServer {
		txOptions = &sql.TxOptions{Isolation: sql.LevelSnapshot}
	}
	tx, err := pool.BeginTx(ctx, txOptions)
	if err != nil {
		return Footer{}, fmt.Errorf("begin logical snapshot: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if err := validateCatalogProfile(ctx, tx, profile); err != nil {
		return Footer{}, err
	}
	if err := validateUserIdentities(ctx, tx); err != nil {
		return Footer{}, err
	}
	ledger, err := readLedger(ctx, tx)
	if err != nil {
		return Footer{}, err
	}
	if err := verifyLedger(ledger, expectedLedger); err != nil {
		return Footer{}, err
	}
	exportID, err := randomUUID()
	if err != nil {
		return Footer{}, err
	}
	backend := "postgres"
	if pool.Dialect() == database.SQLServer {
		backend = "azure_sql"
	}
	header := Header{
		Type: "header", Format: FormatVersion, LogicalSchema: profile.LogicalSchema,
		CatalogSHA256: catalogDigest(profile.Catalog), Environment: options.Environment,
		Generation: options.Generation, SourceBackend: backend,
		SourceIdentity: options.SourceIdentity, SourceArtifact: options.SourceArtifact,
		ExportID: exportID, SnapshotStartedAt: formatInstant(started), MigrationLedger: ledger,
	}
	bounded := &countWriter{w: output, limit: maxBytes}
	encoder := json.NewEncoder(bounded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(header); err != nil {
		return Footer{}, fmt.Errorf("write logical archive header: %w", err)
	}
	root := sha256.New()
	_, _ = io.WriteString(root, header.CatalogSHA256+"\n")
	userShops := make(map[string]string)
	for _, table := range profile.Catalog {
		start := TableStart{Type: "table", Table: table.Name, Key: table.Key, Columns: table.Columns}
		if err := encoder.Encode(start); err != nil {
			return Footer{}, fmt.Errorf("write logical table %s: %w", table.Name, err)
		}
		summary, err := exportTable(ctx, tx, table, encoder, userShops)
		if err != nil {
			return Footer{}, err
		}
		if err := encoder.Encode(summary); err != nil {
			return Footer{}, fmt.Errorf("write logical summary %s: %w", table.Name, err)
		}
		rootLine, _ := json.Marshal(summary)
		_, _ = root.Write(rootLine)
		_, _ = root.Write([]byte{'\n'})
	}
	if err := tx.Commit(ctx); err != nil {
		return Footer{}, fmt.Errorf("finish logical snapshot: %w", err)
	}
	footer := Footer{
		Type: "footer", Tables: len(profile.Catalog), RootSHA256: hex.EncodeToString(root.Sum(nil)),
		SnapshotCompletedAt: formatInstant(now().UTC().Truncate(time.Microsecond)),
	}
	if err := encoder.Encode(footer); err != nil {
		return Footer{}, fmt.Errorf("write logical archive footer: %w", err)
	}
	return footer, nil
}

func exportProfile(options ExportOptions) (Profile, error) {
	schema := options.LogicalSchema
	if schema == "" {
		if options.Environment == "production" {
			return Profile{}, fmt.Errorf("production logical export requires an explicit release profile")
		}
		schema = LogicalSchema
	}
	profile, ok := profileBySchema(schema)
	if !ok {
		return Profile{}, fmt.Errorf("unknown logical release profile")
	}
	if (options.Environment == "production") != (profile.LogicalSchema == ProductionV29LogicalSchema) {
		return Profile{}, fmt.Errorf("logical release profile does not match environment")
	}
	return profile, nil
}

func validateUserIdentities(ctx context.Context, queryer database.Queryer) error {
	query := `SELECT email FROM users ORDER BY id`
	if queryer.Dialect() == database.SQLServer {
		query = `SELECT email,email_normalized FROM dbo.users ORDER BY LOWER(CONVERT(char(36),id)) COLLATE Latin1_General_100_BIN2`
	}
	rows, err := queryer.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("validate logical user identities: %w", err)
	}
	defer rows.Close()
	seen := make(map[string]struct{})
	for rows.Next() {
		var email string
		var stored string
		if queryer.Dialect() == database.SQLServer {
			err = rows.Scan(&email, &stored)
		} else {
			err = rows.Scan(&email)
		}
		if err != nil {
			return fmt.Errorf("validate logical user identities: %w", err)
		}
		normalized := strings.ToLower(strings.TrimSpace(email))
		if queryer.Dialect() == database.SQLServer && stored != normalized {
			return fmt.Errorf("SQL Server user identity derivation is incompatible")
		}
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("logical users collide after identity normalization")
		}
		seen[normalized] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("validate logical user identities: %w", err)
	}
	return nil
}

func exportTable(ctx context.Context, queryer database.Queryer, table Table, encoder *json.Encoder, userShops map[string]string) (TableSummary, error) {
	query := selectTableSQL(queryer.Dialect(), table)
	rows, err := queryer.Query(ctx, query)
	if err != nil {
		return TableSummary{}, fmt.Errorf("export logical table %s: %w", table.Name, err)
	}
	defer rows.Close()
	tableDigest := newDigestRows()
	shopDigests := make(map[string]*digestRows)
	for rows.Next() {
		values := make([]sql.NullString, len(table.Columns))
		destinations := make([]any, len(values))
		for i := range values {
			destinations[i] = &values[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			return TableSummary{}, fmt.Errorf("scan logical table %s: %w", table.Name, err)
		}
		canonical := make([]*string, len(values))
		for i, value := range values {
			if !value.Valid {
				if !table.Columns[i].Nullable {
					return TableSummary{}, fmt.Errorf("logical table %s has null %s", table.Name, table.Columns[i].Name)
				}
				continue
			}
			normalized, err := canonicalValue(table.Columns[i].Kind, value.String)
			if err != nil {
				return TableSummary{}, fmt.Errorf("logical table %s column %s: %w", table.Name, table.Columns[i].Name, err)
			}
			canonical[i] = &normalized
		}
		row := Row{Type: "row", Values: canonical}
		rowDigest := digestRow(table, canonical)
		tableDigest.add(rowDigest)
		shopID, err := rowShop(table, canonical, userShops)
		if err != nil {
			return TableSummary{}, err
		}
		if shopID != "" {
			digest := shopDigests[shopID]
			if digest == nil {
				digest = newDigestRows()
				shopDigests[shopID] = digest
			}
			digest.add(rowDigest)
		}
		if table.Name == "users" {
			userShops[valueAt(table, canonical, "id")] = valueAt(table, canonical, "shop_id")
		}
		if err := encoder.Encode(row); err != nil {
			return TableSummary{}, fmt.Errorf("write logical row %s: %w", table.Name, err)
		}
	}
	if err := rows.Err(); err != nil {
		return TableSummary{}, fmt.Errorf("export logical table %s: %w", table.Name, err)
	}
	if err := rows.Close(); err != nil {
		return TableSummary{}, fmt.Errorf("close logical table %s: %w", table.Name, err)
	}
	shops := make([]ShopSummary, 0, len(shopDigests))
	for shopID, digest := range shopDigests {
		shops = append(shops, ShopSummary{ShopID: shopID, Rows: digest.rows, SHA256: digest.sum()})
	}
	sort.Slice(shops, func(i, j int) bool { return shops[i].ShopID < shops[j].ShopID })
	return TableSummary{Type: "table_end", Table: table.Name, Rows: tableDigest.rows, SHA256: tableDigest.sum(), ByShop: shops}, nil
}

func validateExportOptions(dialect database.Dialect, options ExportOptions) error {
	if dialect != database.Postgres && dialect != database.SQLServer {
		return fmt.Errorf("unsupported logical export backend")
	}
	if options.Environment != "staging" && options.Environment != "production" {
		return fmt.Errorf("logical export requires an exact environment")
	}
	if !uuidValue.MatchString(options.Generation) {
		return fmt.Errorf("logical export requires a data generation UUID")
	}
	if options.SourceIdentity == "" || len(options.SourceIdentity) > 512 || options.SourceArtifact == "" || len(options.SourceArtifact) > 512 {
		return fmt.Errorf("logical export requires bounded source identity and artifact")
	}
	if options.MaxBytes < 0 {
		return fmt.Errorf("logical export size limit is invalid")
	}
	return nil
}

func readLedger(ctx context.Context, queryer database.Queryer) ([]LedgerEntry, error) {
	table := "schema_migrations"
	if queryer.Dialect() == database.SQLServer {
		table = "dbo.schema_migrations"
	}
	rows, err := queryer.Query(ctx, "SELECT version,checksum FROM "+table+" ORDER BY version")
	if err != nil {
		return nil, fmt.Errorf("read source migration ledger: %w", err)
	}
	defer rows.Close()
	var result []LedgerEntry
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, fmt.Errorf("read source migration ledger: %w", err)
		}
		checksum = strings.ToLower(strings.TrimSpace(checksum))
		if version <= 0 || len(checksum) != 64 || !hexValue.MatchString(checksum) {
			return nil, fmt.Errorf("source migration ledger is incomplete")
		}
		result = append(result, LedgerEntry{Version: version, Checksum: checksum})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read source migration ledger: %w", err)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("source migration ledger is empty")
	}
	return result, nil
}

func verifyLedger(actual []LedgerEntry, expected []string) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("source migration ledger does not match release profile")
	}
	for index, checksum := range expected {
		if actual[index].Version != index+1 || actual[index].Checksum != checksum {
			return fmt.Errorf("source migration %d checksum does not match embedded release", index+1)
		}
	}
	return nil
}

func selectTableSQL(dialect database.Dialect, table Table) string {
	columns := make([]string, len(table.Columns))
	for i, column := range table.Columns {
		columns[i] = selectColumnSQL(dialect, column)
	}
	keys := make([]string, len(table.Key))
	for i, key := range table.Key {
		var column Column
		for _, candidate := range table.Columns {
			if candidate.Name == key {
				column = candidate
				break
			}
		}
		keys[i] = orderColumnSQL(dialect, column)
	}
	prefix := ""
	if dialect == database.SQLServer {
		prefix = "dbo."
	}
	return "SELECT " + strings.Join(columns, ",") + " FROM " + prefix + quoteIdentifier(dialect, table.Name) + " ORDER BY " + strings.Join(keys, ",")
}

func orderColumnSQL(dialect database.Dialect, column Column) string {
	name := quoteIdentifier(dialect, column.Name)
	if dialect == database.SQLServer {
		switch column.Kind {
		case UUID:
			return "LOWER(CONVERT(char(36)," + name + ")) COLLATE Latin1_General_100_BIN2"
		case Text:
			// Compare the UTF-8 encoding used by PostgreSQL C collation and Go.
			// SQL Server BIN2 compares nvarchar as UTF-16 code units, which
			// reverses some BMP/supplementary pairs.
			return "CONVERT(varbinary(max),CONVERT(varchar(max)," + name + " COLLATE Latin1_General_100_BIN2_UTF8))"
		default:
			return name
		}
	}
	if column.Kind == Text {
		return name + ` COLLATE "C"`
	}
	return name
}

func selectColumnSQL(dialect database.Dialect, column Column) string {
	name := quoteIdentifier(dialect, column.Name)
	if dialect == database.Postgres {
		switch column.Kind {
		case UUID, Text, Integer, Decimal:
			return name + "::text"
		case TextArray, UUIDArray:
			return "array_to_json(" + name + ")::text"
		case JSON:
			return name + "::text"
		case Instant:
			return "to_char(" + name + " AT TIME ZONE 'UTC','YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"')"
		case Date:
			return "to_char(" + name + ",'YYYY-MM-DD')"
		case Boolean:
			return "CASE WHEN " + name + " IS NULL THEN NULL WHEN " + name + " THEN '1' ELSE '0' END"
		case Bytes:
			return "encode(" + name + ",'hex')"
		}
	}
	switch column.Kind {
	case UUID:
		return "LOWER(CONVERT(char(36)," + name + "))"
	case Text, TextArray, UUIDArray, JSON:
		return "CONVERT(nvarchar(max)," + name + ")"
	case Integer:
		return "CONVERT(varchar(32)," + name + ")"
	case Instant:
		return "CONVERT(varchar(33)," + name + ",126)+'Z'"
	case Date:
		return "CONVERT(char(10)," + name + ",23)"
	case Decimal:
		return "CONVERT(varchar(64)," + name + ")"
	case Boolean:
		return "CASE WHEN " + name + " IS NULL THEN NULL WHEN " + name + "=1 THEN '1' ELSE '0' END"
	case Bytes:
		return "CONVERT(varchar(max)," + name + ",2)"
	default:
		panic("unsupported logical column kind")
	}
}

func quoteIdentifier(dialect database.Dialect, value string) string {
	if !identifierValue.MatchString(value) {
		panic("unsafe logical identifier")
	}
	if dialect == database.SQLServer {
		return "[" + value + "]"
	}
	return `"` + value + `"`
}

func canonicalValue(kind Kind, value string) (string, error) {
	switch kind {
	case Text:
		if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
			return "", fmt.Errorf("text is not valid application UTF-8")
		}
		return value, nil
	case UUID:
		value = strings.ToLower(value)
		if !uuidValue.MatchString(value) {
			return "", fmt.Errorf("invalid UUID")
		}
		return value, nil
	case Integer:
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return "", fmt.Errorf("invalid integer")
		}
		return strconv.FormatInt(parsed, 10), nil
	case Boolean:
		if value != "0" && value != "1" {
			return "", fmt.Errorf("invalid boolean")
		}
		return value, nil
	case Instant:
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil || parsed.Nanosecond()%1000 != 0 {
			return "", fmt.Errorf("invalid microsecond UTC instant")
		}
		return formatInstant(parsed), nil
	case Date:
		parsed, err := time.Parse("2006-01-02", value)
		if err != nil || parsed.Format("2006-01-02") != value {
			return "", fmt.Errorf("invalid date")
		}
		return value, nil
	case Decimal:
		return canonicalDecimal(value)
	case Bytes:
		value = strings.ToLower(value)
		if !hexValue.MatchString(value) {
			return "", fmt.Errorf("invalid binary encoding")
		}
		return value, nil
	case JSON, TextArray, UUIDArray:
		return canonicalJSON(kind, value)
	default:
		return "", fmt.Errorf("unsupported logical value kind")
	}
}

func canonicalDecimal(value string) (string, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 2 || len(parts[1]) != 3 {
		return "", fmt.Errorf("invalid decimal(14,3)")
	}
	negative := strings.HasPrefix(parts[0], "-")
	integer := strings.TrimPrefix(parts[0], "-")
	if integer == "" || len(integer) > 11 || !digitsValue.MatchString(integer+parts[1]) {
		return "", fmt.Errorf("invalid decimal(14,3)")
	}
	scaled := new(big.Int)
	if _, ok := scaled.SetString(integer+parts[1], 10); !ok {
		return "", fmt.Errorf("invalid decimal(14,3)")
	}
	if negative {
		scaled.Neg(scaled)
	}
	abs := new(big.Int).Abs(new(big.Int).Set(scaled)).String()
	for len(abs) < 4 {
		abs = "0" + abs
	}
	prefix := ""
	if scaled.Sign() < 0 {
		prefix = "-"
	}
	whole := strings.TrimLeft(abs[:len(abs)-3], "0")
	if whole == "" {
		whole = "0"
	}
	return prefix + whole + "." + abs[len(abs)-3:], nil
}

func canonicalJSON(kind Kind, value string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "", fmt.Errorf("invalid JSON")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("invalid JSON suffix")
	}
	if kind == TextArray || kind == UUIDArray {
		items, ok := decoded.([]any)
		if !ok {
			return "", fmt.Errorf("expected JSON array")
		}
		for index, item := range items {
			if item == nil {
				continue
			}
			text, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("array member is not text")
			}
			if kind == UUIDArray {
				text = strings.ToLower(text)
				if !uuidValue.MatchString(text) {
					return "", fmt.Errorf("array member is not a UUID")
				}
				items[index] = text
			}
		}
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "", fmt.Errorf("encode canonical JSON")
	}
	return string(encoded), nil
}

func digestRow(table Table, values []*string) [sha256.Size]byte {
	payload := struct {
		Table   string    `json:"table"`
		Columns []Column  `json:"columns"`
		Values  []*string `json:"values"`
	}{Table: table.Name, Columns: table.Columns, Values: values}
	encoded, _ := json.Marshal(payload)
	return sha256.Sum256(encoded)
}

func valueAt(table Table, values []*string, column string) string {
	for i := range table.Columns {
		if table.Columns[i].Name == column && values[i] != nil {
			return *values[i]
		}
	}
	return ""
}

func rowShop(table Table, values []*string, userShops map[string]string) (string, error) {
	if table.ShopColumn != "" {
		shop := valueAt(table, values, table.ShopColumn)
		if shop == "" {
			return "", fmt.Errorf("logical table %s has no shop owner", table.Name)
		}
		return shop, nil
	}
	if table.ShopViaUser != "" {
		user := valueAt(table, values, table.ShopViaUser)
		shop := userShops[user]
		if shop == "" {
			return "", fmt.Errorf("logical table %s has an unknown user owner", table.Name)
		}
		return shop, nil
	}
	return "", nil
}

func randomUUID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create logical export ID")
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func formatInstant(value time.Time) string {
	return value.UTC().Truncate(time.Microsecond).Format("2006-01-02T15:04:05.000000Z")
}

// ReadRecord is shared by restore and tests. The limit admits one maximum-size
// normalized photo row while keeping malformed unbounded lines out of memory.
func ReadRecord(reader *bufio.Reader) ([]byte, error) {
	const maxRecord = 12 * 1024 * 1024
	line := make([]byte, 0, min(reader.Size(), maxRecord))
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(fragment) > maxRecord-len(line) {
			return nil, fmt.Errorf("logical archive record exceeds limit")
		}
		line = append(line, fragment...)
		switch err {
		case nil:
			return line, nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			if len(line) > 0 {
				return nil, fmt.Errorf("logical archive is truncated")
			}
			return nil, io.EOF
		default:
			return nil, err
		}
	}
}

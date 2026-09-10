package transfer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

const defaultBatchSize = 100

type RestoreOptions struct {
	Environment            string
	Generation             string
	DestinationIdentity    string
	ExpectedArtifactSHA256 string
	BatchSize              int
	MaxBytes               int64
}

type RestoreResult struct {
	Inspection Inspection
	Resumed    bool
	Verified   bool
}

type restoreState struct {
	ArchiveSHA string
	ExportID   string
	Generation string
	RootSHA    string
	CatalogSHA string
	NextTable  int
	TableRows  int64
	Completed  bool
}

// RestoreSQLServer validates the complete seekable archive before making any
// target changes, then imports bounded transactions into an empty SQL Server
// database. A target-side checkpoint makes rerunning the same artifact safe.
func RestoreSQLServer(ctx context.Context, pool database.Pool, source io.Reader, options RestoreOptions) (RestoreResult, error) {
	if pool.Dialect() != database.SQLServer {
		return RestoreResult{}, fmt.Errorf("logical restore requires SQL Server")
	}
	if options.Environment != "staging" && options.Environment != "production" {
		return RestoreResult{}, fmt.Errorf("logical restore requires an exact environment")
	}
	if !uuidValue.MatchString(options.Generation) {
		return RestoreResult{}, fmt.Errorf("logical restore requires a data generation UUID")
	}
	if options.DestinationIdentity == "" || len(options.DestinationIdentity) > 512 {
		return RestoreResult{}, fmt.Errorf("logical restore requires a bounded destination identity")
	}
	if len(options.ExpectedArtifactSHA256) != 64 || !hexValue.MatchString(options.ExpectedArtifactSHA256) {
		return RestoreResult{}, fmt.Errorf("logical restore requires an authenticated artifact SHA-256")
	}
	batchSize := options.BatchSize
	if batchSize == 0 {
		batchSize = defaultBatchSize
	}
	if batchSize < 1 || batchSize > 1000 {
		return RestoreResult{}, fmt.Errorf("logical restore batch size must be between 1 and 1000")
	}
	checker, ok := pool.(interface {
		CheckSchema(context.Context) error
		CheckDataIntegrity(context.Context) error
	})
	if !ok {
		return RestoreResult{}, fmt.Errorf("logical restore requires SQL Server schema and integrity verification")
	}
	if err := checker.CheckSchema(ctx); err != nil {
		return RestoreResult{}, err
	}
	if err := checker.CheckDataIntegrity(ctx); err != nil {
		return RestoreResult{}, err
	}
	spooled, err := spoolArchive(source, options.MaxBytes)
	if err != nil {
		return RestoreResult{}, err
	}
	defer spooled.Close()
	inspection, err := Inspect(spooled, options.MaxBytes)
	if err != nil {
		return RestoreResult{}, err
	}
	if inspection.ArtifactSHA256 != options.ExpectedArtifactSHA256 {
		return RestoreResult{}, fmt.Errorf("logical archive does not match its authenticated artifact digest")
	}
	if inspection.Header.Environment != options.Environment || inspection.Header.Generation != options.Generation {
		return RestoreResult{}, fmt.Errorf("logical archive does not match target environment and generation")
	}
	state, resumed, err := initializeRestore(ctx, pool, inspection)
	if err != nil {
		return RestoreResult{}, err
	}
	result := RestoreResult{Inspection: inspection, Resumed: resumed}
	if !state.Completed {
		if _, err := spooled.Seek(0, io.SeekStart); err != nil {
			return result, fmt.Errorf("rewind logical archive: %w", err)
		}
		visitor := &restoreVisitor{ctx: ctx, pool: pool, inspection: inspection, state: state, batchSize: batchSize}
		walked, err := walkArchive(spooled, options.MaxBytes, visitor)
		if err != nil {
			return result, err
		}
		if walked.ArtifactSHA256 != inspection.ArtifactSHA256 {
			return result, fmt.Errorf("logical archive changed between validation and restore")
		}
	}
	verified, err := Export(ctx, pool, io.Discard, ExportOptions{
		Environment: options.Environment, Generation: options.Generation,
		SourceIdentity: options.DestinationIdentity,
		SourceArtifact: "restored:" + inspection.ArtifactSHA256,
		LogicalSchema:  inspection.Header.LogicalSchema,
		MaxBytes:       options.MaxBytes,
	})
	if err != nil {
		return result, fmt.Errorf("verify restored logical data: %w", err)
	}
	if verified.RootSHA256 != inspection.Footer.RootSHA256 {
		return result, fmt.Errorf("restored logical root does not match source")
	}
	if err := checker.CheckDataIntegrity(ctx); err != nil {
		return result, fmt.Errorf("verify restored SQL Server integrity: %w", err)
	}
	result.Verified = true
	return result, nil
}

func spoolArchive(source io.Reader, maxBytes int64) (*os.File, error) {
	if maxBytes == 0 {
		maxBytes = DefaultMaxArchiveBytes
	}
	if maxBytes < 0 {
		return nil, fmt.Errorf("logical archive size limit is invalid")
	}
	file, err := os.CreateTemp("", "sideyard-logical-*.ndjson")
	if err != nil {
		return nil, fmt.Errorf("create private logical restore spool: %w", err)
	}
	name := file.Name()
	cleanup := func() {
		file.Close()
		_ = os.Remove(name)
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return nil, fmt.Errorf("protect logical restore spool: %w", err)
	}
	// Unlink immediately. Only this process's descriptor can mutate or reopen
	// the validated bytes during the resumable import.
	if err := os.Remove(name); err != nil {
		cleanup()
		return nil, fmt.Errorf("unlink logical restore spool: %w", err)
	}
	written, err := io.Copy(file, io.LimitReader(source, maxBytes+1))
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("copy logical restore spool: %w", err)
	}
	if written > maxBytes {
		file.Close()
		return nil, fmt.Errorf("logical archive exceeds %d bytes", maxBytes)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, fmt.Errorf("sync logical restore spool: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, fmt.Errorf("rewind logical restore spool: %w", err)
	}
	return file, nil
}

func initializeRestore(ctx context.Context, pool database.Pool, inspection Inspection) (restoreState, bool, error) {
	state, err := readRestoreState(ctx, pool)
	if err == nil {
		if err := state.matches(inspection); err != nil {
			return restoreState{}, false, err
		}
		return state, true, nil
	}
	if !errors.Is(err, database.ErrNoRows) {
		return restoreState{}, false, err
	}
	tx, err := pool.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return restoreState{}, false, fmt.Errorf("begin logical restore initialization: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	for _, table := range Catalog {
		var exists int
		if err := tx.QueryRow(ctx, "SELECT CASE WHEN EXISTS(SELECT TOP(1) 1 FROM dbo."+quoteIdentifier(database.SQLServer, table.Name)+") THEN 1 ELSE 0 END").Scan(&exists); err != nil {
			return restoreState{}, false, fmt.Errorf("check logical target table %s: %w", table.Name, err)
		}
		if exists != 0 {
			return restoreState{}, false, fmt.Errorf("logical restore target is not empty: %s has rows", table.Name)
		}
	}
	state = restoreState{
		ArchiveSHA: inspection.ArtifactSHA256, ExportID: inspection.Header.ExportID,
		Generation: inspection.Header.Generation, RootSHA: inspection.Footer.RootSHA256,
		CatalogSHA: inspection.Header.CatalogSHA256,
	}
	if _, err := tx.Exec(ctx, `INSERT INTO sideyard_transfer.restore_state
		(singleton,archive_sha,export_id,generation,root_sha,catalog_sha,next_table,table_rows,completed,updated_at)
		VALUES(1,@p1,CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),@p4,@p5,0,0,0,SYSUTCDATETIME())`,
		state.ArchiveSHA, state.ExportID, state.Generation, state.RootSHA, state.CatalogSHA); err != nil {
		return restoreState{}, false, fmt.Errorf("initialize logical restore checkpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return restoreState{}, false, fmt.Errorf("commit logical restore initialization: %w", err)
	}
	return state, false, nil
}

func readRestoreState(ctx context.Context, queryer database.Queryer) (restoreState, error) {
	var state restoreState
	err := queryer.QueryRow(ctx, `SELECT archive_sha,LOWER(CONVERT(char(36),export_id)),LOWER(CONVERT(char(36),generation)),
		root_sha,catalog_sha,next_table,table_rows,completed
		FROM sideyard_transfer.restore_state WHERE singleton=1`).Scan(
		&state.ArchiveSHA, &state.ExportID, &state.Generation, &state.RootSHA,
		&state.CatalogSHA, &state.NextTable, &state.TableRows, &state.Completed,
	)
	if err != nil {
		return restoreState{}, err
	}
	return state, nil
}

func (state restoreState) matches(inspection Inspection) error {
	if state.ArchiveSHA != inspection.ArtifactSHA256 || state.ExportID != inspection.Header.ExportID ||
		state.Generation != inspection.Header.Generation || state.RootSHA != inspection.Footer.RootSHA256 ||
		state.CatalogSHA != inspection.Header.CatalogSHA256 {
		return fmt.Errorf("logical restore checkpoint belongs to another archive")
	}
	tables := inspection.Footer.Tables
	if state.NextTable < 0 || state.NextTable > tables || state.TableRows < 0 || (state.Completed && (state.NextTable != tables || state.TableRows != 0)) {
		return fmt.Errorf("logical restore checkpoint is invalid")
	}
	return nil
}

type restoreVisitor struct {
	ctx        context.Context
	pool       database.Pool
	inspection Inspection
	state      restoreState
	batchSize  int
	rows       []Row
}

func (visitor *restoreVisitor) Begin(header Header) error {
	if header.ExportID != visitor.inspection.Header.ExportID {
		return fmt.Errorf("logical archive changed before restore")
	}
	return nil
}

func (visitor *restoreVisitor) BeginTable(index int, _ Table) error {
	visitor.rows = visitor.rows[:0]
	if visitor.state.NextTable < index {
		return fmt.Errorf("logical restore checkpoint skipped a table")
	}
	return nil
}

func (visitor *restoreVisitor) VisitRow(index int, table Table, rowNumber int64, row Row) error {
	if index < visitor.state.NextTable || rowNumber <= visitor.state.TableRows {
		return nil
	}
	if index != visitor.state.NextTable {
		return fmt.Errorf("logical restore row is ahead of its checkpoint")
	}
	visitor.rows = append(visitor.rows, row)
	if len(visitor.rows) == visitor.batchSize {
		return visitor.commitRows(table, false)
	}
	return nil
}

func (visitor *restoreVisitor) EndTable(index int, table Table, summary TableSummary) error {
	if index < visitor.state.NextTable {
		return nil
	}
	if index != visitor.state.NextTable || visitor.state.TableRows+int64(len(visitor.rows)) != summary.Rows {
		return fmt.Errorf("logical restore checkpoint row count for %s is invalid", table.Name)
	}
	return visitor.commitRows(table, true)
}

func (visitor *restoreVisitor) End(footer Footer) error {
	tables := visitor.inspection.Footer.Tables
	if visitor.state.NextTable != tables || visitor.state.TableRows != 0 {
		return fmt.Errorf("logical restore did not complete every table")
	}
	if !visitor.state.Completed {
		tx, err := visitor.pool.Begin(visitor.ctx)
		if err != nil {
			return fmt.Errorf("begin logical restore completion: %w", err)
		}
		defer tx.Rollback(context.WithoutCancel(visitor.ctx))
		result, err := tx.Exec(visitor.ctx, `UPDATE sideyard_transfer.restore_state SET completed=1,updated_at=SYSUTCDATETIME()
			WHERE singleton=1 AND archive_sha=@p1 AND next_table=@p2 AND table_rows=0 AND completed=0`, visitor.state.ArchiveSHA, tables)
		if err != nil || result.RowsAffected() != 1 {
			if err == nil {
				err = fmt.Errorf("checkpoint changed")
			}
			return fmt.Errorf("complete logical restore: %w", err)
		}
		if err := tx.Commit(visitor.ctx); err != nil {
			return visitor.acceptUnknownCommit(tables, 0, true, err)
		}
		visitor.state.Completed = true
	}
	return nil
}

func (visitor *restoreVisitor) commitRows(table Table, completeTable bool) error {
	if len(visitor.rows) == 0 && !completeTable {
		return nil
	}
	tx, err := visitor.pool.Begin(visitor.ctx)
	if err != nil {
		return fmt.Errorf("begin logical restore batch for %s: %w", table.Name, err)
	}
	defer tx.Rollback(context.WithoutCancel(visitor.ctx))
	statement := insertSQL(table)
	for _, row := range visitor.rows {
		if _, err := tx.Exec(visitor.ctx, statement, insertArgs(table, row)...); err != nil {
			return fmt.Errorf("restore logical row in %s: %w", table.Name, err)
		}
	}
	nextTable := visitor.state.NextTable
	nextRows := visitor.state.TableRows + int64(len(visitor.rows))
	if completeTable {
		nextTable++
		nextRows = 0
	}
	result, err := tx.Exec(visitor.ctx, `UPDATE sideyard_transfer.restore_state
		SET next_table=@p1,table_rows=@p2,updated_at=SYSUTCDATETIME()
		WHERE singleton=1 AND archive_sha=@p3 AND next_table=@p4 AND table_rows=@p5 AND completed=0`,
		nextTable, nextRows, visitor.state.ArchiveSHA, visitor.state.NextTable, visitor.state.TableRows)
	if err != nil || result.RowsAffected() != 1 {
		if err == nil {
			err = fmt.Errorf("checkpoint changed")
		}
		return fmt.Errorf("advance logical restore checkpoint for %s: %w", table.Name, err)
	}
	if err := tx.Commit(visitor.ctx); err != nil {
		if accepted := visitor.acceptUnknownCommit(nextTable, nextRows, false, err); accepted != nil {
			return accepted
		}
	} else {
		visitor.state.NextTable = nextTable
		visitor.state.TableRows = nextRows
	}
	visitor.rows = visitor.rows[:0]
	return nil
}

func (visitor *restoreVisitor) acceptUnknownCommit(nextTable int, nextRows int64, completed bool, commitErr error) error {
	reconcile, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	state, err := readRestoreState(reconcile, visitor.pool)
	if err == nil && state.ArchiveSHA == visitor.state.ArchiveSHA && state.NextTable == nextTable && state.TableRows == nextRows && state.Completed == completed {
		visitor.state = state
		return nil
	}
	return fmt.Errorf("commit logical restore checkpoint has unknown outcome: %w", commitErr)
}

func insertSQL(table Table) string {
	columns := make([]string, 0, len(table.Columns)+1)
	values := make([]string, 0, len(table.Columns)+1)
	for index, column := range table.Columns {
		columns = append(columns, quoteIdentifier(database.SQLServer, column.Name))
		values = append(values, insertParameterSQL(column.Kind, index+1))
	}
	if table.Name == "users" {
		columns = append(columns, "[email_normalized]")
		values = append(values, fmt.Sprintf("@p%d", len(values)+1))
	}
	return "INSERT INTO dbo." + quoteIdentifier(database.SQLServer, table.Name) + " (" + strings.Join(columns, ",") + ") VALUES (" + strings.Join(values, ",") + ")"
}

func insertParameterSQL(kind Kind, index int) string {
	parameter := fmt.Sprintf("@p%d", index)
	switch kind {
	case UUID:
		return "CONVERT(uniqueidentifier," + parameter + ")"
	case Integer:
		return "CONVERT(bigint," + parameter + ")"
	case Instant:
		return "CONVERT(datetime2(6)," + parameter + ",127)"
	case Date:
		return "CONVERT(date," + parameter + ",23)"
	case Decimal:
		return "CONVERT(decimal(14,3)," + parameter + ")"
	case Boolean:
		return "CONVERT(bit," + parameter + ")"
	case Bytes:
		return "CONVERT(varbinary(max)," + parameter + ",2)"
	default:
		return parameter
	}
}

func insertArgs(table Table, row Row) []any {
	arguments := make([]any, len(row.Values), len(row.Values)+1)
	for index, value := range row.Values {
		if value != nil {
			arguments[index] = *value
		}
	}
	if table.Name == "users" {
		arguments = append(arguments, strings.ToLower(strings.TrimSpace(valueAt(table, row.Values, "email"))))
	}
	return arguments
}

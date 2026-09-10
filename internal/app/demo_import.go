package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

const (
	publicDemoMaxImportCSVBytes  = 128 << 10
	publicDemoMaxImportRows      = 100
	publicDemoMaxImportCellBytes = 2000
	publicDemoImportRowBudget    = 500
	publicDemoOccurrenceBudget   = 20000
	publicDemoImportPreviewTTL   = 15 * time.Minute

	// Multipart framing, the CSRF token, and the step field need a little room
	// above the accepted file limit. The file itself is independently bounded.
	publicDemoMaxImportRequestBytes = publicDemoMaxImportCSVBytes + 32<<10
)

var (
	errPublicDemoImportPreviewInvalid    = errors.New("public demo import preview is invalid")
	errPublicDemoImportRowBudgetReached  = errors.New("public demo import row budget reached")
	errPublicDemoOccurrenceBudgetReached = errors.New("public demo occurrence budget reached")
)

type PublicDemoImportPayload struct {
	CSV                  []byte
	RowCount             int
	ProjectedOccurrences int
	ProjectionDate       time.Time
}

func (s *Store) CreatePublicDemoImportPreview(
	ctx context.Context,
	shopID, tokenHash string,
	raw []byte,
	rowCount, projectedOccurrences int,
	projectionDate time.Time,
) error {
	if validateRecordIDs(shopID) != nil || !validStoredText(tokenHash) || tokenHash == "" || len(raw) == 0 || len(raw) > publicDemoMaxImportCSVBytes ||
		rowCount < 1 || rowCount > publicDemoMaxImportRows ||
		projectedOccurrences < 0 || projectedOccurrences > publicDemoMaxImportRows*121 || projectionDate.IsZero() || projectionDate.UTC().Year() < 1 || projectionDate.UTC().Year() > 9999 {
		return errPublicDemoImportPreviewInvalid
	}
	projectionDate = time.Date(
		projectionDate.UTC().Year(), projectionDate.UTC().Month(), projectionDate.UTC().Day(),
		0, 0, 0, 0, time.UTC,
	)

	now := s.nowUTC().Truncate(time.Microsecond)
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin public demo import preview: %w", err)
	}
	defer tx.Rollback(ctx)

	var sandboxExpiry time.Time
	var rowsUsed, occurrencesUsed int
	if err := tx.QueryRow(ctx, database.Statement(tx.Dialect(), `
		SELECT expires_at, demo_import_rows_used, demo_import_occurrences_used
		FROM shops
		WHERE id = $1 AND kind = 'demo_sandbox' AND expires_at > $2
		FOR UPDATE`, `SELECT expires_at,demo_import_rows_used,demo_import_occurrences_used FROM dbo.shops WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1) AND kind='demo_sandbox' AND expires_at>@p2`), shopID, now).Scan(&sandboxExpiry, &rowsUsed, &occurrencesUsed); err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return errPublicDemoImportPreviewInvalid
		}
		return fmt.Errorf("lock public demo for import preview: %w", err)
	}
	if rowsUsed > publicDemoImportRowBudget-rowCount {
		return errPublicDemoImportRowBudgetReached
	}
	if occurrencesUsed > publicDemoOccurrenceBudget-projectedOccurrences {
		return errPublicDemoOccurrenceBudgetReached
	}

	expiresAt := now.Add(publicDemoImportPreviewTTL)
	if sandboxExpiry.Before(expiresAt) {
		expiresAt = sandboxExpiry
	}
	if _, err := tx.Exec(ctx, database.Statement(tx.Dialect(), `DELETE FROM demo_import_previews WHERE shop_id = $1`, `DELETE FROM dbo.demo_import_previews WHERE shop_id=CONVERT(uniqueidentifier,@p1)`), shopID); err != nil {
		return fmt.Errorf("replace public demo import preview: %w", err)
	}
	if _, err := tx.Exec(ctx, database.Statement(tx.Dialect(), `
		INSERT INTO demo_import_previews (
			token_hash, shop_id, csv_data, row_count, projected_occurrences, projection_date, expires_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)`, `INSERT INTO dbo.demo_import_previews(token_hash,shop_id,csv_data,row_count,projected_occurrences,projection_date,expires_at) VALUES(@p1,CONVERT(uniqueidentifier,@p2),@p3,@p4,@p5,CONVERT(date,@p6),@p7)`),
		tokenHash, shopID, raw, rowCount, projectedOccurrences, projectionDate.Format("2006-01-02"), expiresAt); err != nil {
		return fmt.Errorf("create public demo import preview: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit public demo import preview: %w", err)
	}
	return nil
}

// ConsumePublicDemoImportPreview atomically makes a preview single-use and
// reserves its row fan-out. Admitted attempts remain charged if the later
// domain import fails, matching the public-demo mutation budget's fail-closed
// abuse semantics.
func (s *Store) ConsumePublicDemoImportPreview(ctx context.Context, shopID, tokenHash string) (PublicDemoImportPayload, error) {
	if validateRecordIDs(shopID) != nil || !validStoredText(tokenHash) || tokenHash == "" {
		return PublicDemoImportPayload{}, errPublicDemoImportPreviewInvalid
	}

	now := s.nowUTC().Truncate(time.Microsecond)
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return PublicDemoImportPayload{}, fmt.Errorf("begin public demo import commit: %w", err)
	}
	defer tx.Rollback(ctx)

	var activeShopID string
	var rowsUsed, occurrencesUsed int
	if err := tx.QueryRow(ctx, database.Statement(tx.Dialect(), `
		SELECT id, demo_import_rows_used, demo_import_occurrences_used
		FROM shops
		WHERE id = $1 AND kind = 'demo_sandbox' AND expires_at > $2
		FOR UPDATE`, `SELECT LOWER(CONVERT(char(36),id)),demo_import_rows_used,demo_import_occurrences_used FROM dbo.shops WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1) AND kind='demo_sandbox' AND expires_at>@p2`), shopID, now).Scan(&activeShopID, &rowsUsed, &occurrencesUsed); err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return PublicDemoImportPayload{}, errPublicDemoImportPreviewInvalid
		}
		return PublicDemoImportPayload{}, fmt.Errorf("lock public demo for import commit: %w", err)
	}

	var payload PublicDemoImportPayload
	if err := tx.QueryRow(ctx, database.Statement(tx.Dialect(), `
		DELETE FROM demo_import_previews
		WHERE token_hash = $1
		  AND shop_id = $2
		  AND expires_at > $3
		RETURNING csv_data, row_count, projected_occurrences, projection_date`, `DELETE FROM dbo.demo_import_previews OUTPUT DELETED.csv_data,DELETED.row_count,DELETED.projected_occurrences,DELETED.projection_date
 WHERE token_key=CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),CONVERT(nvarchar(max),@p1)))) AND token_hash=@p1 AND DATALENGTH(token_hash)=DATALENGTH(@p1) AND shop_id=CONVERT(uniqueidentifier,@p2) AND expires_at>@p3`), tokenHash, shopID, now).Scan(
		&payload.CSV, &payload.RowCount, &payload.ProjectedOccurrences, &payload.ProjectionDate,
	); err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return PublicDemoImportPayload{}, errPublicDemoImportPreviewInvalid
		}
		return PublicDemoImportPayload{}, fmt.Errorf("consume public demo import preview: %w", err)
	}
	if rowsUsed > publicDemoImportRowBudget-payload.RowCount {
		return PublicDemoImportPayload{}, errPublicDemoImportRowBudgetReached
	}
	if occurrencesUsed > publicDemoOccurrenceBudget-payload.ProjectedOccurrences {
		return PublicDemoImportPayload{}, errPublicDemoOccurrenceBudgetReached
	}

	result, err := tx.Exec(ctx, database.Statement(tx.Dialect(), `
		UPDATE shops
		SET demo_import_rows_used = demo_import_rows_used + $2,
			demo_import_occurrences_used = demo_import_occurrences_used + $3
		WHERE id = $1
		  AND kind = 'demo_sandbox'
		  AND expires_at > $4
		  AND $2 BETWEEN 1 AND $5
		  AND $3 BETWEEN 0 AND $6
		  AND demo_import_rows_used <= $7 - $2
		  AND demo_import_occurrences_used <= $8 - $3`, `UPDATE dbo.shops SET demo_import_rows_used=demo_import_rows_used+@p2,demo_import_occurrences_used=demo_import_occurrences_used+@p3 WHERE id=CONVERT(uniqueidentifier,@p1) AND kind='demo_sandbox' AND expires_at>@p4 AND @p2 BETWEEN 1 AND @p5 AND @p3 BETWEEN 0 AND @p6 AND demo_import_rows_used<=@p7-@p2 AND demo_import_occurrences_used<=@p8-@p3`),
		shopID, payload.RowCount, payload.ProjectedOccurrences, now,
		publicDemoMaxImportRows, publicDemoMaxImportRows*121,
		publicDemoImportRowBudget, publicDemoOccurrenceBudget)
	if err != nil {
		return PublicDemoImportPayload{}, fmt.Errorf("reserve public demo import capacity: %w", err)
	}
	if result.RowsAffected() != 1 {
		return PublicDemoImportPayload{}, errPublicDemoImportPreviewInvalid
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicDemoImportPayload{}, fmt.Errorf("commit public demo import controls: %w", err)
	}
	return payload, nil
}

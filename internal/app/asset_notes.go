package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

var errAssetNoteInput = errInternalNoteInput
var errAssetNoteConflict = errors.New("This form was already used for a different note, or the asset is unavailable. Start a new note to preserve the earlier entry.")

func (s *Store) GetAssetHistoryTarget(ctx context.Context, shopID, kind, id string) (historyTarget, error) {
	if err := validateRecordIDs(shopID, id); err != nil {
		return historyTarget{}, err
	}
	var target historyTarget
	var query string
	switch kind {
	case "vehicle":
		query = database.Statement(s.db.Pool.Dialect(), `SELECT v.id,v.name,v.customer_id FROM customer_vehicles v WHERE v.shop_id=$1 AND v.id=$2`, `SELECT LOWER(CONVERT(char(36),v.id)),v.name,LOWER(CONVERT(char(36),v.customer_id)) FROM dbo.customer_vehicles v WHERE v.shop_id=CONVERT(uniqueidentifier,@p1) AND v.id=CONVERT(uniqueidentifier,@p2)`)
	case "equipment":
		query = database.Statement(s.db.Pool.Dialect(), `SELECT e.id,e.kind,p.customer_id FROM equipment e JOIN properties p ON p.id=e.property_id AND p.shop_id=e.shop_id WHERE e.shop_id=$1 AND e.id=$2`, `SELECT LOWER(CONVERT(char(36),e.id)),e.kind,LOWER(CONVERT(char(36),p.customer_id)) FROM dbo.equipment e JOIN dbo.properties p ON p.id=e.property_id AND p.shop_id=e.shop_id WHERE e.shop_id=CONVERT(uniqueidentifier,@p1) AND e.id=CONVERT(uniqueidentifier,@p2)`)
	default:
		return target, errAssetNoteInput
	}
	err := s.db.Pool.QueryRow(ctx, query, shopID, id).Scan(&target.ID, &target.Name, &target.CustomerID)
	target.BackURL = "/customers/" + target.CustomerID + "#vehicles"
	if kind == "equipment" {
		target.BackURL = "/equipment/" + target.ID
	}
	return target, err
}

func validateAssetNote(kind string, input internalNoteInput, now time.Time) (eventTime, error) {
	if kind != "vehicle" && kind != "equipment" {
		return eventTime{}, errAssetNoteInput
	}
	return validateInternalNote(input, now)
}

func (s *Store) AddAssetNote(ctx context.Context, shopID, kind, id, author string, input internalNoteInput) error {
	if err := validateRecordIDs(shopID, id); err != nil {
		return err
	}
	if !validStoredText(author) {
		return errAssetNoteInput
	}
	event, err := validateAssetNote(kind, input, s.nowUTC())
	if err != nil {
		return err
	}
	if s.db.Pool.Dialect() == database.SQLServer {
		return s.addSQLServerAssetNote(ctx, shopID, kind, id, author, input, event)
	}
	body := strings.TrimSpace(input.Body)
	tag, err := s.db.Pool.Exec(ctx, `WITH target AS (
	 SELECT id AS vehicle_id,NULL::uuid AS equipment_id FROM customer_vehicles WHERE shop_id=$1 AND id=$3 AND $2='vehicle'
	 UNION ALL SELECT NULL::uuid,id FROM equipment WHERE shop_id=$1 AND id=$3 AND $2='equipment')
	 INSERT INTO asset_notes(shop_id,vehicle_id,equipment_id,submission_key,body,occurred_on,occurred_at,event_timezone,recorded_by)
	 SELECT $1,vehicle_id,equipment_id,$4,$5,$6,$7,$8,$9 FROM target
	 ON CONFLICT(shop_id,submission_key) DO UPDATE SET submission_key=EXCLUDED.submission_key
	 WHERE asset_notes.vehicle_id IS NOT DISTINCT FROM EXCLUDED.vehicle_id
	 AND asset_notes.equipment_id IS NOT DISTINCT FROM EXCLUDED.equipment_id
	 AND asset_notes.body=EXCLUDED.body AND asset_notes.occurred_on=EXCLUDED.occurred_on
	 AND asset_notes.occurred_at IS NOT DISTINCT FROM EXCLUDED.occurred_at
	 AND asset_notes.event_timezone=EXCLUDED.event_timezone AND asset_notes.recorded_by=EXCLUDED.recorded_by`, shopID, kind, id, input.Key, body, event.Date, event.At, event.Zone, author)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errAssetNoteConflict
	}
	return nil
}

func (s *Store) ListAssetNotes(ctx context.Context, shopID, kind, id string) ([]internalNote, error) {
	if kind != "vehicle" && kind != "equipment" {
		return nil, errAssetNoteInput
	}
	if err := validateRecordIDs(shopID, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at FROM asset_notes WHERE shop_id=$1 AND (($2='vehicle' AND vehicle_id=$3) OR ($2='equipment' AND equipment_id=$3)) ORDER BY occurred_on DESC,recorded_at DESC,id DESC LIMIT 100`, `SELECT TOP(100) body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at FROM dbo.asset_notes WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND ((@p2=N'vehicle' AND vehicle_id=CONVERT(uniqueidentifier,@p3)) OR (@p2=N'equipment' AND equipment_id=CONVERT(uniqueidentifier,@p3))) ORDER BY occurred_on DESC,recorded_at DESC,CONVERT(char(36),id) COLLATE Latin1_General_100_BIN2 DESC`), shopID, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []internalNote
	for rows.Next() {
		var n internalNote
		if err := rows.Scan(&n.Body, &n.Event.Date, &n.Event.At, &n.Event.Zone, &n.RecordedBy, &n.RecordedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

// Match cascading deletion: retain the shop, then asset and submission key locks.
// Triggers cover direct SQL writers; application writes avoid the inverse order.
func (s *Store) addSQLServerAssetNote(ctx context.Context, shopID, kind, id, author string, input internalNoteInput, event eventTime) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var shop string
	if err := tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.shops WITH(HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, shopID).Scan(&shop); err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return errAssetNoteConflict
		}
		return err
	}
	parent := `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.customer_vehicles WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`
	if kind == "equipment" {
		parent = `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.equipment WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`
	}
	var locked string
	if err := tx.QueryRow(ctx, parent, shopID, id).Scan(&locked); err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return errAssetNoteConflict
		}
		return err
	}
	var vehicle, equipment, body, zone, previousAuthor string
	var date time.Time
	var at *time.Time
	err = tx.QueryRow(ctx, `SELECT COALESCE(LOWER(CONVERT(char(36),vehicle_id)),''),COALESCE(LOWER(CONVERT(char(36),equipment_id)),''),body,occurred_on,occurred_at,event_timezone,recorded_by FROM dbo.asset_notes WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND submission_key=@p2 AND DATALENGTH(submission_key)=DATALENGTH(@p2)`, shopID, input.Key).Scan(&vehicle, &equipment, &body, &date, &at, &zone, &previousAuthor)
	if err == nil {
		sameAsset := (kind == "vehicle" && strings.EqualFold(vehicle, id) && equipment == "") || (kind == "equipment" && strings.EqualFold(equipment, id) && vehicle == "")
		sameInstant := at == nil && event.At == nil
		if at != nil && event.At != nil {
			sameInstant = at.Equal(*event.At)
		}
		if !sameAsset || body != strings.TrimSpace(input.Body) || date.Format("2006-01-02") != event.Date.Format("2006-01-02") || !sameInstant || zone != event.Zone || previousAuthor != author {
			return errAssetNoteConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, database.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO dbo.asset_notes(shop_id,vehicle_id,equipment_id,submission_key,body,occurred_on,occurred_at,event_timezone,recorded_by) VALUES(CONVERT(uniqueidentifier,@p1),CASE WHEN @p2=N'vehicle' THEN CONVERT(uniqueidentifier,@p3) ELSE NULL END,CASE WHEN @p2=N'equipment' THEN CONVERT(uniqueidentifier,@p3) ELSE NULL END,@p4,@p5,@p6,@p7,@p8,@p9)`, shopID, kind, id, input.Key, strings.TrimSpace(input.Body), event.Date, event.At, event.Zone, author)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

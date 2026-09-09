package app

import (
	"context"
	"errors"
	"strings"
	"time"
)

var errAssetNoteInput = errInternalNoteInput
var errAssetNoteConflict = errors.New("This form was already used for a different note, or the asset is unavailable. Start a new note to preserve the earlier entry.")

func (s *Store) GetAssetHistoryTarget(ctx context.Context, shopID, kind, id string) (historyTarget, error) {
	var target historyTarget
	var query string
	switch kind {
	case "vehicle":
		query = `SELECT v.id,v.name,v.customer_id FROM customer_vehicles v WHERE v.shop_id=$1 AND v.id=$2`
	case "equipment":
		query = `SELECT e.id,e.kind,p.customer_id FROM equipment e JOIN properties p ON p.id=e.property_id AND p.shop_id=e.shop_id WHERE e.shop_id=$1 AND e.id=$2`
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
	event, err := validateAssetNote(kind, input, s.nowUTC())
	if err != nil {
		return err
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
	rows, err := s.db.Pool.Query(ctx, `SELECT body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at FROM asset_notes WHERE shop_id=$1 AND (($2='vehicle' AND vehicle_id=$3) OR ($2='equipment' AND equipment_id=$3)) ORDER BY occurred_on DESC,recorded_at DESC,id DESC LIMIT 100`, shopID, kind, id)
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

package app

import (
	"context"
	"errors"
	"strings"
	"time"
)

type assetNoteInput struct{ Key, Body, Date, Time, Zone, Offset string }
type assetNote struct {
	Body, RecordedBy string
	Event            eventTime
	RecordedAt       time.Time
}

func (n assetNote) EventLabel() string {
	if n.Event.At == nil {
		return n.Event.Date.Format("Jan 2, 2006") + " · Date only · " + n.Event.Zone
	}
	loc, err := time.LoadLocation(n.Event.Zone)
	if err != nil {
		return n.Event.At.UTC().Format(time.RFC3339)
	}
	at := n.Event.At.In(loc)
	return at.Format("Jan 2, 2006 15:04 MST") + " (UTC" + eventOffset(at) + ")"
}

var errAssetNoteInput = errors.New("Enter a note up to 2,000 bytes. Reload the page if this form is out of date.")
var errAssetNoteConflict = errors.New("This form was already used for a different note, or the asset is unavailable. Start a new note to preserve the earlier entry.")

type assetHistoryTarget struct{ ID, Name, CustomerID, BackURL string }

func (s *Store) GetAssetHistoryTarget(ctx context.Context, shopID, kind, id string) (assetHistoryTarget, error) {
	var target assetHistoryTarget
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

func validateAssetNote(kind string, input assetNoteInput, now time.Time) (eventTime, error) {
	body := strings.TrimSpace(input.Body)
	if (kind != "vehicle" && kind != "equipment") || !noteSubmissionKey.MatchString(input.Key) || body == "" || len(body) > 2000 || strings.ContainsRune(body, '\x00') {
		return eventTime{}, errAssetNoteInput
	}
	return parseEventTime(input.Date, input.Time, input.Zone, input.Offset, now)
}

func (s *Store) AddAssetNote(ctx context.Context, shopID, kind, id, author string, input assetNoteInput) error {
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

func (s *Store) ListAssetNotes(ctx context.Context, shopID, kind, id string) ([]assetNote, error) {
	rows, err := s.db.Pool.Query(ctx, `SELECT body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at FROM asset_notes WHERE shop_id=$1 AND (($2='vehicle' AND vehicle_id=$3) OR ($2='equipment' AND equipment_id=$3)) ORDER BY occurred_on DESC,recorded_at DESC,id DESC LIMIT 100`, shopID, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []assetNote
	for rows.Next() {
		var n assetNote
		if err := rows.Scan(&n.Body, &n.Event.Date, &n.Event.At, &n.Event.Zone, &n.RecordedBy, &n.RecordedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

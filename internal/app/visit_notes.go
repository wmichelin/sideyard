package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

var errVisitNoteConflict = errors.New("This form was already used for a different note, or the visit is unavailable. Start a new note to preserve the earlier entry.")

func (s *Store) GetVisitHistoryTarget(ctx context.Context, shopID, kind, id string) (historyTarget, error) {
	var target historyTarget
	target.BackURL = "/visits?filter=all"
	switch kind {
	case "recurring":
		var service, customer string
		var date time.Time
		err := s.db.Pool.QueryRow(ctx, `SELECT o.id,sc.service_name,c.name,c.id,COALESCE(o.scheduled_on,o.due_on)
   FROM service_occurrences o
   JOIN service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id
   JOIN agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
   JOIN customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
   WHERE o.shop_id=$1 AND o.id=$2`, shopID, id).Scan(&target.ID, &service, &customer, &target.CustomerID, &date)
		target.Name = service + " · " + customer + " · Due " + date.Format("Jan 2, 2006")
		return target, err
	case "seasonal":
		var service, customer, season string
		var year int
		err := s.db.Pool.QueryRow(ctx, `SELECT v.id,a.plan_snapshot_name,c.name,c.id,v.season,v.due_year
   FROM visit_dues v JOIN agreements a ON a.id=v.agreement_id AND a.shop_id=v.shop_id
   JOIN customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
   WHERE v.shop_id=$1 AND v.id=$2`, shopID, id).Scan(&target.ID, &service, &customer, &target.CustomerID, &season, &year)
		target.Name = fmt.Sprintf("%s · %s · %s %d", service, customer, season, year)
		return target, err
	default:
		return target, errInternalNoteInput
	}
}

func (s *Store) AddVisitNote(ctx context.Context, shopID, kind, id, author string, input internalNoteInput) error {
	if kind != "recurring" && kind != "seasonal" {
		return errInternalNoteInput
	}
	event, err := validateInternalNote(input, s.nowUTC())
	if err != nil {
		return err
	}
	tag, err := s.db.Pool.Exec(ctx, `WITH target AS (
  SELECT id AS occurrence_id,NULL::uuid AS seasonal_visit_id FROM service_occurrences WHERE shop_id=$1 AND id=$3 AND $2='recurring'
  UNION ALL SELECT NULL::uuid,id FROM visit_dues WHERE shop_id=$1 AND id=$3 AND $2='seasonal')
  INSERT INTO visit_notes(shop_id,occurrence_id,seasonal_visit_id,submission_key,body,occurred_on,occurred_at,event_timezone,recorded_by)
  SELECT $1,occurrence_id,seasonal_visit_id,$4,$5,$6,$7,$8,$9 FROM target
  ON CONFLICT(shop_id,submission_key) DO UPDATE SET submission_key=EXCLUDED.submission_key
  WHERE visit_notes.occurrence_id IS NOT DISTINCT FROM EXCLUDED.occurrence_id
  AND visit_notes.seasonal_visit_id IS NOT DISTINCT FROM EXCLUDED.seasonal_visit_id
  AND visit_notes.body=EXCLUDED.body AND visit_notes.occurred_on=EXCLUDED.occurred_on
  AND visit_notes.occurred_at IS NOT DISTINCT FROM EXCLUDED.occurred_at
  AND visit_notes.event_timezone=EXCLUDED.event_timezone AND visit_notes.recorded_by=EXCLUDED.recorded_by`, shopID, kind, id, input.Key, strings.TrimSpace(input.Body), event.Date, event.At, event.Zone, author)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errVisitNoteConflict
	}
	return nil
}

func (s *Store) ListVisitNotes(ctx context.Context, shopID, kind, id string) ([]internalNote, error) {
	rows, err := s.db.Pool.Query(ctx, `SELECT body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at FROM visit_notes WHERE shop_id=$1 AND (($2='recurring' AND occurrence_id=$3) OR ($2='seasonal' AND seasonal_visit_id=$3)) ORDER BY occurred_on DESC,recorded_at DESC,id DESC LIMIT 100`, shopID, kind, id)
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

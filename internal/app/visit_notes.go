package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/wmichelin/clubdue/internal/database"
	"strings"
	"time"
)

var errVisitNoteConflict = errors.New("This form was already used for a different note, or the visit is unavailable. Start a new note to preserve the earlier entry.")

func (s *Store) GetVisitHistoryTarget(ctx context.Context, shopID, kind, id string) (historyTarget, error) {
	if err := validateRecordIDs(shopID, id); err != nil {
		return historyTarget{}, err
	}
	var target historyTarget
	target.BackURL = "/visits?filter=all"
	switch kind {
	case "recurring":
		var service, customer string
		var date time.Time
		err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT o.id,sc.service_name,c.name,c.id,COALESCE(o.scheduled_on,o.due_on)
   FROM service_occurrences o
   JOIN service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id
   JOIN agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
   JOIN customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
   WHERE o.shop_id=$1 AND o.id=$2`, `SELECT LOWER(CONVERT(char(36),o.id)),sc.service_name,c.name,LOWER(CONVERT(char(36),c.id)),o.effective_on FROM dbo.service_occurrences o JOIN dbo.service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id JOIN dbo.agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id JOIN dbo.customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id WHERE o.shop_id=CONVERT(uniqueidentifier,@p1) AND o.id=CONVERT(uniqueidentifier,@p2)`), shopID, id).Scan(&target.ID, &service, &customer, &target.CustomerID, &date)
		target.Name = service + " · " + customer + " · Due " + date.Format("Jan 2, 2006")
		return target, err
	case "seasonal":
		var service, customer, season string
		var year int
		err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT v.id,a.plan_snapshot_name,c.name,c.id,v.season,v.due_year
   FROM visit_dues v JOIN agreements a ON a.id=v.agreement_id AND a.shop_id=v.shop_id
   JOIN customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
   WHERE v.shop_id=$1 AND v.id=$2`, `SELECT LOWER(CONVERT(char(36),v.id)),a.plan_snapshot_name,c.name,LOWER(CONVERT(char(36),c.id)),v.season,v.due_year FROM dbo.visit_dues v JOIN dbo.agreements a ON a.id=v.agreement_id AND a.shop_id=v.shop_id JOIN dbo.customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id WHERE v.shop_id=CONVERT(uniqueidentifier,@p1) AND v.id=CONVERT(uniqueidentifier,@p2)`), shopID, id).Scan(&target.ID, &service, &customer, &target.CustomerID, &season, &year)
		target.Name = fmt.Sprintf("%s · %s · %s %d", service, customer, season, year)
		return target, err
	default:
		return target, errInternalNoteInput
	}
}

func (s *Store) AddVisitNote(ctx context.Context, shopID, kind, id, author string, input internalNoteInput) error {
	if err := validateRecordIDs(shopID, id); err != nil {
		return err
	}
	if !validStoredText(author) {
		return errInternalNoteInput
	}
	if kind != "recurring" && kind != "seasonal" {
		return errInternalNoteInput
	}
	event, err := validateInternalNote(input, s.nowUTC())
	if err != nil {
		return err
	}
	if s.db.Pool.Dialect() == database.SQLServer {
		return s.addSQLServerVisitNote(ctx, shopID, kind, id, author, input, event)
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
	if kind != "recurring" && kind != "seasonal" {
		return nil, errInternalNoteInput
	}
	if err := validateRecordIDs(shopID, id); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at FROM visit_notes WHERE shop_id=$1 AND (($2='recurring' AND occurrence_id=$3) OR ($2='seasonal' AND seasonal_visit_id=$3)) ORDER BY occurred_on DESC,recorded_at DESC,id DESC LIMIT 100`, `SELECT TOP(100) body,occurred_on,occurred_at,event_timezone,recorded_by,recorded_at FROM dbo.visit_notes WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND ((@p2=N'recurring' AND occurrence_id=CONVERT(uniqueidentifier,@p3)) OR (@p2=N'seasonal' AND seasonal_visit_id=CONVERT(uniqueidentifier,@p3))) ORDER BY occurred_on DESC,recorded_at DESC,CONVERT(char(36),id) COLLATE Latin1_General_100_BIN2 DESC`), shopID, kind, id)
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

// Match cascading deletion: retain the shop, then visit and submission key locks.
// Triggers cover direct SQL writers; application writes avoid the inverse order.
func (s *Store) addSQLServerVisitNote(ctx context.Context, shopID, kind, id, author string, input internalNoteInput, event eventTime) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var shop string
	if err := tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.shops WITH(HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, shopID).Scan(&shop); err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return errVisitNoteConflict
		}
		return err
	}
	parent := `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.service_occurrences WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`
	if kind == "seasonal" {
		parent = `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.visit_dues WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`
	}
	var locked string
	if err := tx.QueryRow(ctx, parent, shopID, id).Scan(&locked); err != nil {
		if errors.Is(err, database.ErrNoRows) {
			return errVisitNoteConflict
		}
		return err
	}
	var recurring, seasonal, body, zone, previousAuthor string
	var date time.Time
	var at *time.Time
	err = tx.QueryRow(ctx, `SELECT COALESCE(LOWER(CONVERT(char(36),occurrence_id)),''),COALESCE(LOWER(CONVERT(char(36),seasonal_visit_id)),''),body,occurred_on,occurred_at,event_timezone,recorded_by FROM dbo.visit_notes WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND submission_key=@p2 AND DATALENGTH(submission_key)=DATALENGTH(@p2)`, shopID, input.Key).Scan(&recurring, &seasonal, &body, &date, &at, &zone, &previousAuthor)
	if err == nil {
		sameVisit := (kind == "recurring" && strings.EqualFold(recurring, id) && seasonal == "") || (kind == "seasonal" && strings.EqualFold(seasonal, id) && recurring == "")
		sameInstant := at == nil && event.At == nil
		if at != nil && event.At != nil {
			sameInstant = at.Equal(*event.At)
		}
		if !sameVisit || body != strings.TrimSpace(input.Body) || date.Format("2006-01-02") != event.Date.Format("2006-01-02") || !sameInstant || zone != event.Zone || previousAuthor != author {
			return errVisitNoteConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, database.ErrNoRows) {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO dbo.visit_notes(shop_id,occurrence_id,seasonal_visit_id,submission_key,body,occurred_on,occurred_at,event_timezone,recorded_by) VALUES(CONVERT(uniqueidentifier,@p1),CASE WHEN @p2=N'recurring' THEN CONVERT(uniqueidentifier,@p3) ELSE NULL END,CASE WHEN @p2=N'seasonal' THEN CONVERT(uniqueidentifier,@p3) ELSE NULL END,@p4,@p5,@p6,@p7,@p8,@p9)`, shopID, kind, id, input.Key, strings.TrimSpace(input.Body), event.Date, event.At, event.Zone, author)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

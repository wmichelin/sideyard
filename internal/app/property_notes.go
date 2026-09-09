package app

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

type propertyNote struct {
	SubmissionKey, Body, RecordedBy string
	OccurredOn, RecordedAt          time.Time
}

var noteSubmissionKey = regexp.MustCompile(`^[a-f0-9]{32}$`)
var errPropertyNoteInput = errors.New("Enter a note up to 2,000 bytes and a date no later than today. Reload the page if this form is out of date.")

func (s *Store) AddPropertyNote(ctx context.Context, shopID, propertyID string, n propertyNote) error {
	n.Body = strings.TrimSpace(n.Body)
	if n.Body == "" || len(n.Body) > 2000 || strings.ContainsRune(n.Body, '\x00') || !noteSubmissionKey.MatchString(n.SubmissionKey) || n.OccurredOn.IsZero() || n.OccurredOn.Format("2006-01-02") > s.nowUTC().Format("2006-01-02") {
		return errPropertyNoteInput
	}
	// Repeating the same submission acknowledges the original note. Reusing a
	// submission key for changed content is rejected rather than losing an edit.
	tag, err := s.db.Pool.Exec(ctx, `INSERT INTO property_notes(shop_id,property_id,submission_key,body,occurred_on,recorded_by)
	 SELECT $1,id,$3,$4,$5,$6 FROM properties WHERE shop_id=$1 AND id=$2
	 ON CONFLICT(shop_id,submission_key) DO UPDATE SET submission_key=EXCLUDED.submission_key
	 WHERE property_notes.property_id=EXCLUDED.property_id AND property_notes.body=EXCLUDED.body
	 AND property_notes.occurred_on=EXCLUDED.occurred_on AND property_notes.recorded_by=EXCLUDED.recorded_by`, shopID, propertyID, n.SubmissionKey, n.Body, n.OccurredOn, n.RecordedBy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errPropertyConflict
	}
	return nil
}

func (s *Store) ListPropertyNotes(ctx context.Context, shopID, propertyID string) ([]propertyNote, error) {
	rows, err := s.db.Pool.Query(ctx, `SELECT body,occurred_on,recorded_by,recorded_at FROM property_notes WHERE shop_id=$1 AND property_id=$2 ORDER BY recorded_at DESC,id DESC LIMIT 100`, shopID, propertyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notes []propertyNote
	for rows.Next() {
		var n propertyNote
		if err := rows.Scan(&n.Body, &n.OccurredOn, &n.RecordedBy, &n.RecordedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, rows.Err()
}

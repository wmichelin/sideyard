package app

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

type propertyNote struct {
	SubmissionKey, Body, RecordedBy string
	OccurredOn, RecordedAt          time.Time
}

var noteSubmissionKey = regexp.MustCompile(`^[a-f0-9]{32}$`)
var errPropertyNoteInput = errors.New("Enter a note up to 2,000 bytes and a date no later than today. Reload the page if this form is out of date.")

func (s *Store) AddPropertyNote(ctx context.Context, shopID, propertyID string, n propertyNote) error {
	if err := validateRecordIDs(shopID, propertyID); err != nil {
		return err
	}
	if !validStoredText(n.Body, n.RecordedBy) {
		return errPropertyNoteInput
	}
	n.Body = strings.TrimSpace(n.Body)
	if n.Body == "" || len(n.Body) > 2000 || strings.ContainsRune(n.Body, '\x00') || !noteSubmissionKey.MatchString(n.SubmissionKey) || n.OccurredOn.IsZero() || n.OccurredOn.Year() < 1 || n.OccurredOn.Year() > 9999 || n.OccurredOn.Format("2006-01-02") > s.nowUTC().Format("2006-01-02") {
		return errPropertyNoteInput
	}
	if s.db.Pool.Dialect() == database.SQLServer {
		return s.addSQLServerPropertyNote(ctx, shopID, propertyID, n)
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
	if err := validateRecordIDs(shopID, propertyID); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT body,occurred_on,recorded_by,recorded_at FROM property_notes WHERE shop_id=$1 AND property_id=$2 ORDER BY recorded_at DESC,id DESC LIMIT 100`, `SELECT TOP(100) body,occurred_on,recorded_by,recorded_at FROM dbo.property_notes WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND property_id=CONVERT(uniqueidentifier,@p2) ORDER BY recorded_at DESC,CONVERT(char(36),id) COLLATE Latin1_General_100_BIN2 DESC`), shopID, propertyID)
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

// Serialize each key before its first insert. Go compares retained values exactly;
// SQL padded/case-insensitive string equality must not acknowledge different input.
func (s *Store) addSQLServerPropertyNote(ctx context.Context, shopID, propertyID string, n propertyNote) error {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var existingProperty, body, author string
	var occurred time.Time
	err = tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),property_id)),body,occurred_on,recorded_by FROM dbo.property_notes WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND submission_key=@p2 AND DATALENGTH(submission_key)=DATALENGTH(@p2)`, shopID, n.SubmissionKey).Scan(&existingProperty, &body, &occurred, &author)
	if err == nil {
		if !strings.EqualFold(existingProperty, propertyID) || body != n.Body || author != n.RecordedBy || occurred.Format("2006-01-02") != n.OccurredOn.Format("2006-01-02") {
			return errPropertyConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, database.ErrNoRows) {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO dbo.property_notes(shop_id,property_id,submission_key,body,occurred_on,recorded_by) SELECT CONVERT(uniqueidentifier,@p1),id,@p3,@p4,@p5,@p6 FROM dbo.properties WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, propertyID, n.SubmissionKey, n.Body, n.OccurredOn, n.RecordedBy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errPropertyConflict
	}
	return tx.Commit(ctx)
}

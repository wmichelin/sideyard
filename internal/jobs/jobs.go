// Package jobs owns standalone work and its authored change history.
package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func New(pool *pgxpool.Pool, now func() time.Time) *Service { return &Service{pool: pool, now: now} }

type Input struct{ CustomerID, PropertyID, Kind, Title, Details, Date string }
type Record struct {
	ID, CustomerID, CustomerName, PropertyID, Address, Kind, Title, Details, Date, Status string
	Revision                                                                              int
	CompletedAt                                                                           *time.Time
}

func (r Record) DateLabel() string {
	if r.Date == "" {
		return "Date not set"
	}
	d, _ := time.Parse("2006-01-02", r.Date)
	return d.Format("Jan 2, 2006")
}
func (r Record) KindLabel() string {
	if r.Kind == "survey" {
		return "Site survey"
	}
	return "Service job"
}

type Snapshot struct{ Title, Kind, Details, Date, Status, PropertyID string }

func (v Snapshot) DateLabel() string { return (Record{Date: v.Date}).DateLabel() }

type Event struct {
	Revision       int
	Action, Author string
	At             time.Time
	Snapshot       Snapshot
}

var ErrInput = errors.New("Choose a valid customer, their property, a job type and a title up to 160 bytes. Details can contain up to 4,000 bytes.")
var ErrConflict = errors.New("This job changed or is no longer open. Reload and review its current state before saving.")
var ErrSubmission = errors.New("This form was already used for a different job. Start a new job to preserve the original.")
var ErrDate = errors.New("Choose a valid planned date, or leave it blank.")
var uuid = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var submissionKey = regexp.MustCompile(`^[0-9a-f]{32}$`)

func ValidID(id string) bool { return uuid.MatchString(id) }

func Validate(in Input) (Input, error) {
	in.CustomerID = strings.ToLower(strings.TrimSpace(in.CustomerID))
	in.PropertyID = strings.ToLower(strings.TrimSpace(in.PropertyID))
	in.Title = strings.TrimSpace(in.Title)
	in.Details = strings.TrimSpace(in.Details)
	if !ValidID(in.CustomerID) || (in.PropertyID != "" && !ValidID(in.PropertyID)) || (in.Kind != "job" && in.Kind != "survey") || in.Title == "" || len(in.Title) > 160 || len(in.Details) > 4000 || strings.ContainsRune(in.Title+in.Details, '\x00') {
		return in, ErrInput
	}
	if in.Date != "" {
		d, err := time.Parse("2006-01-02", in.Date)
		if err != nil || d.Year() < 1 || d.Format("2006-01-02") != in.Date {
			return in, ErrDate
		}
	}
	return in, nil
}

func (s *Service) Create(ctx context.Context, shopID, key, author string, in Input) (string, error) {
	in, err := Validate(in)
	if err != nil {
		return "", err
	}
	if !submissionKey.MatchString(key) {
		return "", ErrInput
	}
	payload, _ := json.Marshal(struct {
		Input  Input
		Author string
	}{in, author})
	sum := sha256.Sum256(payload)
	fingerprint := hex.EncodeToString(sum[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO jobs(shop_id,customer_id,property_id,submission_key,creation_fingerprint,kind,title,details,scheduled_on)
  SELECT $1,c.id,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8,NULLIF($9,'')::date FROM customers c
  WHERE c.shop_id=$1 AND c.id=$2 AND ($3='' OR EXISTS(SELECT 1 FROM properties p WHERE p.shop_id=$1 AND p.customer_id=c.id AND p.id=NULLIF($3,'')::uuid))
  ON CONFLICT(shop_id,submission_key) DO NOTHING RETURNING id`, shopID, in.CustomerID, in.PropertyID, key, fingerprint, in.Kind, in.Title, in.Details, in.Date).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		var prior string
		err = tx.QueryRow(ctx, `SELECT id,creation_fingerprint FROM jobs WHERE shop_id=$1 AND submission_key=$2`, shopID, key).Scan(&id, &prior)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrInput
		}
		if err != nil {
			return "", err
		}
		if prior != fingerprint {
			return "", ErrSubmission
		}
	} else if err != nil {
		return "", err
	} else if err = s.recordEvent(ctx, tx, shopID, id, "created", author); err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Service) Edit(ctx context.Context, shopID, id, author string, revision int, in Input) error {
	in, err := Validate(in)
	if err != nil {
		return err
	}
	if !ValidID(id) || revision < 1 {
		return ErrInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE jobs j SET property_id=NULLIF($4,'')::uuid,kind=$5,title=$6,details=$7,scheduled_on=NULLIF($8,'')::date,revision=revision+1,updated_at=$10
  WHERE j.shop_id=$1 AND j.id=$2 AND j.customer_id=$3 AND j.revision=$9 AND j.status='planned'
  AND ($4='' OR EXISTS(SELECT 1 FROM properties p WHERE p.shop_id=$1 AND p.customer_id=j.customer_id AND p.id=NULLIF($4,'')::uuid))`, shopID, id, in.CustomerID, in.PropertyID, in.Kind, in.Title, in.Details, in.Date, revision, s.now().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if err = s.recordEvent(ctx, tx, shopID, id, "edited", author); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) SetOutcome(ctx context.Context, shopID, id, author, action string, revision int) error {
	if !ValidID(id) || revision < 1 {
		return ErrInput
	}
	next, prior := action, "planned"
	switch action {
	case "completed", "cancelled":
	case "reopened":
		next = "planned"
		prior = ""
	default:
		return errors.New("Choose complete, cancel or reopen.")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE jobs SET status=$4,completed_at=CASE WHEN $4='completed' THEN $6::timestamptz ELSE NULL END,revision=revision+1,updated_at=$6
  WHERE shop_id=$1 AND id=$2 AND revision=$3 AND (($5='planned' AND status='planned') OR ($5='' AND status IN ('completed','cancelled')))`, shopID, id, revision, next, prior, s.now().UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if err = s.recordEvent(ctx, tx, shopID, id, action, author); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) recordEvent(ctx context.Context, tx pgx.Tx, shopID, id, action, author string) error {
	_, err := tx.Exec(ctx, `INSERT INTO job_events(shop_id,job_id,revision,action,snapshot,recorded_by,recorded_at)
 SELECT shop_id,id,revision,$3,jsonb_build_object('Title',title,'Kind',kind,'Details',details,'Date',COALESCE(to_char(scheduled_on,'YYYY-MM-DD'),''),'Status',status,'PropertyID',COALESCE(property_id::text,'')),$4,$5 FROM jobs WHERE shop_id=$1 AND id=$2`, shopID, id, action, author, s.now().UTC())
	return err
}

const recordSelect = `SELECT j.id,j.customer_id,c.name,COALESCE(j.property_id::text,''),COALESCE(concat_ws(', ',p.address_line1,NULLIF(p.address_line2,''),p.city,p.state,p.postal_code),''),j.kind,j.title,j.details,COALESCE(to_char(j.scheduled_on,'YYYY-MM-DD'),''),j.status,j.revision,j.completed_at
 FROM jobs j JOIN customers c ON c.id=j.customer_id AND c.shop_id=j.shop_id LEFT JOIN properties p ON p.id=j.property_id AND p.shop_id=j.shop_id`

func scanRecord(row pgx.Row) (Record, error) {
	var r Record
	err := row.Scan(&r.ID, &r.CustomerID, &r.CustomerName, &r.PropertyID, &r.Address, &r.Kind, &r.Title, &r.Details, &r.Date, &r.Status, &r.Revision, &r.CompletedAt)
	return r, err
}
func (s *Service) Get(ctx context.Context, shopID, id string) (Record, error) {
	if !ValidID(id) {
		return Record{}, pgx.ErrNoRows
	}
	return scanRecord(s.pool.QueryRow(ctx, recordSelect+` WHERE j.shop_id=$1 AND j.id=$2`, shopID, id))
}
func (s *Service) List(ctx context.Context, shopID, filter string) ([]Record, error) {
	if filter != "open" && filter != "all" {
		return nil, fmt.Errorf("invalid job filter")
	}
	rows, err := s.pool.Query(ctx, recordSelect+` WHERE j.shop_id=$1 AND ($2='all' OR j.status='planned') ORDER BY j.scheduled_on ASC NULLS LAST,j.created_at DESC,j.id LIMIT 100`, shopID, filter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Service) History(ctx context.Context, shopID, id string) ([]Event, error) {
	if !ValidID(id) {
		return nil, pgx.ErrNoRows
	}
	rows, err := s.pool.Query(ctx, `SELECT revision,action,snapshot,recorded_by,recorded_at FROM job_events WHERE shop_id=$1 AND job_id=$2 ORDER BY revision DESC LIMIT 100`, shopID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var data []byte
		if err := rows.Scan(&e.Revision, &e.Action, &data, &e.Author, &e.At); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &e.Snapshot); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

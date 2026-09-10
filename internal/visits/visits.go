package visits

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/activation"
	"github.com/wmichelin/clubdue/internal/database"
)

// Service owns visit-due persistence and the visit workflow.
type Service struct {
	pool database.Pool
	now  func() time.Time
}

func New(pool database.Pool, now func() time.Time) *Service {
	return &Service{pool: pool, now: now}
}

type VisitStatus string

const (
	VisitDue       VisitStatus = "due"
	VisitScheduled VisitStatus = "scheduled"
	VisitCompleted VisitStatus = "completed"
)

type VisitDueRow struct {
	ID              string
	AgreementID     string
	CustomerName    string
	CustomerPhone   string
	PropertyAddress string
	PlanName        string
	Season          string
	DueYear         int
	Status          VisitStatus
	ScheduledAt     *time.Time
	CompletedAt     *time.Time
	Notes           string
	WindowLabel     string
	IsOverdue       bool
}

type VisitExportRow struct {
	CustomerName string
	Phone        string
	Address      string
	ServiceType  string
	Notes        string
}

var (
	ErrOutcomeInput    = errors.New("invalid seasonal visit outcome")
	ErrOutcomeConflict = errors.New("seasonal visit outcome conflict")
)

var seasonWindows = map[string]struct{ startMonth, endMonth time.Month }{
	"spring": {time.March, time.May},
	"fall":   {time.September, time.November},
}

func seasonWindowLabel(season string, year int) string {
	w, ok := seasonWindows[season]
	if !ok {
		return fmt.Sprintf("%s %d", season, year)
	}
	start := time.Date(year, w.startMonth, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year, w.endMonth+1, 0, 0, 0, 0, 0, time.UTC)
	return fmt.Sprintf("%s %d – %s %d", start.Format("Jan 2"), year, end.Format("Jan 2"), year)
}

func SeasonWindowEnd(season string, year int) time.Time {
	return seasonWindowEnd(season, year)
}

func IsOverdue(season string, year int, status VisitStatus, now time.Time) bool {
	return isVisitOverdue(season, year, status, now)
}

func seasonWindowEnd(season string, year int) time.Time {
	w, ok := seasonWindows[season]
	if !ok {
		return time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	return time.Date(year, w.endMonth+1, 0, 23, 59, 59, 0, time.UTC)
}

func isVisitOverdue(season string, year int, status VisitStatus, now time.Time) bool {
	if status != VisitDue {
		return false
	}
	return now.After(seasonWindowEnd(season, year))
}

func (s *Service) SyncVisitDues(ctx context.Context, shopID string) error {
	if !recordIDPattern.MatchString(shopID) {
		return fmt.Errorf("invalid shop identifier")
	}
	year := s.now().Year()
	rows, err := s.pool.Query(ctx, database.Statement(s.pool.Dialect(), `
 SELECT a.id,a.plan_snapshot_visit_windows FROM agreements a
 WHERE a.shop_id=$1 AND a.status='active' AND a.visits_used<a.visits_entitled`,
		`SELECT LOWER(CONVERT(char(36),a.id)),a.plan_snapshot_visit_windows FROM dbo.agreements a
 WHERE a.shop_id=CONVERT(uniqueidentifier,@p1) AND a.status=N'active' AND a.visits_used<a.visits_entitled`), shopID)
	if err != nil {
		return err
	}
	type candidate struct {
		id      string
		seasons []string
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if s.pool.Dialect() == database.SQLServer {
			var encoded string
			if err := rows.Scan(&c.id, &encoded); err != nil {
				rows.Close()
				return err
			}
			if err := json.Unmarshal([]byte(encoded), &c.seasons); err != nil {
				rows.Close()
				return err
			}
		} else {
			if err := rows.Scan(&c.id, &c.seasons); err != nil {
				rows.Close()
				return err
			}
		}
		candidates = append(candidates, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	// Release the source connection before inserts, including global maintenance.
	for _, c := range candidates {
		if len(c.seasons) == 0 {
			c.seasons = []string{"spring", "fall"}
		}
		for _, season := range c.seasons {
			season = strings.TrimSpace(strings.ToLower(season))
			if season != "spring" && season != "fall" {
				continue
			}
			if s.pool.Dialect() == database.SQLServer {
				err = s.insertSQLServerVisit(ctx, shopID, c.id, season, year)
			} else {
				_, err = s.pool.Exec(ctx, `INSERT INTO visit_dues(shop_id,agreement_id,season,due_year,status)
     VALUES($1,$2,$3,$4,'due') ON CONFLICT(shop_id,agreement_id,season,due_year) DO NOTHING`, shopID, c.id, season, year)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) SyncAllVisitDues(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, database.Statement(s.pool.Dialect(), `SELECT id FROM shops WHERE kind <> 'demo_sandbox'`, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.shops WHERE kind<>N'demo_sandbox'`))
	if err != nil {
		return err
	}
	var shops []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		shops = append(shops, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range shops {
		if err := s.SyncVisitDues(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ListVisitDues(ctx context.Context, shopID string, dueOnly bool) ([]VisitDueRow, error) {
	if err := s.SyncVisitDues(ctx, shopID); err != nil {
		return nil, err
	}

	query := database.Statement(s.pool.Dialect(), `
		SELECT
			vd.id,
			vd.agreement_id,
			c.name,
			COALESCE(c.phone, ''),
			COALESCE(p.address_line1 || ', ' || p.city || ', ' || p.state || ' ' || p.postal_code, ''),
			a.plan_snapshot_name,
			vd.season,
			vd.due_year,
			vd.status,
			vd.scheduled_at,
			vd.completed_at,
			COALESCE(vd.notes, '')
		FROM visit_dues vd
		JOIN agreements a ON a.id = vd.agreement_id
		JOIN customers c ON c.id = a.customer_id
		LEFT JOIN properties p ON p.id = a.property_id
		WHERE vd.shop_id = $1`, sqlServerListVisits)
	if dueOnly {
		query += ` AND vd.status IN ('due', 'scheduled')`
	}
	query += ` ORDER BY vd.due_year, vd.season, c.name`

	rows, err := s.pool.Query(ctx, query, shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := s.now()
	var out []VisitDueRow
	for rows.Next() {
		var v VisitDueRow
		var status string
		if err := rows.Scan(
			&v.ID, &v.AgreementID, &v.CustomerName, &v.CustomerPhone,
			&v.PropertyAddress, &v.PlanName, &v.Season, &v.DueYear, &status,
			&v.ScheduledAt, &v.CompletedAt, &v.Notes,
		); err != nil {
			return nil, err
		}
		v.Status = VisitStatus(status)
		v.WindowLabel = seasonWindowLabel(v.Season, v.DueYear)
		v.IsOverdue = isVisitOverdue(v.Season, v.DueYear, v.Status, now)
		out = append(out, v)
	}
	if s.pool.Dialect() == database.SQLServer {
		slices.SortStableFunc(out, func(a, b VisitDueRow) int {
			if a.DueYear < b.DueYear {
				return -1
			}
			if a.DueYear > b.DueYear {
				return 1
			}
			if n := strings.Compare(a.Season, b.Season); n != 0 {
				return n
			}
			return strings.Compare(a.CustomerName, b.CustomerName)
		})
	}
	return out, rows.Err()
}

func (s *Service) CountVisitsOverdue(ctx context.Context, shopID string) (int, error) {

	rows, err := s.ListVisitDues(ctx, shopID, true)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, v := range rows {
		if v.IsOverdue {
			count++
		}
	}
	return count, nil
}

func (s *Service) BulkUpdateVisitStatus(ctx context.Context, shopID string, visitIDs []string, action string) error {
	if len(visitIDs) == 0 {
		return nil
	}

	if !recordIDPattern.MatchString(shopID) {
		return fmt.Errorf("%w: invalid shop identifier", ErrOutcomeInput)
	}
	for _, id := range visitIDs {
		if !recordIDPattern.MatchString(id) {
			return fmt.Errorf("%w: invalid visit identifier", ErrOutcomeInput)
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var expectedAgreements map[string]string
	if s.pool.Dialect() == database.SQLServer {
		resolved, err := prepareSQLServerVisits(ctx, tx, shopID, visitIDs)
		if err != nil {
			return err
		}
		expectedAgreements = make(map[string]string, len(resolved))
		visitIDs = make([]string, 0, len(resolved))
		for _, v := range resolved {
			visitIDs = append(visitIDs, v.id)
			expectedAgreements[v.id] = v.agreement
		}
	}

	now := s.now()

	for _, visitID := range visitIDs {
		var agreementID string
		var currentStatus string
		var visitsUsed, visitsEntitled int
		var err error
		if s.pool.Dialect() == database.SQLServer {
			err = tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),agreement_id)),status FROM dbo.visit_dues WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, visitID).Scan(&agreementID, &currentStatus)
			if err == nil && agreementID != expectedAgreements[visitID] {
				return fmt.Errorf("%w: visit changed while selecting its agreement", ErrOutcomeConflict)
			}
			if err == nil {
				err = tx.QueryRow(ctx, `SELECT visits_used,visits_entitled FROM dbo.agreements WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, agreementID).Scan(&visitsUsed, &visitsEntitled)
			}
		} else {
			err = tx.QueryRow(ctx, `
			SELECT v.agreement_id, v.status, a.visits_used, a.visits_entitled
			FROM visit_dues v
			JOIN agreements a ON a.id = v.agreement_id AND a.shop_id = v.shop_id
			WHERE v.shop_id = $1 AND v.id = $2
			FOR UPDATE OF v, a`, shopID, visitID,
			).Scan(&agreementID, &currentStatus, &visitsUsed, &visitsEntitled)
		}
		if err != nil {
			if errors.Is(err, database.ErrNoRows) {
				return fmt.Errorf("%w: visit not found: %w", ErrOutcomeConflict, err)
			}
			return err
		}

		switch action {
		case "scheduled":
			if currentStatus == string(VisitCompleted) {
				return fmt.Errorf("%w: completed visit cannot be handed off", ErrOutcomeConflict)
			}
			_, err = tx.Exec(ctx, database.Statement(s.pool.Dialect(), `
				UPDATE visit_dues SET status = 'scheduled', scheduled_at = $3
				WHERE shop_id = $1 AND id = $2 AND status = 'due'`, `UPDATE dbo.visit_dues SET status=N'scheduled',scheduled_at=@p3 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2) AND status=N'due'`),
				shopID, visitID, now,
			)
		case "completed":
			if currentStatus == string(VisitCompleted) {
				continue
			}
			if visitsUsed >= visitsEntitled {
				return fmt.Errorf("%w: agreement visit entitlement is exhausted", ErrOutcomeConflict)
			}
			_, err = tx.Exec(ctx, database.Statement(s.pool.Dialect(), `
				UPDATE visit_dues SET status = 'completed', completed_at = $3
				WHERE shop_id = $1 AND id = $2`, `UPDATE dbo.visit_dues SET status=N'completed',completed_at=@p3 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`),
				shopID, visitID, now,
			)
			if err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, database.Statement(s.pool.Dialect(), `
				UPDATE agreements SET visits_used = visits_used + 1, updated_at = now()
				WHERE shop_id = $1 AND id = $2 AND visits_used < visits_entitled`, `UPDATE dbo.agreements SET visits_used=visits_used+1,updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2) AND visits_used<visits_entitled`),
				shopID, agreementID,
			)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("%w: agreement visit entitlement is exhausted", ErrOutcomeConflict)
			}
		default:
			return fmt.Errorf("%w: unknown action: %s", ErrOutcomeInput, action)
		}
		if err != nil {
			return err
		}
	}
	if err := activation.RecordTx(ctx, tx, shopID, activation.EventFirstVisitAction); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Service) VisitExportRows(ctx context.Context, shopID string, dueOnly bool) ([]VisitExportRow, error) {
	visits, err := s.ListVisitDues(ctx, shopID, dueOnly)
	if err != nil {
		return nil, err
	}

	var out []VisitExportRow
	for _, v := range visits {
		if dueOnly && v.Status == VisitCompleted {
			continue
		}
		notes := fmt.Sprintf("agreement %s %s %d", v.AgreementID, v.Season, v.DueYear)
		if v.Notes != "" {
			notes += "; " + v.Notes
		}
		out = append(out, VisitExportRow{
			CustomerName: v.CustomerName,
			Phone:        v.CustomerPhone,
			Address:      v.PropertyAddress,
			ServiceType:  v.PlanName,
			Notes:        notes,
		})
	}
	return out, nil
}

func writeVisitExportCSV(w interface {
	Write(p []byte) (n int, err error)
}, rows []VisitExportRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"customer_name", "phone", "address", "service_type", "notes"}); err != nil {
		return err
	}
	for _, r := range rows {
		if err := cw.Write([]string{r.CustomerName, r.Phone, r.Address, r.ServiceType, r.Notes}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func WriteExportCSV(w interface {
	Write(p []byte) (n int, err error)
}, rows []VisitExportRow) error {
	return writeVisitExportCSV(w, rows)
}

func visitStatusLabel(s VisitStatus) string {
	switch s {
	case VisitDue:
		return "Due"
	case VisitScheduled:
		return "Handed off"
	case VisitCompleted:
		return "Completed"
	default:
		return string(s)
	}
}

func StatusLabel(status VisitStatus) string {
	return visitStatusLabel(status)
}

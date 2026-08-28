package main

import (
	"context"
	"encoding/csv"
	"fmt"
	"strings"
	"time"
)

type VisitStatus string

const (
	VisitDue        VisitStatus = "due"
	VisitScheduled  VisitStatus = "scheduled"
	VisitCompleted  VisitStatus = "completed"
)

type VisitDueRow struct {
	ID              string
	AgreementID     string
	CustomerName    string
	CustomerPhone   string
	PropertyAddress string
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

func (s *Store) SyncVisitDues(ctx context.Context, shopID string) error {
	year := time.Now().UTC().Year()
	rows, err := s.db.Pool.Query(ctx, `
		SELECT a.id, a.plan_snapshot_visit_windows
		FROM agreements a
		WHERE a.shop_id = $1
		  AND a.status = 'active'
		  AND a.visits_used < a.visits_entitled`, shopID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var agreementID string
		var seasons []string
		if err := rows.Scan(&agreementID, &seasons); err != nil {
			return err
		}
		if len(seasons) == 0 {
			seasons = []string{"spring", "fall"}
		}
		for _, season := range seasons {
			season = strings.TrimSpace(strings.ToLower(season))
			if season != "spring" && season != "fall" {
				continue
			}
			_, err := s.db.Pool.Exec(ctx, `
				INSERT INTO visit_dues (shop_id, agreement_id, season, due_year, status)
				VALUES ($1, $2, $3, $4, 'due')
				ON CONFLICT (shop_id, agreement_id, season, due_year) DO NOTHING`,
				shopID, agreementID, season, year,
			)
			if err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func (s *Store) SyncAllVisitDues(ctx context.Context) error {
	rows, err := s.db.Pool.Query(ctx, `SELECT id FROM shops`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var shopID string
		if err := rows.Scan(&shopID); err != nil {
			return err
		}
		if err := s.SyncVisitDues(ctx, shopID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) ListVisitDues(ctx context.Context, shopID string, dueOnly bool) ([]VisitDueRow, error) {
	if err := s.SyncVisitDues(ctx, shopID); err != nil {
		return nil, err
	}

	query := `
		SELECT
			vd.id,
			vd.agreement_id,
			c.name,
			COALESCE(c.phone, ''),
			COALESCE(p.address_line1 || ', ' || p.city || ', ' || p.state || ' ' || p.postal_code, ''),
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
		WHERE vd.shop_id = $1`
	if dueOnly {
		query += ` AND vd.status IN ('due', 'scheduled')`
	}
	query += ` ORDER BY vd.due_year, vd.season, c.name`

	rows, err := s.db.Pool.Query(ctx, query, shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now().UTC()
	var out []VisitDueRow
	for rows.Next() {
		var v VisitDueRow
		var status string
		if err := rows.Scan(
			&v.ID, &v.AgreementID, &v.CustomerName, &v.CustomerPhone,
			&v.PropertyAddress, &v.Season, &v.DueYear, &status,
			&v.ScheduledAt, &v.CompletedAt, &v.Notes,
		); err != nil {
			return nil, err
		}
		v.Status = VisitStatus(status)
		v.WindowLabel = seasonWindowLabel(v.Season, v.DueYear)
		v.IsOverdue = isVisitOverdue(v.Season, v.DueYear, v.Status, now)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) CountVisitsOverdue(ctx context.Context, shopID string) (int, error) {
	if err := s.SyncVisitDues(ctx, shopID); err != nil {
		return 0, err
	}

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

func (s *Store) BulkUpdateVisitStatus(ctx context.Context, shopID string, visitIDs []string, action string) error {
	if len(visitIDs) == 0 {
		return nil
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	now := time.Now().UTC()

	for _, visitID := range visitIDs {
		var agreementID string
		var currentStatus string
		err := tx.QueryRow(ctx, `
			SELECT agreement_id, status FROM visit_dues
			WHERE shop_id = $1 AND id = $2`, shopID, visitID,
		).Scan(&agreementID, &currentStatus)
		if err != nil {
			continue
		}

		switch action {
		case "scheduled":
			_, err = tx.Exec(ctx, `
				UPDATE visit_dues SET status = 'scheduled', scheduled_at = $3
				WHERE shop_id = $1 AND id = $2 AND status = 'due'`,
				shopID, visitID, now,
			)
		case "completed":
			if currentStatus == string(VisitCompleted) {
				continue
			}
			_, err = tx.Exec(ctx, `
				UPDATE visit_dues SET status = 'completed', completed_at = $3
				WHERE shop_id = $1 AND id = $2`,
				shopID, visitID, now,
			)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `
				UPDATE agreements SET visits_used = visits_used + 1, updated_at = now()
				WHERE shop_id = $1 AND id = $2 AND visits_used < visits_entitled`,
				shopID, agreementID,
			)
		default:
			return fmt.Errorf("unknown action: %s", action)
		}
		if err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func (s *Store) VisitExportRows(ctx context.Context, shopID string, dueOnly bool) ([]VisitExportRow, error) {
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
			ServiceType:  "HVAC tune-up",
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

func visitStatusLabel(s VisitStatus) string {
	switch s {
	case VisitDue:
		return "Due"
	case VisitScheduled:
		return "Scheduled"
	case VisitCompleted:
		return "Completed"
	default:
		return string(s)
	}
}

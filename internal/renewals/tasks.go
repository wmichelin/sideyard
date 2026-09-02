package renewals

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/activation"
)

type RenewalTask struct {
	ID, AgreementID, CustomerID                        string
	CustomerName, CustomerEmail, CustomerPhone         string
	PropertyAddress, PlanName, Status, Notes, DueState string
	Anniversary, DueAt                                 time.Time
	Milestone, Days                                    int
}

// renewalCycleStart returns the most recent agreement cycle that has started
// by today. Keeping an overdue agreement in that cycle makes its work visible
// until an owner records an explicit renewed or lost outcome.
func renewalCycleStart(anniversary, today time.Time, termMonths int) (time.Time, error) {
	if termMonths <= 0 {
		return time.Time{}, fmt.Errorf("agreement term must be positive")
	}
	cycleStart := time.Date(anniversary.UTC().Year(), anniversary.UTC().Month(), anniversary.UTC().Day(), 0, 0, 0, 0, time.UTC)
	today = time.Date(today.UTC().Year(), today.UTC().Month(), today.UTC().Day(), 0, 0, 0, 0, time.UTC)
	for next := cycleStart.AddDate(0, termMonths, 0); !next.After(today); next = cycleStart.AddDate(0, termMonths, 0) {
		cycleStart = next
	}
	return cycleStart, nil
}

func renewalDueState(days int) string {
	switch {
	case days < 0:
		return "Past due"
	case days == 0:
		return "Due today"
	default:
		return "Upcoming"
	}
}

func CycleStart(anniversary, today time.Time, termMonths int) (time.Time, error) {
	return renewalCycleStart(anniversary, today, termMonths)
}

func DueState(days int) string {
	return renewalDueState(days)
}

func (s *Service) GenerateRenewalTasks(ctx context.Context, shopID string, asOf time.Time) error {
	rows, err := s.pool.Query(ctx, `
		SELECT id, anniversary_date, plan_snapshot_term_months
		FROM agreements
		WHERE shop_id = $1 AND status = 'active'`, shopID)
	if err != nil {
		return err
	}
	defer rows.Close()

	today := time.Date(asOf.UTC().Year(), asOf.UTC().Month(), asOf.UTC().Day(), 0, 0, 0, 0, time.UTC)
	latestStart := today.AddDate(0, 0, 90)
	for rows.Next() {
		var agreementID string
		var cycleStart time.Time
		var termMonths int
		if err := rows.Scan(&agreementID, &cycleStart, &termMonths); err != nil {
			return err
		}
		cycleStart, err = renewalCycleStart(cycleStart, today, termMonths)
		if err != nil {
			return fmt.Errorf("agreement %s: %w", agreementID, err)
		}
		if cycleStart.After(latestStart) {
			continue
		}
		for _, milestone := range []int{90, 60, 30, 14, 7} {
			if _, err := s.pool.Exec(ctx, `
				INSERT INTO renewal_tasks (shop_id, agreement_id, cycle_start_date, due_at, milestone_days)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (shop_id, agreement_id, cycle_start_date, milestone_days) DO NOTHING`,
				shopID, agreementID, cycleStart, cycleStart.AddDate(0, 0, -milestone), milestone,
			); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func (s *Service) GenerateAllRenewalTasks(ctx context.Context, asOf time.Time) error {
	rows, err := s.pool.Query(ctx, `SELECT id FROM shops`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var shopID string
		if err := rows.Scan(&shopID); err != nil {
			return err
		}
		if err := s.GenerateRenewalTasks(ctx, shopID, asOf); err != nil {
			return err
		}
	}
	return rows.Err()
}

// QueueRenewalDigests snapshots the due, actionable task IDs once per shop and
// day. It intentionally does not send anything; a configured delivery worker
// can later claim the durable outbox record.
func (s *Service) QueueRenewalDigests(ctx context.Context, asOf time.Time) error {
	digestDate := time.Date(asOf.UTC().Year(), asOf.UTC().Month(), asOf.UTC().Day(), 0, 0, 0, 0, time.UTC)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO renewal_digest_outbox (shop_id, digest_date, task_ids)
		SELECT t.shop_id, $1::date, ARRAY_AGG(t.id ORDER BY t.due_at, t.id)
		FROM renewal_tasks t
		WHERE t.due_at <= $1::date AND t.status IN ('open', 'contacted')
		GROUP BY t.shop_id
		ON CONFLICT (shop_id, digest_date) DO NOTHING`, digestDate)
	return err
}

func (s *Service) ListRenewalTasks(ctx context.Context, shopID string) ([]RenewalTask, error) {
	asOf := s.now()
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, a.id, c.id, c.name, COALESCE(c.email, ''), COALESCE(c.phone, ''),
			COALESCE(p.address_line1 || ', ' || p.city || ', ' || p.state || ' ' || p.postal_code, ''),
			a.plan_snapshot_name, a.anniversary_date, t.due_at, t.milestone_days,
			(a.anniversary_date-$2::date)::int, t.status, COALESCE(t.notes, '')
		FROM renewal_tasks t
		JOIN agreements a ON a.id = t.agreement_id
		JOIN customers c ON c.id = a.customer_id
		LEFT JOIN properties p ON p.id = a.property_id
		WHERE t.shop_id = $1 AND (t.due_at <= $2::date OR t.status <> 'open')
		ORDER BY t.due_at, a.anniversary_date, t.milestone_days DESC`, shopID, asOf)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RenewalTask
	for rows.Next() {
		var t RenewalTask
		if err := rows.Scan(
			&t.ID, &t.AgreementID, &t.CustomerID, &t.CustomerName, &t.CustomerEmail, &t.CustomerPhone,
			&t.PropertyAddress, &t.PlanName, &t.Anniversary, &t.DueAt, &t.Milestone, &t.Days, &t.Status, &t.Notes,
		); err != nil {
			return nil, err
		}
		t.DueState = renewalDueState(t.Days)
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Service) UpdateRenewalTask(ctx context.Context, shopID, id, status, notes string) error {
	if status != "open" && status != "contacted" && status != "renewed" && status != "lost" {
		return fmt.Errorf("invalid status")
	}
	notes = strings.TrimSpace(notes)
	if len(notes) > 2000 {
		return fmt.Errorf("renewal notes must not exceed 2000 bytes")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var agreementID, currentStatus string
	var cycleStart time.Time
	var termMonths int
	err = tx.QueryRow(ctx, `
		SELECT t.agreement_id, t.cycle_start_date, t.status, a.plan_snapshot_term_months
		FROM renewal_tasks t
		JOIN agreements a ON a.id = t.agreement_id AND a.shop_id = t.shop_id
		WHERE t.shop_id = $1 AND t.id = $2
		FOR UPDATE`, shopID, id,
	).Scan(&agreementID, &cycleStart, &currentStatus, &termMonths)
	if err != nil {
		return fmt.Errorf("task not found")
	}
	if currentStatus == "renewed" || currentStatus == "lost" {
		if currentStatus == status {
			return tx.Commit(ctx)
		}
		return fmt.Errorf("task outcome is already final")
	}

	if _, err := tx.Exec(ctx, `
		UPDATE renewal_tasks SET status = $3, notes = NULLIF($4, ''), updated_at = now()
		WHERE shop_id = $1 AND id = $2`, shopID, id, status, notes); err != nil {
		return err
	}
	if err := activation.RecordTx(ctx, tx, shopID, activation.EventFirstRenewalAction); err != nil {
		return err
	}

	switch status {
	case "renewed":
		if _, err := tx.Exec(ctx, `
			UPDATE agreements
			SET anniversary_date = $3, visits_used = 0, status = 'active', updated_at = now()
			WHERE shop_id = $1 AND id = $2`, shopID, agreementID, cycleStart.AddDate(0, termMonths, 0)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE renewal_tasks SET status = 'renewed', updated_at = now()
			WHERE shop_id = $1 AND agreement_id = $2 AND cycle_start_date = $3
			  AND id <> $4 AND status IN ('open', 'contacted')`, shopID, agreementID, cycleStart, id); err != nil {
			return err
		}
		nextCycleStart := cycleStart.AddDate(0, termMonths, 0)
		for _, milestone := range []int{90, 60, 30, 14, 7} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO renewal_tasks (shop_id, agreement_id, cycle_start_date, due_at, milestone_days)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (shop_id, agreement_id, cycle_start_date, milestone_days) DO NOTHING`,
				shopID, agreementID, nextCycleStart, nextCycleStart.AddDate(0, 0, -milestone), milestone,
			); err != nil {
				return err
			}
		}
	case "lost":
		if _, err := tx.Exec(ctx, `
			UPDATE agreements SET status = 'lapsed', updated_at = now()
			WHERE shop_id = $1 AND id = $2`, shopID, agreementID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE renewal_tasks SET status = 'lost', updated_at = now()
			WHERE shop_id = $1 AND agreement_id = $2 AND cycle_start_date = $3
			  AND id <> $4 AND status IN ('open', 'contacted')`, shopID, agreementID, cycleStart, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

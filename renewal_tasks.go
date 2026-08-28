package main

import (
	"context"
	"fmt"
	"time"
)

type RenewalTask struct {
	ID, CustomerName, PlanName, Status, Notes string
	Anniversary, DueAt                        time.Time
	Milestone, Days                           int
}

func (s *Store) GenerateRenewalTasks(ctx context.Context, shopID string, asOf time.Time) error {
	rows, err := s.db.Pool.Query(ctx, `
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
		for cycleStart.Before(today) {
			cycleStart = cycleStart.AddDate(0, termMonths, 0)
		}
		if cycleStart.After(latestStart) {
			continue
		}
		for _, milestone := range []int{90, 60, 30, 14, 7} {
			if _, err := s.db.Pool.Exec(ctx, `
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

func (s *Store) GenerateAllRenewalTasks(ctx context.Context, asOf time.Time) error {
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
		if err := s.GenerateRenewalTasks(ctx, shopID, asOf); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *Store) ListRenewalTasks(ctx context.Context, shopID string) ([]RenewalTask, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT t.id, c.name, a.plan_snapshot_name, a.anniversary_date, t.due_at,
			t.milestone_days, (a.anniversary_date-CURRENT_DATE)::int, t.status, COALESCE(t.notes, '')
		FROM renewal_tasks t
		JOIN agreements a ON a.id = t.agreement_id
		JOIN customers c ON c.id = a.customer_id
		WHERE t.shop_id = $1 AND (t.due_at <= CURRENT_DATE OR t.status <> 'open')
		ORDER BY t.due_at, a.anniversary_date, t.milestone_days DESC`, shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RenewalTask
	for rows.Next() {
		var t RenewalTask
		if err := rows.Scan(&t.ID, &t.CustomerName, &t.PlanName, &t.Anniversary, &t.DueAt, &t.Milestone, &t.Days, &t.Status, &t.Notes); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (s *Store) UpdateRenewalTask(ctx context.Context, shopID, id, status, notes string) error {
	if status != "open" && status != "contacted" && status != "renewed" && status != "lost" {
		return fmt.Errorf("invalid status")
	}
	tx, err := s.db.Pool.Begin(ctx)
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

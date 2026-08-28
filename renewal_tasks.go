package main

import (
	"context"
	"fmt"
	"time"
)

type RenewalTask struct {
	ID, CustomerName, PlanName, Status, Notes string
	Anniversary                               time.Time
	Milestone, Days                           int
}

func (s *Store) ListRenewalTasks(ctx context.Context, shopID string) ([]RenewalTask, error) {
	if _, err := s.db.Pool.Exec(ctx, `INSERT INTO renewal_tasks (shop_id,agreement_id,milestone_days) SELECT a.shop_id,a.id,m.days FROM agreements a CROSS JOIN (VALUES (90),(60),(30),(14),(7)) m(days) WHERE a.shop_id=$1 AND a.status='active' AND a.anniversary_date BETWEEN CURRENT_DATE AND CURRENT_DATE+90 AND (a.anniversary_date-CURRENT_DATE)<=m.days ON CONFLICT DO NOTHING`, shopID); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, `SELECT t.id,c.name,a.plan_snapshot_name,a.anniversary_date,t.milestone_days,(a.anniversary_date-CURRENT_DATE)::int,t.status,COALESCE(t.notes,'') FROM renewal_tasks t JOIN agreements a ON a.id=t.agreement_id JOIN customers c ON c.id=a.customer_id WHERE t.shop_id=$1 ORDER BY a.anniversary_date,t.milestone_days DESC`, shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RenewalTask
	for rows.Next() {
		var t RenewalTask
		if err := rows.Scan(&t.ID, &t.CustomerName, &t.PlanName, &t.Anniversary, &t.Milestone, &t.Days, &t.Status, &t.Notes); err != nil {
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
	tag, err := s.db.Pool.Exec(ctx, `UPDATE renewal_tasks SET status=$3,notes=NULLIF($4,''),updated_at=now() WHERE shop_id=$1 AND id=$2`, shopID, id, status, notes)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task not found")
	}
	return nil
}

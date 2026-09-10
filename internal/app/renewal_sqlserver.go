package app

import (
	"context"
	"time"
)

func (s *Store) sqlServerDashboardCounts(ctx context.Context, shopID string, asOf time.Time) (DashboardStats, error) {
	var stats DashboardStats
	today := asOf.UTC()
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthEnd := month.AddDate(0, 1, 0).AddDate(0, 0, -1)
	horizon := today.AddDate(0, 0, 90)
	if horizon.Year() > 9999 {
		horizon = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	err := s.db.Pool.QueryRow(ctx, `SELECT
 COUNT_BIG(CASE WHEN a.status=N'active' THEN 1 END),
 COUNT_BIG(CASE WHEN a.status=N'active' AND a.anniversary_date>=CONVERT(date,@p2) AND a.anniversary_date<=CONVERT(date,@p3) THEN 1 END),
 COUNT_BIG(CASE WHEN a.status=N'active' AND a.anniversary_date BETWEEN CONVERT(date,@p2) AND CONVERT(date,@p5) THEN 1 END),
 COUNT_BIG(CASE WHEN a.status=N'lapsed' AND a.updated_at>=CONVERT(date,@p4) THEN 1 END),
 COALESCE(SUM(CONVERT(bigint,CASE WHEN a.status=N'active' THEN CASE WHEN a.plan_snapshot_billing_interval=N'monthly' THEN a.price_cents ELSE a.price_cents/12 END ELSE 0 END)),0)
 FROM dbo.agreements a WHERE a.shop_id=CONVERT(uniqueidentifier,@p1)`, shopID, today.Format("2006-01-02"), monthEnd.Format("2006-01-02"), month.Format("2006-01-02"), horizon.Format("2006-01-02")).Scan(&stats.ActiveCount, &stats.RenewalsDueMonth, &stats.RenewalsDue90, &stats.LapsedMTD, &stats.MRRCents)
	return stats, err
}

const sqlServerRenewalQueue = `SELECT LOWER(CONVERT(char(36),a.id)),c.name,
 CASE WHEN p.id IS NULL THEN N'' ELSE p.address_line1+N', '+p.city+N', '+p.state END,
 a.plan_snapshot_name,a.anniversary_date,DATEDIFF(day,CONVERT(date,@p2),a.anniversary_date),a.price_cents` + sqlServerAgreementFrom + `
 WHERE a.shop_id=CONVERT(uniqueidentifier,@p1) AND a.status=N'active' AND a.anniversary_date BETWEEN CONVERT(date,@p2) AND CONVERT(date,@p3) ORDER BY a.anniversary_date`

package servicework

import (
	"context"
	"fmt"
	"time"

	"database/sql"
)

const CalendarPageSize = 50

type CalendarVisit struct {
	ID, AgreementID, CustomerName, PropertyAddress, ServiceName string
	OriginalDateLabel                                           string
}

type Calendar struct {
	Counts      map[string]int
	Visits      []CalendarVisit
	Total, Page int
}

// CalendarWork reads already-planned work. Browsing a month never generates
// additional recurrence dates beyond the normal rolling planning horizon.
func (s *Service) CalendarWork(ctx context.Context, shopID string, start, end, day time.Time, page int) (Calendar, error) {
	result := Calendar{Counts: make(map[string]int)}
	start, end, day = dateOnly(start), dateOnly(end), dateOnly(day)
	if !end.After(start) || end.Sub(start) > 31*24*time.Hour || day.Before(start) || !day.Before(end) {
		return result, fmt.Errorf("invalid calendar month or selected date")
	}
	tx, err := s.pool.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
	 SELECT COALESCE(o.scheduled_on,o.due_on),count(*)
	 FROM service_occurrences o
	 JOIN service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id
	 JOIN agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
	 WHERE o.shop_id=$1 AND o.status='due' AND sc.active AND a.status='active'
	 AND COALESCE(o.scheduled_on,o.due_on)>=$2 AND COALESCE(o.scheduled_on,o.due_on)<$3
	 GROUP BY COALESCE(o.scheduled_on,o.due_on)`, shopID, start, end)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var date time.Time
		var count int
		if err := rows.Scan(&date, &count); err != nil {
			rows.Close()
			return result, err
		}
		result.Counts[date.Format("2006-01-02")] = count
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	result.Total = result.Counts[day.Format("2006-01-02")]
	lastPage := (result.Total-1)/CalendarPageSize + 1
	if page < 1 {
		page = 1
	}
	if page > lastPage {
		page = lastPage
	}
	result.Page = page
	rows, err = tx.Query(ctx, `
	 SELECT o.id,sc.agreement_id,c.name,
	 COALESCE(p.address_line1 || ', ' || p.city || ', ' || p.state || ' ' || p.postal_code,''),
	 sc.service_name,o.due_on
	 FROM service_occurrences o
	 JOIN service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id
	 JOIN agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
	 JOIN customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
	 LEFT JOIN properties p ON p.id=a.property_id AND p.shop_id=a.shop_id
	 WHERE o.shop_id=$1 AND o.status='due' AND sc.active AND a.status='active'
	 AND COALESCE(o.scheduled_on,o.due_on)=$2
	 ORDER BY c.name,sc.service_name,o.id LIMIT $3 OFFSET $4`, shopID, day, CalendarPageSize, (page-1)*CalendarPageSize)
	if err != nil {
		return result, err
	}
	for rows.Next() {
		var visit CalendarVisit
		var original time.Time
		if err := rows.Scan(&visit.ID, &visit.AgreementID, &visit.CustomerName, &visit.PropertyAddress, &visit.ServiceName, &original); err != nil {
			rows.Close()
			return result, err
		}
		if !original.Equal(day) {
			visit.OriginalDateLabel = original.Format("Jan 2, 2006")
		}
		result.Visits = append(result.Visits, visit)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

package servicework

import (
	"context"
	"fmt"
	"time"

	"database/sql"
	"github.com/wmichelin/clubdue/internal/database"
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
	if err := validateIDs(shopID); err != nil {
		return result, err
	}
	lastDay := end.AddDate(0, 0, -1)
	if !validDate(start) || !validDate(lastDay) || !validDate(day) {
		return result, fmt.Errorf("invalid calendar date")
	}
	options := &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	if s.pool.Dialect() == database.SQLServer {
		options = &sql.TxOptions{Isolation: sql.LevelSnapshot}
	}
	tx, err := s.pool.BeginTx(ctx, options)
	if err != nil {
		return result, err
	}
	defer tx.Rollback(ctx)
	bound := dateArgument(s.pool.Dialect(), end)
	if s.pool.Dialect() == database.SQLServer {
		bound = dateArgument(s.pool.Dialect(), lastDay)
	}
	rows, err := tx.Query(ctx, database.Statement(s.pool.Dialect(), `
	 SELECT COALESCE(o.scheduled_on,o.due_on),count(*)
	 FROM service_occurrences o
	 JOIN service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id
	 JOIN agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
	 WHERE o.shop_id=$1 AND o.status='due' AND sc.active AND a.status='active'
	 AND COALESCE(o.scheduled_on,o.due_on)>=$2 AND COALESCE(o.scheduled_on,o.due_on)<$3
	 GROUP BY COALESCE(o.scheduled_on,o.due_on)`, `SELECT o.effective_on,COUNT(*) FROM dbo.service_occurrences o JOIN dbo.service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id JOIN dbo.agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id WHERE o.shop_id=CONVERT(uniqueidentifier,@p1) AND o.status=N'due' AND sc.active=1 AND a.status=N'active' AND o.effective_on>=CONVERT(date,@p2) AND o.effective_on<=CONVERT(date,@p3) GROUP BY o.effective_on`), shopID, dateArgument(s.pool.Dialect(), start), bound)
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
	rows, err = tx.Query(ctx, database.Statement(s.pool.Dialect(), `
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
	 ORDER BY c.name,sc.service_name,o.id LIMIT $3 OFFSET $4`, `SELECT LOWER(CONVERT(char(36),o.id)),LOWER(CONVERT(char(36),sc.agreement_id)),c.name,`+sqlServerPropertyAddress+`,sc.service_name,o.due_on`+sqlServerOccurrenceJoins+` WHERE o.shop_id=CONVERT(uniqueidentifier,@p1) AND o.status=N'due' AND sc.active=1 AND a.status=N'active' AND o.effective_on=CONVERT(date,@p2) ORDER BY `+sqlServerNameOrder+`,LOWER(CONVERT(char(36),o.id)) COLLATE Latin1_General_100_BIN2 OFFSET @p4 ROWS FETCH NEXT @p3 ROWS ONLY`), shopID, dateArgument(s.pool.Dialect(), day), CalendarPageSize, (page-1)*CalendarPageSize)
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

package servicework

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

// ScheduleSummary keeps each service independent, even on the same agreement.
type ScheduleSummary struct {
	ID, AgreementID, CustomerName, PropertyAddress, ServiceName, CadenceLabel string
	LastCompletedLabel, NextServiceLabel, NextOccurrenceID, State             string
	NextOn                                                                    *time.Time
	Overdue                                                                   bool
}

func (s *Service) ListScheduleSummaries(ctx context.Context, shopID, agreementID string) ([]ScheduleSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sc.id, sc.agreement_id, c.name,
		 COALESCE(p.address_line1 || ', ' || p.city || ', ' || p.state || ' ' || p.postal_code, ''),
		 sc.service_name, sc.cadence_unit, sc.cadence_interval, sc.active, a.status='active', sc.ends_on,
		 history.last_completed, COALESCE(upcoming.id::text, ''), upcoming.next_on
		FROM service_schedules sc
		JOIN agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
		JOIN customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
		LEFT JOIN properties p ON p.id=a.property_id AND p.shop_id=a.shop_id
		LEFT JOIN LATERAL (
		 SELECT MAX(completed_at) AS last_completed FROM service_occurrences
		 WHERE shop_id=sc.shop_id AND schedule_id=sc.id AND status='completed'
		) history ON true
		LEFT JOIN LATERAL (
		 SELECT id, COALESCE(scheduled_on,due_on) AS next_on FROM service_occurrences
		 WHERE shop_id=sc.shop_id AND schedule_id=sc.id AND status='due' AND sc.active AND a.status='active'
		 ORDER BY COALESCE(scheduled_on,due_on),due_on,id LIMIT 1
		) upcoming ON true
		WHERE sc.shop_id=$1 AND ($2='' OR sc.agreement_id::text=$2)
		ORDER BY upcoming.next_on NULLS LAST,c.name,sc.service_name,sc.id`, shopID, agreementID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ScheduleSummary
	today := dateOnly(s.now())
	for rows.Next() {
		var item ScheduleSummary
		var unit string
		var interval int
		var active, agreementActive bool
		var end, last *time.Time
		if err := rows.Scan(&item.ID, &item.AgreementID, &item.CustomerName, &item.PropertyAddress,
			&item.ServiceName, &unit, &interval, &active, &agreementActive, &end, &last, &item.NextOccurrenceID, &item.NextOn); err != nil {
			return nil, err
		}
		item.CadenceLabel = CadenceLabel(unit, interval)
		item.LastCompletedLabel = "No completion recorded"
		if last != nil {
			item.LastCompletedLabel = last.UTC().Format("Jan 2, 2006")
		}
		switch {
		case !agreementActive:
			item.NextServiceLabel, item.State = "Plan inactive", "inactive"
		case !active:
			item.NextServiceLabel, item.State = "Paused", "paused"
		case item.NextOn != nil:
			item.NextServiceLabel = item.NextOn.Format("Mon, Jan 2, 2006")
			item.Overdue = item.NextOn.Before(today)
		case end != nil && end.Before(today):
			item.NextServiceLabel = "Schedule ended"
		default:
			item.NextServiceLabel = "No visit in the next 90 days"
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

type VisitDate struct {
	ID, ScheduleID, AgreementID, CustomerName, PropertyAddress, ServiceName string
	CurrentOn, OriginalOn, StartsOn                                         time.Time
	EndsOn                                                                  *time.Time
	Revision                                                                int
	Actionable                                                              bool
}

var ErrVisitUnavailable = errors.New("This visit is no longer available to reschedule. It may have been completed, skipped, or paused.")
var ErrDateChanged = errors.New("This visit changed since you opened it. Review its current date before saving again.")
var ErrDateConflict = errors.New("This service already has a visit on that date. Choose another date.")
var ErrDateOutsideSchedule = errors.New("Choose a date within this service's start and end dates.")

func (s *Service) GetVisitDate(ctx context.Context, shopID, occurrenceID string) (VisitDate, error) {
	var visit VisitDate
	err := s.pool.QueryRow(ctx, `
	 SELECT o.id,o.schedule_id,sc.agreement_id,c.name,
	 COALESCE(p.address_line1 || ', ' || p.city || ', ' || p.state || ' ' || p.postal_code,''),
	 sc.service_name,COALESCE(o.scheduled_on,o.due_on),o.due_on,o.schedule_revision,
	 sc.starts_on,sc.ends_on,(o.status='due' AND sc.active AND a.status='active')
	 FROM service_occurrences o
	 JOIN service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id
	 JOIN agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
	 JOIN customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
	 LEFT JOIN properties p ON p.id=a.property_id AND p.shop_id=a.shop_id
	 WHERE o.shop_id=$1 AND o.id=$2`, shopID, occurrenceID).Scan(
		&visit.ID, &visit.ScheduleID, &visit.AgreementID, &visit.CustomerName, &visit.PropertyAddress,
		&visit.ServiceName, &visit.CurrentOn, &visit.OriginalOn, &visit.Revision, &visit.StartsOn, &visit.EndsOn, &visit.Actionable)
	return visit, err
}

// MoveVisit changes one obligation's planned date, never its recurrence identity.
func (s *Service) MoveVisit(ctx context.Context, shopID, occurrenceID string, newDate time.Time, expectedRevision int) error {
	if newDate.IsZero() || newDate.Year() < 1 || newDate.Year() > 9999 {
		return ErrDateOutsideSchedule
	}
	newDate = dateOnly(newDate)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var schedule Schedule
	// Match generation, pause and import: lock the schedule before its child.
	err = tx.QueryRow(ctx, `
	 SELECT sc.id,sc.cadence_unit,sc.cadence_interval,sc.starts_on,sc.ends_on
	 FROM service_schedules sc JOIN agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
	 WHERE sc.shop_id=$1 AND sc.active AND a.status='active'
	 AND EXISTS (SELECT 1 FROM service_occurrences o WHERE o.shop_id=sc.shop_id AND o.schedule_id=sc.id AND o.id=$2)
	 FOR UPDATE OF sc`, shopID, occurrenceID).Scan(&schedule.ID, &schedule.CadenceUnit, &schedule.CadenceInterval, &schedule.StartsOn, &schedule.EndsOn)
	if errors.Is(err, database.ErrNoRows) {
		return ErrVisitUnavailable
	}
	if err != nil {
		return err
	}
	var original, current time.Time
	var revision int
	var status string
	err = tx.QueryRow(ctx, `SELECT due_on,COALESCE(scheduled_on,due_on),schedule_revision,status
	 FROM service_occurrences WHERE shop_id=$1 AND id=$2 FOR UPDATE`, shopID, occurrenceID).Scan(&original, &current, &revision, &status)
	if errors.Is(err, database.ErrNoRows) || (err == nil && status != "due") {
		return ErrVisitUnavailable
	}
	if err != nil {
		return err
	}
	if revision != expectedRevision {
		return ErrDateChanged
	}
	if newDate.Before(dateOnly(schedule.StartsOn)) || (schedule.EndsOn != nil && newDate.After(dateOnly(*schedule.EndsOn))) {
		return ErrDateOutsideSchedule
	}
	if current.Equal(newDate) {
		return tx.Commit(ctx)
	}
	var collision, anchorExists bool
	err = tx.QueryRow(ctx, `SELECT
	 EXISTS(SELECT 1 FROM service_occurrences WHERE shop_id=$1 AND schedule_id=$2 AND id<>$3 AND COALESCE(scheduled_on,due_on)=$4),
	 EXISTS(SELECT 1 FROM service_occurrences WHERE shop_id=$1 AND schedule_id=$2 AND due_on=$4)`, shopID, schedule.ID, occurrenceID, newDate).Scan(&collision, &anchorExists)
	if err != nil {
		return err
	}
	// Also protect cadence dates beyond the currently generated horizon.
	index := firstOccurrenceIndex(schedule, newDate)
	if collision || (!anchorExists && occurrenceAt(schedule, index).Equal(newDate)) {
		return ErrDateConflict
	}
	_, err = tx.Exec(ctx, `UPDATE service_occurrences
	 SET scheduled_on=CASE WHEN due_on=$3 THEN NULL ELSE $3 END,
	 schedule_revision=schedule_revision+1,updated_at=$4
	 WHERE shop_id=$1 AND id=$2`, shopID, occurrenceID, newDate, s.now())
	if err != nil {
		return fmt.Errorf("move service visit: %w", err)
	}
	return tx.Commit(ctx)
}

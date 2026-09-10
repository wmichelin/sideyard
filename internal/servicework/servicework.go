// Package servicework owns recurring service schedules and their generated
// occurrences. It is additive to the established seasonal visit workflow so
// either capability can evolve or roll back independently.
package servicework

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/activation"
	"github.com/wmichelin/clubdue/internal/database"
)

var ErrOutcomeInput = errors.New("invalid service outcome")
var ErrOutcomeConflict = errors.New("service outcome conflict")

const (
	lookBehindDays = 30
	lookAheadDays  = 90
)

type Service struct {
	pool database.Pool
	now  func() time.Time
}

func New(pool database.Pool, now func() time.Time) *Service {
	return &Service{pool: pool, now: now}
}

type ScheduleInput struct {
	AgreementID             string
	ExternalID              string
	ServiceName             string
	CadenceUnit             string
	CadenceInterval         int
	StartsOn                time.Time
	EndsOn                  *time.Time
	CountsTowardEntitlement bool
}

type Schedule struct {
	ID                      string
	AgreementID             string
	ExternalID              string
	ServiceName             string
	CadenceUnit             string
	CadenceInterval         int
	StartsOn                time.Time
	EndsOn                  *time.Time
	Active                  bool
	CountsTowardEntitlement bool
	CadenceLabel            string
	StartsOnLabel           string
	EndsOnLabel             string
	LastCompletedLabel      string
	NextServiceLabel        string
	NextOccurrenceID        string
}

type Occurrence struct {
	ID                string
	ScheduleID        string
	AgreementID       string
	CustomerName      string
	CustomerPhone     string
	PropertyAddress   string
	ServiceName       string
	DueOn             time.Time
	Status            string
	StatusLabel       string
	Notes             string
	DueOnLabel        string
	IsOverdue         bool
	Actionable        bool
	OriginalDueOn     time.Time
	OriginalDateLabel string
	ScheduleRevision  int
}

type ExportRow struct {
	CustomerName string
	Phone        string
	Address      string
	ServiceType  string
	Notes        string
}

func ValidateSchedule(input ScheduleInput) error {
	input.ServiceName = strings.TrimSpace(input.ServiceName)
	if input.AgreementID == "" || input.ServiceName == "" {
		return fmt.Errorf("agreement and service name are required")
	}
	if input.CadenceUnit != "day" && input.CadenceUnit != "week" && input.CadenceUnit != "month" {
		return fmt.Errorf("cadence must use days, weeks, or months")
	}
	if input.CadenceInterval <= 0 || input.CadenceInterval > 365 {
		return fmt.Errorf("cadence interval must be between 1 and 365")
	}
	if input.StartsOn.IsZero() || !validDate(input.StartsOn) {
		return fmt.Errorf("start date is required")
	}
	if !validText(input.ServiceName) || !validText(input.ExternalID) {
		return fmt.Errorf("service text must be valid UTF-8 without NUL")
	}
	if input.EndsOn != nil && (!validDate(*input.EndsOn) || dateOnly(*input.EndsOn).Before(dateOnly(input.StartsOn))) {
		return fmt.Errorf("end date must not precede start date")
	}
	return nil
}

// ProjectedOccurrenceCount uses the same rolling window and cadence logic as
// materialization without writing anything. Admission controls can use it to
// bound future fan-out before accepting a schedule.
func ProjectedOccurrenceCount(input ScheduleInput, asOf time.Time) (int, error) {
	if err := ValidateSchedule(input); err != nil {
		return 0, err
	}
	today := dateOnly(asOf)
	schedule := Schedule{
		AgreementID:     input.AgreementID,
		ServiceName:     strings.TrimSpace(input.ServiceName),
		CadenceUnit:     input.CadenceUnit,
		CadenceInterval: input.CadenceInterval,
		StartsOn:        dateOnly(input.StartsOn),
		EndsOn:          input.EndsOn,
		Active:          true,
	}
	windowStart, windowEnd := planningWindow(today)
	return len(occurrenceDates(
		schedule,
		windowStart, windowEnd,
	)), nil
}

func (s *Service) CreateSchedule(ctx context.Context, shopID string, input ScheduleInput) (string, error) {
	input.ServiceName = strings.TrimSpace(input.ServiceName)
	input.ExternalID = strings.TrimSpace(input.ExternalID)
	if err := ValidateSchedule(input); err != nil {
		return "", err
	}
	if err := validateIDs(shopID, input.AgreementID); err != nil {
		return "", err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	if s.pool.Dialect() == database.SQLServer {
		if err := lockSQLServerShop(ctx, tx, shopID); err != nil {
			return "", err
		}
		if err := lockSQLServerAgreement(ctx, tx, shopID, input.AgreementID, true); err != nil {
			return "", err
		}
	}
	var id string
	err = tx.QueryRow(ctx, database.Statement(s.pool.Dialect(), `
		INSERT INTO service_schedules (
			shop_id, agreement_id, external_id, service_name, cadence_unit,
			cadence_interval, starts_on, ends_on, counts_toward_entitlement
		)
		SELECT $1, a.id, NULLIF($3, ''), $4, $5, $6, $7, $8, $9
		FROM agreements a
		WHERE a.shop_id = $1 AND a.id = $2 AND a.status = 'active'
		RETURNING id`, `INSERT INTO dbo.service_schedules(shop_id,agreement_id,external_id,service_name,cadence_unit,cadence_interval,starts_on,ends_on,counts_toward_entitlement)
 OUTPUT LOWER(CONVERT(char(36),inserted.id))
 VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CASE WHEN DATALENGTH(@p3)=0 THEN NULL ELSE @p3 END,@p4,@p5,@p6,CONVERT(date,@p7),CONVERT(date,@p8),@p9)`), shopID, input.AgreementID, input.ExternalID, input.ServiceName,
		input.CadenceUnit, input.CadenceInterval, dateArgument(s.pool.Dialect(), input.StartsOn), optionalDateArgument(s.pool.Dialect(), input.EndsOn),
		input.CountsTowardEntitlement,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create service schedule: %w", err)
	}
	today := dateOnly(s.now())
	windowStart, windowEnd := planningWindow(today)
	schedule := Schedule{
		ID: id, AgreementID: input.AgreementID, ServiceName: input.ServiceName,
		CadenceUnit: input.CadenceUnit, CadenceInterval: input.CadenceInterval,
		StartsOn: dateOnly(input.StartsOn), EndsOn: input.EndsOn, Active: true,
	}
	dates := occurrenceDates(schedule, windowStart, windowEnd)
	if err := insertOccurrences(ctx, tx, shopID, id, dates); err != nil {
		return "", fmt.Errorf("generate initial service occurrences: %w", err)
	}
	generatedThrough := windowEnd
	if input.EndsOn != nil && dateOnly(*input.EndsOn).Before(generatedThrough) {
		generatedThrough = dateOnly(*input.EndsOn)
	}
	if len(dates) == 500 {
		generatedThrough = dates[len(dates)-1]
	}
	if _, err := tx.Exec(ctx, database.Statement(s.pool.Dialect(), `
		UPDATE service_schedules SET generated_through = $3, updated_at = now()
		WHERE shop_id = $1 AND id = $2`, `UPDATE dbo.service_schedules SET generated_through=CONVERT(date,@p3),updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, id, dateArgument(s.pool.Dialect(), generatedThrough)); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return id, nil
}

func (s *Service) ListSchedulesForAgreement(ctx context.Context, shopID, agreementID string) ([]Schedule, error) {
	if err := validateIDs(shopID, agreementID); err != nil {
		return nil, err
	}
	if err := s.GenerateShopOccurrences(ctx, shopID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, database.Statement(s.pool.Dialect(), `
		SELECT id, agreement_id, COALESCE(external_id, ''), service_name, cadence_unit,
			cadence_interval, starts_on, ends_on, active, counts_toward_entitlement
		FROM service_schedules
		WHERE shop_id = $1 AND agreement_id = $2
		ORDER BY active DESC, service_name, starts_on`, `SELECT LOWER(CONVERT(char(36),id)),LOWER(CONVERT(char(36),agreement_id)),COALESCE(external_id,N''),service_name,cadence_unit,cadence_interval,starts_on,ends_on,active,counts_toward_entitlement FROM dbo.service_schedules WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND agreement_id=CONVERT(uniqueidentifier,@p2)`), shopID, agreementID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var schedules []Schedule
	for rows.Next() {
		var schedule Schedule
		if err := rows.Scan(
			&schedule.ID, &schedule.AgreementID, &schedule.ExternalID, &schedule.ServiceName,
			&schedule.CadenceUnit, &schedule.CadenceInterval, &schedule.StartsOn,
			&schedule.EndsOn, &schedule.Active, &schedule.CountsTowardEntitlement,
		); err != nil {
			return nil, err
		}
		schedule.CadenceLabel = CadenceLabel(schedule.CadenceUnit, schedule.CadenceInterval)
		schedule.StartsOnLabel = schedule.StartsOn.Format("Jan 2, 2006")
		if schedule.EndsOn != nil {
			schedule.EndsOnLabel = schedule.EndsOn.Format("Jan 2, 2006")
		}
		schedules = append(schedules, schedule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	summaries, err := s.ListScheduleSummaries(ctx, shopID, agreementID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]ScheduleSummary, len(summaries))
	for _, summary := range summaries {
		byID[summary.ID] = summary
	}
	for i := range schedules {
		summary := byID[schedules[i].ID]
		schedules[i].LastCompletedLabel = summary.LastCompletedLabel
		schedules[i].NextServiceLabel = summary.NextServiceLabel
		schedules[i].NextOccurrenceID = summary.NextOccurrenceID
	}
	if s.pool.Dialect() == database.SQLServer {
		slices.SortStableFunc(schedules, func(a, b Schedule) int {
			if a.Active != b.Active {
				if a.Active {
					return -1
				}
				return 1
			}
			if n := strings.Compare(a.ServiceName, b.ServiceName); n != 0 {
				return n
			}
			return a.StartsOn.Compare(b.StartsOn)
		})
	}
	return schedules, nil
}

func (s *Service) SetScheduleActive(ctx context.Context, shopID, scheduleID string, active bool) error {
	if err := validateIDs(shopID, scheduleID); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if s.pool.Dialect() == database.SQLServer {
		if err := lockSQLServerSchedule(ctx, tx, shopID, scheduleID, false); err != nil {
			return err
		}
	}
	var current bool
	if err := tx.QueryRow(ctx, database.Statement(s.pool.Dialect(), `
		SELECT active FROM service_schedules
		WHERE shop_id = $1 AND id = $2
		FOR UPDATE`, `SELECT active FROM dbo.service_schedules WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, scheduleID).Scan(&current); err != nil {
		return fmt.Errorf("load service schedule: %w", err)
	}
	if current == active {
		return tx.Commit(ctx)
	}
	boundary := dateOnly(s.now())
	generatedThrough := boundary.AddDate(0, 0, -1)
	var checkpoint any = dateArgument(s.pool.Dialect(), generatedThrough)
	if generatedThrough.Year() < 1 {
		checkpoint = nil
	}
	pausedAt := any(s.now())
	if active {
		pausedAt = nil
	}
	if _, err := tx.Exec(ctx, database.Statement(s.pool.Dialect(), `
		UPDATE service_schedules
		SET active = $3, paused_at = $4, generated_through = $5, updated_at = now()
		WHERE shop_id = $1 AND id = $2`, `UPDATE dbo.service_schedules SET active=@p3,paused_at=@p4,generated_through=CONVERT(date,@p5),updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, scheduleID, active, pausedAt, checkpoint); err != nil {
		return err
	}
	// Pausing cancels non-final work from today forward. Resuming replans from
	// today on the original cadence anchor, without backfilling the pause.
	if _, err := tx.Exec(ctx, database.Statement(s.pool.Dialect(), `
		DELETE FROM service_occurrences
		WHERE shop_id = $1 AND schedule_id = $2 AND status = 'due'
		  AND (due_on >= $3 OR COALESCE(scheduled_on, due_on) >= $3)`, `DELETE FROM dbo.service_occurrences WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND schedule_id=CONVERT(uniqueidentifier,@p2) AND status=N'due' AND (due_on>=CONVERT(date,@p3) OR effective_on>=CONVERT(date,@p3))`),
		shopID, scheduleID, dateArgument(s.pool.Dialect(), boundary)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) GenerateScheduleOccurrences(ctx context.Context, shopID, scheduleID string) error {
	if err := validateIDs(shopID, scheduleID); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if s.pool.Dialect() == database.SQLServer {
		if err := lockSQLServerSchedule(ctx, tx, shopID, scheduleID, true); err != nil {
			return err
		}
	}

	var schedule Schedule
	var generatedThrough *time.Time
	err = tx.QueryRow(ctx, database.Statement(s.pool.Dialect(), `
		SELECT id, agreement_id, service_name, cadence_unit, cadence_interval,
			starts_on, ends_on, active, generated_through
		FROM service_schedules sc
		WHERE sc.shop_id = $1 AND sc.id = $2
		  AND EXISTS (
			SELECT 1 FROM agreements a
			WHERE a.shop_id = sc.shop_id AND a.id = sc.agreement_id AND a.status = 'active'
		  )
		FOR UPDATE`, `SELECT LOWER(CONVERT(char(36),id)),LOWER(CONVERT(char(36),agreement_id)),service_name,cadence_unit,cadence_interval,starts_on,ends_on,active,generated_through FROM dbo.service_schedules WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, scheduleID,
	).Scan(
		&schedule.ID, &schedule.AgreementID, &schedule.ServiceName, &schedule.CadenceUnit,
		&schedule.CadenceInterval, &schedule.StartsOn, &schedule.EndsOn, &schedule.Active,
		&generatedThrough,
	)
	if err != nil {
		return fmt.Errorf("load service schedule: %w", err)
	}
	if !schedule.Active {
		return nil
	}

	today := dateOnly(s.now())
	windowStart, windowEnd := planningWindow(today)
	if generatedThrough != nil {
		windowStart = dateOnly(*generatedThrough).AddDate(0, 0, 1)
	}
	if schedule.EndsOn != nil && dateOnly(*schedule.EndsOn).Before(windowEnd) {
		windowEnd = dateOnly(*schedule.EndsOn)
	}
	if !windowEnd.Before(windowStart) {
		dates := occurrenceDates(schedule, windowStart, windowEnd)
		if err := insertOccurrences(ctx, tx, shopID, scheduleID, dates); err != nil {
			return err
		}
		newGeneratedThrough := windowEnd
		if len(dates) == 500 {
			newGeneratedThrough = dates[len(dates)-1]
		}
		if _, err := tx.Exec(ctx, database.Statement(s.pool.Dialect(), `
			UPDATE service_schedules SET generated_through = $3, updated_at = now()
			WHERE shop_id = $1 AND id = $2`, `UPDATE dbo.service_schedules SET generated_through=CONVERT(date,@p3),updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, scheduleID, dateArgument(s.pool.Dialect(), newGeneratedThrough)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) GenerateShopOccurrences(ctx context.Context, shopID string) error {
	if err := validateIDs(shopID); err != nil {
		return err
	}
	rows, err := s.pool.Query(ctx, database.Statement(s.pool.Dialect(), `
		SELECT sc.id
		FROM service_schedules sc
		JOIN agreements a ON a.id = sc.agreement_id AND a.shop_id = sc.shop_id
		WHERE sc.shop_id = $1 AND sc.active AND a.status = 'active'`, `SELECT LOWER(CONVERT(char(36),sc.id)) FROM dbo.service_schedules sc JOIN dbo.agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id WHERE sc.shop_id=CONVERT(uniqueidentifier,@p1) AND sc.active=1 AND a.status=N'active'`), shopID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, id := range ids {
		if err := s.GenerateScheduleOccurrences(ctx, shopID, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) GenerateAllOccurrences(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, database.Statement(s.pool.Dialect(), `
		SELECT DISTINCT sc.shop_id
		FROM service_schedules sc
		JOIN agreements a ON a.id = sc.agreement_id AND a.shop_id = sc.shop_id
		JOIN shops sh ON sh.id = sc.shop_id
		WHERE sc.active AND a.status = 'active' AND sh.kind <> 'demo_sandbox'`, `SELECT DISTINCT LOWER(CONVERT(char(36),sc.shop_id)) FROM dbo.service_schedules sc JOIN dbo.agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id JOIN dbo.shops sh ON sh.id=sc.shop_id WHERE sc.active=1 AND a.status=N'active' AND sh.kind<>N'demo_sandbox'`))
	if err != nil {
		return err
	}
	defer rows.Close()
	var shopIDs []string
	for rows.Next() {
		var shopID string
		if err := rows.Scan(&shopID); err != nil {
			return err
		}
		shopIDs = append(shopIDs, shopID)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, shopID := range shopIDs {
		if err := s.GenerateShopOccurrences(ctx, shopID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) ListOccurrences(ctx context.Context, shopID string, dueOnly bool) ([]Occurrence, error) {
	if err := s.GenerateShopOccurrences(ctx, shopID); err != nil {
		return nil, err
	}
	today := dateOnly(s.now())
	args := []any{shopID}
	query := `
		SELECT o.id, o.schedule_id, sc.agreement_id, c.name, COALESCE(c.phone, ''),
			COALESCE(p.address_line1 || ', ' || p.city || ', ' || p.state || ' ' || p.postal_code, ''),
			sc.service_name, COALESCE(o.scheduled_on, o.due_on), o.status, COALESCE(o.notes, ''),
			sc.active, a.status = 'active', o.due_on, o.schedule_revision
		FROM service_occurrences o
		JOIN service_schedules sc ON sc.id = o.schedule_id AND sc.shop_id = o.shop_id
		JOIN agreements a ON a.id = sc.agreement_id AND a.shop_id = sc.shop_id
		JOIN customers c ON c.id = a.customer_id
		LEFT JOIN properties p ON p.id = a.property_id
		WHERE o.shop_id = $1`
	if s.pool.Dialect() == database.SQLServer {
		query = sqlServerListOccurrences
	}
	if dueOnly {
		query += database.Statement(s.pool.Dialect(), ` AND o.status = 'due' AND sc.active AND a.status = 'active' AND COALESCE(o.scheduled_on, o.due_on) <= $2`, ` AND o.status=N'due' AND sc.active=1 AND a.status=N'active' AND o.effective_on<=CONVERT(date,@p2)`)
		args = append(args, dateArgument(s.pool.Dialect(), today))
	}
	query += database.Statement(s.pool.Dialect(), ` ORDER BY COALESCE(o.scheduled_on, o.due_on), c.name, sc.service_name, o.id`, ``)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var occurrences []Occurrence
	for rows.Next() {
		var occurrence Occurrence
		var scheduleActive, agreementActive bool
		if err := rows.Scan(
			&occurrence.ID, &occurrence.ScheduleID, &occurrence.AgreementID,
			&occurrence.CustomerName, &occurrence.CustomerPhone, &occurrence.PropertyAddress,
			&occurrence.ServiceName, &occurrence.DueOn, &occurrence.Status, &occurrence.Notes,
			&scheduleActive, &agreementActive, &occurrence.OriginalDueOn, &occurrence.ScheduleRevision,
		); err != nil {
			return nil, err
		}
		occurrence.DueOnLabel = occurrence.DueOn.Format("Mon, Jan 2")
		if !occurrence.OriginalDueOn.Equal(occurrence.DueOn) {
			occurrence.OriginalDateLabel = occurrence.OriginalDueOn.Format("Jan 2, 2006")
		}
		occurrence.Actionable = occurrence.Status == "due" && scheduleActive && agreementActive
		occurrence.IsOverdue = occurrence.Actionable && occurrence.DueOn.Before(today)
		switch {
		case occurrence.Status == "due" && !agreementActive:
			occurrence.StatusLabel = "Plan inactive"
		case occurrence.Status == "due" && !scheduleActive:
			occurrence.StatusLabel = "Paused"
		case occurrence.IsOverdue:
			occurrence.StatusLabel = "Overdue"
		case occurrence.Status == "due" && occurrence.DueOn.Equal(today):
			occurrence.StatusLabel = "Due today"
		case occurrence.Status == "due":
			occurrence.StatusLabel = "Upcoming"
		case occurrence.Status == "completed":
			occurrence.StatusLabel = "Completed"
		case occurrence.Status == "skipped":
			occurrence.StatusLabel = "Skipped"
		}
		occurrences = append(occurrences, occurrence)
	}
	if s.pool.Dialect() == database.SQLServer {
		slices.SortFunc(occurrences, func(a, b Occurrence) int {
			if n := a.DueOn.Compare(b.DueOn); n != 0 {
				return n
			}
			if n := strings.Compare(a.CustomerName, b.CustomerName); n != 0 {
				return n
			}
			if n := strings.Compare(a.ServiceName, b.ServiceName); n != 0 {
				return n
			}
			return strings.Compare(a.ID, b.ID)
		})
	}
	return occurrences, rows.Err()
}

func (s *Service) UpdateOccurrenceStatus(ctx context.Context, shopID, occurrenceID, status, notes string) error {
	return s.UpdateOccurrenceStatuses(ctx, shopID, []string{occurrenceID}, status, notes)
}

func (s *Service) UpdateOccurrenceStatuses(ctx context.Context, shopID string, occurrenceIDs []string, status, notes string) error {
	if status != "completed" && status != "skipped" {
		return ErrOutcomeInput
	}
	if len(occurrenceIDs) == 0 {
		return nil
	}
	notes = strings.TrimSpace(notes)
	if len(notes) > 2000 {
		return fmt.Errorf("service outcome notes must not exceed 2000 bytes: %w", ErrOutcomeInput)
	}
	if !validText(notes) {
		return fmt.Errorf("service notes must be valid UTF-8 without NUL: %w", ErrOutcomeInput)
	}
	if err := validateIDs(append([]string{shopID}, occurrenceIDs...)...); err != nil {
		return err
	}
	unique := make(map[string]struct{}, len(occurrenceIDs))
	ids := make([]string, 0, len(occurrenceIDs))
	for _, id := range occurrenceIDs {
		if _, exists := unique[id]; exists {
			continue
		}
		unique[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	resolved := map[string]resolvedOccurrence{}
	if s.pool.Dialect() == database.SQLServer {
		values, err := prepareSQLServerOccurrences(ctx, tx, shopID, ids)
		if err != nil {
			return fmt.Errorf("load service occurrences: %w", err)
		}
		ids = ids[:0]
		for _, v := range values {
			ids = append(ids, v.id)
			resolved[v.id] = v
		}
	}
	changed := false
	for _, occurrenceID := range ids {
		var agreementID, currentStatus string
		var countsTowardEntitlement bool
		if s.pool.Dialect() == database.SQLServer {
			v := resolved[occurrenceID]
			var scheduleID string
			err = tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),schedule_id)),status FROM dbo.service_occurrences WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, occurrenceID).Scan(&scheduleID, &currentStatus)
			if err == nil && scheduleID != v.schedule {
				err = database.ErrNoRows
			}
			agreementID, countsTowardEntitlement = v.agreement, v.counts
		} else {
			err = tx.QueryRow(ctx, `
			SELECT sc.agreement_id, o.status, sc.counts_toward_entitlement
			FROM service_occurrences o
			JOIN service_schedules sc ON sc.id = o.schedule_id AND sc.shop_id = o.shop_id
			JOIN agreements a ON a.id = sc.agreement_id AND a.shop_id = sc.shop_id
			WHERE o.shop_id = $1 AND o.id = $2 AND sc.active AND a.status = 'active'
			FOR UPDATE OF o`, shopID, occurrenceID,
			).Scan(&agreementID, &currentStatus, &countsTowardEntitlement)
		}
		if err != nil {
			return fmt.Errorf("load service occurrence: %w", err)
		}
		if currentStatus != "due" {
			if currentStatus == status {
				continue
			}
			return fmt.Errorf("service outcome is already final: %w", ErrOutcomeConflict)
		}
		completedAt := any(nil)
		if status == "completed" {
			completedAt = s.now()
		}
		if _, err := tx.Exec(ctx, database.Statement(s.pool.Dialect(), `
			UPDATE service_occurrences
			SET status = $3, completed_at = $4, notes = NULLIF($5, ''), updated_at = now()
			WHERE shop_id = $1 AND id = $2`, `UPDATE dbo.service_occurrences SET status=@p3,completed_at=@p4,notes=CASE WHEN DATALENGTH(@p5)=0 THEN NULL ELSE @p5 END,updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, occurrenceID, status, completedAt, notes); err != nil {
			return err
		}
		if status == "completed" && countsTowardEntitlement {
			tag, err := tx.Exec(ctx, database.Statement(s.pool.Dialect(), `
				UPDATE agreements SET visits_used = visits_used + 1, updated_at = now()
				WHERE shop_id = $1 AND id = $2 AND visits_used < visits_entitled`, `UPDATE dbo.agreements SET visits_used=visits_used+1,updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2) AND visits_used<visits_entitled`), shopID, agreementID)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("agreement service entitlement is exhausted: %w", ErrOutcomeConflict)
			}
		}
		changed = true
	}
	if changed {
		if err := activation.RecordTx(ctx, tx, shopID, activation.EventFirstVisitAction); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) CountOverdue(ctx context.Context, shopID string) (int, error) {
	if err := s.GenerateShopOccurrences(ctx, shopID); err != nil {
		return 0, err
	}
	var count int
	err := s.pool.QueryRow(ctx, database.Statement(s.pool.Dialect(), `SELECT count(*) FROM service_occurrences o
	 JOIN service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id
	 JOIN agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
	 WHERE o.shop_id=$1 AND o.status='due' AND sc.active AND a.status='active'
	 AND COALESCE(o.scheduled_on,o.due_on)<$2`, `SELECT COUNT(*) FROM dbo.service_occurrences o JOIN dbo.service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id JOIN dbo.agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id WHERE o.shop_id=CONVERT(uniqueidentifier,@p1) AND o.status=N'due' AND sc.active=1 AND a.status=N'active' AND o.effective_on<CONVERT(date,@p2)`), shopID, dateArgument(s.pool.Dialect(), s.now())).Scan(&count)
	return count, err
}

func (s *Service) ExportRows(ctx context.Context, shopID string, dueOnly bool) ([]ExportRow, error) {
	occurrences, err := s.ListOccurrences(ctx, shopID, dueOnly)
	if err != nil {
		return nil, err
	}
	rows := make([]ExportRow, 0, len(occurrences))
	for _, occurrence := range occurrences {
		notes := fmt.Sprintf("status %s; due %s; agreement %s", occurrence.Status, occurrence.DueOn.Format("2006-01-02"), occurrence.AgreementID)
		if occurrence.Notes != "" {
			notes += "; " + occurrence.Notes
		}
		rows = append(rows, ExportRow{
			CustomerName: occurrence.CustomerName,
			Phone:        occurrence.CustomerPhone,
			Address:      occurrence.PropertyAddress,
			ServiceType:  occurrence.ServiceName,
			Notes:        notes,
		})
	}
	return rows, nil
}

func WriteExportCSV(w interface{ Write([]byte) (int, error) }, rows []ExportRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"customer_name", "phone", "address", "service_type", "notes"}); err != nil {
		return err
	}
	for _, row := range rows {
		if err := cw.Write([]string{row.CustomerName, row.Phone, row.Address, row.ServiceType, row.Notes}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func CadenceLabel(unit string, interval int) string {
	if interval == 1 {
		switch unit {
		case "day":
			return "Daily"
		case "week":
			return "Weekly"
		case "month":
			return "Monthly"
		}
	}
	name := unit
	if interval != 1 {
		name += "s"
	}
	return fmt.Sprintf("Every %d %s", interval, name)
}

func occurrenceDates(schedule Schedule, windowStart, windowEnd time.Time) []time.Time {
	startsOn := dateOnly(schedule.StartsOn)
	windowStart = dateOnly(windowStart)
	windowEnd = dateOnly(windowEnd)
	if schedule.EndsOn != nil && schedule.EndsOn.Before(windowEnd) {
		windowEnd = dateOnly(*schedule.EndsOn)
	}
	if windowEnd.Before(startsOn) || windowEnd.Before(windowStart) {
		return nil
	}

	index := firstOccurrenceIndex(schedule, windowStart)
	var dates []time.Time
	for len(dates) < 500 {
		dueOn := occurrenceAt(schedule, index)
		if dueOn.After(windowEnd) {
			break
		}
		if !dueOn.Before(windowStart) && !dueOn.Before(startsOn) {
			dates = append(dates, dueOn)
		}
		index++
	}
	return dates
}

func firstOccurrenceIndex(schedule Schedule, windowStart time.Time) int {
	startsOn := dateOnly(schedule.StartsOn)
	if !windowStart.After(startsOn) {
		return 0
	}
	switch schedule.CadenceUnit {
	case "day":
		return int((dateOnly(windowStart).Unix()-startsOn.Unix())/86400) / schedule.CadenceInterval
	case "week":
		return int((dateOnly(windowStart).Unix()-startsOn.Unix())/(86400*7)) / schedule.CadenceInterval
	case "month":
		months := (windowStart.Year()-startsOn.Year())*12 + int(windowStart.Month()-startsOn.Month())
		index := months / schedule.CadenceInterval
		for occurrenceAt(schedule, index).Before(windowStart) {
			index++
		}
		return index
	default:
		return 0
	}
}

func occurrenceAt(schedule Schedule, index int) time.Time {
	startsOn := dateOnly(schedule.StartsOn)
	switch schedule.CadenceUnit {
	case "day":
		return startsOn.AddDate(0, 0, index*schedule.CadenceInterval)
	case "week":
		return startsOn.AddDate(0, 0, index*schedule.CadenceInterval*7)
	case "month":
		return addMonthsClamped(startsOn, index*schedule.CadenceInterval)
	default:
		return startsOn
	}
}

func addMonthsClamped(date time.Time, months int) time.Time {
	first := time.Date(date.Year(), date.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
	lastDay := time.Date(first.Year(), first.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	day := date.Day()
	if day > lastDay {
		day = lastDay
	}
	return time.Date(first.Year(), first.Month(), day, 0, 0, 0, 0, time.UTC)
}

func dateOnly(value time.Time) time.Time {
	value = value.UTC()
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, time.UTC)
}

func nullableDate(value *time.Time) any {
	if value == nil {
		return nil
	}
	return dateOnly(*value)
}

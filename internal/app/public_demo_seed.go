package app

import (
	"context"
	"fmt"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func insertPublicDemoFixtureTx(ctx context.Context, tx database.Tx, shopID string, now time.Time) error {
	planIDs := make(map[string]string, len(publicDemoFixture.Plans))
	for _, plan := range publicDemoFixture.Plans {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO plan_templates (
				shop_id, name, description, price_cents, billing_interval,
				term_months, visits_per_year, visit_windows
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id`, shopID, plan.Name, plan.Description, plan.PriceCents,
			plan.BillingInterval, plan.TermMonths, plan.VisitsPerYear, plan.VisitWindows,
		).Scan(&id); err != nil {
			return fmt.Errorf("insert public demo plan %q: %w", plan.Key, err)
		}
		planIDs[plan.Key] = id
	}

	customerIDs := make(map[string]string, len(publicDemoFixture.Customers))
	for _, customer := range publicDemoFixture.Customers {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO customers (shop_id, external_id, name, email, phone)
			VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''))
			RETURNING id`, shopID, "demo-customer-"+customer.Key, customer.Name,
			customer.Email, customer.Phone,
		).Scan(&id); err != nil {
			return fmt.Errorf("insert public demo customer %q: %w", customer.Key, err)
		}
		customerIDs[customer.Key] = id
	}

	propertyIDs := make(map[string]string, len(publicDemoFixture.Properties))
	for _, property := range publicDemoFixture.Properties {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO properties (
				shop_id, customer_id, external_id, label, address_line1,
				address_line2, city, state, postal_code
			) VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), $7, $8, $9)
			RETURNING id`, shopID, customerIDs[property.CustomerKey],
			"demo-property-"+property.Key, property.Label, property.AddressLine1,
			property.AddressLine2, property.City, property.State, property.ZIP,
		).Scan(&id); err != nil {
			return fmt.Errorf("insert public demo property %q: %w", property.Key, err)
		}
		propertyIDs[property.Key] = id
	}

	for _, asset := range publicDemoFixture.Equipment {
		if _, err := tx.Exec(ctx, `
			INSERT INTO equipment (
				shop_id, property_id, external_id, kind, make, model,
				serial_number, installed_on
			) VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8)`,
			shopID, propertyIDs[asset.PropertyKey], "demo-equipment-"+asset.Key,
			asset.Kind, asset.Make, asset.Model, asset.Identifier,
			now.AddDate(0, -asset.InstalledMonthsAgo, 0),
		); err != nil {
			return fmt.Errorf("insert public demo site asset %q: %w", asset.Key, err)
		}
	}

	agreementIDs := make(map[string]string, len(publicDemoFixture.Agreements))
	for _, agreement := range publicDemoFixture.Agreements {
		var id string
		if err := tx.QueryRow(ctx, `
			INSERT INTO agreements (
				shop_id, customer_id, property_id, plan_template_id, external_id, status,
				anniversary_date, visits_entitled, visits_used, price_cents, notes,
				plan_snapshot_name, plan_snapshot_description, plan_snapshot_billing_interval,
				plan_snapshot_term_months, plan_snapshot_visits_per_year, plan_snapshot_visit_windows,
				created_at, updated_at
			)
			SELECT $1, $2, $3, pt.id, $4, $5, $6, $7, $8, $9, $10,
				pt.name, COALESCE(pt.description, ''), pt.billing_interval,
				pt.term_months, pt.visits_per_year, pt.visit_windows, $12, $12
			FROM plan_templates pt
			WHERE pt.shop_id = $1 AND pt.id = $11
			RETURNING id`, shopID, customerIDs[agreement.CustomerKey], propertyIDs[agreement.PropertyKey],
			"demo-agreement-"+agreement.Key, string(agreement.Status),
			now.AddDate(0, 0, agreement.RenewalDays), agreement.VisitsEntitled,
			agreement.VisitsUsed, agreement.PriceCents, agreement.Notes, planIDs[agreement.PlanKey],
			now,
		).Scan(&id); err != nil {
			return fmt.Errorf("insert public demo agreement %q: %w", agreement.Key, err)
		}
		agreementIDs[agreement.Key] = id
	}

	for _, schedule := range publicDemoFixture.Schedules {
		var endsOn any
		if schedule.HasEnd {
			endsOn = now.AddDate(0, 0, schedule.EndsDaysFromNow)
		}
		var pausedAt any
		if !schedule.Active {
			pausedAt = now.AddDate(0, 0, -7)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO service_schedules (
				shop_id, agreement_id, external_id, service_name, cadence_unit,
				cadence_interval, starts_on, ends_on, active, paused_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			shopID, agreementIDs[schedule.AgreementKey], "demo-service-"+schedule.Key,
			schedule.ServiceName, schedule.CadenceUnit, schedule.CadenceInterval,
			now.AddDate(0, 0, -schedule.StartsDaysAgo), endsOn, schedule.Active, pausedAt,
		); err != nil {
			return fmt.Errorf("insert public demo schedule %q: %w", schedule.Key, err)
		}
	}
	return nil
}

func (s *Store) hydratePublicDemoDerivedData(ctx context.Context, shopID string, now time.Time) error {
	if err := s.serviceWorkService().GenerateShopOccurrences(ctx, shopID); err != nil {
		return fmt.Errorf("generate public demo recurring work: %w", err)
	}
	if err := s.curatePublicDemoRecurringHistory(ctx, shopID, now); err != nil {
		return err
	}
	if err := s.SyncVisitDues(ctx, shopID); err != nil {
		return fmt.Errorf("generate public demo seasonal work: %w", err)
	}
	if err := s.curatePublicDemoSeasonalHistory(ctx, shopID, now); err != nil {
		return err
	}
	if err := s.GenerateRenewalTasks(ctx, shopID, now); err != nil {
		return fmt.Errorf("generate public demo renewal work: %w", err)
	}
	if err := s.curatePublicDemoRenewalHistory(ctx, shopID); err != nil {
		return err
	}
	return s.verifyPublicDemoRuntimeBounds(ctx, shopID, now)
}

func (s *Store) curatePublicDemoRecurringHistory(ctx context.Context, shopID string, now time.Time) error {
	if _, err := s.db.Pool.Exec(ctx, `
		UPDATE service_occurrences o
		SET status = 'completed', completed_at = o.due_on + interval '16 hours',
			notes = 'Completed on the fictional demo route', updated_at = now()
		FROM service_schedules sc
		JOIN agreements a ON a.id = sc.agreement_id AND a.shop_id = sc.shop_id
		WHERE o.shop_id = $1 AND o.schedule_id = sc.id
		  AND sc.active AND a.status = 'active' AND o.due_on < $2::date`, shopID, now); err != nil {
		return fmt.Errorf("complete public demo recurring history: %w", err)
	}

	for _, outcome := range publicDemoFixture.RecurringOutcomes {
		tag, err := s.db.Pool.Exec(ctx, `
			UPDATE service_occurrences
			SET status = $3, completed_at = NULL, notes = $4, updated_at = now()
			WHERE id = (
				SELECT o.id
				FROM service_occurrences o
				JOIN service_schedules sc ON sc.id = o.schedule_id AND sc.shop_id = o.shop_id
				WHERE o.shop_id = $1 AND sc.external_id = $2 AND o.due_on < $5::date
				ORDER BY o.due_on DESC LIMIT 1
			)`, shopID, "demo-service-"+outcome.ScheduleKey, outcome.Status, outcome.Notes, now)
		if err != nil {
			return fmt.Errorf("curate public demo recurring outcome %q: %w", outcome.ScheduleKey, err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("public demo recurring outcome %q had no source work", outcome.ScheduleKey)
		}
	}

	for _, history := range publicDemoFixture.NonActionableHistory {
		completedAt := any(nil)
		if history.Status == "completed" {
			completedAt = now.AddDate(0, 0, -history.DaysAgo).Add(16 * time.Hour)
		}
		tag, err := s.db.Pool.Exec(ctx, `
			INSERT INTO service_occurrences (
				shop_id, schedule_id, due_on, status, completed_at, notes
			)
			SELECT $1, sc.id, $3::date, $4, $5, $6
			FROM service_schedules sc
			WHERE sc.shop_id = $1 AND sc.external_id = $2
			ON CONFLICT (shop_id, schedule_id, due_on) DO NOTHING`,
			shopID, "demo-service-"+history.ScheduleKey, now.AddDate(0, 0, -history.DaysAgo),
			history.Status, completedAt, history.Notes)
		if err != nil {
			return fmt.Errorf("insert public demo inactive history %q: %w", history.ScheduleKey, err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("public demo inactive history %q was not inserted", history.ScheduleKey)
		}
	}
	return nil
}

func (s *Store) curatePublicDemoSeasonalHistory(ctx context.Context, shopID string, now time.Time) error {
	completedSeason, allowCompleted, completedAt, scheduledSeason := publicDemoSeasonalState(now)
	for _, history := range publicDemoFixture.SeasonalHistory {
		season := completedSeason
		if history.Outcome == "completed" && !allowCompleted {
			continue
		}
		if history.Outcome == "scheduled" {
			season = scheduledSeason
		}
		auditTime := completedAt
		if history.Outcome == "scheduled" {
			auditTime = now
		}
		tag, err := s.db.Pool.Exec(ctx, `
			UPDATE visit_dues vd
			SET status = $3,
				scheduled_at = CASE WHEN $3 = 'scheduled' THEN $4::timestamptz ELSE NULL END,
				completed_at = CASE WHEN $3 = 'completed' THEN $4::timestamptz ELSE NULL END,
				notes = CASE WHEN $3 = 'completed'
					THEN 'Completed in the fictional demo'
					ELSE 'Assigned on the fictional demo route' END
			FROM agreements a
			WHERE vd.shop_id = $1 AND vd.agreement_id = a.id AND a.shop_id = vd.shop_id
			  AND a.external_id = $2 AND vd.season = $5`,
			shopID, "demo-agreement-"+history.AgreementKey, history.Outcome, auditTime, season)
		if err != nil {
			return fmt.Errorf("curate public demo seasonal visit %q: %w", history.AgreementKey, err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("public demo seasonal visit %q was not curated", history.AgreementKey)
		}
		if history.Outcome == "completed" {
			tag, err := s.db.Pool.Exec(ctx, `
				UPDATE agreements SET visits_used = visits_used + 1, updated_at = now()
				WHERE shop_id = $1 AND external_id = $2 AND visits_used < visits_entitled`,
				shopID, "demo-agreement-"+history.AgreementKey)
			if err != nil {
				return fmt.Errorf("update public demo seasonal entitlement %q: %w", history.AgreementKey, err)
			}
			if tag.RowsAffected() != 1 {
				return fmt.Errorf("public demo seasonal entitlement %q was not updated", history.AgreementKey)
			}
		}
	}
	return nil
}

func (s *Store) curatePublicDemoRenewalHistory(ctx context.Context, shopID string) error {
	for _, history := range publicDemoFixture.RenewalHistory {
		tag, err := s.db.Pool.Exec(ctx, `
			UPDATE renewal_tasks t
			SET status = $3, notes = $4, updated_at = now()
			FROM agreements a
			WHERE t.shop_id = $1 AND t.agreement_id = a.id AND a.shop_id = t.shop_id
			  AND a.external_id = $2 AND t.milestone_days = $5`,
			shopID, "demo-agreement-"+history.AgreementKey, history.Status,
			history.Notes, history.MilestoneDays)
		if err != nil {
			return fmt.Errorf("curate public demo renewal history %q: %w", history.AgreementKey, err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("public demo renewal history %q was not found", history.AgreementKey)
		}
	}
	return nil
}

func publicDemoSeasonalState(now time.Time) (completedSeason string, allowCompleted bool, completedAt time.Time, scheduledSeason string) {
	switch now.Month() {
	case time.January, time.February:
		return "", false, time.Time{}, "spring"
	case time.March, time.April, time.May:
		completedAt = now.AddDate(0, 0, -2)
		springStart := time.Date(now.Year(), time.March, 1, 0, 0, 0, 0, time.UTC)
		if completedAt.Before(springStart) {
			completedAt = now
		}
		return "spring", true, completedAt, "spring"
	case time.June, time.July, time.August, time.September, time.October, time.November:
		return "spring", true, time.Date(now.Year(), time.May, 15, 16, 0, 0, 0, time.UTC), "fall"
	default:
		return "fall", true, time.Date(now.Year(), time.November, 15, 16, 0, 0, 0, time.UTC), "fall"
	}
}

func (s *Store) verifyPublicDemoRuntimeBounds(ctx context.Context, shopID string, now time.Time) error {
	var totalOccurrences, dueOccurrences int
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (
			WHERE o.status = 'due' AND sc.active AND a.status = 'active'
		)
		FROM service_occurrences o
		JOIN service_schedules sc ON sc.id = o.schedule_id AND sc.shop_id = o.shop_id
		JOIN agreements a ON a.id = sc.agreement_id AND a.shop_id = sc.shop_id
		WHERE o.shop_id = $1`, shopID).Scan(&totalOccurrences, &dueOccurrences); err != nil {
		return fmt.Errorf("measure public demo recurring work: %w", err)
	}
	if totalOccurrences == 0 || totalOccurrences > publicDemoBaseOccurrenceCeiling {
		return fmt.Errorf("public demo generated %d recurring occurrences", totalOccurrences)
	}
	if dueOccurrences == 0 || dueOccurrences > publicDemoDueOccurrenceCeiling {
		return fmt.Errorf("public demo generated %d actionable recurring occurrences", dueOccurrences)
	}

	var visitCount, visibleRenewalTasks int
	if err := s.db.Pool.QueryRow(ctx, `SELECT count(*) FROM visit_dues WHERE shop_id = $1`, shopID).Scan(&visitCount); err != nil {
		return fmt.Errorf("measure public demo seasonal work: %w", err)
	}
	if visitCount != publicDemoSeasonalVisitCount {
		return fmt.Errorf("public demo generated %d seasonal visits", visitCount)
	}
	if err := s.db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM renewal_tasks
		WHERE shop_id = $1 AND (due_at <= $2::date OR status <> 'open')`,
		shopID, now).Scan(&visibleRenewalTasks); err != nil {
		return fmt.Errorf("measure public demo renewal work: %w", err)
	}
	if visibleRenewalTasks == 0 || visibleRenewalTasks > publicDemoVisibleRenewalTaskCeiling {
		return fmt.Errorf("public demo generated %d visible renewal tasks", visibleRenewalTasks)
	}
	return nil
}

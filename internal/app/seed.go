package app

import (
	"context"
	"fmt"
	"time"
)

type SeedResult struct {
	ShopID   string `json:"shop_id"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Message  string `json:"message"`
}

const publicDemoTTL = 4 * time.Hour

type PublicDemoResult struct {
	ShopID    string
	UserID    string
	ExpiresAt time.Time
}

// RetireLegacySharedDemo removes the old public staging tenant whose published
// password granted unrestricted writes. The caller restricts this cleanup to
// staging; production customer tenants are never considered.
func (s *Store) RetireLegacySharedDemo(ctx context.Context) (int64, error) {
	result, err := s.db.Pool.Exec(ctx, `
		DELETE FROM shops s
		WHERE s.slug = 'demo-hvac-shop'
		  AND EXISTS (
			SELECT 1 FROM users u
			WHERE u.shop_id = s.id AND lower(u.email) = 'owner@demo.local'
		  )`)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (s *Store) CleanupExpiredPublicDemos(ctx context.Context) error {
	_, err := s.db.Pool.Exec(ctx,
		`DELETE FROM shops WHERE kind = 'demo_sandbox' AND expires_at <= $1`, s.nowUTC())
	return err
}

// SeedPublicDemo creates a fictional, isolated landscaping tenant. Each demo
// visitor receives their own workspace; opportunistic cleanup and a bounded
// active count prevent abandoned public sandboxes from accumulating forever.
func (s *Store) SeedPublicDemo(ctx context.Context) (PublicDemoResult, error) {
	now := s.nowUTC()
	expiresAt := now.Add(publicDemoTTL)
	suffix, err := randomToken(8)
	if err != nil {
		return PublicDemoResult{}, err
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return PublicDemoResult{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('clubdue-public-demo-sandboxes'))`); err != nil {
		return PublicDemoResult{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM shops WHERE kind = 'demo_sandbox' AND expires_at <= $1`, now); err != nil {
		return PublicDemoResult{}, err
	}
	var activeDemos int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM shops WHERE kind = 'demo_sandbox'`).Scan(&activeDemos); err != nil {
		return PublicDemoResult{}, err
	}
	if activeDemos >= 100 {
		return PublicDemoResult{}, fmt.Errorf("public demo capacity reached")
	}

	var result PublicDemoResult
	result.ExpiresAt = expiresAt
	if err := tx.QueryRow(ctx, `
		INSERT INTO shops (name, slug, kind, expires_at)
		VALUES ('Evergreen Lawn & Landscape', $1, 'demo_sandbox', $2)
		RETURNING id`, "public-demo-"+suffix, expiresAt).Scan(&result.ShopID); err != nil {
		return PublicDemoResult{}, err
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO users (shop_id, email, password_hash, role)
		VALUES ($1, $2, '!public-demo-password-login-disabled', 'owner')
		RETURNING id`, result.ShopID, "demo-"+suffix+"@clubdue.local").Scan(&result.UserID); err != nil {
		return PublicDemoResult{}, err
	}

	insertPlan := func(name, description, billing string, priceCents, visits int) (string, error) {
		var id string
		err := tx.QueryRow(ctx, `
			INSERT INTO plan_templates (
				shop_id, name, description, price_cents, billing_interval, term_months, visits_per_year, visit_windows
			) VALUES ($1, $2, $3, $4, $5, 12, $6, '{}')
			RETURNING id`, result.ShopID, name, description, priceCents, billing, visits).Scan(&id)
		return id, err
	}
	weeklyPlanID, err := insertPlan("Weekly Lawn Care", "Mow, edge, and blow with property-specific notes", "monthly", 18500, 0)
	if err != nil {
		return PublicDemoResult{}, err
	}
	propertyPlanID, err := insertPlan("Property Care", "Flexible recurring exterior maintenance", "monthly", 12500, 0)
	if err != nil {
		return PublicDemoResult{}, err
	}

	type demoCustomer struct {
		externalID, name, email, phone, address, city, state, postal string
		planID, serviceName, cadenceUnit                             string
		cadenceInterval, priceCents, renewalDays                     int
	}
	customers := []demoCustomer{
		{"land-001", "Maya Thompson", "maya@example.test", "555-0141", "184 Willow Lane", "Columbus", "OH", "43215", weeklyPlanID, "Weekly mow, edge & blow", "week", 1, 18500, 14},
		{"land-002", "Daniel Brooks", "daniel@example.test", "555-0142", "72 Garden Court", "Columbus", "OH", "43209", propertyPlanID, "Biweekly property care", "week", 2, 12500, 45},
		{"land-003", "Avery Patel", "avery@example.test", "555-0143", "910 Sycamore Drive", "Dublin", "OH", "43017", propertyPlanID, "Monthly irrigation check", "month", 1, 8900, 75},
	}
	for _, customer := range customers {
		var customerID, propertyID, agreementID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO customers (shop_id, external_id, name, email, phone)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`, result.ShopID, customer.externalID,
			customer.name, customer.email, customer.phone).Scan(&customerID); err != nil {
			return PublicDemoResult{}, err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO properties (shop_id, customer_id, external_id, label, address_line1, city, state, postal_code)
			VALUES ($1, $2, $3, 'Primary service location', $4, $5, $6, $7) RETURNING id`,
			result.ShopID, customerID, customer.externalID, customer.address, customer.city,
			customer.state, customer.postal).Scan(&propertyID); err != nil {
			return PublicDemoResult{}, err
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO agreements (
				shop_id, customer_id, property_id, plan_template_id, external_id, status,
				anniversary_date, visits_entitled, visits_used, price_cents, notes,
				plan_snapshot_name, plan_snapshot_description, plan_snapshot_billing_interval,
				plan_snapshot_term_months, plan_snapshot_visits_per_year, plan_snapshot_visit_windows
			)
			SELECT $1, $2, $3, pt.id, $4, 'active', $5, 0, 0, $6,
				'Fictional public-demo data', pt.name, COALESCE(pt.description, ''),
				pt.billing_interval, pt.term_months, pt.visits_per_year, pt.visit_windows
			FROM plan_templates pt WHERE pt.shop_id = $1 AND pt.id = $7
			RETURNING id`, result.ShopID, customerID, propertyID, customer.externalID,
			now.AddDate(0, 0, customer.renewalDays), customer.priceCents, customer.planID,
		).Scan(&agreementID); err != nil {
			return PublicDemoResult{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO service_schedules (
				shop_id, agreement_id, external_id, service_name, cadence_unit,
				cadence_interval, starts_on, ends_on
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, result.ShopID, agreementID,
			customer.externalID, customer.serviceName, customer.cadenceUnit,
			customer.cadenceInterval, now.AddDate(0, 0, -28), now.AddDate(0, 4, 0)); err != nil {
			return PublicDemoResult{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return PublicDemoResult{}, err
	}

	cleanup := func(cause error) (PublicDemoResult, error) {
		_, _ = s.db.Pool.Exec(context.WithoutCancel(ctx), `DELETE FROM shops WHERE id = $1 AND kind = 'demo_sandbox'`, result.ShopID)
		return PublicDemoResult{}, cause
	}
	if err := s.serviceWorkService().GenerateShopOccurrences(ctx, result.ShopID); err != nil {
		return cleanup(err)
	}
	if _, err := s.db.Pool.Exec(ctx, `
		UPDATE service_occurrences
		SET status = 'completed', completed_at = due_on + interval '16 hours', notes = 'Completed in fictional demo'
		WHERE shop_id = $1 AND due_on < $2::date - 7`, result.ShopID, now); err != nil {
		return cleanup(err)
	}
	if err := s.GenerateRenewalTasks(ctx, result.ShopID, now); err != nil {
		return cleanup(err)
	}
	return result, nil
}

func (s *Store) SeedDemo(ctx context.Context) (SeedResult, error) {
	const (
		shopSlug = "demo-hvac-shop"
		email    = "owner@demo.local"
		password = "demo1234"
	)

	var shopID string
	err := s.db.Pool.QueryRow(ctx, `SELECT id FROM shops WHERE slug = $1`, shopSlug).Scan(&shopID)
	if err != nil {
		hash, err := bcryptGenerate(password)
		if err != nil {
			return SeedResult{}, err
		}

		tx, err := s.db.Pool.Begin(ctx)
		if err != nil {
			return SeedResult{}, err
		}
		defer tx.Rollback(ctx)

		if err := tx.QueryRow(ctx,
			`INSERT INTO shops (name, slug) VALUES ($1, $2) RETURNING id`,
			"Demo HVAC Shop", shopSlug,
		).Scan(&shopID); err != nil {
			return SeedResult{}, err
		}

		if _, err := tx.Exec(ctx,
			`INSERT INTO users (shop_id, email, password_hash, role) VALUES ($1, $2, $3, 'owner')`,
			shopID, email, hash,
		); err != nil {
			return SeedResult{}, err
		}

		if err := tx.Commit(ctx); err != nil {
			return SeedResult{}, err
		}
	}

	var agreementCount int
	_ = s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM agreements WHERE shop_id = $1`, shopID).Scan(&agreementCount)
	if agreementCount >= 3 {
		if err := s.SyncVisitDues(ctx, shopID); err != nil {
			return SeedResult{}, err
		}
		return SeedResult{
			ShopID:   shopID,
			Email:    email,
			Password: password,
			Message:  "demo data already present (visit dues synced)",
		}, nil
	}

	planID, err := s.ensurePlanTemplate(ctx, shopID, "Gold HVAC Plan", "Spring + fall tune-ups", 24900, 2)
	if err != nil {
		return SeedResult{}, err
	}

	c1, err := s.ensureCustomer(ctx, shopID, "seed-cust-1", "Alex Rivera", "alex@example.com", "555-0101")
	if err != nil {
		return SeedResult{}, err
	}
	c2, err := s.ensureCustomer(ctx, shopID, "seed-cust-2", "Jordan Lee", "jordan@example.com", "555-0102")
	if err != nil {
		return SeedResult{}, err
	}

	now := time.Now().UTC()
	agreements := []struct {
		extID       string
		customerID  string
		status      AgreementStatus
		anniversary time.Time
		visitsUsed  int
		price       int
	}{
		{"seed-agreement-1", c1, AgreementActive, now.AddDate(0, 0, 14), 1, 24900},
		{"seed-agreement-2", c2, AgreementActive, now.AddDate(0, 1, 0), 0, 24900},
		{"seed-agreement-3", c1, AgreementLapsed, now.AddDate(0, -2, 0), 2, 24900},
	}

	for _, a := range agreements {
		if err := s.ensureAgreement(ctx, shopID, a.extID, a.customerID, planID, a.status, a.anniversary, a.visitsUsed, a.price); err != nil {
			return SeedResult{}, err
		}
	}

	if err := s.SyncVisitDues(ctx, shopID); err != nil {
		return SeedResult{}, err
	}

	return SeedResult{
		ShopID:   shopID,
		Email:    email,
		Password: password,
		Message:  "seeded demo shop with 2 customers, 3 agreements, and visit dues",
	}, nil
}

func (s *Store) ensurePlanTemplate(ctx context.Context, shopID, name, desc string, priceCents, visits int) (string, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx,
		`SELECT id FROM plan_templates WHERE shop_id = $1 AND name = $2`, shopID, name,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	return s.CreatePlanTemplate(ctx, shopID, name, desc, priceCents, 12, visits)
}

func (s *Store) ensureCustomer(ctx context.Context, shopID, extID, name, email, phone string) (string, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx,
		`SELECT id FROM customers WHERE shop_id = $1 AND external_id = $2`, shopID, extID,
	).Scan(&id)
	if err == nil {
		return id, nil
	}

	err = s.db.Pool.QueryRow(ctx, `
		INSERT INTO customers (shop_id, external_id, name, email, phone)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, shopID, extID, name, email, phone).Scan(&id)
	return id, err
}

func (s *Store) ensureAgreement(
	ctx context.Context,
	shopID, extID, customerID, planID string,
	status AgreementStatus,
	anniversary time.Time,
	visitsUsed, priceCents int,
) error {
	var exists bool
	if err := s.db.Pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM agreements WHERE shop_id = $1 AND external_id = $2)`,
		shopID, extID,
	).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}

	_, err := s.CreateAgreement(ctx, shopID, CreateAgreementInput{
		CustomerID:      customerID,
		PlanTemplateID:  planID,
		ExternalID:      extID,
		Status:          status,
		AnniversaryDate: anniversary,
		VisitsEntitled:  2,
		VisitsUsed:      visitsUsed,
		PriceCents:      priceCents,
	})
	return err
}

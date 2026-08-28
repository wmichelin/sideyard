package main

import (
	"context"
	"time"
)

type SeedResult struct {
	ShopID   string `json:"shop_id"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Message  string `json:"message"`
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

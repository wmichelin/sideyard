package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

type SeedResult struct {
	ShopID   string `json:"shop_id"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Message  string `json:"message"`
}

const (
	publicDemoTTL            = 4 * time.Hour
	publicDemoMutationBudget = 30
)

var errPublicDemoMutationBudgetReached = errors.New("public demo mutation budget reached")

type PublicDemoResult struct {
	ShopID    string
	UserID    string
	ExpiresAt time.Time
}

// ReservePublicDemoMutation admits one state-changing request for an active
// sandbox. Counting admitted attempts, including handler-level validation
// failures, keeps the abuse boundary independent of every product mutation.
func (s *Store) ReservePublicDemoMutation(ctx context.Context, shopID string) error {
	if validateRecordIDs(shopID) != nil {
		return errPublicDemoMutationBudgetReached
	}
	result, err := s.db.Pool.Exec(ctx, database.Statement(s.db.Pool.Dialect(), `
		UPDATE shops
		SET demo_mutations_used = demo_mutations_used + 1
		WHERE id = $1
		  AND kind = 'demo_sandbox'
		  AND expires_at > $2
		  AND demo_mutations_used < $3`, `UPDATE dbo.shops SET demo_mutations_used=demo_mutations_used+1 WHERE id=CONVERT(uniqueidentifier,@p1) AND kind='demo_sandbox' AND expires_at>@p2 AND demo_mutations_used<@p3`), shopID, s.nowUTC(), publicDemoMutationBudget)
	if err != nil {
		return fmt.Errorf("reserve public demo mutation: %w", err)
	}
	if result.RowsAffected() != 1 {
		return errPublicDemoMutationBudgetReached
	}
	return nil
}

// RetireLegacySharedDemo removes the old public staging tenant whose published
// password granted unrestricted writes. The caller restricts this cleanup to
// staging; production customer tenants are never considered.
func (s *Store) RetireLegacySharedDemo(ctx context.Context) (int64, error) {
	result, err := s.db.Pool.Exec(ctx, database.Statement(s.db.Pool.Dialect(), `
		DELETE FROM shops s
		WHERE s.slug = 'demo-hvac-shop'
		  AND EXISTS (
			SELECT 1 FROM users u
			WHERE u.shop_id = s.id AND lower(u.email) = 'owner@demo.local'
		  )`, `DELETE s FROM dbo.shops s WHERE s.slug=N'demo-hvac-shop' AND DATALENGTH(s.slug)=DATALENGTH(N'demo-hvac-shop') AND EXISTS(SELECT 1 FROM dbo.users u WHERE u.shop_id=s.id AND u.email_normalized=N'owner@demo.local' AND DATALENGTH(u.email_normalized)=DATALENGTH(N'owner@demo.local'))`))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

func (s *Store) CleanupExpiredPublicDemos(ctx context.Context) error {
	_, err := s.db.Pool.Exec(ctx,
		database.Statement(s.db.Pool.Dialect(), `DELETE FROM shops WHERE kind = 'demo_sandbox' AND expires_at <= $1`, `DELETE FROM dbo.shops WHERE kind='demo_sandbox' AND expires_at<=@p1`), s.nowUTC())
	return err
}

// SeedPublicDemo creates a fictional, isolated landscaping tenant. Each demo
// visitor receives their own workspace; opportunistic cleanup and a bounded
// active count prevent abandoned public sandboxes from accumulating forever.
func (s *Store) SeedPublicDemo(ctx context.Context) (PublicDemoResult, error) {
	// Serialize rich fixture creation inside this process so concurrent visitors
	// do not occupy the small database pool while waiting on the cross-process
	// advisory lock. The database lock still protects capacity during deploys.
	s.publicDemoSeedOnce.Do(func() { s.publicDemoSeedGate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return PublicDemoResult{}, err
	}
	select {
	case s.publicDemoSeedGate <- struct{}{}:
	case <-ctx.Done():
		return PublicDemoResult{}, ctx.Err()
	}
	defer func() { <-s.publicDemoSeedGate }()

	now := s.nowUTC().Truncate(time.Microsecond)
	if err := publicDemoFixture.validate(now); err != nil {
		return PublicDemoResult{}, err
	}
	expiresAt := now.Add(publicDemoTTL)
	if now.Year() < 1 || expiresAt.Year() > 9999 {
		return PublicDemoResult{}, fmt.Errorf("public demo date outside supported range")
	}
	suffix, err := randomToken(8)
	if err != nil {
		return PublicDemoResult{}, err
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return PublicDemoResult{}, err
	}
	defer tx.Rollback(ctx)
	if tx.Dialect() == database.SQLServer {
		var result int
		if err := tx.QueryRow(ctx, `DECLARE @result int; EXEC @result=sys.sp_getapplock @Resource=N'clubdue-public-demo-sandboxes',@LockMode=N'Exclusive',@LockOwner=N'Transaction',@LockTimeout=15000; SELECT @result;`).Scan(&result); err != nil {
			return PublicDemoResult{}, err
		}
		if result < 0 {
			return PublicDemoResult{}, fmt.Errorf("public demo admission unavailable")
		}
	} else if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('clubdue-public-demo-sandboxes'))`); err != nil {
		return PublicDemoResult{}, err
	}
	if _, err := tx.Exec(ctx, database.Statement(tx.Dialect(), `DELETE FROM shops WHERE kind = 'demo_sandbox' AND expires_at <= $1`, `DELETE FROM dbo.shops WHERE kind='demo_sandbox' AND expires_at<=@p1`), now); err != nil {
		return PublicDemoResult{}, err
	}
	var activeDemos int
	if err := tx.QueryRow(ctx, database.Statement(tx.Dialect(), `SELECT count(*) FROM shops WHERE kind = 'demo_sandbox'`, `SELECT COUNT_BIG(*) FROM dbo.shops WHERE kind='demo_sandbox'`)).Scan(&activeDemos); err != nil {
		return PublicDemoResult{}, err
	}
	if activeDemos >= 100 {
		return PublicDemoResult{}, fmt.Errorf("public demo capacity reached")
	}

	var result PublicDemoResult
	result.ExpiresAt = expiresAt
	if err := tx.QueryRow(ctx, database.Statement(tx.Dialect(), `
		INSERT INTO shops (name, slug, kind, expires_at)
		VALUES ('Evergreen Lawn & Landscape', $1, 'demo_sandbox', $2)
		RETURNING id`, `INSERT INTO dbo.shops(name,slug,kind,expires_at) OUTPUT LOWER(CONVERT(char(36),inserted.id)) VALUES(N'Evergreen Lawn & Landscape',@p1,'demo_sandbox',@p2)`), "public-demo-"+suffix, expiresAt).Scan(&result.ShopID); err != nil {
		return PublicDemoResult{}, err
	}
	demoEmail := "demo-" + suffix + "@clubdue.local"
	userArgs := []any{result.ShopID, demoEmail}
	if tx.Dialect() == database.SQLServer {
		userArgs = append(userArgs, strings.ToLower(demoEmail))
	}
	if err := tx.QueryRow(ctx, database.Statement(tx.Dialect(), `
		INSERT INTO users (shop_id, email, password_hash, role)
		VALUES ($1, $2, '!public-demo-password-login-disabled', 'owner')
		RETURNING id`, `INSERT INTO dbo.users(shop_id,email,email_normalized,password_hash,role) OUTPUT LOWER(CONVERT(char(36),inserted.id)) VALUES(CONVERT(uniqueidentifier,@p1),@p2,@p3,N'!public-demo-password-login-disabled','owner')`), userArgs...).Scan(&result.UserID); err != nil {
		return PublicDemoResult{}, err
	}

	if err := insertPublicDemoFixtureTx(ctx, tx, result.ShopID, now); err != nil {
		return PublicDemoResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		_ = tx.Rollback(ctx)
		s.cleanupFailedPublicDemo(ctx, result.ShopID)
		return PublicDemoResult{}, err
	}

	cleanup := func(cause error) (PublicDemoResult, error) {
		s.cleanupFailedPublicDemo(ctx, result.ShopID)
		return PublicDemoResult{}, cause
	}
	seedStore := &Store{db: s.db, clock: seedClock{now: now}, photoObjects: s.photoObjects}
	if err := seedStore.hydratePublicDemoDerivedData(ctx, result.ShopID, now); err != nil {
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
	err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT id FROM shops WHERE slug = $1`, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.shops WHERE slug=@p1 AND DATALENGTH(slug)=DATALENGTH(@p1)`), shopSlug).Scan(&shopID)
	if err != nil && !errors.Is(err, database.ErrNoRows) {
		return SeedResult{}, err
	}
	if errors.Is(err, database.ErrNoRows) {
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
			database.Statement(tx.Dialect(), `INSERT INTO shops (name, slug) VALUES ($1, $2) RETURNING id`, `INSERT INTO dbo.shops(name,slug) OUTPUT LOWER(CONVERT(char(36),inserted.id)) VALUES(@p1,@p2)`),
			"Demo HVAC Shop", shopSlug,
		).Scan(&shopID); err != nil {
			return SeedResult{}, err
		}

		if _, err := tx.Exec(ctx,
			database.Statement(tx.Dialect(), `INSERT INTO users (shop_id, email, password_hash, role) VALUES ($1, $2, $3, 'owner')`, `INSERT INTO dbo.users(shop_id,email,email_normalized,password_hash,role) VALUES(CONVERT(uniqueidentifier,@p1),@p2,@p2,@p3,'owner')`),
			shopID, email, hash,
		); err != nil {
			return SeedResult{}, err
		}

		if err := tx.Commit(ctx); err != nil {
			return SeedResult{}, err
		}
	}

	var agreementCount int
	if err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT COUNT(*) FROM agreements WHERE shop_id = $1`, `SELECT COUNT(*) FROM dbo.agreements WHERE shop_id=CONVERT(uniqueidentifier,@p1)`), shopID).Scan(&agreementCount); err != nil {
		return SeedResult{}, err
	}
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

	now := s.nowUTC()
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
		database.Statement(s.db.Pool.Dialect(), `SELECT id FROM plan_templates WHERE shop_id = $1 AND name = $2`, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.plan_templates WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND name=@p2 AND DATALENGTH(name)=DATALENGTH(@p2)`), shopID, name,
	).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, database.ErrNoRows) {
		return "", err
	}
	return s.CreatePlanTemplate(ctx, shopID, name, desc, priceCents, 12, visits)
}

func (s *Store) ensureCustomer(ctx context.Context, shopID, extID, name, email, phone string) (string, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx,
		database.Statement(s.db.Pool.Dialect(), `SELECT id FROM customers WHERE shop_id = $1 AND external_id = $2`, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.customers WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND external_id=@p2 AND DATALENGTH(external_id)=DATALENGTH(@p2)`), shopID, extID,
	).Scan(&id)
	if err == nil {
		return id, nil
	}

	if !errors.Is(err, database.ErrNoRows) {
		return "", err
	}
	err = s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `
		INSERT INTO customers (shop_id, external_id, name, email, phone)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`, `INSERT INTO dbo.customers(shop_id,external_id,name,email,phone) OUTPUT LOWER(CONVERT(char(36),inserted.id)) VALUES(CONVERT(uniqueidentifier,@p1),@p2,@p3,@p4,@p5)`), shopID, extID, name, email, phone).Scan(&id)
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
		database.Statement(s.db.Pool.Dialect(), `SELECT EXISTS(SELECT 1 FROM agreements WHERE shop_id = $1 AND external_id = $2)`, `SELECT CONVERT(bit,CASE WHEN EXISTS(SELECT 1 FROM dbo.agreements WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND external_id=@p2 AND DATALENGTH(external_id)=DATALENGTH(@p2)) THEN 1 ELSE 0 END)`),
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

// Cleanup remains independent of a cancelled request, but cannot wait forever.
func (s *Store) cleanupFailedPublicDemo(ctx context.Context, shop string) {
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	_, err := s.db.Pool.Exec(cleanup, database.Statement(s.db.Pool.Dialect(), `DELETE FROM shops WHERE id=$1 AND kind='demo_sandbox'`, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1) AND kind='demo_sandbox'`), shop)
	if err != nil {
		log.Printf("public demo cleanup incomplete: %v", err)
	}
}

type seedClock struct{ now time.Time }

func (c seedClock) Now() time.Time { return c.now }

package renewals

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Service owns renewal-link, renewal-task, and digest persistence.
type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func New(pool *pgxpool.Pool, now func() time.Time) *Service {
	return &Service{pool: pool, now: now}
}

type HomeownerRenewal struct {
	CustomerName    string
	PlanName        string
	AnniversaryDate time.Time
	PriceCents      int
	ShopName        string
}

func (s *Service) CreateRenewalLink(ctx context.Context, shopID, agreementID, tokenHash string, expiresAt time.Time) error {
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM agreements WHERE id = $1 AND shop_id = $2)`, agreementID, shopID,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("agreement not found")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO renewal_links (shop_id, agreement_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`, shopID, agreementID, tokenHash, expiresAt)
	return err
}

func (s *Service) GetHomeownerRenewal(ctx context.Context, tokenHash string) (*HomeownerRenewal, error) {
	var renewal HomeownerRenewal
	err := s.pool.QueryRow(ctx, `
		SELECT c.name, a.plan_snapshot_name, a.anniversary_date,
			a.price_cents, sh.name
		FROM renewal_links rl
		JOIN agreements a ON a.id = rl.agreement_id AND a.shop_id = rl.shop_id
		JOIN customers c ON c.id = a.customer_id
		JOIN shops sh ON sh.id = rl.shop_id
		WHERE rl.token_hash = $1 AND rl.expires_at > $2
		LIMIT 1`, tokenHash, s.now(),
	).Scan(&renewal.CustomerName, &renewal.PlanName, &renewal.AnniversaryDate,
		&renewal.PriceCents, &renewal.ShopName)
	if err != nil {
		return nil, fmt.Errorf("renewal link not found")
	}
	return &renewal, nil
}

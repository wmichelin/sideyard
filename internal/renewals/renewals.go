package renewals

import (
	"context"
	"fmt"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

// Service owns renewal-link, renewal-task, and digest persistence.
type Service struct {
	pool database.Pool
	now  func() time.Time
}

func New(pool database.Pool, now func() time.Time) *Service {
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
	if err := validateIDs(shopID, agreementID); err != nil {
		return err
	}
	if !validText(tokenHash) || !validDate(expiresAt) {
		return ErrInput
	}
	if s.pool.Dialect() == database.SQLServer {
		expiresAt = expiresAt.UTC().Truncate(time.Microsecond)
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if err := lockShop(ctx, tx, shopID); err != nil {
			return err
		}
		if _, _, _, err := lockAgreement(ctx, tx, shopID, agreementID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO dbo.renewal_links(shop_id,agreement_id,token_hash,expires_at) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,@p4)`, shopID, agreementID, tokenHash, expiresAt); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM agreements WHERE id = $1 AND shop_id = $2)`, agreementID, shopID,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("agreement not found: %w", database.ErrNoRows)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO renewal_links (shop_id, agreement_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)`, shopID, agreementID, tokenHash, expiresAt)
	return err
}

func (s *Service) GetHomeownerRenewal(ctx context.Context, tokenHash string) (*HomeownerRenewal, error) {
	if !validText(tokenHash) {
		return nil, database.ErrNoRows
	}
	var renewal HomeownerRenewal
	err := s.pool.QueryRow(ctx, database.Statement(s.pool.Dialect(), `
		SELECT c.name, a.plan_snapshot_name, a.anniversary_date,
			a.price_cents, sh.name
		FROM renewal_links rl
		JOIN agreements a ON a.id = rl.agreement_id AND a.shop_id = rl.shop_id
		JOIN customers c ON c.id = a.customer_id
		JOIN shops sh ON sh.id = rl.shop_id
		WHERE rl.token_hash = $1 AND rl.expires_at > $2
		  AND (sh.expires_at IS NULL OR sh.expires_at > $2)
		LIMIT 1`, `SELECT TOP(1) c.name,a.plan_snapshot_name,a.anniversary_date,a.price_cents,sh.name
 FROM dbo.renewal_links rl JOIN dbo.agreements a ON a.id=rl.agreement_id AND a.shop_id=rl.shop_id JOIN dbo.customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id JOIN dbo.shops sh ON sh.id=rl.shop_id
 WHERE rl.token_key=CONVERT(binary(32),HASHBYTES('SHA2_256',CONVERT(varbinary(max),CONVERT(nvarchar(max),@p1)))) AND rl.token_hash=@p1 AND DATALENGTH(rl.token_hash)=DATALENGTH(@p1)
 AND rl.expires_at>@p2 AND (sh.expires_at IS NULL OR sh.expires_at>@p2)`), tokenHash, s.now().UTC().Truncate(time.Microsecond),
	).Scan(&renewal.CustomerName, &renewal.PlanName, &renewal.AnniversaryDate,
		&renewal.PriceCents, &renewal.ShopName)
	if err != nil {
		return nil, fmt.Errorf("load renewal link: %w", err)
	}
	return &renewal, nil
}

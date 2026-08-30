package clubdue

import (
	"context"
	"time"

	"github.com/wmichelin/clubdue/internal/renewals"
)

type HomeownerRenewal = renewals.HomeownerRenewal
type RenewalTask = renewals.RenewalTask

func (s *Store) renewalService() *renewals.Service {
	return renewals.New(s.db.Pool, s.nowUTC)
}

func (s *Store) CreateRenewalLink(ctx context.Context, shopID, agreementID, tokenHash string, expiresAt time.Time) error {
	return s.renewalService().CreateRenewalLink(ctx, shopID, agreementID, tokenHash, expiresAt)
}

func (s *Store) GetHomeownerRenewal(ctx context.Context, tokenHash string) (*HomeownerRenewal, error) {
	return s.renewalService().GetHomeownerRenewal(ctx, tokenHash)
}

func (s *Store) GenerateRenewalTasks(ctx context.Context, shopID string, asOf time.Time) error {
	return s.renewalService().GenerateRenewalTasks(ctx, shopID, asOf)
}

func (s *Store) GenerateAllRenewalTasks(ctx context.Context, asOf time.Time) error {
	return s.renewalService().GenerateAllRenewalTasks(ctx, asOf)
}

func (s *Store) QueueRenewalDigests(ctx context.Context, asOf time.Time) error {
	return s.renewalService().QueueRenewalDigests(ctx, asOf)
}

func (s *Store) ListRenewalTasks(ctx context.Context, shopID string) ([]RenewalTask, error) {
	return s.renewalService().ListRenewalTasks(ctx, shopID)
}

func (s *Store) UpdateRenewalTask(ctx context.Context, shopID, id, status, notes string) error {
	return s.renewalService().UpdateRenewalTask(ctx, shopID, id, status, notes)
}

func renewalCycleStart(anniversary, today time.Time, termMonths int) (time.Time, error) {
	return renewals.CycleStart(anniversary, today, termMonths)
}

func renewalDueState(days int) string {
	return renewals.DueState(days)
}

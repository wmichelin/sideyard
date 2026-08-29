package clubdue

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type pilotActivationEvent string

const (
	pilotEventInvited            pilotActivationEvent = "invited"
	pilotEventAccountCreated     pilotActivationEvent = "account_created"
	pilotEventImportPreviewed    pilotActivationEvent = "import_previewed"
	pilotEventImportCommitted    pilotActivationEvent = "import_committed"
	pilotEventFirstRenewalAction pilotActivationEvent = "first_renewal_action"
	pilotEventFirstVisitAction   pilotActivationEvent = "first_visit_action"
	pilotEventReturningUse       pilotActivationEvent = "returning_use"
)

func validPilotActivationEvent(event pilotActivationEvent) bool {
	switch event {
	case pilotEventInvited, pilotEventAccountCreated, pilotEventImportPreviewed,
		pilotEventImportCommitted, pilotEventFirstRenewalAction,
		pilotEventFirstVisitAction, pilotEventReturningUse:
		return true
	default:
		return false
	}
}

func recordPilotActivationTx(ctx context.Context, tx pgx.Tx, shopID string, event pilotActivationEvent) error {
	if !validPilotActivationEvent(event) {
		return fmt.Errorf("invalid pilot activation event %q", event)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO pilot_activation_events (shop_id, event_type)
		VALUES ($1, $2)
		ON CONFLICT (shop_id, event_type) DO NOTHING`, shopID, event)
	return err
}

func (s *Store) RecordPilotActivation(ctx context.Context, shopID string, event pilotActivationEvent) error {
	if !validPilotActivationEvent(event) {
		return fmt.Errorf("invalid pilot activation event %q", event)
	}
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO pilot_activation_events (shop_id, event_type)
		VALUES ($1, $2)
		ON CONFLICT (shop_id, event_type) DO NOTHING`, shopID, event)
	return err
}

// RecordReturningPilotUse records one later-day authenticated use. It avoids
// treating the first post-provision login as retention or tracking each visit.
func (s *Store) RecordReturningPilotUse(ctx context.Context, shopID string) error {
	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO pilot_activation_events (shop_id, event_type)
		SELECT $1, $2
		WHERE EXISTS (
			SELECT 1 FROM pilot_activation_events
			WHERE shop_id = $1 AND event_type = 'account_created'
			  AND occurred_at < date_trunc('day', now())
		)
		ON CONFLICT (shop_id, event_type) DO NOTHING`, shopID, pilotEventReturningUse)
	return err
}

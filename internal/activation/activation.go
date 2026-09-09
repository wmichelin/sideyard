// Package activation records the first safe pilot-use milestones.
package activation

import (
	"context"
	"fmt"

	"github.com/wmichelin/clubdue/internal/database"
)

type Event string

const (
	EventInvited            Event = "invited"
	EventAccountCreated     Event = "account_created"
	EventImportPreviewed    Event = "import_previewed"
	EventImportCommitted    Event = "import_committed"
	EventFirstRenewalAction Event = "first_renewal_action"
	EventFirstVisitAction   Event = "first_visit_action"
	EventReturningUse       Event = "returning_use"
)

func validEvent(event Event) bool {
	switch event {
	case EventInvited, EventAccountCreated, EventImportPreviewed,
		EventImportCommitted, EventFirstRenewalAction,
		EventFirstVisitAction, EventReturningUse:
		return true
	default:
		return false
	}
}

// RecordTx records an event in the caller's transaction.
func RecordTx(ctx context.Context, tx database.Tx, shopID string, event Event) error {
	if !validEvent(event) {
		return fmt.Errorf("invalid pilot activation event %q", event)
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO pilot_activation_events (shop_id, event_type)
		VALUES ($1, $2)
		ON CONFLICT (shop_id, event_type) DO NOTHING`, shopID, event)
	return err
}

// Record stores an event with the supplied Postgres pool.
func Record(ctx context.Context, pool database.Pool, shopID string, event Event) error {
	if !validEvent(event) {
		return fmt.Errorf("invalid pilot activation event %q", event)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO pilot_activation_events (shop_id, event_type)
		VALUES ($1, $2)
		ON CONFLICT (shop_id, event_type) DO NOTHING`, shopID, event)
	return err
}

// RecordReturningUse records one later-day authenticated use. It avoids
// treating the first post-provision login as retention or tracking each visit.
func RecordReturningUse(ctx context.Context, pool database.Pool, shopID string) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO pilot_activation_events (shop_id, event_type)
		SELECT $1, $2
		WHERE EXISTS (
			SELECT 1 FROM pilot_activation_events
			WHERE shop_id = $1 AND event_type = 'account_created'
			  AND occurred_at < date_trunc('day', now())
		)
		ON CONFLICT (shop_id, event_type) DO NOTHING`, shopID, EventReturningUse)
	return err
}

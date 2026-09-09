package app

import (
	"context"

	"github.com/wmichelin/clubdue/internal/activation"
	"github.com/wmichelin/clubdue/internal/database"
)

type pilotActivationEvent = activation.Event

const (
	pilotEventInvited            = activation.EventInvited
	pilotEventAccountCreated     = activation.EventAccountCreated
	pilotEventImportPreviewed    = activation.EventImportPreviewed
	pilotEventImportCommitted    = activation.EventImportCommitted
	pilotEventFirstRenewalAction = activation.EventFirstRenewalAction
	pilotEventFirstVisitAction   = activation.EventFirstVisitAction
	pilotEventReturningUse       = activation.EventReturningUse
)

func recordPilotActivationTx(ctx context.Context, tx database.Tx, shopID string, event pilotActivationEvent) error {
	return activation.RecordTx(ctx, tx, shopID, event)
}

func (s *Store) RecordPilotActivation(ctx context.Context, shopID string, event pilotActivationEvent) error {
	return activation.Record(ctx, s.db.Pool, shopID, event)
}

func (s *Store) RecordReturningPilotUse(ctx context.Context, shopID string) error {
	return activation.RecordReturningUse(ctx, s.db.Pool, shopID)
}

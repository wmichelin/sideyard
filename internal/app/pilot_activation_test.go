package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestPilotActivationEventsCaptureFirstSafeMilestones(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}
	stamp := time.Now().UTC().Format("20060102150405.000000000")

	auth := newAuth(db, strings.Repeat("s", 32), false)
	ownerEmail := "pilot-owner-" + stamp + "@example.test"
	if action, err := auth.provisionOwner(ctx, OwnerCredentials{Email: ownerEmail, Password: "pilot-password", ShopName: "Pilot Activation " + stamp}); err != nil || action != "provisioned" {
		t.Fatalf("provision owner = %q, %v", action, err)
	}
	var provisionedShopID string
	if err := db.Pool.QueryRow(ctx, `SELECT shop_id FROM users WHERE email = $1`, ownerEmail).Scan(&provisionedShopID); err != nil {
		t.Fatal(err)
	}
	assertPilotEvents(t, ctx, db, provisionedShopID, pilotEventInvited, pilotEventAccountCreated)

	if err := store.RecordPilotActivation(ctx, provisionedShopID, pilotActivationEvent("unexpected")); err == nil {
		t.Fatal("invalid activation event was accepted")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE pilot_activation_events SET occurred_at = now() - interval '1 day' WHERE shop_id = $1 AND event_type = 'account_created'`, provisionedShopID); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordReturningPilotUse(ctx, provisionedShopID); err != nil {
		t.Fatal(err)
	}
	assertPilotEvents(t, ctx, db, provisionedShopID, pilotEventReturningUse)

	shopID := createTestShop(t, ctx, db, "pilot-events-"+stamp)
	planID := createTestPlan(t, ctx, db, shopID, "Pilot Event Plan")
	customerID := createTestCustomer(t, ctx, db, shopID, "Pilot Event Customer")
	agreementID, err := store.CreateAgreement(ctx, shopID, CreateAgreementInput{CustomerID: customerID, PlanTemplateID: planID, Status: AgreementActive, AnniversaryDate: time.Now().UTC().AddDate(0, 0, 7), VisitsEntitled: 2, PriceCents: 19900})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordPilotActivation(ctx, shopID, pilotEventImportPreviewed); err != nil {
		t.Fatal(err)
	}
	rows, err := parseImportCSV(strings.NewReader("external_id,customer_name,email,phone,address_line1,city,state,postal_code,plan_name,anniversary_date,status,price_cents\npilot-event-import,Import Example,import@example.test,555-0111,1 Example Way,Denver,CO,80202,Pilot Event Plan,2026-10-01,active,19900\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitImport(ctx, shopID, rows); err != nil {
		t.Fatal(err)
	}
	if err := store.GenerateRenewalTasks(ctx, shopID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListRenewalTasks(ctx, shopID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("renewal tasks = %d, %v", len(tasks), err)
	}
	if err := store.UpdateRenewalTask(ctx, shopID, tasks[0].ID, "contacted", "pilot rehearsal"); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncVisitDues(ctx, shopID); err != nil {
		t.Fatal(err)
	}
	visits, err := store.ListVisitDues(ctx, shopID, false)
	if err != nil || len(visits) == 0 {
		t.Fatalf("visit dues = %d, %v", len(visits), err)
	}
	if err := store.BulkUpdateVisitStatus(ctx, shopID, []string{visits[0].ID}, "scheduled"); err != nil {
		t.Fatal(err)
	}
	_ = agreementID
	assertPilotEvents(t, ctx, db, shopID, pilotEventImportPreviewed, pilotEventImportCommitted, pilotEventFirstRenewalAction, pilotEventFirstVisitAction)
}

func assertPilotEvents(t *testing.T, ctx context.Context, db *DB, shopID string, want ...pilotActivationEvent) {
	t.Helper()
	for _, event := range want {
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM pilot_activation_events WHERE shop_id = $1 AND event_type = $2`, shopID, event).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("event %q count = %d, want 1", event, count)
		}
	}
}

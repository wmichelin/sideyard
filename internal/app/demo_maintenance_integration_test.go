package app

import (
	"context"
	"testing"
	"time"
)

func TestGlobalMaintenanceSkipsPublicDemoSandboxes(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	demo, err := store.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var agreementID, scheduleID string
	if err := db.Pool.QueryRow(ctx, `
		SELECT id FROM agreements
		WHERE shop_id = $1 AND status = 'active'
		ORDER BY id LIMIT 1`, demo.ShopID).Scan(&agreementID); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `
		INSERT INTO service_schedules (
			shop_id, agreement_id, external_id, service_name,
			cadence_unit, cadence_interval, starts_on
		) VALUES ($1, $2, 'maintenance-exclusion-test', 'Daily route', 'day', 1, $3)
		RETURNING id`, demo.ShopID, agreementID, now.AddDate(0, 0, -30)).Scan(&scheduleID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `
		UPDATE agreements
		SET visits_entitled = 2, visits_used = 0,
			plan_snapshot_visits_per_year = 2,
			plan_snapshot_visit_windows = ARRAY['spring']::text[],
			anniversary_date = $2
		WHERE shop_id = $1 AND id = $3`, demo.ShopID, now.AddDate(0, 0, 30), agreementID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM renewal_tasks WHERE shop_id = $1`, demo.ShopID); err != nil {
		t.Fatal(err)
	}

	stamp := time.Now().UTC().Format("20060102150405.000000000")
	customerShopID := createTestShop(t, ctx, db, "maintenance-customer-"+stamp)
	customerID := createTestCustomer(t, ctx, db, customerShopID, "Maintenance Customer")
	planID, err := store.CreatePlanTemplate(ctx, customerShopID, "Maintenance Plan", "Global maintenance control", 19900, 12, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAgreement(ctx, customerShopID, CreateAgreementInput{
		CustomerID: customerID, PlanTemplateID: planID, Status: AgreementActive,
		AnniversaryDate: now.AddDate(0, 0, 30), VisitsEntitled: 2, PriceCents: 19900,
	}); err != nil {
		t.Fatal(err)
	}

	if err := store.GenerateAllServiceOccurrences(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countScheduleOccurrences(t, ctx, db, demo.ShopID, scheduleID); got != 0 {
		t.Fatalf("global maintenance generated %d public-demo occurrences", got)
	}
	if err := store.SyncAllVisitDues(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countShopRows(t, ctx, db, "visit_dues", demo.ShopID); got != 0 {
		t.Fatalf("global maintenance generated %d public-demo visit dues", got)
	}
	if got := countShopRows(t, ctx, db, "visit_dues", customerShopID); got != 2 {
		t.Fatalf("global maintenance generated %d customer visit dues, want 2", got)
	}
	if err := store.GenerateAllRenewalTasks(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got := countShopRows(t, ctx, db, "renewal_tasks", demo.ShopID); got != 0 {
		t.Fatalf("global maintenance generated %d public-demo renewal tasks", got)
	}
	if got := countShopRows(t, ctx, db, "renewal_tasks", customerShopID); got != 5 {
		t.Fatalf("global maintenance generated %d customer renewal tasks, want 5", got)
	}
	if err := store.QueueRenewalDigests(ctx, now); err != nil {
		t.Fatal(err)
	}
	if got := countShopRows(t, ctx, db, "renewal_digest_outbox", demo.ShopID); got != 0 {
		t.Fatalf("global maintenance queued %d public-demo renewal digests", got)
	}
	if got := countShopRows(t, ctx, db, "renewal_digest_outbox", customerShopID); got != 1 {
		t.Fatalf("global maintenance queued %d customer renewal digests, want 1", got)
	}

	if _, err := store.ListServiceOccurrences(ctx, demo.ShopID, false); err != nil {
		t.Fatal(err)
	}
	if got := countScheduleOccurrences(t, ctx, db, demo.ShopID, scheduleID); got == 0 {
		t.Fatal("tenant-local service-work view did not generate public-demo occurrences")
	}
	if err := store.SyncVisitDues(ctx, demo.ShopID); err != nil {
		t.Fatal(err)
	}
	if got := countShopRows(t, ctx, db, "visit_dues", demo.ShopID); got != 1 {
		t.Fatalf("tenant-local sync generated %d public-demo visit dues, want 1", got)
	}
	if err := store.GenerateRenewalTasks(ctx, demo.ShopID, now); err != nil {
		t.Fatal(err)
	}
	if got := countShopRows(t, ctx, db, "renewal_tasks", demo.ShopID); got == 0 {
		t.Fatal("tenant-local generation did not create public-demo renewal tasks")
	}
}

func countScheduleOccurrences(t *testing.T, ctx context.Context, db *DB, shopID, scheduleID string) int {
	t.Helper()
	var count int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM service_occurrences
		WHERE shop_id = $1 AND schedule_id = $2`, shopID, scheduleID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

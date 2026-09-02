package app

import (
	"context"
	"testing"
	"time"
)

func TestGlobalOccurrenceMaintenanceSkipsPublicDemoSandboxes(t *testing.T) {
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

	if err := store.GenerateAllServiceOccurrences(ctx); err != nil {
		t.Fatal(err)
	}
	if got := countScheduleOccurrences(t, ctx, db, demo.ShopID, scheduleID); got != 0 {
		t.Fatalf("global maintenance generated %d public-demo occurrences", got)
	}
	if _, err := store.ListServiceOccurrences(ctx, demo.ShopID, false); err != nil {
		t.Fatal(err)
	}
	if got := countScheduleOccurrences(t, ctx, db, demo.ShopID, scheduleID); got == 0 {
		t.Fatal("tenant-local service-work view did not generate public-demo occurrences")
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

package app

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSQLServerOperatorMaintenanceIsBoundedAndPreservesRealShops(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s := &Store{db: db}
	real := createTestShop(t, ctx, db, "operator-real")
	customer := createTestCustomer(t, ctx, db, real, "Operator Customer")
	plan, err := s.CreatePlanTemplate(ctx, real, "Operator Plan", "Maintenance fixture", 19900, 12, 2)
	if err != nil {
		t.Fatal(err)
	}
	agreement, err := s.CreateAgreement(ctx, real, CreateAgreementInput{
		CustomerID: customer, PlanTemplateID: plan, Status: AgreementActive,
		AnniversaryDate: time.Now().UTC().AddDate(0, 0, 30), VisitsEntitled: 2, PriceCents: 19900,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET plan_snapshot_visit_windows=N'["spring","fall"]' WHERE id=CONVERT(uniqueidentifier,@p1)`, agreement); err != nil {
		t.Fatal(err)
	}
	schedule, err := s.CreateServiceSchedule(ctx, real, ServiceScheduleInput{
		AgreementID: agreement, ServiceName: "Operator weekly service", CadenceUnit: "week",
		CadenceInterval: 1, StartsOn: time.Now().UTC().AddDate(0, 0, -14),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.service_occurrences WHERE schedule_id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.service_schedules SET generated_through=NULL WHERE id=CONVERT(uniqueidentifier,@p1)`, schedule); err != nil {
		t.Fatal(err)
	}
	demo := createTestShop(t, ctx, db, "operator-expired")
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.shops SET kind='demo_sandbox',expires_at=@p2 WHERE id=CONVERT(uniqueidentifier,@p1)`, demo, time.Now().UTC().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	var legacy string
	if err := db.Pool.QueryRow(ctx, `DECLARE @created TABLE(id uniqueidentifier); INSERT INTO dbo.shops(name,slug) OUTPUT inserted.id INTO @created VALUES(N'Legacy shared demo',N'demo-hvac-shop'); SELECT LOWER(CONVERT(char(36),id)) FROM @created`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if err := insertTestUser(ctx, db, legacy, "owner@demo.local"); err != nil {
		t.Fatal(err)
	}
	if err := executeDatabaseCommand(ctx, Config{Environment: EnvironmentStaging}, db, "check-db"); err != nil {
		t.Fatal(err)
	}
	// The maintenance implementation remains environment-aware even though the
	// public operator entry point rejects production in this staging-only slice.
	if err := executeDatabaseCommand(ctx, Config{Environment: EnvironmentProduction}, db, "maintenance"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, legacy).Scan(&count); err != nil || count != 1 {
		t.Fatal("production maintenance retired the staging legacy marker", err)
	}
	firstCounts := []int{
		countShopRows(t, ctx, db, "visit_dues", real),
		countShopRows(t, ctx, db, "service_occurrences", real),
		countShopRows(t, ctx, db, "renewal_tasks", real),
		countShopRows(t, ctx, db, "renewal_digest_outbox", real),
	}
	if firstCounts[0] != 2 || firstCounts[1] == 0 || firstCounts[2] != 5 || firstCounts[3] != 1 {
		t.Fatalf("maintenance outcomes = %v, want 2 visit dues, occurrences, 5 renewal tasks and 1 digest", firstCounts)
	}
	if err := executeDatabaseCommand(ctx, Config{Environment: EnvironmentStaging}, db, "maintenance"); err != nil {
		t.Fatal(err)
	}
	if err := executeDatabaseCommand(ctx, Config{Environment: EnvironmentStaging}, db, "maintenance"); err != nil {
		t.Fatal("repeat maintenance", err)
	}
	secondCounts := []int{
		countShopRows(t, ctx, db, "visit_dues", real),
		countShopRows(t, ctx, db, "service_occurrences", real),
		countShopRows(t, ctx, db, "renewal_tasks", real),
		countShopRows(t, ctx, db, "renewal_digest_outbox", real),
	}
	for index := range firstCounts {
		if secondCounts[index] != firstCounts[index] {
			t.Fatalf("repeat maintenance changed outcome counts: first=%v second=%v", firstCounts, secondCounts)
		}
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, real).Scan(&count); err != nil || count != 1 {
		t.Fatal("real shop lost", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, demo).Scan(&count); err != nil || count != 0 {
		t.Fatal("expired demo retained", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, legacy).Scan(&count); err != nil || count != 0 {
		t.Fatal("staging legacy demo retained", err)
	}
	if _, err := s.ListCustomers(ctx, real); err != nil {
		t.Fatal(err)
	}
	stopped, stop := context.WithCancel(ctx)
	stop()
	if err := executeDatabaseCommand(stopped, Config{}, db, "maintenance"); err == nil {
		t.Fatal("cancelled command reported success")
	}
	if err := executeDatabaseCommand(ctx, Config{}, db, "unknown"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal("unknown command accepted", err)
	}
}

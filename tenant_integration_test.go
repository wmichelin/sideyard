package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// This test intentionally uses the same migrations as the application. Set
// DATABASE_URL to a disposable Postgres database to run it locally or in CI.
func TestAgreementReferencesStayWithinShopAndCustomer(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)

	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}

	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopOne := createTestShop(t, ctx, db, "one-"+stamp)
	shopTwo := createTestShop(t, ctx, db, "two-"+stamp)
	customerOne := createTestCustomer(t, ctx, db, shopOne, "Customer One")
	customerOneOther := createTestCustomer(t, ctx, db, shopOne, "Customer One Other")
	customerTwo := createTestCustomer(t, ctx, db, shopTwo, "Customer Two")
	propertyOne := createTestProperty(t, ctx, db, shopOne, customerOne, "1 First St")
	propertyTwo := createTestProperty(t, ctx, db, shopTwo, customerTwo, "2 Second St")
	planOne := createTestPlan(t, ctx, db, shopOne, "Plan One")
	planTwo := createTestPlan(t, ctx, db, shopTwo, "Plan Two")

	valid := CreateAgreementInput{
		CustomerID: customerOne, PropertyID: propertyOne, PlanTemplateID: planOne,
		Status: AgreementActive, AnniversaryDate: time.Now().UTC().AddDate(1, 0, 0),
		VisitsEntitled: 2, PriceCents: 19900,
	}
	agreementID, err := store.CreateAgreement(ctx, shopOne, valid)
	if err != nil {
		t.Fatalf("create valid agreement: %v", err)
	}

	for _, tc := range []struct {
		name string
		in   CreateAgreementInput
	}{
		{"foreign customer", withAgreementField(valid, func(in *CreateAgreementInput) { in.CustomerID = customerTwo })},
		{"foreign property", withAgreementField(valid, func(in *CreateAgreementInput) { in.PropertyID = propertyTwo })},
		{"foreign template", withAgreementField(valid, func(in *CreateAgreementInput) { in.PlanTemplateID = planTwo })},
		{"property for another customer", withAgreementField(valid, func(in *CreateAgreementInput) { in.CustomerID = customerOneOther })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.CreateAgreement(ctx, shopOne, tc.in); err == nil {
				t.Fatal("expected cross-tenant or cross-customer reference to be rejected")
			}
		})
	}
	if err := store.UpdateAgreement(ctx, shopOne, agreementID,
		withAgreementField(valid, func(in *CreateAgreementInput) { in.PlanTemplateID = planTwo })); err == nil {
		t.Fatal("expected update with a foreign plan template to be rejected")
	}

	createTestUser(t, ctx, db, shopOne, "owner-"+stamp+"@example.test")
	if err := insertTestUser(ctx, db, shopTwo, "owner-"+stamp+"@example.test"); err == nil {
		t.Fatal("expected duplicate email across shops to be rejected")
	}
}

func TestAgreementKeepsPlanTermsSnapshotAfterTemplateEdit(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)

	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopID := createTestShop(t, ctx, db, "snapshot-"+stamp)
	customerID := createTestCustomer(t, ctx, db, shopID, "Snapshot Customer")
	planID, err := store.CreatePlanTemplate(ctx, shopID, "Gold 2026", "Two precision tune-ups", 29900, 12, 2)
	if err != nil {
		t.Fatal(err)
	}
	agreementID, err := store.CreateAgreement(ctx, shopID, CreateAgreementInput{
		CustomerID: customerID, PlanTemplateID: planID, Status: AgreementActive,
		AnniversaryDate: time.Now().UTC().AddDate(1, 0, 0), VisitsEntitled: 2, PriceCents: 29900,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdatePlanTemplate(ctx, shopID, planID, "Gold 2027", "One annual check", 39900, 24, 1); err != nil {
		t.Fatal(err)
	}

	var name, description, billingInterval string
	var termMonths, visitsPerYear int
	if err := db.Pool.QueryRow(ctx, `
		SELECT plan_snapshot_name, plan_snapshot_description, plan_snapshot_billing_interval,
			plan_snapshot_term_months, plan_snapshot_visits_per_year
		FROM agreements WHERE id = $1`, agreementID,
	).Scan(&name, &description, &billingInterval, &termMonths, &visitsPerYear); err != nil {
		t.Fatal(err)
	}
	if name != "Gold 2026" || description != "Two precision tune-ups" || billingInterval != "annual" || termMonths != 12 || visitsPerYear != 2 {
		t.Fatalf("agreement snapshot changed or was incomplete: name=%q description=%q interval=%q term=%d visits=%d", name, description, billingInterval, termMonths, visitsPerYear)
	}
	agreement, err := store.GetAgreement(ctx, shopID, agreementID)
	if err != nil {
		t.Fatal(err)
	}
	if agreement.PlanName != "Gold 2026" {
		t.Fatalf("agreement list/detail used live template name %q", agreement.PlanName)
	}
}

func TestGenerateRenewalTasksCreatesCycleAwareMilestonesIdempotently(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)

	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopID := createTestShop(t, ctx, db, "renewal-"+stamp)
	customerID := createTestCustomer(t, ctx, db, shopID, "Renewal Customer")
	planID := createTestPlan(t, ctx, db, shopID, "Renewal Plan")
	asOf := time.Date(2030, time.June, 1, 12, 0, 0, 0, time.UTC)
	agreementID, err := store.CreateAgreement(ctx, shopID, CreateAgreementInput{
		CustomerID: customerID, PlanTemplateID: planID, Status: AgreementActive,
		AnniversaryDate: asOf.AddDate(0, 0, 30), VisitsEntitled: 2, PriceCents: 19900,
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := store.GenerateRenewalTasks(ctx, shopID, asOf); err != nil {
		t.Fatal(err)
	}
	if err := store.GenerateRenewalTasks(ctx, shopID, asOf); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM renewal_tasks WHERE shop_id = $1 AND agreement_id = $2`, shopID, agreementID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Fatalf("expected one task for each milestone after repeated runs, got %d", count)
	}

	var cycleStart, dueAt time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT cycle_start_date, due_at FROM renewal_tasks WHERE agreement_id = $1 AND milestone_days = 30`, agreementID).Scan(&cycleStart, &dueAt); err != nil {
		t.Fatal(err)
	}
	expectedCycleStart := time.Date(2030, time.July, 1, 0, 0, 0, 0, time.UTC)
	expectedDueAt := time.Date(2030, time.June, 1, 0, 0, 0, 0, time.UTC)
	if !cycleStart.Equal(expectedCycleStart) || !dueAt.Equal(expectedDueAt) {
		t.Fatalf("unexpected cycle timing: cycle=%s due=%s", cycleStart, dueAt)
	}
}

func TestRenewedTaskRollsAgreementAndRetiresCycle(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopID := createTestShop(t, ctx, db, "rollover-"+stamp)
	customerID := createTestCustomer(t, ctx, db, shopID, "Rollover Customer")
	planID := createTestPlan(t, ctx, db, shopID, "Rollover Plan")
	asOf := time.Date(2031, time.March, 1, 0, 0, 0, 0, time.UTC)
	cycleStart := asOf.AddDate(0, 0, 30)
	agreementID, err := store.CreateAgreement(ctx, shopID, CreateAgreementInput{
		CustomerID: customerID, PlanTemplateID: planID, Status: AgreementActive,
		AnniversaryDate: cycleStart, VisitsEntitled: 2, VisitsUsed: 2, PriceCents: 19900,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.GenerateRenewalTasks(ctx, shopID, asOf); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM renewal_tasks WHERE agreement_id = $1 AND milestone_days = 30`, agreementID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRenewalTask(ctx, shopID, taskID, "renewed", "confirmed"); err != nil {
		t.Fatal(err)
	}

	var anniversary time.Time
	var visitsUsed int
	if err := db.Pool.QueryRow(ctx, `SELECT anniversary_date, visits_used FROM agreements WHERE id = $1`, agreementID).Scan(&anniversary, &visitsUsed); err != nil {
		t.Fatal(err)
	}
	if !anniversary.Equal(time.Date(2032, time.March, 31, 0, 0, 0, 0, time.UTC)) || visitsUsed != 0 {
		t.Fatalf("renewal did not advance term/reset visits: anniversary=%s visits=%d", anniversary, visitsUsed)
	}
	var staleOpen int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM renewal_tasks WHERE agreement_id = $1 AND cycle_start_date = $2 AND status IN ('open', 'contacted')`, agreementID, cycleStart).Scan(&staleOpen); err != nil {
		t.Fatal(err)
	}
	if staleOpen != 0 {
		t.Fatalf("expected no open tasks in renewed cycle, got %d", staleOpen)
	}
	var nextCycleTasks int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM renewal_tasks WHERE agreement_id = $1 AND cycle_start_date = $2`, agreementID, anniversary).Scan(&nextCycleTasks); err != nil {
		t.Fatal(err)
	}
	if nextCycleTasks != 5 {
		t.Fatalf("expected next cycle tasks after renewal, got %d", nextCycleTasks)
	}
}

func TestQueueRenewalDigestsIsDailyAndIdempotent(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopID := createTestShop(t, ctx, db, "digest-"+stamp)
	customerID := createTestCustomer(t, ctx, db, shopID, "Digest Customer")
	planID := createTestPlan(t, ctx, db, shopID, "Digest Plan")
	asOf := time.Date(2032, time.August, 1, 0, 0, 0, 0, time.UTC)
	_, err = store.CreateAgreement(ctx, shopID, CreateAgreementInput{
		CustomerID: customerID, PlanTemplateID: planID, Status: AgreementActive,
		AnniversaryDate: asOf.AddDate(0, 0, 30), VisitsEntitled: 2, PriceCents: 19900,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.GenerateRenewalTasks(ctx, shopID, asOf); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueRenewalDigests(ctx, asOf); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueRenewalDigests(ctx, asOf); err != nil {
		t.Fatal(err)
	}

	var count, taskCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*), cardinality(task_ids) FROM renewal_digest_outbox WHERE shop_id = $1 GROUP BY task_ids`, shopID).Scan(&count, &taskCount); err != nil {
		t.Fatal(err)
	}
	if count != 1 || taskCount != 3 {
		t.Fatalf("expected one digest containing the 90/60/30-day tasks, got rows=%d tasks=%d", count, taskCount)
	}
}

func TestCompletedVisitCannotExceedAgreementEntitlement(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopID := createTestShop(t, ctx, db, "visit-"+stamp)
	customerID := createTestCustomer(t, ctx, db, shopID, "Visit Customer")
	planID := createTestPlan(t, ctx, db, shopID, "Visit Plan")
	agreementID, err := store.CreateAgreement(ctx, shopID, CreateAgreementInput{CustomerID: customerID, PlanTemplateID: planID, Status: AgreementActive, AnniversaryDate: time.Now().UTC().AddDate(0, 1, 0), VisitsEntitled: 1, VisitsUsed: 1})
	if err != nil {
		t.Fatal(err)
	}
	var visitID string
	if err := db.Pool.QueryRow(ctx, `INSERT INTO visit_dues (shop_id, agreement_id, season, due_year) VALUES ($1, $2, 'spring', 2033) RETURNING id`, shopID, agreementID).Scan(&visitID); err != nil {
		t.Fatal(err)
	}
	if err := store.BulkUpdateVisitStatus(ctx, shopID, []string{visitID}, "completed"); err == nil {
		t.Fatal("expected exhausted agreement completion to be rejected")
	}
	var status string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM visit_dues WHERE id = $1`, visitID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "due" {
		t.Fatalf("rejected completion changed due status to %q", status)
	}
}

func withAgreementField(in CreateAgreementInput, update func(*CreateAgreementInput)) CreateAgreementInput {
	update(&in)
	return in
}

func createTestShop(t *testing.T, ctx context.Context, db *DB, suffix string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(ctx, `INSERT INTO shops (name, slug) VALUES ($1, $2) RETURNING id`, suffix, suffix).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createTestCustomer(t *testing.T, ctx context.Context, db *DB, shopID, name string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(ctx, `INSERT INTO customers (shop_id, name) VALUES ($1, $2) RETURNING id`, shopID, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createTestProperty(t *testing.T, ctx context.Context, db *DB, shopID, customerID, address string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(ctx, `INSERT INTO properties (shop_id, customer_id, address_line1, city, state, postal_code) VALUES ($1, $2, $3, 'Cary', 'NC', '27511') RETURNING id`, shopID, customerID, address).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createTestPlan(t *testing.T, ctx context.Context, db *DB, shopID, name string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(ctx, `INSERT INTO plan_templates (shop_id, name) VALUES ($1, $2) RETURNING id`, shopID, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createTestUser(t *testing.T, ctx context.Context, db *DB, shopID, email string) {
	t.Helper()
	if err := insertTestUser(ctx, db, shopID, email); err != nil {
		t.Fatal(err)
	}
}

func insertTestUser(ctx context.Context, db *DB, shopID, email string) error {
	_, err := db.Pool.Exec(ctx, `INSERT INTO users (shop_id, email, password_hash) VALUES ($1, $2, 'test')`, shopID, email)
	return err
}

func integrationDatabaseURL(t *testing.T) string {
	t.Helper()
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL != "" {
		return databaseURL
	}
	if os.Getenv("CLUBDUE_REQUIRE_POSTGRES_INTEGRATION") == "1" {
		t.Fatal("DATABASE_URL is required when Postgres integration tests are required")
	}
	t.Skip("DATABASE_URL is required for Postgres integration tests")
	return ""
}

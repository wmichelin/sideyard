package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
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
	testAgreementReferencesStayWithinShopAndCustomer(t, db)
}

func testAgreementReferencesStayWithinShopAndCustomer(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
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
	testAgreementKeepsPlanTermsSnapshotAfterTemplateEdit(t, db)
}

func testAgreementKeepsPlanTermsSnapshotAfterTemplateEdit(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
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
	if err := db.Pool.QueryRow(ctx, database.Statement(db.Pool.Dialect(), `
		SELECT plan_snapshot_name, plan_snapshot_description, plan_snapshot_billing_interval,
			plan_snapshot_term_months, plan_snapshot_visits_per_year
		FROM agreements WHERE id = $1`, `SELECT plan_snapshot_name,plan_snapshot_description,plan_snapshot_billing_interval,plan_snapshot_term_months,plan_snapshot_visits_per_year FROM dbo.agreements WHERE id=CONVERT(uniqueidentifier,@p1)`), agreementID,
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

func TestSignupEmailCannotCreateMultipleTenantIdentities(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := newAuth(db, strings.Repeat("s", 32), false)
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	email := "signup-" + stamp + "@example.test"
	firstShop := "Signup One " + stamp
	secondShop := "Signup Two " + stamp
	if _, err := auth.signup(ctx, firstShop, email, "valid-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.signup(ctx, secondShop, strings.ToUpper(email), "valid-password"); err == nil {
		t.Fatal("duplicate signup email created a second tenant identity")
	}
	var users, shops int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE lower(email) = $1`, email).Scan(&users); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM shops WHERE name IN ($1, $2)`, firstShop, secondShop).Scan(&shops); err != nil {
		t.Fatal(err)
	}
	if users != 1 || shops != 1 {
		t.Fatalf("duplicate signup leaked identities: users=%d shops=%d", users, shops)
	}
}

func TestRetireLegacySharedDemoIsNarrowAndIdempotent(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	controlShop := createTestShop(t, ctx, db, "legacy-control-"+stamp)
	var legacyShop string
	if err := db.Pool.QueryRow(ctx, `INSERT INTO shops (name, slug) VALUES ('Legacy shared demo', 'demo-hvac-shop') RETURNING id`).Scan(&legacyShop); err != nil {
		t.Fatal(err)
	}
	if err := insertTestUser(ctx, db, legacyShop, "owner@demo.local"); err != nil {
		t.Fatal(err)
	}
	retired, err := store.RetireLegacySharedDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if retired != 1 {
		t.Fatalf("retired shops = %d, want 1", retired)
	}
	var legacyRemaining, controlRemaining int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM shops WHERE id = $1`, legacyShop).Scan(&legacyRemaining); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM shops WHERE id = $1`, controlShop).Scan(&controlRemaining); err != nil {
		t.Fatal(err)
	}
	if legacyRemaining != 0 || controlRemaining != 1 {
		t.Fatalf("legacy cleanup scope: legacy=%d control=%d", legacyRemaining, controlRemaining)
	}
	retired, err = store.RetireLegacySharedDemo(ctx)
	if err != nil || retired != 0 {
		t.Fatalf("idempotent cleanup = %d, %v", retired, err)
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

func TestProvisionOwnerCreatesAndRecoversAccess(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	auth := newAuth(db, "test-session-secret", false)
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	credentials := OwnerCredentials{
		Email:    "owner-" + stamp + "@example.test",
		Password: "first-password",
		ShopName: "Provision " + stamp,
	}
	action, err := auth.provisionOwner(ctx, credentials)
	if err != nil || action != "provisioned" {
		t.Fatalf("provision owner = %q, %v", action, err)
	}
	if _, err := auth.login(ctx, credentials.Email, credentials.Password); err != nil {
		t.Fatalf("provisioned owner could not log in: %v", err)
	}

	credentials.Password = "recovered-password"
	action, err = auth.provisionOwner(ctx, credentials)
	if err != nil || action != "recovered" {
		t.Fatalf("recover owner = %q, %v", action, err)
	}
	if _, err := auth.login(ctx, credentials.Email, "first-password"); err == nil {
		t.Fatal("old owner password remained valid after recovery")
	}
	if _, err := auth.login(ctx, credentials.Email, credentials.Password); err != nil {
		t.Fatalf("recovered owner could not log in: %v", err)
	}

	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM owner_provision_events WHERE action IN ('provisioned', 'recovered')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count < 2 {
		t.Fatalf("expected provision and recovery audit events, got %d", count)
	}
}

func TestLoginCreatesIndependentSessionsAndKeepsFailuresGeneric(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	auth := newAuth(db, "test-session-secret", false)
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	credentials := OwnerCredentials{
		Email:    "rotation-" + stamp + "@example.test",
		Password: "correct-password",
		ShopName: "Rotation " + stamp,
	}
	if _, err := auth.provisionOwner(ctx, credentials); err != nil {
		t.Fatal(err)
	}
	firstToken, err := auth.login(ctx, credentials.Email, credentials.Password)
	if err != nil {
		t.Fatal(err)
	}
	secondToken, err := auth.login(ctx, credentials.Email, credentials.Password)
	if err != nil {
		t.Fatal(err)
	}
	if firstToken == secondToken {
		t.Fatal("successful login reused an existing session token")
	}

	firstRequest := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	firstRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: firstToken})
	if _, err := auth.userFromRequest(ctx, firstRequest); err != nil {
		t.Fatalf("prior session was invalidated by successful re-authentication: %v", err)
	}
	secondRequest := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	secondRequest.AddCookie(&http.Cookie{Name: sessionCookieName, Value: secondToken})
	if _, err := auth.userFromRequest(ctx, secondRequest); err != nil {
		t.Fatalf("new session was not valid: %v", err)
	}

	unknownErr := func() error {
		_, err := auth.login(ctx, "unknown-"+stamp+"@example.test", "wrong-password")
		return err
	}()
	wrongPasswordErr := func() error {
		_, err := auth.login(ctx, credentials.Email, "wrong-password")
		return err
	}()
	if unknownErr == nil || wrongPasswordErr == nil || unknownErr.Error() != wrongPasswordErr.Error() {
		t.Fatalf("login failures differ: unknown=%v wrong=%v", unknownErr, wrongPasswordErr)
	}
}

func TestReadyzReflectsPostgresAvailability(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{store: &Store{db: db}}
	readyRequest := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	readyResponse := httptest.NewRecorder()
	server.handleReadyz(readyResponse, readyRequest)
	if readyResponse.Code != http.StatusOK {
		t.Fatalf("readyz with Postgres = %d %q", readyResponse.Code, readyResponse.Body.String())
	}

	db.Close()
	readyResponse = httptest.NewRecorder()
	server.handleReadyz(readyResponse, readyRequest)
	if readyResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz with closed Postgres = %d, want %d", readyResponse.Code, http.StatusServiceUnavailable)
	}
}

func TestRecurringServiceOccurrencesAreIdempotentTenantScopedAndFinal(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2034, time.March, 15, 12, 0, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopOne := createTestShop(t, ctx, db, "service-one-"+stamp)
	shopTwo := createTestShop(t, ctx, db, "service-two-"+stamp)
	customerID := createTestCustomer(t, ctx, db, shopOne, "Landscape Customer")
	propertyID := createTestProperty(t, ctx, db, shopOne, customerID, "14 Green Way")
	planID := createTestPlan(t, ctx, db, shopOne, "Recurring Landscape Plan")
	agreementID, err := store.CreateAgreement(ctx, shopOne, CreateAgreementInput{
		CustomerID: customerID, PropertyID: propertyID, PlanTemplateID: planID,
		Status: AgreementActive, AnniversaryDate: now.AddDate(1, 0, 0), PriceCents: 18500,
	})
	if err != nil {
		t.Fatal(err)
	}
	scheduleID, err := store.CreateServiceSchedule(ctx, shopOne, ServiceScheduleInput{
		AgreementID: agreementID, ExternalID: "weekly-landscape", ServiceName: "Weekly mow, edge & blow",
		CadenceUnit: "week", CadenceInterval: 1, StartsOn: now.AddDate(0, 0, -14),
	})
	if err != nil {
		t.Fatal(err)
	}
	var firstCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM service_occurrences WHERE shop_id = $1 AND schedule_id = $2`, shopOne, scheduleID).Scan(&firstCount); err != nil {
		t.Fatal(err)
	}
	if firstCount < 10 {
		t.Fatalf("generated occurrences = %d, want bounded lookback and horizon", firstCount)
	}
	if err := store.serviceWorkService().GenerateScheduleOccurrences(ctx, shopOne, scheduleID); err != nil {
		t.Fatal(err)
	}
	var secondCount int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM service_occurrences WHERE shop_id = $1 AND schedule_id = $2`, shopOne, scheduleID).Scan(&secondCount); err != nil {
		t.Fatal(err)
	}
	if secondCount != firstCount {
		t.Fatalf("repeated generation changed count from %d to %d", firstCount, secondCount)
	}

	occurrences, err := store.ListServiceOccurrences(ctx, shopOne, true)
	if err != nil || len(occurrences) == 0 {
		t.Fatalf("list occurrences = %d, %v", len(occurrences), err)
	}
	occurrenceID := occurrences[0].ID
	if err := store.UpdateServiceOccurrences(ctx, shopOne,
		[]string{occurrenceID, "00000000-0000-0000-0000-000000000000"}, "completed", "must roll back"); err == nil {
		t.Fatal("bulk update with a missing occurrence unexpectedly succeeded")
	}
	var statusAfterFailedBatch string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM service_occurrences WHERE id = $1`, occurrenceID).Scan(&statusAfterFailedBatch); err != nil {
		t.Fatal(err)
	}
	if statusAfterFailedBatch != "due" {
		t.Fatalf("failed bulk update partially committed status %q", statusAfterFailedBatch)
	}
	if err := store.UpdateServiceOccurrence(ctx, shopTwo, occurrenceID, "completed", "cross tenant"); err == nil {
		t.Fatal("cross-tenant service outcome was accepted")
	}
	if err := store.UpdateServiceOccurrence(ctx, shopOne, occurrenceID, "completed", "finished from phone"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateServiceOccurrence(ctx, shopOne, occurrenceID, "completed", "retry"); err != nil {
		t.Fatalf("idempotent completion retry failed: %v", err)
	}
	if err := store.UpdateServiceOccurrence(ctx, shopOne, occurrenceID, "skipped", "rewrite"); err == nil {
		t.Fatal("final completed outcome was rewritten")
	}
	futureNow := now.AddDate(0, 0, 200)
	futureStore := &Store{db: db, clock: fixedClock{now: futureNow}}
	if err := futureStore.serviceWorkService().GenerateScheduleOccurrences(ctx, shopOne, scheduleID); err != nil {
		t.Fatal(err)
	}
	var recoveredGap int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM service_occurrences
		WHERE shop_id = $1 AND schedule_id = $2
		  AND due_on > $3::date + 90 AND due_on < $4::date - 30`,
		shopOne, scheduleID, now, futureNow).Scan(&recoveredGap); err != nil {
		t.Fatal(err)
	}
	if recoveredGap == 0 {
		t.Fatal("long-outage recurrence gap was silently skipped")
	}
	if err := futureStore.SetServiceScheduleActive(ctx, shopOne, scheduleID, false); err != nil {
		t.Fatal(err)
	}
	resumeNow := futureNow.AddDate(0, 0, 100)
	resumeStore := &Store{db: db, clock: fixedClock{now: resumeNow}}
	if err := resumeStore.SetServiceScheduleActive(ctx, shopOne, scheduleID, true); err != nil {
		t.Fatal(err)
	}
	if err := resumeStore.serviceWorkService().GenerateScheduleOccurrences(ctx, shopOne, scheduleID); err != nil {
		t.Fatal(err)
	}
	var pausedIntervalWork int
	if err := db.Pool.QueryRow(ctx, `
		SELECT count(*) FROM service_occurrences
		WHERE shop_id = $1 AND schedule_id = $2
		  AND due_on >= $3::date AND due_on < $4::date`,
		shopOne, scheduleID, futureNow, resumeNow).Scan(&pausedIntervalWork); err != nil {
		t.Fatal(err)
	}
	if pausedIntervalWork != 0 {
		t.Fatalf("pause/resume backfilled %d bogus obligations", pausedIntervalWork)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE agreements SET status = 'cancelled' WHERE shop_id = $1 AND id = $2`, shopOne, agreementID); err != nil {
		t.Fatal(err)
	}
	dueAfterCancellation, err := store.ListServiceOccurrences(ctx, shopOne, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(dueAfterCancellation) != 0 {
		t.Fatalf("cancelled agreement still exposed %d due occurrences", len(dueAfterCancellation))
	}
	exportRows, err := store.ServiceOccurrenceExportRows(ctx, shopOne, true)
	if err != nil || len(exportRows) != 0 {
		t.Fatalf("cancelled service export = %+v, %v", exportRows, err)
	}
	allExportRows, err := store.ServiceOccurrenceExportRows(ctx, shopOne, false)
	if err != nil || len(allExportRows) == 0 || allExportRows[0].ServiceType != "Weekly mow, edge & blow" {
		t.Fatalf("service history export = %+v, %v", allExportRows, err)
	}
}

func TestPublicDemoCreatesIsolatedExpiringFictionalTenants(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2035, time.April, 8, 9, 0, 0, 0, time.UTC)
	clock := fixedClock{now: now}
	store := &Store{db: db, clock: clock}
	first, err := store.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SeedPublicDemo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.ShopID == second.ShopID || first.UserID == second.UserID {
		t.Fatal("public demo visitors shared a tenant or user")
	}
	if !first.ExpiresAt.Equal(now.Add(publicDemoTTL)) {
		t.Fatalf("demo expiry = %s", first.ExpiresAt)
	}
	for _, result := range []PublicDemoResult{first, second} {
		var kind string
		var customers, schedules int
		if err := db.Pool.QueryRow(ctx, `SELECT kind FROM shops WHERE id = $1`, result.ShopID).Scan(&kind); err != nil {
			t.Fatal(err)
		}
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE shop_id = $1`, result.ShopID).Scan(&customers); err != nil {
			t.Fatal(err)
		}
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM service_schedules WHERE shop_id = $1`, result.ShopID).Scan(&schedules); err != nil {
			t.Fatal(err)
		}
		if kind != "demo_sandbox" || customers != publicDemoCustomerCount || schedules != publicDemoScheduleCount {
			t.Fatalf("demo fixture kind=%q customers=%d schedules=%d", kind, customers, schedules)
		}
	}
	reservationResults := make(chan error, publicDemoMutationBudget*2)
	var reservations sync.WaitGroup
	for range publicDemoMutationBudget * 2 {
		reservations.Add(1)
		go func() {
			defer reservations.Done()
			reservationResults <- store.ReservePublicDemoMutation(ctx, first.ShopID)
		}()
	}
	reservations.Wait()
	close(reservationResults)
	succeeded, capped := 0, 0
	for err := range reservationResults {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, errPublicDemoMutationBudgetReached):
			capped++
		default:
			t.Fatalf("concurrent mutation reservation: %v", err)
		}
	}
	if succeeded != publicDemoMutationBudget || capped != publicDemoMutationBudget {
		t.Fatalf("concurrent mutation reservations succeeded=%d capped=%d", succeeded, capped)
	}
	if err := store.ReservePublicDemoMutation(ctx, first.ShopID); !errors.Is(err, errPublicDemoMutationBudgetReached) {
		t.Fatalf("first sandbox mutation beyond budget = %v", err)
	}
	if err := store.ReservePublicDemoMutation(ctx, second.ShopID); err != nil {
		t.Fatalf("second sandbox did not have an independent mutation budget: %v", err)
	}
	var firstMutations, secondMutations int
	if err := db.Pool.QueryRow(ctx, `
		SELECT
			(SELECT demo_mutations_used FROM shops WHERE id = $1),
			(SELECT demo_mutations_used FROM shops WHERE id = $2)`,
		first.ShopID, second.ShopID).Scan(&firstMutations, &secondMutations); err != nil {
		t.Fatal(err)
	}
	if firstMutations != publicDemoMutationBudget || secondMutations != 1 {
		t.Fatalf("sandbox mutation budgets leaked: first=%d second=%d", firstMutations, secondMutations)
	}
	auth := newAuthWithClock(db, "demo-session-secret", false, clock)
	token, err := auth.createSessionUntil(ctx, first.UserID, first.ExpiresAt)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	user, err := auth.userFromRequest(ctx, request)
	if err != nil || !user.IsDemo || user.ShopID != first.ShopID {
		t.Fatalf("demo session user = %+v, %v", user, err)
	}
	if err := auth.logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.userFromRequest(ctx, request); err == nil {
		t.Fatal("server accepted a revoked demo session token")
	}
	var firstAgreementID string
	if err := db.Pool.QueryRow(ctx, `SELECT id FROM agreements WHERE shop_id = $1 ORDER BY id LIMIT 1`, first.ShopID).Scan(&firstAgreementID); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateRenewalLink(ctx, first.ShopID, firstAgreementID, "expiring-demo-link", now.Add(30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	expiredStore := &Store{db: db, clock: fixedClock{now: now.Add(publicDemoTTL)}}
	if err := expiredStore.ReservePublicDemoMutation(ctx, second.ShopID); !errors.Is(err, errPublicDemoMutationBudgetReached) {
		t.Fatalf("expired sandbox accepted a mutation: %v", err)
	}
	if _, err := expiredStore.GetHomeownerRenewal(ctx, "expiring-demo-link"); err == nil {
		t.Fatal("public renewal link outlived its demo sandbox")
	}
	if err := expiredStore.CleanupExpiredPublicDemos(ctx); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM shops WHERE id IN ($1, $2)`, first.ShopID, second.ShopID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("expired demo cleanup left %d sandboxes", remaining)
	}
}

func TestLandscapingSheetsImportCreatesIdempotentRecurringWork(t *testing.T) {
	databaseURL := integrationDatabaseURL(t)
	ctx := context.Background()
	db, err := openDB(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	store := &Store{db: db, clock: fixedClock{now: now}}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	shopID := createTestShop(t, ctx, db, "landscape-import-"+stamp)

	fixture, err := os.Open("testdata/landscaping-google-sheets-template.csv")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := parseImportCSV(fixture)
	_ = fixture.Close()
	if err != nil {
		t.Fatal(err)
	}
	rows[1].ServiceExternalID = "bespoke-service-key"
	analysis, err := store.AnalyzeImport(ctx, shopID, rows)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Summary.Errors != 0 || analysis.Summary.NewSchedules != 3 {
		t.Fatalf("landscaping preview = %+v", analysis.Summary)
	}
	if _, err := store.CommitImport(ctx, shopID, rows); err != nil {
		t.Fatal(err)
	}
	occurrences, err := store.ListServiceOccurrences(ctx, shopID, true)
	if err != nil || len(occurrences) == 0 {
		t.Fatalf("imported recurring work = %d, %v", len(occurrences), err)
	}
	var overdueID string
	for _, occurrence := range occurrences {
		if occurrence.IsOverdue && occurrence.ServiceName == rows[0].ServiceName {
			overdueID = occurrence.ID
			break
		}
	}
	if overdueID == "" {
		t.Fatal("fixture did not produce an unresolved overdue commitment")
	}
	if _, err := store.CommitImport(ctx, shopID, rows); err != nil {
		t.Fatal(err)
	}
	var customers, agreements, schedules int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM customers WHERE shop_id = $1`, shopID).Scan(&customers); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM agreements WHERE shop_id = $1`, shopID).Scan(&agreements); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM service_schedules WHERE shop_id = $1`, shopID).Scan(&schedules); err != nil {
		t.Fatal(err)
	}
	if customers != 3 || agreements != 3 || schedules != 3 {
		t.Fatalf("re-import duplicated rows: customers=%d agreements=%d schedules=%d", customers, agreements, schedules)
	}
	if _, err := store.CreatePlanTemplate(ctx, shopID, "Imported Upgrade", "Should not replace captured terms", 9900, 12, 7); err != nil {
		t.Fatal(err)
	}
	planMismatch := append([]ImportRow(nil), rows...)
	planMismatch[0].PlanName = "Imported Upgrade"
	if _, err := store.CommitImport(ctx, shopID, planMismatch); err == nil {
		t.Fatal("re-import silently replaced immutable agreement plan terms")
	}
	var planTemplateID *string
	var capturedPlan string
	var capturedVisits, entitledVisits int
	if err := db.Pool.QueryRow(ctx, `
		SELECT plan_template_id, plan_snapshot_name, plan_snapshot_visits_per_year, visits_entitled
		FROM agreements WHERE shop_id = $1 AND external_id = $2`, shopID, rows[0].ExternalID,
	).Scan(&planTemplateID, &capturedPlan, &capturedVisits, &entitledVisits); err != nil {
		t.Fatal(err)
	}
	if planTemplateID != nil || capturedPlan != "Custom plan" || capturedVisits != 0 || entitledVisits != 0 {
		t.Fatalf("immutable terms changed: plan=%v snapshot=%q captured=%d entitled=%d", planTemplateID, capturedPlan, capturedVisits, entitledVisits)
	}
	rows[0].CadenceInterval = 2
	if _, err := store.CommitImport(ctx, shopID, rows); err != nil {
		t.Fatal(err)
	}
	var cadence int
	if err := db.Pool.QueryRow(ctx, `
		SELECT cadence_interval FROM service_schedules
		WHERE shop_id = $1 AND external_id = $2`, shopID, rows[0].ServiceExternalID).Scan(&cadence); err != nil {
		t.Fatal(err)
	}
	if cadence != 2 {
		t.Fatalf("corrected recurrence cadence = %d, want 2", cadence)
	}
	var preservedStatus string
	if err := db.Pool.QueryRow(ctx, `SELECT status FROM service_occurrences WHERE id = $1`, overdueID).Scan(&preservedStatus); err != nil {
		t.Fatalf("corrected recurrence deleted unresolved overdue work: %v", err)
	}
	if preservedStatus != "due" {
		t.Fatalf("corrected recurrence changed unresolved outcome to %q", preservedStatus)
	}
	omitted := append([]ImportRow(nil), rows...)
	omitted[1].ServiceName = ""
	omitted[1].ServiceExternalID = omitted[1].ExternalID + "-service"
	if _, err := store.CommitImport(ctx, shopID, omitted); err == nil {
		t.Fatal("re-import silently orphaned an existing recurring service")
	}
	rekeyed := append([]ImportRow(nil), rows...)
	rekeyed[1].ServiceExternalID = "rekeyed-service"
	if _, err := store.CommitImport(ctx, shopID, rekeyed); err == nil {
		t.Fatal("re-import silently created a second service for an existing agreement")
	}
	reassigned := append([]ImportRow(nil), rows...)
	reassigned[2].ServiceExternalID = rows[1].ServiceExternalID
	if _, err := store.CommitImport(ctx, shopID, reassigned); err == nil {
		t.Fatal("stable service identity was reassigned with its outcome history")
	}
}

func withAgreementField(in CreateAgreementInput, update func(*CreateAgreementInput)) CreateAgreementInput {
	update(&in)
	return in
}

func createTestShop(t *testing.T, ctx context.Context, db *DB, suffix string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(ctx, database.Statement(db.Pool.Dialect(), `INSERT INTO shops (name, slug) VALUES ($1, $2) RETURNING id`, sqlServerInsertUnclaimedShop), suffix, suffix).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createTestCustomer(t *testing.T, ctx context.Context, db *DB, shopID, name string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(ctx, database.Statement(db.Pool.Dialect(), `INSERT INTO customers (shop_id, name) VALUES ($1, $2) RETURNING id`, `DECLARE @created TABLE(id uniqueidentifier); INSERT INTO dbo.customers(shop_id,name) OUTPUT inserted.id INTO @created VALUES(CONVERT(uniqueidentifier,@p1),@p2); SELECT LOWER(CONVERT(char(36),id)) FROM @created;`), shopID, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createTestProperty(t *testing.T, ctx context.Context, db *DB, shopID, customerID, address string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(ctx, database.Statement(db.Pool.Dialect(), `INSERT INTO properties (shop_id, customer_id, address_line1, city, state, postal_code) VALUES ($1, $2, $3, 'Cary', 'NC', '27511') RETURNING id`, `DECLARE @created TABLE(id uniqueidentifier); INSERT INTO dbo.properties(shop_id,customer_id,address_line1,city,state,postal_code) OUTPUT inserted.id INTO @created VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,'Cary','NC','27511'); SELECT LOWER(CONVERT(char(36),id)) FROM @created;`), shopID, customerID, address).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func createTestPlan(t *testing.T, ctx context.Context, db *DB, shopID, name string) string {
	t.Helper()
	var id string
	if err := db.Pool.QueryRow(ctx, database.Statement(db.Pool.Dialect(), `INSERT INTO plan_templates (shop_id, name) VALUES ($1, $2) RETURNING id`, `DECLARE @created TABLE(id uniqueidentifier); INSERT INTO dbo.plan_templates(shop_id,name) OUTPUT inserted.id INTO @created VALUES(CONVERT(uniqueidentifier,@p1),@p2); SELECT LOWER(CONVERT(char(36),id)) FROM @created;`), shopID, name).Scan(&id); err != nil {
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
	var err error
	if db.Pool.Dialect() == database.SQLServer {
		_, err = db.Pool.Exec(ctx, `INSERT INTO dbo.users(shop_id,email,email_normalized,password_hash) VALUES(CONVERT(uniqueidentifier,@p1),@p2,@p3,N'test')`, shopID, email, strings.ToLower(email))
	} else {
		_, err = db.Pool.Exec(ctx, `INSERT INTO users (shop_id, email, password_hash) VALUES ($1, $2, 'test')`, shopID, email)
	}
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

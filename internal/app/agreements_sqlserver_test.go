package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/jobs"
	"strings"
	"testing"
	"time"
)

func TestSQLServerAgreementReferencesStayWithinShopAndCustomer(t *testing.T) {
	testAgreementReferencesStayWithinShopAndCustomer(t, sqlServerTestDB(t))
}
func TestSQLServerAgreementKeepsPlanTermsSnapshotAfterTemplateEdit(t *testing.T) {
	testAgreementKeepsPlanTermsSnapshotAfterTemplateEdit(t, sqlServerTestDB(t))
}

type testAgreementSnapshot struct {
	Name, Description, Billing, Windows string
	Term, Visits                        int
}

func readTestAgreementSnapshot(t *testing.T, db *DB, id string) (testAgreementSnapshot, time.Time) {
	t.Helper()
	var snapshot testAgreementSnapshot
	var updated time.Time
	if err := db.Pool.QueryRow(context.Background(), `SELECT plan_snapshot_name,plan_snapshot_description,plan_snapshot_billing_interval,plan_snapshot_term_months,plan_snapshot_visits_per_year,plan_snapshot_visit_windows,updated_at FROM dbo.agreements WHERE id=CONVERT(uniqueidentifier,@p1)`, id).Scan(&snapshot.Name, &snapshot.Description, &snapshot.Billing, &snapshot.Term, &snapshot.Visits, &snapshot.Windows, &updated); err != nil {
		t.Fatal(err)
	}
	return snapshot, updated
}

func TestSQLServerAgreementSnapshotSurvivesEditsAndConcurrentParentDeletion(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	store := &Store{db: db}
	shop := createTestShop(t, ctx, db, "agreement-delete")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	property := createTestProperty(t, ctx, db, shop, customer, "1 Original")
	plan := createTestPlan(t, ctx, db, shop, "Original plan")
	windows := `["fall","spring","fall","unknown"]`
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.plan_templates SET description=N'Original terms',billing_interval=N'monthly',term_months=24,visits_per_year=5,visit_windows=@p2,price_cents=777 WHERE id=CONVERT(uniqueidentifier,@p1)`, plan, windows); err != nil {
		t.Fatal(err)
	}
	in := CreateAgreementInput{CustomerID: customer, PropertyID: property, PlanTemplateID: plan, ExternalID: "original-id", Status: AgreementActive, AnniversaryDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), VisitsEntitled: 0, VisitsUsed: 3, PriceCents: 12345, Notes: "  "}
	id, err := store.CreateAgreement(ctx, shop, in)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := readTestAgreementSnapshot(t, db, id)
	want := testAgreementSnapshot{Name: "Original plan", Description: "Original terms", Billing: "monthly", Windows: windows, Term: 24, Visits: 5}
	if before != want {
		t.Fatalf("snapshot: %+v", before)
	}
	if err := store.UpdatePlanTemplate(ctx, shop, plan, "New plan", "New terms", 999, 12, 1); err != nil {
		t.Fatal(err)
	}
	in.ExternalID = "attempted-change"
	in.PriceCents = 45678
	in.VisitsEntitled = 1
	in.VisitsUsed = 4
	if err := store.UpdateAgreement(ctx, shop, id, in); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetAgreement(ctx, shop, id)
	if err != nil || record.ExternalID != "original-id" || record.PlanName != "Original plan" || record.PriceCents != 45678 || record.VisitsEntitled != 1 || record.VisitsUsed != 4 || record.Notes != "  " {
		t.Fatal("agreement facts changed unexpectedly", err)
	}
	before, updated := readTestAgreementSnapshot(t, db, id)
	if before != want {
		t.Fatal("template edit rewrote snapshot")
	}
	otherPlan := createTestPlan(t, ctx, db, shop, "Other plan")
	attempt := in
	attempt.PlanTemplateID = otherPlan
	if err := store.UpdateAgreement(ctx, shop, id, attempt); !errors.Is(err, database.ErrNoRows) {
		t.Fatal("template switch accepted", err)
	}
	// Hold each parent first. Both deletions must finish without a trigger trying
	// to lock the unchanged other parent while holding the agreement row.
	propertyTx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer propertyTx.Rollback(ctx)
	planTx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer planTx.Rollback(ctx)
	var found int
	if err := propertyTx.QueryRow(ctx, `SELECT 1 FROM dbo.properties WITH(XLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, property).Scan(&found); err != nil {
		t.Fatal(err)
	}
	if err := planTx.QueryRow(ctx, `SELECT 1 FROM dbo.plan_templates WITH(XLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, plan).Scan(&found); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		_, err := propertyTx.Exec(ctx, `DELETE FROM dbo.properties WHERE id=CONVERT(uniqueidentifier,@p1)`, property)
		if err == nil {
			err = propertyTx.Commit(ctx)
		}
		done <- err
	}()
	go func() {
		<-start
		_, err := planTx.Exec(ctx, `DELETE FROM dbo.plan_templates WHERE id=CONVERT(uniqueidentifier,@p1)`, plan)
		if err == nil {
			err = planTx.Commit(ctx)
		}
		done <- err
	}()
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal("independent parent deletion", err)
		}
	}
	record, err = store.GetAgreement(ctx, shop, id)
	if err != nil || record.PropertyID != "" || record.PlanTemplateID != "" || record.PlanName != "Original plan" {
		t.Fatal("link cleanup lost contract", err)
	}
	after, afterUpdated := readTestAgreementSnapshot(t, db, id)
	if after != before || !afterUpdated.Equal(updated) {
		t.Fatal("parent deletion changed captured terms/timestamp")
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.customers WHERE id=CONVERT(uniqueidentifier,@p1)`, customer); err == nil {
		t.Fatal("referenced customer deleted")
	}
	other := createTestShop(t, ctx, db, "agreement-other")
	createTestPlan(t, ctx, db, other, "Keep me")
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal("shop cleanup", err)
	}
	var agreements, plans int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.agreements),(SELECT COUNT(*) FROM dbo.plan_templates)`).Scan(&agreements, &plans); err != nil || agreements != 0 || plans != 1 {
		t.Fatal("shop cleanup crossed tenant or left contract", err)
	}
}

func TestSQLServerAgreementOwnershipAndMultirowFailuresAreAtomic(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	store := &Store{db: db}
	shop := createTestShop(t, ctx, db, "agreement-ownership")
	other := createTestShop(t, ctx, db, "agreement-foreign")
	customer := createTestCustomer(t, ctx, db, shop, "First")
	second := createTestCustomer(t, ctx, db, shop, "Second")
	foreignCustomer := createTestCustomer(t, ctx, db, other, "Foreign")
	property := createTestProperty(t, ctx, db, shop, customer, "First address")
	nextProperty := createTestProperty(t, ctx, db, shop, second, "Second address")
	foreignProperty := createTestProperty(t, ctx, db, other, foreignCustomer, "Foreign address")
	plan := createTestPlan(t, ctx, db, shop, "Plan")
	foreignPlan := createTestPlan(t, ctx, db, other, "Foreign plan")
	for _, tc := range []struct{ property, plan string }{{foreignProperty, plan}, {property, foreignPlan}, {nextProperty, plan}} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.agreements(shop_id,customer_id,property_id,plan_template_id,anniversary_date) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),CONVERT(uniqueidentifier,@p4),'2026-12-01'),(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p5),CONVERT(uniqueidentifier,@p6),'2026-12-01')`, shop, customer, property, plan, tc.property, tc.plan); err == nil {
			t.Fatal("multirow reference validation accepted invalid row")
		}
		var count int
		if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.agreements`).Scan(&count); err != nil || count != 0 {
			t.Fatal("invalid second row retained first", err)
		}
	}
	in := CreateAgreementInput{CustomerID: customer, PropertyID: property, PlanTemplateID: plan, Status: AgreementActive, AnniversaryDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}
	id, err := store.CreateAgreement(ctx, shop, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE dbo.properties SET customer_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1)`,
		`UPDATE dbo.properties SET id=NEWID() WHERE id=CONVERT(uniqueidentifier,@p1)`,
	} {
		if _, err := db.Pool.Exec(ctx, statement, property, second); err == nil {
			t.Fatal("property update orphaned agreement")
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.plan_templates SET shop_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1)`, plan, other); err == nil {
		t.Fatal("template move leaked agreement")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.plan_templates SET id=NEWID() WHERE id=CONVERT(uniqueidentifier,@p1)`, plan); err == nil {
		t.Fatal("template identity orphaned agreement")
	}
	in.CustomerID = second
	in.PropertyID = nextProperty
	if err := store.UpdateAgreement(ctx, shop, id, in); err != nil {
		t.Fatal("valid agreement customer change failed", err)
	}
	got, err := store.GetAgreement(ctx, shop, id)
	if err != nil || got.CustomerID != second || got.PropertyID != nextProperty {
		t.Fatal("agreement reassignment changed identity")
	}
	// A failed parent delete must undo pointer cleanup and every other dependent effect.
	jobInput := jobs.Input{CustomerID: second, PropertyID: nextProperty, Kind: "job", Title: "Protected job"}
	if _, err := store.jobsService().Create(ctx, shop, strings.Repeat("a", 32), "Owner", jobInput); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.properties WHERE id=CONVERT(uniqueidentifier,@p1)`, nextProperty); err == nil {
		t.Fatal("job property deletion accepted")
	}
	got, err = store.GetAgreement(ctx, shop, id)
	if err != nil || got.PropertyID != nextProperty {
		t.Fatal("failed deletion cleared agreement pointer")
	}
}

func TestSQLServerAgreementTextDatesCountersAndWindowsRemainExact(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	store := &Store{db: db}
	shop := createTestShop(t, ctx, db, "agreement-values")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	long := strings.Repeat("🔧", 2200)
	var plan string
	for _, name := range []string{long, long + " ", "Plan", "plan", "plan "} {
		id, err := store.CreatePlanTemplate(ctx, shop, name, "  ", -2147483648, 2147483647, 2147483647)
		if err != nil {
			t.Fatal(err)
		}
		if name == long {
			plan = id
		}
	}
	if _, err := store.CreatePlanTemplate(ctx, shop, long, "", 0, 12, 0); err == nil {
		t.Fatal("duplicate plan name accepted")
	}
	p, err := store.GetPlanTemplate(ctx, shop, plan)
	if err != nil || p.Name != long || p.Description != "  " || p.PriceCents != -2147483648 || p.TermMonths != 2147483647 || p.VisitsPerYear != 2147483647 {
		t.Fatal("historical plan values changed", err)
	}
	windows, _ := json.Marshal([]string{long, "", "fall", "fall"})
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.plan_templates SET visit_windows=@p2 WHERE id=CONVERT(uniqueidentifier,@p1)`, plan, string(windows)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`["fall",null]`, `[1]`, `{}`, `null`} {
		if _, err := db.Pool.Exec(ctx, `UPDATE dbo.plan_templates SET visit_windows=@p2 WHERE id=CONVERT(uniqueidentifier,@p1)`, plan, bad); err == nil {
			t.Fatal("invalid windows accepted")
		}
	}
	in := CreateAgreementInput{CustomerID: customer, PlanTemplateID: plan, Status: AgreementPastDue, AnniversaryDate: time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), VisitsEntitled: -2147483648, VisitsUsed: 2147483647, PriceCents: -2147483648, Notes: long + "  "}
	var first string
	for _, key := range []string{long, long + " ", "Key", "key", "key "} {
		in.ExternalID = key
		id, err := store.CreateAgreement(ctx, shop, in)
		if err != nil {
			t.Fatal(err)
		}
		if key == long {
			first = id
		}
	}
	in.ExternalID = long
	if _, err := store.CreateAgreement(ctx, shop, in); err == nil {
		t.Fatal("duplicate agreement identity accepted")
	}
	got, err := store.GetAgreement(ctx, shop, first)
	if err != nil || got.ExternalID != long || got.Notes != long+"  " || got.AnniversaryDate.Year() != 1 || got.VisitsEntitled != -2147483648 || got.VisitsUsed != 2147483647 || got.PriceCents != -2147483648 {
		t.Fatal("agreement values changed", err)
	}
	snap, _ := readTestAgreementSnapshot(t, db, first)
	if snap.Windows != string(windows) || snap.Name != long || snap.Description != "  " {
		t.Fatal("snapshot text/JSON truncated")
	}
	in.AnniversaryDate = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	if err := store.UpdateAgreement(ctx, shop, first, in); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetAgreement(ctx, shop, first)
	if err != nil || got.AnniversaryDate.Format("2006-01-02") != "9999-12-31" {
		t.Fatal("upper date changed")
	}
	for _, bad := range []string{"bad\x00", "bad\xff"} {
		invalid := in
		invalid.Notes = bad
		if _, err := store.CreateAgreement(ctx, shop, invalid); !errors.Is(err, errAgreementInput) {
			t.Fatal("invalid text accepted", err)
		}
	}
	if _, err := store.GetAgreement(ctx, shop, first+"suffix"); !errors.Is(err, errInvalidRecordID) {
		t.Fatal("UUID prefix alias accepted")
	}
	custom := in
	custom.PlanTemplateID = ""
	custom.ExternalID = ""
	custom.VisitsEntitled = 0
	custom.VisitsUsed = 4
	id, err := store.CreateAgreement(ctx, shop, custom)
	if err != nil {
		t.Fatal(err)
	}
	snap, _ = readTestAgreementSnapshot(t, db, id)
	if snap.Name != "Custom plan" || snap.Visits != 0 || snap.Windows != `["spring","fall"]` {
		t.Fatal("custom snapshot defaults changed")
	}
}

func TestSQLServerFullyLinkedAgreementShopCascadeAndStaleDeletedPlan(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	store := &Store{db: db}
	shop := createTestShop(t, ctx, db, "agreement-full-cascade")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	property := createTestProperty(t, ctx, db, shop, customer, "Address")
	plan := createTestPlan(t, ctx, db, shop, "Plan")
	in := CreateAgreementInput{CustomerID: customer, PropertyID: property, PlanTemplateID: plan, Status: AgreementActive, AnniversaryDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}
	id, err := store.CreateAgreement(ctx, shop, in)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := readTestAgreementSnapshot(t, db, id)
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.plan_templates WHERE id=CONVERT(uniqueidentifier,@p1)`, plan); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAgreement(ctx, shop, id, in); !errors.Is(err, database.ErrNoRows) {
		t.Fatal("stale deleted-plan form lost conflict result", err)
	}
	after, _ := readTestAgreementSnapshot(t, db, id)
	if after != before {
		t.Fatal("stale form changed contract")
	}
	// This second contract retains both live optional links when the shop is deleted.
	in.PlanTemplateID = createTestPlan(t, ctx, db, shop, "Current linked plan")
	if _, err := store.CreateAgreement(ctx, shop, in); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.agreements)+(SELECT COUNT(*) FROM dbo.plan_templates)+(SELECT COUNT(*) FROM dbo.customers)+(SELECT COUNT(*) FROM dbo.properties)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("fully linked shop cleanup incomplete", err)
	}
}

func TestSQLServerAgreementSnapshotWaitsForCompleteTemplateEdit(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store := &Store{db: db}
	shop := createTestShop(t, ctx, db, "agreement-capture")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	plan := createTestPlan(t, ctx, db, shop, "Before")
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE dbo.plan_templates SET name=N'After',description=N'Complete edit',billing_interval=N'monthly',term_months=36,visits_per_year=7,visit_windows=N'["fall"]' WHERE id=CONVERT(uniqueidentifier,@p1)`, plan); err != nil {
		t.Fatal(err)
	}
	type result struct {
		id  string
		err error
	}
	done := make(chan result, 1)
	go func() {
		id, err := store.CreateAgreement(ctx, shop, CreateAgreementInput{CustomerID: customer, PlanTemplateID: plan, Status: AgreementActive, AnniversaryDate: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), PriceCents: 123, VisitsEntitled: 2})
		done <- result{id, err}
	}()
	select {
	case <-done:
		t.Fatal("snapshot ignored in-flight template edit")
	case <-time.After(30 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	created := <-done
	if created.err != nil {
		t.Fatal(created.err)
	}
	snapshot, _ := readTestAgreementSnapshot(t, db, created.id)
	want := testAgreementSnapshot{Name: "After", Description: "Complete edit", Billing: "monthly", Windows: `["fall"]`, Term: 36, Visits: 7}
	if snapshot != want {
		t.Fatalf("mixed template snapshot: %+v", snapshot)
	}
}

func TestSQLServerContractFormsRejectInvalidNumbersAndUnconfirmedSaves(t *testing.T) {
	testContractFormsRejectInvalidNumbersAndUnconfirmedSaves(t, sqlServerTestDB(t))
}

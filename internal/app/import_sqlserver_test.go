package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/jobs"
	"reflect"
)

func importTestRows(t *testing.T) []ImportRow {
	t.Helper()
	rows, err := parseImportCSV(strings.NewReader("external_id,customer_name,address_line1,city,state,postal_code,anniversary_date,service_name,cadence_unit,service_start_date\nkey-1,Customer One,1 Test Way,Cary,NC,27511,2035-09-09,Weekly care,week,2034-08-01\n"))
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
func TestImportExternalCustomerWinsBeforeManualAdoption(t *testing.T) {
	db, err := openDB(context.Background(), integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	testImportExternalCustomerWins(t, db)
}
func TestSQLServerImportExternalCustomerWinsBeforeManualAdoption(t *testing.T) {
	testImportExternalCustomerWins(t, sqlServerTestDB(t))
}
func testImportExternalCustomerWins(t *testing.T, db *DB) {
	ctx := context.Background()
	shop := createTestShop(t, ctx, db, fmt.Sprint("import-identity-", time.Now().UnixNano()))
	s := &Store{db: db}
	rows := importTestRows(t)
	if _, err := s.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal(err)
	}
	first, _, err := findImportCustomer(ctx, db.Pool, shop, rows[0])
	if err != nil {
		t.Fatal(err)
	}
	manual := createTestCustomer(t, ctx, db, shop, rows[0].CustomerName)
	createTestProperty(t, ctx, db, shop, manual, rows[0].AddressLine1)
	rows[0].Email = "changed@example.test"
	if _, err := s.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal("external identity lost to matching manual record", err)
	}
	id, _, err := findImportCustomer(ctx, db.Pool, shop, rows[0])
	if err != nil || id != first {
		t.Fatal("reimport reassigned identity", err)
	}
	next := rows[0]
	next.ExternalID = "manual-first"
	next.ServiceExternalID = "manual-first-service"
	last := next
	last.ExternalID = "manual-second"
	last.ServiceExternalID = "manual-second-service"
	result, err := s.CommitImport(ctx, shop, []ImportRow{next, last})
	if err != nil {
		t.Fatal(err)
	}
	one, _, _ := findImportCustomer(ctx, db.Pool, shop, next)
	two, _, _ := findImportCustomer(ctx, db.Pool, shop, last)
	if one != manual || two == manual || one == two {
		t.Fatal("manual adoption was not consumptive")
	}
	if db.Pool.Dialect() == database.SQLServer && (result.Summary.NewCustomers != 1 || result.Summary.UpdatedCustomers != 1) {
		t.Fatal("committed counts used stale preview", result.Summary)
	}
}

func TestSQLServerImportExactKeysAndCapturedTerms(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "import-exact")
	other := createTestShop(t, ctx, db, "import-exact-other")
	keys := []string{"Key", "key", "key ", "key\u2060", strings.Repeat("界🔧", 2500)}
	plan := createTestPlan(t, ctx, db, shop, "Captured")
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.plan_templates SET visit_windows=N'[]',description=N'Original terms',visits_per_year=5 WHERE id=CONVERT(uniqueidentifier,@p1)`, plan); err != nil {
		t.Fatal(err)
	}
	rows := make([]ImportRow, 0, len(keys))
	for i, key := range keys {
		row := importTestRows(t)[0]
		row.ExternalID = key
		row.ServiceExternalID = key + "-service"
		row.CustomerName += fmt.Sprint(i)
		row.PlanName = "Captured"
		rows = append(rows, row)
	}
	if _, err := s.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal(err)
	}
	first, _, err := findAgreementByExternalID(ctx, db.Pool, shop, keys[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET visits_used=3,visits_entitled=9,notes=N'Keep private notes' WHERE shop_id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.service_schedules SET active=0,paused_at='2034-01-01',counts_toward_entitlement=1,generated_through='2034-02-01' WHERE shop_id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.plan_templates SET visits_per_year=7,visit_windows=N'["summer"]',description=N'Changed template' WHERE id=CONVERT(uniqueidentifier,@p2)`, shop, plan); err != nil {
		t.Fatal(err)
	}
	before := importTableSnapshot(t, db, shop, "service_schedules", "id")
	for i := range rows {
		rows[i].Email = "updated@example.test"
		rows[i].PriceCents = 12345
	}
	if _, err := s.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal(err)
	}
	if before != importTableSnapshot(t, db, shop, "service_schedules", "id") {
		t.Fatal("unchanged import rewrote schedule")
	}
	var name, description, windows, notes string
	var used, entitled, price, visits int
	err = db.Pool.QueryRow(ctx, `SELECT plan_snapshot_name,plan_snapshot_description,plan_snapshot_visit_windows,notes,visits_used,visits_entitled,price_cents,plan_snapshot_visits_per_year FROM dbo.agreements WHERE id=CONVERT(uniqueidentifier,@p1)`, first).Scan(&name, &description, &windows, &notes, &used, &entitled, &price, &visits)
	if err != nil || name != "Captured" || description != "Original terms" || windows != "[]" || notes != "Keep private notes" || used != 3 || entitled != 9 || price != 12345 || visits != 5 {
		t.Fatal("captured terms or usage overwritten", err)
	}
	for _, row := range rows {
		if _, exists, err := findImportCustomer(ctx, db.Pool, other, row); err != nil || exists {
			t.Fatal("foreign identity found", err)
		}
	}
	if n := countShopRows(t, ctx, db, "customers", shop); n != len(keys) {
		t.Fatal("exact keys collapsed", n)
	}
	if n := countShopRows(t, ctx, db, "import_rows", shop); n != 4*len(keys) {
		t.Fatal("provenance duplicated", n)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.agreements WHERE id=CONVERT(uniqueidentifier,@p1)`, first); err != nil {
		t.Fatal(err)
	}
	if n := countShopRows(t, ctx, db, "import_rows", shop); n != 4*len(keys) {
		t.Fatal("historical provenance deleted", n)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	if n := countShopRows(t, ctx, db, "import_rows", shop); n != 0 {
		t.Fatal("shop deletion retained provenance", n)
	}
}

func importTableSnapshot(t *testing.T, db *DB, shop, table, order string) string {
	t.Helper()
	var result string
	// Callers supply only fixed test-owned table/column names.
	err := db.Pool.QueryRow(context.Background(), `SELECT COALESCE((SELECT * FROM dbo.`+table+` WHERE shop_id=CONVERT(uniqueidentifier,@p1) ORDER BY `+order+` FOR JSON PATH,INCLUDE_NULL_VALUES),N'[]')`, shop).Scan(&result)
	if err != nil {
		t.Fatal(table, err)
	}
	return result
}
func importShopSnapshot(t *testing.T, db *DB, shop string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, table := range []string{"customers", "properties", "agreements", "service_schedules", "service_occurrences", "visit_notes", "property_notes", "property_measurements", "property_photos", "equipment", "import_rows", "pilot_activation_events"} {
		out[table] = importTableSnapshot(t, db, shop, table, "id")
	}
	out["property_details"] = importTableSnapshot(t, db, shop, "property_details", "property_id")
	return out
}

func TestSQLServerImportReparentAndLateFailurePreserveHistory(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, "import-reparent")
	rows := importTestRows(t)
	if _, err := s.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal(err)
	}
	customer, _, _ := findImportCustomer(ctx, db.Pool, shop, rows[0])
	property, _, _ := findPropertyByExternalID(ctx, db.Pool, shop, rows[0].ExternalID)
	agreement, _, _ := findAgreementByExternalID(ctx, db.Pool, shop, rows[0].ExternalID)
	old := createTestCustomer(t, ctx, db, shop, "Former owner")
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET property_id=NULL WHERE id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.properties SET customer_id=CONVERT(uniqueidentifier,@p3) WHERE id=CONVERT(uniqueidentifier,@p2); UPDATE dbo.agreements SET customer_id=CONVERT(uniqueidentifier,@p3),property_id=CONVERT(uniqueidentifier,@p2) WHERE id=CONVERT(uniqueidentifier,@p1); DELETE FROM dbo.pilot_activation_events WHERE shop_id=CONVERT(uniqueidentifier,@p4)`, agreement, property, old, shop); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePropertyDetails(ctx, shop, property, "Side gate", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPropertyNote(ctx, shop, property, propertyNote{SubmissionKey: strings.Repeat("c", 32), Body: "Survey", OccurredOn: now, RecordedBy: "Owner"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPropertyMeasurement(ctx, shop, property, propertyMeasurement{Kind: "turf", Zone: "Lawn", Value: "123.125", Unit: "sq_ft", Source: "Tape", MeasuredOn: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEquipment(ctx, shop, property, "Panel", "", "", "serial"); err != nil {
		t.Fatal(err)
	}
	photo, err := normalizePhoto(photoFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPropertyPhoto(ctx, shop, property, strings.Repeat("d", 32), "Site", "general", "Owner", photo); err != nil {
		t.Fatal(err)
	}
	work, err := s.ListServiceOccurrences(ctx, shop, false)
	if err != nil || len(work) < 3 {
		t.Fatal("work fixture", err)
	}
	for i := 0; i < 3; i++ {
		if err := s.AddVisitNote(ctx, shop, "recurring", work[i].ID, "Owner", internalNoteInput{Key: fmt.Sprintf("%032x", i+1), Body: "Site visit", Date: "2034-09-09", Zone: "UTC"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpdateServiceOccurrence(ctx, shop, work[0].ID, "completed", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateServiceOccurrence(ctx, shop, work[1].ID, "skipped", ""); err != nil {
		t.Fatal(err)
	}
	// Ensure the pending note is attached to a reset candidate, including a move.
	if err := s.MoveServiceVisit(ctx, shop, work[2].ID, now.AddDate(0, 0, 1), 0); err != nil {
		t.Fatal(err)
	}
	rows[0].ServiceName = "Changed service"
	rows[0].AddressLine1 = "Corrected site"
	before := importShopSnapshot(t, db, shop)
	for _, table := range []string{"import_rows", "pilot_activation_events"} {
		if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_import_late ON dbo.`+table+` AFTER INSERT,UPDATE AS BEGIN THROW 51000,'synthetic late import failure',1; END`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CommitImport(ctx, shop, rows); err == nil {
			t.Fatal("late failure reported saved")
		}
		if !reflect.DeepEqual(before, importShopSnapshot(t, db, shop)) {
			t.Fatal("late failure changed domain or history")
		}
		if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_import_late`); err != nil {
			t.Fatal(err)
		}
	}
	// Relationships outside this CSV must not be silently detached.
	outside, err := s.CreateAgreement(ctx, shop, CreateAgreementInput{CustomerID: old, PropertyID: property, Status: AgreementActive, AnniversaryDate: now.AddDate(1, 0, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitImport(ctx, shop, rows); err == nil {
		t.Fatal("outside agreement reassigned")
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.agreements WHERE id=CONVERT(uniqueidentifier,@p1)`, outside); err != nil {
		t.Fatal(err)
	}
	job, err := s.jobsService().Create(ctx, shop, strings.Repeat("e", 32), "Owner", jobs.Input{CustomerID: old, PropertyID: property, Kind: "survey", Title: "Outside job"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitImport(ctx, shop, rows); err == nil {
		t.Fatal("outside job reassigned")
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.jobs WHERE id=CONVERT(uniqueidentifier,@p1)`, job); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal("valid included reparent rejected", err)
	}
	address, err := s.GetPropertyAddress(ctx, shop, property)
	if err != nil || address.CustomerID != customer || address.AddressLine1 != "Corrected site" {
		t.Fatal("property identity lost", err)
	}
	for _, table := range []string{"property_details", "property_notes", "property_measurements", "property_photos", "equipment"} {
		order := "id"
		if table == "property_details" {
			order = "property_id"
		}
		if before[table] != importTableSnapshot(t, db, shop, table, order) {
			t.Fatal("property history changed", table)
		}
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.visit_notes WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&n); err != nil || n != 2 {
		t.Fatal("visit cleanup removed retained history or kept reset history", n, err)
	}
}

func TestSQLServerImportConcurrentAndOneConnection(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "import-races")
	one := importTestRows(t)[0]
	two := one
	two.ExternalID = "second"
	two.ServiceExternalID = "second-service"
	gate := make(chan struct{})
	done := make(chan error, 2)
	for _, rows := range [][]ImportRow{{one, two}, {two, one}} {
		go func(rows []ImportRow) { <-gate; _, err := s.CommitImport(ctx, shop, rows); done <- err }(rows)
	}
	close(gate)
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal("overlapping import", err)
		}
	}
	if n := countShopRows(t, ctx, db, "agreements", shop); n != 2 {
		t.Fatal("competing import duplicated", n)
	}
	lock, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Exec(ctx, `UPDATE dbo.shops SET name=name WHERE id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	wait, stop := context.WithTimeout(ctx, 80*time.Millisecond)
	defer stop()
	if _, err := s.CommitImport(wait, shop, []ImportRow{one}); err == nil {
		t.Fatal("cancelled import succeeded")
	}
	if err := lock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var held []database.Tx
	defer func() {
		for _, tx := range held {
			tx.Rollback(ctx)
		}
	}()
	for i := 0; i < 3; i++ {
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, tx)
	}
	if _, err := s.CommitImport(ctx, shop, []ImportRow{one, two}); err != nil {
		t.Fatal("one free connection or lock leaked", err)
	}
}

type importCommitFailurePool struct{ database.Pool }

func (p importCommitFailurePool) Begin(ctx context.Context) (database.Tx, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &importCommitFailureTx{Tx: tx}, nil
}

type importCommitFailureTx struct {
	database.Tx
	domain bool
}

func (tx *importCommitFailureTx) Exec(ctx context.Context, query string, args ...any) (database.Result, error) {
	if strings.Contains(query, "dbo.import_rows") {
		tx.domain = true
	}
	return tx.Tx.Exec(ctx, query, args...)
}
func (tx *importCommitFailureTx) Commit(ctx context.Context) error {
	if err := tx.Tx.Commit(ctx); err != nil {
		return err
	}
	if tx.domain {
		return errors.New("synthetic lost import commit acknowledgement")
	}
	return nil
}

func TestSQLServerImportUnconfirmedCommitKeepsSavedRowsAndPrivateFeedback(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}
	for _, demo := range []bool{false, true} {
		t.Run(fmt.Sprint(demo), func(t *testing.T) {
			store := &Store{db: db, clock: clock}
			shop := createTestShop(t, ctx, db, fmt.Sprint("unconfirmed-import-", demo))
			if demo {
				if _, err := db.Pool.Exec(ctx, `UPDATE dbo.shops SET kind='demo_sandbox',expires_at=@p2 WHERE id=CONVERT(uniqueidentifier,@p1)`, shop, clock.now.Add(time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			raw := []byte("external_id,customer_name,address_line1,city,state,postal_code,anniversary_date\nunknown-result,Customer,1 Test Way,Cary,NC,27511,2035-09-09\n")
			auth := newAuthWithClock(db, "synthetic-import-secret", false, clock)
			form := url.Values{"csv_data": {base64.StdEncoding.EncodeToString(raw)}}
			if demo {
				token := "protected-preview"
				if err := store.CreatePublicDemoImportPreview(ctx, shop, hashToken(token, auth.sessionSecret), raw, 1, 0, clock.now); err != nil {
					t.Fatal(err)
				}
				form.Set("preview_token", token)
			}
			failing := &Store{db: &DB{Pool: importCommitFailurePool{db.Pool}}, clock: clock}
			server := &Server{store: failing, auth: auth, config: securityTestConfig(), tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
			server.config.RuntimeLimits.MaxRequestBodyBytes = 8 << 20
			req := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			user := &User{ShopID: shop, Role: "owner", IsDemo: demo}
			w := httptest.NewRecorder()
			server.handleImportCommit(w, req, user)
			if w.Code != 503 || !strings.Contains(w.Body.String(), "could not be confirmed") || strings.Contains(w.Body.String(), "synthetic") || strings.Contains(w.Body.String(), "failed safely") || strings.Contains(w.Body.String(), "Import complete") {
				t.Fatal("unconfirmed save feedback", w.Code)
			}
			if n := countShopRows(t, ctx, db, "agreements", shop); n != 1 {
				t.Fatal("uncertain committed import was deleted", n)
			}
			if demo {
				var used int
				if err := db.Pool.QueryRow(ctx, `SELECT demo_import_rows_used FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&used); err != nil || used != 1 {
					t.Fatal("admitted allowance lost", used, err)
				}
				if _, err := store.ConsumePublicDemoImportPreview(ctx, shop, hashToken("protected-preview", auth.sessionSecret)); !errors.Is(err, errPublicDemoImportPreviewInvalid) {
					t.Fatal("uncertain import restored preview", err)
				}
			}
		})
	}
}

type importBarrierPool struct {
	database.Pool
	reached    chan int
	release    chan struct{}
	afterPlans bool
}

func (p importBarrierPool) Begin(ctx context.Context) (database.Tx, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &importBarrierTx{Tx: tx, p: p, ctx: ctx}, nil
}

type importBarrierTx struct {
	database.Tx
	p    importBarrierPool
	ctx  context.Context
	used bool
}

func (tx *importBarrierTx) pause() {
	if tx.used {
		return
	}
	tx.used = true
	var spid int
	if err := tx.Tx.QueryRow(tx.ctx, `SELECT @@SPID`).Scan(&spid); err != nil {
		return
	}
	select {
	case tx.p.reached <- spid:
	case <-tx.ctx.Done():
		return
	}
	select {
	case <-tx.p.release:
	case <-tx.ctx.Done():
	}
}

type importBarrierRow struct {
	database.Row
	after func()
}

func (r importBarrierRow) Scan(args ...any) error {
	err := r.Row.Scan(args...)
	if err == nil {
		r.after()
	}
	return err
}
func (tx *importBarrierTx) QueryRow(ctx context.Context, query string, args ...any) database.Row {
	row := tx.Tx.QueryRow(ctx, query, args...)
	if !tx.p.afterPlans && strings.Contains(query, "dbo.shops WITH(XLOCK,HOLDLOCK)") {
		return importBarrierRow{row, tx.pause}
	}
	return row
}

type importBarrierRows struct {
	database.Rows
	after func()
}

func (r importBarrierRows) Close() error { err := r.Rows.Close(); r.after(); return err }
func (tx *importBarrierTx) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	rows, err := tx.Tx.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if tx.p.afterPlans && strings.Contains(query, "dbo.plan_templates WITH(HOLDLOCK)") {
		return importBarrierRows{rows, tx.pause}, nil
	}
	return rows, nil
}

func TestSQLServerImportActivationAndWorkUseParentFirstLocks(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
	clock := fixedClock{now: now}
	s := &Store{db: db, clock: clock}
	shop := createTestShop(t, ctx, db, "import-activation-race")
	rows := importTestRows(t)
	if _, err := s.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal(err)
	}
	work, err := s.ListServiceOccurrences(ctx, shop, false)
	if err != nil || len(work) == 0 {
		t.Fatal(err)
	}
	// Empty event range makes the former event-key -> shop inversion deterministic.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.pilot_activation_events WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	barrier := importBarrierPool{Pool: db.Pool, reached: make(chan int, 1), release: make(chan struct{})}
	defer func() {
		select {
		case <-barrier.release:
		default:
			close(barrier.release)
		}
	}()
	importing := &Store{db: &DB{Pool: barrier}, clock: clock}
	done := make(chan error, 3)
	go func() { _, err := importing.CommitImport(ctx, shop, rows); done <- err }()
	var blocker int
	select {
	case blocker = <-barrier.reached:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	go func() { done <- s.RecordPilotActivation(ctx, shop, pilotEventImportPreviewed) }()
	go func() { done <- s.UpdateServiceOccurrence(ctx, shop, work[0].ID, "completed", "") }()
	waitForSeasonalBlockedSessions(t, ctx, db, blocker, 2)
	close(barrier.release)
	for i := 0; i < 3; i++ {
		if err := <-done; err != nil {
			t.Fatal("import, activation, or work deadlocked", err)
		}
	}
}

func TestSQLServerImportHoldsCurrentTemplateSnapshot(t *testing.T) {
	for _, remove := range []bool{false, true} {
		t.Run(fmt.Sprint(remove), func(t *testing.T) {
			db := sqlServerTestDB(t)
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			shop := createTestShop(t, ctx, db, fmt.Sprint("import-plan-", remove))
			plan := createTestPlan(t, ctx, db, shop, "Snapshot plan")
			rows := importTestRows(t)
			rows[0].PlanName = "Snapshot plan"
			barrier := importBarrierPool{Pool: db.Pool, reached: make(chan int, 1), release: make(chan struct{}), afterPlans: true}
			defer func() {
				select {
				case <-barrier.release:
				default:
					close(barrier.release)
				}
			}()
			importing := &Store{db: &DB{Pool: barrier}}
			done := make(chan error, 2)
			go func() { _, err := importing.CommitImport(ctx, shop, rows); done <- err }()
			var blocker int
			select {
			case blocker = <-barrier.reached:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			go func() {
				query := `UPDATE dbo.plan_templates SET visits_per_year=8,description=N'New template' WHERE id=CONVERT(uniqueidentifier,@p1)`
				if remove {
					query = `DELETE FROM dbo.plan_templates WHERE id=CONVERT(uniqueidentifier,@p1)`
				}
				_, err := db.Pool.Exec(ctx, query, plan)
				done <- err
			}()
			waitForSeasonalBlockedSessions(t, ctx, db, blocker, 1)
			close(barrier.release)
			for i := 0; i < 2; i++ {
				if err := <-done; err != nil {
					t.Fatal("template race", err)
				}
			}
			var visits int
			var description string
			var pointer *string
			if err := db.Pool.QueryRow(ctx, `SELECT plan_snapshot_visits_per_year,plan_snapshot_description,LOWER(CONVERT(char(36),plan_template_id)) FROM dbo.agreements WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&visits, &description, &pointer); err != nil || visits != 2 || description != "" || (remove && pointer != nil) {
				t.Fatal("torn template snapshot", err)
			}
		})
	}
}

func TestSQLServerImportUnconfirmedPreviewAdmissionExplainsAllowance(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	clock := fixedClock{now: time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)}
	s := &Store{db: db, clock: clock}
	demo, err := seedDemoControlsFixture(t, db, s)
	if err != nil {
		t.Fatal(err)
	}
	auth := newAuthWithClock(db, "synthetic-admission-secret", false, clock)
	token := "admission-token"
	key := hashToken(token, auth.sessionSecret)
	raw := []byte("external_id,customer_name,address_line1,city,state,postal_code,anniversary_date\nkey,Customer,1 Test Way,Cary,NC,27511,2035-09-09\n")
	if err := s.CreatePublicDemoImportPreview(ctx, demo.ShopID, key, raw, 1, 0, clock.now); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: &Store{db: &DB{Pool: committedDemoFailurePool{db.Pool}}, clock: clock}, auth: auth}
	req := httptest.NewRequest(http.MethodPost, "/import", strings.NewReader(url.Values{"preview_token": {token}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	server.handleImportCommit(w, req, &User{ShopID: demo.ShopID, IsDemo: true})
	if w.Code != 503 || !strings.Contains(w.Body.String(), "allowance may already have been consumed") {
		t.Fatal("ambiguous admission message", w.Code)
	}
	if n := countShopRows(t, ctx, db, "agreements", demo.ShopID); n != 0 {
		t.Fatal("unconfirmed admission entered domain import")
	}
	if _, err := s.ConsumePublicDemoImportPreview(ctx, demo.ShopID, key); !errors.Is(err, errPublicDemoImportPreviewInvalid) {
		t.Fatal("lost acknowledgement restored token", err)
	}
}

type cancelledImportPool struct {
	database.Pool
	cancel context.CancelFunc
}

func (p cancelledImportPool) Begin(ctx context.Context) (database.Tx, error) {
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return cancelledImportTx{Tx: tx, cancel: p.cancel}, nil
}

type cancelledImportTx struct {
	database.Tx
	cancel context.CancelFunc
}

func (tx cancelledImportTx) Exec(ctx context.Context, query string, args ...any) (database.Result, error) {
	result, err := tx.Tx.Exec(ctx, query, args...)
	if err == nil && strings.Contains(query, "dbo.import_rows") {
		tx.cancel()
		return result, context.Canceled
	}
	return result, err
}
func TestSQLServerImportCancellationAfterWritesRollsBack(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	shop := createTestShop(t, ctx, db, "cancel-written-import")
	before := importShopSnapshot(t, db, shop)
	request, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &Store{db: &DB{Pool: cancelledImportPool{db.Pool, cancel}}}
	if _, err := s.CommitImport(request, shop, importTestRows(t)); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel seam", err)
	}
	if !reflect.DeepEqual(before, importShopSnapshot(t, db, shop)) {
		t.Fatal("cancelled import retained partial writes")
	}
	if _, err := (&Store{db: db}).CommitImport(ctx, shop, importTestRows(t)); err != nil {
		t.Fatal("cancelled import leaked locks", err)
	}
}
func TestSQLServerImportYearOneCheckpointRejectsAtomically(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	shop := createTestShop(t, ctx, db, "year-one-import")
	s := &Store{db: db, clock: fixedClock{now: time.Date(1, 1, 1, 23, 59, 0, 0, time.UTC)}}
	rows := importTestRows(t)
	rows[0].ServiceStartsOn = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	rows[0].AnniversaryDate = rows[0].ServiceStartsOn
	if _, err := s.CommitImport(ctx, shop, rows); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.service_schedules SET generated_through='0001-01-01' WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	before := importShopSnapshot(t, db, shop)
	rows[0].ServiceName = "Changed"
	var input *importRowError
	if _, err := s.CommitImport(ctx, shop, rows); !errors.As(err, &input) {
		t.Fatal("year zero checkpoint accepted", err)
	}
	if !reflect.DeepEqual(before, importShopSnapshot(t, db, shop)) {
		t.Fatal("checkpoint rejection changed rows")
	}
}

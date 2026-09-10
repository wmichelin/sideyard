package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/wmichelin/clubdue/internal/jobs"
	"strings"
	"testing"
	"time"
)

func TestSQLServerDemoPreviewExactIdentityExpiryAndRollback(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	now := time.Date(2034, 9, 9, 23, 59, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	first, err := seedDemoControlsFixture(t, db, s)
	if err != nil {
		t.Fatal(err)
	}
	second, err := seedDemoControlsFixture(t, db, s)
	if err != nil {
		t.Fatal(err)
	}
	projection := time.Date(2034, 9, 9, 22, 0, 0, 0, time.FixedZone("west", -4*3600))
	raw := []byte{0, 255, 10, 13, 128}
	for _, token := range []string{"Token", "token", "token ", "\u2060", strings.Repeat("界", 3000)} {
		if err := s.CreatePublicDemoImportPreview(ctx, first.ShopID, token, raw, 1, 121, projection); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ConsumePublicDemoImportPreview(ctx, first.ShopID, token+" "); !errors.Is(err, errPublicDemoImportPreviewInvalid) {
			t.Fatal("identity normalized", err)
		}
		payload, err := s.ConsumePublicDemoImportPreview(ctx, first.ShopID, token)
		if err != nil || !bytes.Equal(payload.CSV, raw) || payload.ProjectionDate.Format("2006-01-02") != "2034-09-10" {
			t.Fatal("raw bytes/UTC date lost", err)
		}
	}
	if err := s.CreatePublicDemoImportPreview(ctx, first.ShopID, "old", raw, 1, 121, now); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePublicDemoImportPreview(ctx, second.ShopID, "occupied", raw, 1, 121, now); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePublicDemoImportPreview(ctx, first.ShopID, "occupied", raw, 1, 121, now); err == nil {
		t.Fatal("global token collision accepted")
	}
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_demo_reservation ON dbo.shops AFTER UPDATE AS BEGIN IF UPDATE(demo_import_rows_used) THROW 51000,'synthetic reservation failure',1; END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumePublicDemoImportPreview(ctx, first.ShopID, "old"); err == nil {
		t.Fatal("reservation failure accepted")
	}
	var rows, occurrences int
	if err := db.Pool.QueryRow(ctx, `SELECT demo_import_rows_used,demo_import_occurrences_used FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, first.ShopID).Scan(&rows, &occurrences); err != nil || rows != 5 || occurrences != 605 {
		t.Fatal("reservation leaked counters", err)
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_demo_reservation`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumePublicDemoImportPreview(ctx, first.ShopID, "old"); err != nil {
		t.Fatal("failed replacement/consume lost old preview", err)
	}
	// A shorter sandbox lifetime caps the preview, including exact microsecond expiry.
	expiry := now.Add(2 * time.Minute)
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.shops SET expires_at=@p2 WHERE id=CONVERT(uniqueidentifier,@p1)`, first.ShopID, expiry); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePublicDemoImportPreview(ctx, first.ShopID, "short", raw, 1, 0, now); err != nil {
		t.Fatal(err)
	}
	var stored time.Time
	if err := db.Pool.QueryRow(ctx, `SELECT expires_at FROM dbo.demo_import_previews WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, first.ShopID).Scan(&stored); err != nil || !stored.Equal(expiry) {
		t.Fatal("sandbox TTL not applied", stored, err)
	}
	s.clock = fixedClock{now: expiry}
	if _, err := s.ConsumePublicDemoImportPreview(ctx, first.ShopID, "short"); !errors.Is(err, errPublicDemoImportPreviewInvalid) {
		t.Fatal("exact expiry accepted", err)
	}
	s.clock = fixedClock{now: expiry.Add(-time.Microsecond)}
	if _, err := s.ConsumePublicDemoImportPreview(ctx, first.ShopID, "short"); err != nil {
		t.Fatal("expired early", err)
	}
	// A budget race after preview creation must roll back its deletion.
	s.clock = fixedClock{now: now}
	if err := s.CreatePublicDemoImportPreview(ctx, first.ShopID, "budget", raw, 1, 0, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.shops SET demo_import_rows_used=500 WHERE id=CONVERT(uniqueidentifier,@p1)`, first.ShopID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ConsumePublicDemoImportPreview(ctx, first.ShopID, "budget"); !errors.Is(err, errPublicDemoImportRowBudgetReached) {
		t.Fatal("budget overflow", err)
	}
	var token string
	if err := db.Pool.QueryRow(ctx, `SELECT token_hash FROM dbo.demo_import_previews WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, first.ShopID).Scan(&token); err != nil || token != "budget" {
		t.Fatal("budget failure consumed preview", err)
	}
	for _, bad := range []string{"not-uuid", first.ShopID + "suffix"} {
		if err := s.CreatePublicDemoImportPreview(ctx, bad, "x", raw, 1, 0, now); !errors.Is(err, errPublicDemoImportPreviewInvalid) {
			t.Fatal("malformed identity", err)
		}
	}
}

func TestSQLServerDemoMutationAdmissionAndCleanup(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}, photoObjects: &fakePhotoObjects{}}
	demo, err := seedDemoControlsFixture(t, db, s)
	if err != nil {
		t.Fatal(err)
	}
	customerShop := createTestShop(t, ctx, db, "demo-cleanup-real")
	if err := s.ReservePublicDemoMutation(ctx, customerShop); !errors.Is(err, errPublicDemoMutationBudgetReached) {
		t.Fatal("real shop admitted", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.shops SET demo_mutations_used=29 WHERE id=CONVERT(uniqueidentifier,@p1)`, demo.ShopID); err != nil {
		t.Fatal(err)
	}
	gate := make(chan struct{})
	done := make(chan error, 6)
	for i := 0; i < 6; i++ {
		go func() { <-gate; done <- s.ReservePublicDemoMutation(ctx, demo.ShopID) }()
	}
	close(gate)
	wins := 0
	for i := 0; i < 6; i++ {
		err := <-done
		if err == nil {
			wins++
		} else if !errors.Is(err, errPublicDemoMutationBudgetReached) {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatal("mutation budget admitted", wins)
	}
	customer := createTestCustomer(t, ctx, db, demo.ShopID, "Fictional")
	property := createTestProperty(t, ctx, db, demo.ShopID, customer, "Address")
	photo, err := normalizePhoto(photoFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPropertyPhoto(ctx, demo.ShopID, property, strings.Repeat("a", 32), "", "general", "Owner", photo); err != nil {
		t.Fatal(err)
	}
	if _, err := s.jobsService().Create(ctx, demo.ShopID, strings.Repeat("b", 32), "Owner", jobs.Input{CustomerID: customer, PropertyID: property, Kind: "job", Title: "Fictional job"}); err != nil {
		t.Fatal(err)
	}
	a, _, work := recurringFixture(t, db, s, demo.ShopID, 5, true)
	if err := s.AddVisitNote(ctx, demo.ShopID, "recurring", work[0].ID, "Owner", internalNoteInput{Key: strings.Repeat("c", 32), Body: "Fictional history", Date: "2034-09-09", Zone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRenewalLink(ctx, demo.ShopID, a, "demo-cleanup-link", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePublicDemoImportPreview(ctx, demo.ShopID, "demo-cleanup-preview", []byte("CSV"), 1, 0, now); err != nil {
		t.Fatal(err)
	}
	if err := insertTestUser(ctx, db, demo.ShopID, "cleanup@demo.test"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.sessions(user_id,token_hash,expires_at) SELECT id,@p2,@p3 FROM dbo.users WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, demo.ShopID, strings.Repeat("d", 64), demo.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	s.clock = fixedClock{now: demo.ExpiresAt}
	if err := s.ReservePublicDemoMutation(ctx, demo.ShopID); !errors.Is(err, errPublicDemoMutationBudgetReached) {
		t.Fatal("expired admission", err)
	}
	if err := s.CleanupExpiredPublicDemos(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"shops", "users", "customers", "properties", "property_photos", "jobs", "job_events", "agreements", "service_schedules", "service_occurrences", "visit_dues", "visit_notes", "renewal_links", "demo_import_previews"} {
		column := "shop_id"
		if table == "shops" {
			column = "id"
		}
		var n int
		if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.`+table+` WHERE `+column+`=CONVERT(uniqueidentifier,@p1)`, demo.ShopID).Scan(&n); err != nil || n != 0 {
			t.Fatal("cleanup left "+table, n, err)
		}
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.sessions`).Scan(&n); err != nil || n != 0 {
		t.Fatal("session cascade", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.property_photo_objects WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND retired_at IS NOT NULL`, demo.ShopID).Scan(&n); err != nil || n != 1 {
		t.Fatal("recovery inventory discarded", n, err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, customerShop).Scan(&n); err != nil || n != 1 {
		t.Fatal("customer shop deleted", err)
	}
}

func TestSQLServerLegacyDemoRetirementRequiresExactIdentity(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	cases := []struct{ slug, email string }{{"demo-hvac-shop", "OWNER@DEMO.LOCAL "}, {"demo-hvac-shop ", "OWNER@DEMO.LOCAL"}, {"Demo-hvac-shop", "elsewhere@example.test"}}
	for i, tc := range cases {
		id := createTestShop(t, ctx, db, fmt.Sprint("legacy-near-", i))
		if _, err := db.Pool.Exec(ctx, `UPDATE dbo.shops SET slug=@p2 WHERE id=CONVERT(uniqueidentifier,@p1)`, id, tc.slug); err != nil {
			t.Fatal(err)
		}
		if err := insertTestUser(ctx, db, id, tc.email); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := s.RetireLegacySharedDemo(ctx); err != nil || n != 0 {
		t.Fatal("retired near-match", n, err)
	}
	// Move the exact normalized owner from the trailing-space slug to the exact slug.
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.users SET email=N'former@example.test',email_normalized=N'former@example.test' WHERE email_normalized=N'owner@demo.local ' AND DATALENGTH(email_normalized)=DATALENGTH(N'owner@demo.local '); UPDATE u SET shop_id=(SELECT id FROM dbo.shops WHERE slug=N'demo-hvac-shop' AND DATALENGTH(slug)=DATALENGTH(N'demo-hvac-shop')) FROM dbo.users u WHERE email_normalized=N'owner@demo.local' AND DATALENGTH(email_normalized)=DATALENGTH(N'owner@demo.local')`); err != nil {
		t.Fatal(err)
	}
	if n, err := s.RetireLegacySharedDemo(ctx); err != nil || n != 1 {
		t.Fatal("exact legacy not retired", n, err)
	}
}

func TestSQLServerDemoPreviewCancellationAndOneConnection(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	demo, err := seedDemoControlsFixture(t, db, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreatePublicDemoImportPreview(ctx, demo.ShopID, "original", []byte("keep"), 1, 0, now); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var n int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.shops WITH(UPDLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, demo.ShopID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	if err := s.CreatePublicDemoImportPreview(short, demo.ShopID, "cancelled", []byte("discard"), 1, 0, now); err == nil || short.Err() == nil {
		t.Fatal("cancelled replacement succeeded", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// A cancelled lock wait must release its connection and leave the old payload.
	for i := 0; i < 3; i++ {
		held, err := db.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Rollback(ctx)
	}
	payload, err := s.ConsumePublicDemoImportPreview(ctx, demo.ShopID, "original")
	if err != nil || string(payload.CSV) != "keep" {
		t.Fatal("cancelled replacement consumed original or leaked connection", err)
	}
	if err := s.CreatePublicDemoImportPreview(ctx, demo.ShopID, "next", []byte("next"), 1, 0, now); err != nil {
		t.Fatal("one-connection preview", err)
	}
	if err := s.ReservePublicDemoMutation(ctx, demo.ShopID); err != nil {
		t.Fatal("one-connection reserve", err)
	}
	if err := s.CleanupExpiredPublicDemos(ctx); err != nil {
		t.Fatal("one-connection cleanup", err)
	}
}

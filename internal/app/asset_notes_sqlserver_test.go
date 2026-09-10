package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSQLServerAssetNotesPreserveExistingHistory(t *testing.T) {
	testAssetNotesKeepIndependentAuthoredHistory(t, sqlServerTestDB(t))
}

func TestSQLServerAssetNoteFailureRetryAndHistoryOrdering(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "asset-history-order")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	if err := s.SaveCustomerVehicle(ctx, shop, customer, customerVehicle{Name: "Truck"}); err != nil {
		t.Fatal(err)
	}
	vehicles, err := s.ListCustomerVehicles(ctx, shop, customer)
	if err != nil {
		t.Fatal(err)
	}
	id := vehicles[0].ID
	input := internalNoteInput{Key: strings.Repeat("f", 32), Body: "Backdated note", Date: "2025-12-31", Zone: "UTC"}
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_test_asset_note ON dbo.asset_notes AFTER INSERT AS BEGIN THROW 51000,'synthetic asset note failure',1; END`); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAssetNote(ctx, shop, "vehicle", id, "Owner", input); err == nil || errors.Is(err, errAssetNoteConflict) {
		t.Fatalf("insert failure acknowledged: %v", err)
	}
	notes, err := s.ListAssetNotes(ctx, shop, "vehicle", id)
	if err != nil || len(notes) != 0 {
		t.Fatal("failed asset note retained history")
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_test_asset_note`); err != nil {
		t.Fatal(err)
	}
	retry, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.AddAssetNote(retry, shop, "vehicle", id, "Owner", input); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 101; i++ {
		uuid := fmt.Sprintf("%08x-0000-0000-0000-%012x", i, 100-i)
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.asset_notes(id,shop_id,vehicle_id,submission_key,body,occurred_on,event_timezone,recorded_by,recorded_at) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),@p4,@p5,'2026-01-01',N'UTC',N'Owner','2026-01-01')`, uuid, shop, id, fmt.Sprintf("%032x", i), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	notes, err = s.ListAssetNotes(ctx, shop, "vehicle", id)
	if err != nil || len(notes) != 100 || notes[0].Body != "100" || notes[99].Body != "1" {
		t.Fatalf("event/recording/UUID order: count%d %v", len(notes), err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.asset_notes WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&count); err != nil || count != 102 {
		t.Fatal("hidden history discarded")
	}
}

func TestSQLServerAssetNoteConcurrentParentChangesKeepIntegrity(t *testing.T) {
	for _, kind := range []string{"vehicle", "equipment"} {
		for _, change := range []string{"delete", "identity"} {
			for _, noteFirst := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%s/note_first_%v", kind, change, noteFirst), func(t *testing.T) {
					db := sqlServerTestDB(t)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					s := &Store{db: db}
					shop := createTestShop(t, ctx, db, "asset-race")
					customer := createTestCustomer(t, ctx, db, shop, "Customer")
					property := createTestProperty(t, ctx, db, shop, customer, "Address")
					var id, table string
					if kind == "vehicle" {
						if err := s.SaveCustomerVehicle(ctx, shop, customer, customerVehicle{Name: "Truck"}); err != nil {
							t.Fatal(err)
						}
						items, err := s.ListCustomerVehicles(ctx, shop, customer)
						if err != nil {
							t.Fatal(err)
						}
						id, table = items[0].ID, "customer_vehicles"
					} else {
						if err := s.CreateEquipment(ctx, shop, property, "Unit", "", "", ""); err != nil {
							t.Fatal(err)
						}
						items, err := s.ListEquipment(ctx, shop, property)
						if err != nil {
							t.Fatal(err)
						}
						id, table = items[0].ID, "equipment"
					}
					query := `DELETE FROM dbo.` + table + ` WHERE id=CONVERT(uniqueidentifier,@p1)`
					if change == "identity" {
						query = `UPDATE dbo.` + table + ` SET id=NEWID() WHERE id=CONVERT(uniqueidentifier,@p1)`
					}
					input := internalNoteInput{Key: strings.Repeat("a", 32), Body: "Concurrent note", Date: "2026-01-01", Zone: "UTC"}
					tx, err := db.Pool.Begin(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback(ctx)
					started := make(chan struct{})
					done := make(chan error, 1)
					if noteFirst {
						column := "vehicle_id"
						if kind == "equipment" {
							column = "equipment_id"
						}
						if _, err := tx.Exec(ctx, `INSERT INTO dbo.asset_notes(shop_id,`+column+`,submission_key,body,occurred_on,event_timezone,recorded_by) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,N'Concurrent note','2026-01-01',N'UTC',N'Owner')`, shop, id, input.Key); err != nil {
							t.Fatal(err)
						}
						go func() { close(started); _, err := db.Pool.Exec(ctx, query, id); done <- err }()
					} else {
						if _, err := tx.Exec(ctx, query, id); err != nil {
							t.Fatal(err)
						}
						go func() { close(started); done <- s.AddAssetNote(ctx, shop, kind, id, "Owner", input) }()
					}
					<-started
					select {
					case err := <-done:
						t.Fatalf("competing write bypassed held parent: %v", err)
					case <-time.After(30 * time.Millisecond):
					}
					if err := tx.Commit(ctx); err != nil {
						t.Fatal(err)
					}
					err = <-done
					if noteFirst && change == "delete" {
						if err != nil {
							t.Fatal(err)
						}
					} else if err == nil {
						t.Fatal("conflicting parent/note write committed")
					}
					var orphans int
					if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.asset_notes n WHERE (n.vehicle_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dbo.customer_vehicles v WHERE v.shop_id=n.shop_id AND v.id=n.vehicle_id)) OR (n.equipment_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM dbo.equipment e WHERE e.shop_id=n.shop_id AND e.id=n.equipment_id))`).Scan(&orphans); err != nil || orphans != 0 {
						t.Fatal("concurrent parent change left orphan history")
					}
					if noteFirst && change == "identity" {
						notes, err := s.ListAssetNotes(ctx, shop, kind, id)
						if err != nil || len(notes) != 1 {
							t.Fatal("rejected identity change lost note")
						}
					}
				})
			}
		}
	}
}

func TestAssetHistoryRejectsNoncanonicalKindsBeforeDatabase(t *testing.T) {
	s := &Store{}
	for _, kind := range []string{"Vehicle", "vehicle ", "equipment ", "unknown"} {
		if _, err := s.ListAssetNotes(context.Background(), "12345678-1234-1234-1234-123456789012", kind, "12345678-1234-1234-1234-123456789013"); !errors.Is(err, errAssetNoteInput) {
			t.Fatalf("invalid kind: %v", err)
		}
	}
}

func TestSQLServerAssetNoteCascadesAndRollbackRemainTenantScoped(t *testing.T) {
	for _, parent := range []string{"equipment", "customer_vehicles", "properties", "customers", "shops"} {
		t.Run(parent, func(t *testing.T) {
			db := sqlServerTestDB(t)
			ctx := context.Background()
			s := &Store{db: db}
			shop := createTestShop(t, ctx, db, "asset-cascade")
			other := createTestShop(t, ctx, db, "asset-cascade-other")
			for _, tenant := range []string{shop, other} {
				customer := createTestCustomer(t, ctx, db, tenant, "Customer")
				for i := 0; i < 2; i++ {
					property := createTestProperty(t, ctx, db, tenant, customer, fmt.Sprint("Address", i))
					if err := s.CreateEquipment(ctx, tenant, property, "Unit", "", "", ""); err != nil {
						t.Fatal(err)
					}
					if err := s.SaveCustomerVehicle(ctx, tenant, customer, customerVehicle{Name: fmt.Sprint("Vehicle", i)}); err != nil {
						t.Fatal(err)
					}
					equipment, err := s.ListEquipment(ctx, tenant, property)
					if err != nil {
						t.Fatal(err)
					}
					if err := s.AddAssetNote(ctx, tenant, "equipment", equipment[0].ID, "Owner", internalNoteInput{Key: fmt.Sprintf("%032x", i), Body: "Equipment note", Date: "2026-01-01", Zone: "UTC"}); err != nil {
						t.Fatal(err)
					}
				}
				vehicles, err := s.ListCustomerVehicles(ctx, tenant, customer)
				if err != nil {
					t.Fatal(err)
				}
				for i, v := range vehicles {
					if err := s.AddAssetNote(ctx, tenant, "vehicle", v.ID, "Owner", internalNoteInput{Key: fmt.Sprintf("%032x", i+10), Body: "Vehicle note", Date: "2026-01-01", Zone: "UTC"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			query := `DELETE FROM dbo.` + parent + ` WHERE shop_id=CONVERT(uniqueidentifier,@p1)`
			if parent == "shops" {
				query = `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`
			}
			tx, err := db.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, query, shop); err != nil {
				tx.Rollback(ctx)
				t.Fatal(err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var own, foreign int
			count := func() {
				t.Helper()
				if err := db.Pool.QueryRow(ctx, `SELECT SUM(CASE WHEN shop_id=CONVERT(uniqueidentifier,@p1) THEN 1 ELSE 0 END),SUM(CASE WHEN shop_id=CONVERT(uniqueidentifier,@p2) THEN 1 ELSE 0 END) FROM dbo.asset_notes`, shop, other).Scan(&own, &foreign); err != nil {
					t.Fatal(err)
				}
			}
			count()
			if own != 4 || foreign != 4 {
				t.Fatal("cascade rollback lost history")
			}
			if _, err := db.Pool.Exec(ctx, query, shop); err != nil {
				t.Fatal(err)
			}
			count()
			expected := 2
			if parent == "customers" || parent == "shops" {
				expected = 0
			}
			if own != expected || foreign != 4 {
				t.Fatalf("cascade: own=%d foreign=%d", own, foreign)
			}
		})
	}
}

func TestAssetEventUTCDateRemainsInSupportedRange(t *testing.T) {
	input := internalNoteInput{Key: strings.Repeat("a", 32), Body: "Note", Date: "0001-01-01", Time: "00:00", Zone: "Asia/Tokyo"}
	if _, err := validateInternalNote(input, time.Now()); err == nil {
		t.Fatal("year-zero UTC instant accepted")
	}
	input.Time = ""
	event, err := validateInternalNote(input, time.Now())
	if err != nil || event.At != nil || event.Date.Year() != 1 {
		t.Fatal("valid date-only note changed")
	}
}

func TestSQLServerAssetNoteIntegrityPreservesMovesAndRejectsOrphans(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "asset-integrity")
	other := createTestShop(t, ctx, db, "asset-other")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	second := createTestCustomer(t, ctx, db, shop, "Second")
	foreign := createTestCustomer(t, ctx, db, other, "Foreign")
	property := createTestProperty(t, ctx, db, shop, customer, "Address")
	secondProperty := createTestProperty(t, ctx, db, shop, second, "Second")
	foreignProperty := createTestProperty(t, ctx, db, other, foreign, "Foreign")
	if err := s.SaveCustomerVehicle(ctx, shop, customer, customerVehicle{Name: "Truck"}); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateEquipment(ctx, shop, property, "Heat pump", "", "", ""); err != nil {
		t.Fatal(err)
	}
	vehicles, err := s.ListCustomerVehicles(ctx, shop, customer)
	if err != nil {
		t.Fatal(err)
	}
	equipment, err := s.ListEquipment(ctx, shop, property)
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct{ kind, table, id, parentColumn, secondParent, foreignParent string }{
		{"vehicle", "customer_vehicles", vehicles[0].ID, "customer_id", second, foreign},
		{"equipment", "equipment", equipment[0].ID, "property_id", secondProperty, foreignProperty},
	} {
		input := internalNoteInput{Key: fmt.Sprintf("%032x", i), Body: "Keep history", Date: "2026-01-01", Zone: "UTC"}
		if err := s.AddAssetNote(ctx, shop, tc.kind, tc.id, "Owner", input); err != nil {
			t.Fatal(err)
		}
		base := `UPDATE dbo.` + tc.table + ` SET `
		if _, err := db.Pool.Exec(ctx, base+`id=NEWID() WHERE id=CONVERT(uniqueidentifier,@p1)`, tc.id); err == nil {
			t.Fatal("referenced identity changed")
		}
		if _, err := db.Pool.Exec(ctx, base+`shop_id=CONVERT(uniqueidentifier,@p2),`+tc.parentColumn+`=CONVERT(uniqueidentifier,@p3) WHERE id=CONVERT(uniqueidentifier,@p1)`, tc.id, other, tc.foreignParent); err == nil {
			t.Fatal("referenced identity moved across shops")
		}
		if _, err := db.Pool.Exec(ctx, base+tc.parentColumn+`=CONVERT(uniqueidentifier,@p2),id=id,shop_id=shop_id WHERE id=CONVERT(uniqueidentifier,@p1)`, tc.id, tc.secondParent); err != nil {
			t.Fatal(err)
		}
		target, err := s.GetAssetHistoryTarget(ctx, shop, tc.kind, tc.id)
		if err != nil || target.CustomerID != second {
			t.Fatal("allowed asset move lost ownership")
		}
		notes, err := s.ListAssetNotes(ctx, shop, tc.kind, tc.id)
		if err != nil || len(notes) != 1 || notes[0].Body != "Keep history" {
			t.Fatal("asset move lost notes")
		}
	}
	// A mixed valid/foreign batch must roll back every row after trigger failure.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.asset_notes(shop_id,vehicle_id,submission_key,body,occurred_on,event_timezone,recorded_by) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p3),REPLICATE(N'c',32),N'Valid','2026-01-01',N'UTC',N'Owner'),(CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),REPLICATE(N'd',32),N'Foreign','2026-01-01',N'UTC',N'Owner')`, shop, other, vehicles[0].ID); err == nil {
		t.Fatal("mixed tenant batch accepted")
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.asset_notes`).Scan(&count); err != nil || count != 2 {
		t.Fatal("failed batch left notes")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.asset_notes SET shop_id=CONVERT(uniqueidentifier,@p1)`, other); err == nil {
		t.Fatal("notes moved to wrong shop")
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.asset_notes SET vehicle_id=NULL,equipment_id=CONVERT(uniqueidentifier,@p1) WHERE vehicle_id IS NOT NULL`, strings.Repeat("0", 8)+"-0000-0000-0000-000000000000"); err == nil {
		t.Fatal("notes attached to nonexistent asset")
	}
}

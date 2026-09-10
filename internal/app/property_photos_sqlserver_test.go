package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func TestSQLServerPropertyPhotosPreserveExistingBehavior(t *testing.T) {
	t.Run("quotas and isolation", func(t *testing.T) { testPropertyPhotosIsolationRetriesQuotasAndDeletion(t, sqlServerTestDB(t)) })
	t.Run("private handlers", func(t *testing.T) { testPropertyPhotoHandlersKeepImagesPrivate(t, sqlServerTestDB(t)) })
	t.Run("handoff", func(t *testing.T) { testAzurePhotoHandoffRetriesAndPrivateReads(t, sqlServerTestDB(t)) })
	t.Run("backfill", func(t *testing.T) { testAzurePhotoBackfillPreservesExistingPhotos(t, sqlServerTestDB(t)) })
	for _, cascade := range []bool{false, true} {
		t.Run(fmt.Sprintf("late handoff cascade %v", cascade), func(t *testing.T) {
			testAzurePhotoDeleteDuringUploadRetainsVersionInventory(t, sqlServerTestDB(t), cascade)
		})
	}
}

func TestSQLServerPhotoHandoffReleasesConnectionsAndRejectsDifferentVersion(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	shop := createTestShop(t, ctx, db, "photo-connections")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	property := createTestProperty(t, ctx, db, shop, customer, "Address")
	photo, err := normalizePhoto(photoFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	objects := &fakePhotoObjects{}
	objects.beforePut = func() {
		probe, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		var held []database.Tx
		defer func() {
			for _, tx := range held {
				tx.Rollback(ctx)
			}
		}()
		for i := 0; i < 4; i++ {
			tx, err := db.Pool.Begin(probe)
			if err != nil {
				t.Fatalf("Blob call retained a pool connection: %v", err)
			}
			held = append(held, tx)
		}
	}
	s := &Store{db: db, photoObjects: objects}
	if _, err := s.AddPropertyPhoto(ctx, shop, property, strings.Repeat("a", 32), "", "general", "Owner", photo); err != nil {
		t.Fatal(err)
	}
	objects.beforePut = nil
	objects.putErr = errors.New("temporary storage failure")
	id, err := s.AddPropertyPhoto(ctx, shop, property, strings.Repeat("b", 32), "", "general", "Owner", photo)
	if err == nil || id == "" {
		t.Fatal("pending photo not retained")
	}
	objects.putErr = nil
	for _, version := range []string{"saved-version ", "Saved-version"} {
		if _, err := db.Pool.Exec(ctx, `UPDATE dbo.property_photo_objects SET version_id=@p2 WHERE photo_id=CONVERT(uniqueidentifier,@p1)`, id, version); err != nil {
			t.Fatal(err)
		}
		if err := s.syncPropertyPhotoObject(ctx, shop, id); !errors.Is(err, errPhotoConflict) {
			t.Fatalf("different version accepted: %q %v", version, err)
		}
		if _, err := s.GetPropertyPhoto(ctx, shop, id); !errors.Is(err, database.ErrNoRows) {
			t.Fatal("conflicting version published")
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.property_photo_objects SET version_id=NULL WHERE photo_id=CONVERT(uniqueidentifier,@p1)`, id); err != nil {
		t.Fatal(err)
	}
	if n, err := s.SyncPhotoObjects(ctx, 1); err != nil || n != 1 {
		t.Fatalf("resume batch: %d %v", n, err)
	}
	if n, err := s.SyncPhotoObjects(ctx, 1); err != nil || n != 0 {
		t.Fatalf("drained batch: %d %v", n, err)
	}
}

func TestSQLServerPhotoHandoffRecoversDatabasePhaseFailures(t *testing.T) {
	for _, phase := range []string{"inventory", "publication"} {
		t.Run(phase, func(t *testing.T) {
			db := sqlServerTestDB(t)
			ctx := context.Background()
			objects := &fakePhotoObjects{}
			s := &Store{db: db, photoObjects: objects}
			shop := createTestShop(t, ctx, db, "photo-phase")
			customer := createTestCustomer(t, ctx, db, shop, "Customer")
			property := createTestProperty(t, ctx, db, shop, customer, "Address")
			photo, err := normalizePhoto(photoFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER dbo.fail_photo_phase ON dbo.property_photo_objects AFTER INSERT AS BEGIN THROW 51000,'synthetic inventory failure',1; END`
			if phase == "publication" {
				trigger = `CREATE TRIGGER dbo.fail_photo_phase ON dbo.property_photos AFTER UPDATE AS BEGIN IF UPDATE(object_version) THROW 51000,'synthetic publication failure',1; END`
			}
			if _, err := db.Pool.Exec(ctx, trigger); err != nil {
				t.Fatal(err)
			}
			key := strings.Repeat("c", 32)
			id, err := s.AddPropertyPhoto(ctx, shop, property, key, "Original", "general", "Owner", photo)
			if err == nil {
				t.Fatal("failed database phase reported saved")
			}
			listed, count, used, err := s.ListPropertyPhotos(ctx, shop, property)
			if err != nil || len(listed) != 0 {
				t.Fatal("failed photo became visible")
			}
			if phase == "inventory" {
				var inventory int
				if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.property_photo_objects`).Scan(&inventory); err != nil || inventory != 0 || count != 0 || used != 0 || objects.puts != 0 {
					t.Fatal("failed inventory left quota, rows or Blob calls")
				}
			} else {
				var version string
				if err := db.Pool.QueryRow(ctx, `SELECT version_id FROM dbo.property_photo_objects WHERE photo_id=CONVERT(uniqueidentifier,@p1)`, id).Scan(&version); err != nil || version != "saved-version" || count != 1 || used != len(photo.Data) || objects.puts != 1 {
					t.Fatal("publication failure lost resumable version/quota")
				}
			}
			if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_photo_phase`); err != nil {
				t.Fatal(err)
			}
			recovered, err := s.AddPropertyPhoto(ctx, shop, property, key, "Original", "general", "Owner", photo)
			if err != nil || (id != "" && id != recovered) || len(objects.objects) != 1 {
				t.Fatalf("phase retry: %s %v", recovered, err)
			}
			listed, count, used, err = s.ListPropertyPhotos(ctx, shop, property)
			if err != nil || len(listed) != 1 || count != 1 || used != len(photo.Data) {
				t.Fatal("retry duplicated or lost photo")
			}
		})
	}
}

func TestSQLServerPhotoRetirementHandlesMultirowCascadesAndRollback(t *testing.T) {
	for _, parent := range []string{"properties", "customers", "shops"} {
		t.Run(parent, func(t *testing.T) {
			db := sqlServerTestDB(t)
			ctx := context.Background()
			objects := &fakePhotoObjects{}
			s := &Store{db: db, photoObjects: objects}
			shop := createTestShop(t, ctx, db, "photo-cascade")
			customer := createTestCustomer(t, ctx, db, shop, "Customer")
			property := createTestProperty(t, ctx, db, shop, customer, "Address")
			photo, err := normalizePhoto(photoFixture(t))
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for i := 0; i < 2; i++ {
				id, err := s.AddPropertyPhoto(ctx, shop, property, fmt.Sprintf("%032x", i), "Caption", "general", "Owner", photo)
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			parentID := map[string]string{"properties": property, "customers": customer, "shops": shop}[parent]
			// The table names above are a fixed, local test set.
			query := `DELETE FROM dbo.` + parent + ` WHERE id=CONVERT(uniqueidentifier,@p1)`
			tx, err := db.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(ctx, query, parentID); err != nil {
				tx.Rollback(ctx)
				t.Fatal(err)
			}
			if err := tx.Rollback(ctx); err != nil {
				t.Fatal(err)
			}
			var active, retired int
			if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.property_photos),(SELECT COUNT(*) FROM dbo.property_photo_objects WHERE retired_at IS NOT NULL)`).Scan(&active, &retired); err != nil || active != 2 || retired != 0 {
				t.Fatalf("rollback: active=%d retired=%d err=%v", active, retired, err)
			}
			if _, err := s.DeletePropertyPhoto(ctx, shop, ids[0]); err != nil {
				t.Fatal(err)
			}
			var first time.Time
			if err := db.Pool.QueryRow(ctx, `SELECT retired_at FROM dbo.property_photo_objects WHERE photo_id=CONVERT(uniqueidentifier,@p1)`, ids[0]).Scan(&first); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DeletePropertyPhoto(ctx, shop, ids[0]); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, query, parentID); err != nil {
				t.Fatal(err)
			}
			if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.property_photos),(SELECT COUNT(*) FROM dbo.property_photo_objects WHERE retired_at IS NOT NULL AND version_id=N'saved-version')`).Scan(&active, &retired); err != nil || active != 0 || retired != 2 {
				t.Fatalf("cascade inventory: active=%d retired=%d err=%v", active, retired, err)
			}
			var preserved time.Time
			if err := db.Pool.QueryRow(ctx, `SELECT retired_at FROM dbo.property_photo_objects WHERE photo_id=CONVERT(uniqueidentifier,@p1)`, ids[0]).Scan(&preserved); err != nil || !preserved.Equal(first) {
				t.Fatal("repeat deletion changed retirement time")
			}
		})
	}
}

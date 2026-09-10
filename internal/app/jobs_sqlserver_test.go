package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/jobs"
)

func TestSQLServerStandaloneJobsPreserveExistingBehavior(t *testing.T) {
	testStandaloneJobsPreserveIdentityHistoryAndIsolation(t, sqlServerTestDB(t))
}

func TestSQLServerJobEventFailureRollsBackEveryMutation(t *testing.T) {
	for _, action := range []string{"create", "edit", "completed"} {
		t.Run(action, func(t *testing.T) {
			db := sqlServerTestDB(t)
			ctx := context.Background()
			s := (&Store{db: db}).jobsService()
			shop := createTestShop(t, ctx, db, "job-event-failure")
			customer := createTestCustomer(t, ctx, db, shop, "Customer")
			in := jobs.Input{CustomerID: customer, Kind: "job", Title: "Original"}
			id, err := s.Create(ctx, shop, strings.Repeat("a", 32), "Owner", in)
			if err != nil {
				t.Fatal(err)
			}
			mutate := func() error {
				switch action {
				case "create":
					_, err := s.Create(ctx, shop, strings.Repeat("b", 32), "Owner", in)
					return err
				case "edit":
					edit := in
					edit.Title = "Changed"
					return s.Edit(ctx, shop, id, "Owner", 1, edit)
				default:
					return s.SetOutcome(ctx, shop, id, "Owner", "completed", 1)
				}
			}
			if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_test_job_event ON dbo.job_events AFTER INSERT AS BEGIN THROW 51000,'synthetic job history failure',1; END`); err != nil {
				t.Fatal(err)
			}
			if err := mutate(); err == nil {
				t.Fatal("history failure reported saved")
			}
			record, err := s.Get(ctx, shop, id)
			if err != nil || record.Revision != 1 || record.Title != "Original" || record.Status != "planned" || record.CompletedAt != nil {
				t.Fatal("failed event left partial job")
			}
			history, err := s.History(ctx, shop, id)
			if err != nil || len(history) != 1 {
				t.Fatal("failed event changed history")
			}
			all, err := s.List(ctx, shop, "all")
			if err != nil || len(all) != 1 {
				t.Fatal("failed creation retained job")
			}
			if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_test_job_event`); err != nil {
				t.Fatal(err)
			}
			if err := mutate(); err != nil {
				t.Fatal("retry failed", err)
			}
		})
	}
}

func TestSQLServerJobSnapshotsAndOriginalRetrySurvivePropertyRemoval(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := (&Store{db: db}).jobsService()
	shop := createTestShop(t, ctx, db, "job-json")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	property := createTestProperty(t, ctx, db, shop, customer, "Address")
	in := jobs.Input{CustomerID: customer, PropertyID: property, Kind: "survey", Title: strings.Repeat("🔧", 40), Details: "Quoted \"work\"\nPath \\site <script> & " + strings.Repeat("🔧", 900), Date: "0001-01-01"}
	key := strings.Repeat("c", 32)
	id, err := s.Create(ctx, shop, key, "Owner 🔧", in)
	if err != nil {
		t.Fatal(err)
	}
	history, err := s.History(ctx, shop, id)
	if err != nil || len(history) != 1 || history[0].Snapshot.Title != in.Title || history[0].Snapshot.Details != in.Details || history[0].Snapshot.Date != in.Date || history[0].Snapshot.PropertyID != property {
		t.Fatal("JSON snapshot changed text/date")
	}
	edit := in
	edit.PropertyID = ""
	edit.Date = "9999-12-31"
	// Escaping doubles this text beyond SQL's 4,000 UTF-16-unit boundary.
	edit.Details = strings.Repeat(`"`, 4000)
	if err := s.Edit(ctx, shop, id, "Owner", 1, edit); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.properties WHERE id=CONVERT(uniqueidentifier,@p1)`, property); err != nil {
		t.Fatal(err)
	}
	if replay, err := s.Create(ctx, shop, key, "Owner 🔧", in); err != nil || replay != id {
		t.Fatalf("original retry after property removal: %s %v", replay, err)
	}
	history, err = s.History(ctx, shop, id)
	if err != nil || len(history) != 2 || history[0].Snapshot.PropertyID != "" || history[0].Snapshot.Date != "9999-12-31" || history[0].Snapshot.Details != edit.Details {
		t.Fatal("retry changed events or blank property")
	}
	edit.Date = ""
	if err := s.Edit(ctx, shop, id, "Owner", 2, edit); err != nil {
		t.Fatal(err)
	}
	record, err := s.Get(ctx, shop, id)
	if err != nil || record.Date != "" || record.PropertyID != "" || record.Address != "" {
		t.Fatal("optional job fields failed to clear")
	}
	for _, bad := range []string{"bad\x00text", "bad\xfftext"} {
		invalid := in
		invalid.Title = bad
		if _, err := s.Create(ctx, shop, strings.Repeat("d", 32), "Owner", invalid); !errors.Is(err, jobs.ErrInput) {
			t.Fatal("invalid title accepted")
		}
		if _, err := s.Create(ctx, shop, strings.Repeat("d", 32), bad, in); !errors.Is(err, jobs.ErrInput) {
			t.Fatal("invalid author fingerprint accepted")
		}
	}
}

func TestSQLServerJobCompetingChangesAndOverflowStayAtomic(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := (&Store{db: db}).jobsService()
	shop := createTestShop(t, ctx, db, "job-race")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	in := jobs.Input{CustomerID: customer, Kind: "job", Title: "Original"}
	for i := 0; i < 3; i++ {
		id, err := s.Create(ctx, shop, fmt.Sprintf("%032x", i), "Owner", in)
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		done := make(chan error, 2)
		go func() { <-start; edit := in; edit.Title = "Edited"; done <- s.Edit(ctx, shop, id, "Editor", 1, edit) }()
		go func() { <-start; done <- s.SetOutcome(ctx, shop, id, "Completer", "completed", 1) }()
		close(start)
		wins, conflicts := 0, 0
		for j := 0; j < 2; j++ {
			switch err := <-done; {
			case err == nil:
				wins++
			case errors.Is(err, jobs.ErrConflict):
				conflicts++
			default:
				t.Fatal(err)
			}
		}
		history, err := s.History(ctx, shop, id)
		if err != nil || wins != 1 || conflicts != 1 || len(history) != 2 {
			t.Fatal("competing change duplicated or lost event")
		}
		record, err := s.Get(ctx, shop, id)
		if err != nil || record.Revision != 2 || history[0].Snapshot.Status != record.Status || history[0].Snapshot.Title != record.Title {
			t.Fatal("event does not match winning job")
		}
	}
	id, err := s.Create(ctx, shop, strings.Repeat("f", 32), "Owner", in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.jobs SET revision=2147483647 WHERE id=CONVERT(uniqueidentifier,@p1)`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOutcome(ctx, shop, id, "Owner", "completed", 2147483647); err == nil {
		t.Fatal("revision overflow succeeded")
	}
	record, err := s.Get(ctx, shop, id)
	if err != nil || record.Revision != 2147483647 || record.Status != "planned" || record.CompletedAt != nil {
		t.Fatal("overflow left changed job")
	}
	history, err := s.History(ctx, shop, id)
	if err != nil || len(history) != 1 {
		t.Fatal("overflow changed history")
	}
}

func TestSQLServerJobListAndHistoryWindowsRetainOrdering(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := (&Store{db: db}).jobsService()
	shop := createTestShop(t, ctx, db, "job-order")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	for i := 0; i < 101; i++ {
		uuid := fmt.Sprintf("%08x-0000-0000-0000-%012x", i, 100-i)
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.jobs(id,shop_id,customer_id,submission_key,creation_fingerprint,kind,title,scheduled_on,created_at) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),@p4,REPLICATE(N'a',64),N'job',@p5,CASE WHEN @p6=0 THEN NULL ELSE CONVERT(date,'2026-01-01') END,'2026-01-01')`, uuid, shop, customer, fmt.Sprintf("%032x", i), fmt.Sprint(i), i); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.List(ctx, shop, "all")
	if err != nil || len(list) != 100 || list[0].Title != "1" || list[99].Title != "100" {
		t.Fatalf("NULLS LAST/UUID window: %d %v", len(list), err)
	}
	id := list[0].ID
	for i := 1; i <= 101; i++ {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.job_events(shop_id,job_id,revision,action,snapshot,recorded_by,recorded_at) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,N'edited',N'{}',N'Owner',@p4)`, shop, id, i, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}
	history, err := s.History(ctx, shop, id)
	if err != nil || len(history) != 100 || history[0].Revision != 101 || history[99].Revision != 2 {
		t.Fatal("history window changed")
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.job_events WHERE job_id=CONVERT(uniqueidentifier,@p1)`, id).Scan(&count); err != nil || count != 101 {
		t.Fatal("older job history discarded")
	}
}

func TestSQLServerJobDeletionProtectionPreservesDependentRecords(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	store := &Store{db: db, photoObjects: &fakePhotoObjects{}}
	service := store.jobsService()
	shop := createTestShop(t, ctx, db, "job-delete")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	property := createTestProperty(t, ctx, db, shop, customer, "Address")
	if err := store.CreateEquipment(ctx, shop, property, "Unit", "", "", ""); err != nil {
		t.Fatal(err)
	}
	equipment, err := store.ListEquipment(ctx, shop, property)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddAssetNote(ctx, shop, "equipment", equipment[0].ID, "Owner", internalNoteInput{Key: strings.Repeat("a", 32), Body: "Retain history", Date: "2026-01-01", Zone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	photo, err := normalizePhoto(photoFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	photoID, err := store.AddPropertyPhoto(ctx, shop, property, strings.Repeat("b", 32), "Photo", "general", "Owner", photo)
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := service.Create(ctx, shop, strings.Repeat("c", 32), "Owner", jobs.Input{CustomerID: customer, PropertyID: property, Title: "Keep job", Kind: "job"})
	if err != nil {
		t.Fatal(err)
	}
	for _, parent := range []struct{ table, id string }{{"properties", property}, {"customers", customer}} {
		if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.`+parent.table+` WHERE id=CONVERT(uniqueidentifier,@p1)`, parent.id); err == nil {
			t.Fatal("job did not protect parent")
		}
		if _, err := store.GetPropertyPhoto(ctx, shop, photoID); err != nil {
			t.Fatal("failed parent deletion lost photo")
		}
		notes, err := store.ListAssetNotes(ctx, shop, "equipment", equipment[0].ID)
		if err != nil || len(notes) != 1 {
			t.Fatal("failed parent deletion lost notes")
		}
		history, err := service.History(ctx, shop, jobID)
		if err != nil || len(history) != 1 {
			t.Fatal("failed parent deletion lost events")
		}
		var retired int
		if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.property_photo_objects WHERE retired_at IS NOT NULL`).Scan(&retired); err != nil || retired != 0 {
			t.Fatal("failed parent deletion retired photo")
		}
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	var remaining, retired int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.jobs)+(SELECT COUNT(*) FROM dbo.job_events)+(SELECT COUNT(*) FROM dbo.asset_notes)+(SELECT COUNT(*) FROM dbo.property_photos),(SELECT COUNT(*) FROM dbo.property_photo_objects WHERE retired_at IS NOT NULL)`).Scan(&remaining, &retired); err != nil || remaining != 0 || retired != 1 {
		t.Fatalf("whole shop cleanup: %d %d %v", remaining, retired, err)
	}
}

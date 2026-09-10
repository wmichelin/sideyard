package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSQLServerPropertyRecordsPreserveExistingBehavior(t *testing.T) {
	t.Run("write availability", func(t *testing.T) { testPropertyRecordWritesReportUnavailable(t, sqlServerTestDB(t)) })
	t.Run("measurements", func(t *testing.T) { testPropertyRecordsKeepHistoryAndTenantBoundaries(t, sqlServerTestDB(t)) })
	t.Run("notes", func(t *testing.T) { testPropertyNotesPreserveAuthorshipAndDeduplicateRetries(t, sqlServerTestDB(t)) })
}

func TestSQLServerPropertyDetailsCompetingSavesAndOverflow(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "property-details")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	id := createTestProperty(t, ctx, db, shop, customer, "Address")
	for _, revision := range []int{0, 1} {
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, notes := range []string{"First", "Second"} {
			go func(notes string) { <-start; results <- s.SavePropertyDetails(ctx, shop, id, notes, revision) }(notes)
		}
		close(start)
		var wins, conflicts int
		for i := 0; i < 2; i++ {
			switch err := <-results; {
			case err == nil:
				wins++
			case errors.Is(err, errPropertyConflict):
				conflicts++
			default:
				t.Fatal(err)
			}
		}
		_, _, got, _, err := s.GetPropertyRecord(ctx, shop, id)
		if err != nil || wins != 1 || conflicts != 1 || got != revision+1 {
			t.Fatalf("competing details: wins=%d conflicts=%d revision=%d err=%v", wins, conflicts, got, err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.property_details SET revision=2147483647,access_notes=N'Keep me' WHERE property_id=CONVERT(uniqueidentifier,@p1)`, id); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePropertyDetails(ctx, shop, id, "Must roll back", 2147483647); err == nil {
		t.Fatal("revision overflow succeeded")
	}
	_, notes, revision, _, err := s.GetPropertyRecord(ctx, shop, id)
	if err != nil || notes != "Keep me" || revision != 2147483647 {
		t.Fatal("overflow left partial property details")
	}
}

func TestSQLServerPropertyNotesRejectChangedRetriesAndKeepExactValues(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "note-retries")
	other := createTestShop(t, ctx, db, "note-other")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	foreign := createTestCustomer(t, ctx, db, other, "Other")
	id := createTestProperty(t, ctx, db, shop, customer, "First")
	second := createTestProperty(t, ctx, db, shop, customer, "Second")
	foreignProperty := createTestProperty(t, ctx, db, other, foreign, "Other")
	n := propertyNote{SubmissionKey: strings.Repeat("a", 32), Body: "Survey 🔧\nKeep details", RecordedBy: "Owner@example.test ", OccurredOn: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := s.AddPropertyNote(ctx, shop, id, n); err != nil {
		t.Fatal(err)
	}
	before, err := s.ListPropertyNotes(ctx, shop, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		edit     func(*propertyNote)
		property string
	}{
		{"body case", func(n *propertyNote) { n.Body = strings.ToLower(n.Body) }, id},
		{"author space", func(n *propertyNote) { n.RecordedBy = strings.TrimSpace(n.RecordedBy) }, id},
		{"author case", func(n *propertyNote) { n.RecordedBy = strings.ToLower(n.RecordedBy) }, id},
		{"date", func(n *propertyNote) { n.OccurredOn = n.OccurredOn.AddDate(0, 0, 1) }, id},
		{"property", func(*propertyNote) {}, second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := n
			tc.edit(&changed)
			if err := s.AddPropertyNote(ctx, shop, tc.property, changed); !errors.Is(err, errPropertyConflict) {
				t.Fatalf("changed retry: %v", err)
			}
		})
	}
	if err := s.AddPropertyNote(ctx, other, foreignProperty, n); err != nil {
		t.Fatal("same key in another shop collided")
	}
	if err := s.AddPropertyNote(ctx, shop, id, n); err != nil {
		t.Fatal(err)
	}
	after, err := s.ListPropertyNotes(ctx, shop, id)
	if err != nil || len(after) != 1 || after[0].Body != before[0].Body || after[0].RecordedBy != before[0].RecordedBy || !after[0].RecordedAt.Equal(before[0].RecordedAt) {
		t.Fatal("retry changed original history")
	}
}

func TestSQLServerPropertyHistoryKeepsDecimalLimitsAndUUIDOrder(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "history-order")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	id := createTestProperty(t, ctx, db, shop, customer, "Address")
	today := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, value := range []string{"0.001", "99999999999.999"} {
		if err := s.AddPropertyMeasurement(ctx, shop, id, propertyMeasurement{Kind: "turf", Zone: "Front", Value: value, Unit: "sq_ft", Source: "Survey 🔧", MeasuredOn: today, RecordedBy: "Owner"}); err != nil {
			t.Fatal(err)
		}
	}
	_, _, _, measurements, err := s.GetPropertyRecord(ctx, shop, id)
	if err != nil || len(measurements) != 2 || measurements[0].Value != "99999999999.999" || measurements[1].Value != "0.001" {
		t.Fatalf("decimal precision: %+v %v", measurements, err)
	}
	// The first UUID group ascends while the final group descends. Native GUID
	// sorting would reverse the retained page when all timestamps tie.
	for i := 0; i < 101; i++ {
		uuid := fmt.Sprintf("%08x-0000-0000-0000-%012x", i, 100-i)
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.property_notes(id,shop_id,property_id,submission_key,body,occurred_on,recorded_by,recorded_at) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),@p4,@p5,'2026-01-01','Owner','2026-01-01')`, uuid, shop, id, fmt.Sprintf("%032x", i), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	notes, err := s.ListPropertyNotes(ctx, shop, id)
	if err != nil || len(notes) != 100 || notes[0].Body != "100" || notes[99].Body != "1" {
		t.Fatalf("history page ordering: count=%d err=%v", len(notes), err)
	}
	var total int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.property_notes WHERE property_id=CONVERT(uniqueidentifier,@p1)`, id).Scan(&total); err != nil || total != 101 {
		t.Fatal("history was discarded")
	}
}

func TestSQLServerPropertyNoteInsertFailureRollsBackAndCanRetry(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "note-rollback")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	id := createTestProperty(t, ctx, db, shop, customer, "Address")
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.reject_test_note ON dbo.property_notes AFTER INSERT AS BEGIN THROW 51000,'synthetic note failure',1; END`); err != nil {
		t.Fatal(err)
	}
	n := propertyNote{SubmissionKey: strings.Repeat("f", 32), Body: "Site visit", RecordedBy: "Owner", OccurredOn: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := s.AddPropertyNote(ctx, shop, id, n); err == nil || errors.Is(err, errPropertyConflict) {
		t.Fatalf("insert failure acknowledged as duplicate: %v", err)
	}
	notes, err := s.ListPropertyNotes(ctx, shop, id)
	if err != nil || len(notes) != 0 {
		t.Fatal("failed note left a row")
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.reject_test_note`); err != nil {
		t.Fatal(err)
	}
	retry, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.AddPropertyNote(retry, shop, id, n); err != nil {
		t.Fatal(err)
	}
	notes, err = s.ListPropertyNotes(ctx, shop, id)
	if err != nil || len(notes) != 1 || notes[0].Body != n.Body {
		t.Fatal("retry did not recover failed note")
	}
}

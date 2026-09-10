package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPropertyNotesPreserveAuthorshipAndDeduplicateRetries(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testPropertyNotesPreserveAuthorshipAndDeduplicateRetries(t, db)
}

func testPropertyNotesPreserveAuthorshipAndDeduplicateRetries(t *testing.T, db *DB) {
	ctx := context.Background()
	s := &Store{db: db}
	stamp := time.Now().Format("150405.000000000")
	shop := createTestShop(t, ctx, db, "notes-"+stamp)
	other := createTestShop(t, ctx, db, "notes-other-"+stamp)
	customer := createTestCustomer(t, ctx, db, shop, "Note customer")
	property := createTestProperty(t, ctx, db, shop, customer, "1 Note St")
	n := propertyNote{SubmissionKey: strings.Repeat("a", 32), Body: "Initial survey\nCheck side gate", OccurredOn: time.Now().UTC().AddDate(0, 0, -1), RecordedBy: "owner@example.test"}
	if err := s.AddPropertyNote(ctx, other, property, n); !errors.Is(err, errPropertyConflict) {
		t.Fatalf("foreign write: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.AddPropertyNote(ctx, shop, property, n) }()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	notes, err := s.ListPropertyNotes(ctx, shop, property)
	if err != nil || len(notes) != 1 || notes[0].Body != n.Body || notes[0].RecordedBy != n.RecordedBy || notes[0].RecordedAt.IsZero() {
		t.Fatalf("journal %+v %v", notes, err)
	}
	originalTime := notes[0].RecordedAt
	n.Body = "Changed content with reused submission"
	if err := s.AddPropertyNote(ctx, shop, property, n); !errors.Is(err, errPropertyConflict) {
		t.Fatalf("reused key replaced note: %v", err)
	}
	n.SubmissionKey = strings.Repeat("b", 32)
	n.Body = "Correction: use the front gate"
	if err := s.AddPropertyNote(ctx, shop, property, n); err != nil {
		t.Fatal(err)
	}
	notes, err = s.ListPropertyNotes(ctx, shop, property)
	if err != nil || len(notes) != 2 || notes[0].Body != n.Body || !notes[1].RecordedAt.Equal(originalTime) {
		t.Fatalf("history changed: %+v %v", notes, err)
	}
	if got, err := s.ListPropertyNotes(ctx, other, property); err != nil || len(got) != 0 {
		t.Fatal("foreign note read")
	}
	for _, body := range []string{"", strings.Repeat("x", 2001), "bad\x00note"} {
		n.Body = body
		if err := s.AddPropertyNote(ctx, shop, property, n); !errors.Is(err, errPropertyNoteInput) {
			t.Fatalf("invalid body accepted: %v", err)
		}
	}
	n.Body = "Future note"
	n.OccurredOn = time.Now().UTC().AddDate(0, 0, 1)
	if err := s.AddPropertyNote(ctx, shop, property, n); !errors.Is(err, errPropertyNoteInput) {
		t.Fatal("future event date accepted")
	}
}

package app

import (
	"context"
	"errors"
	"html/template"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestAssetNotesKeepIndependentAuthoredHistory(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	s := &Store{db: db}
	stamp := time.Now().Format("150405.000000000")
	shop := createTestShop(t, ctx, db, "asset-notes-"+stamp)
	other := createTestShop(t, ctx, db, "asset-notes-other-"+stamp)
	customer := createTestCustomer(t, ctx, db, shop, "Asset customer")
	property := createTestProperty(t, ctx, db, shop, customer, "12 Asset St")
	for _, name := range []string{"Blue sedan", "Work truck"} {
		if err := s.SaveCustomerVehicle(ctx, shop, customer, customerVehicle{Name: name, Notes: "Standing detail"}); err != nil {
			t.Fatal(err)
		}
	}
	vehicles, err := s.ListCustomerVehicles(ctx, shop, customer)
	if err != nil || len(vehicles) != 2 {
		t.Fatal("vehicle fixtures")
	}
	if err := s.CreateEquipment(ctx, shop, property, "Heat pump", "", "", ""); err != nil {
		t.Fatal(err)
	}
	equipment, err := s.ListEquipment(ctx, shop, property)
	if err != nil || len(equipment) != 1 {
		t.Fatal("equipment fixture")
	}
	input := internalNoteInput{Key: strings.Repeat("a", 32), Body: "Inspected seats\nNo damage", Date: "2025-11-02", Time: "01:30", Zone: "America/New_York", Offset: "-04:00"}
	if _, err := s.GetAssetHistoryTarget(ctx, other, "vehicle", vehicles[0].ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign asset lookup: %v", err)
	}
	if err := s.AddAssetNote(ctx, other, "vehicle", vehicles[0].ID, "foreign", input); !errors.Is(err, errAssetNoteConflict) {
		t.Fatalf("foreign note insert: %v", err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- s.AddAssetNote(ctx, shop, "vehicle", vehicles[0].ID, "owner@example.test", input) }()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	notes, err := s.ListAssetNotes(ctx, shop, "vehicle", vehicles[0].ID)
	if err != nil || len(notes) != 1 || notes[0].RecordedBy != "owner@example.test" || notes[0].Event.At == nil || notes[0].Event.At.Format(time.RFC3339) != "2025-11-02T05:30:00Z" {
		t.Fatalf("authored note: %+v %v", notes, err)
	}
	originalTime := notes[0].RecordedAt
	for _, change := range []func(*internalNoteInput){func(n *internalNoteInput) { n.Body = "Changed" }, func(n *internalNoteInput) { n.Offset = "-05:00" }, func(n *internalNoteInput) { n.Time = "" }} {
		n := input
		change(&n)
		if err := s.AddAssetNote(ctx, shop, "vehicle", vehicles[0].ID, "owner@example.test", n); !errors.Is(err, errAssetNoteConflict) {
			t.Fatalf("changed retry overwritten: %v", err)
		}
	}
	if err := s.AddAssetNote(ctx, shop, "vehicle", vehicles[1].ID, "owner@example.test", input); !errors.Is(err, errAssetNoteConflict) {
		t.Fatal("reused form moved to other vehicle")
	}
	if err := s.AddAssetNote(ctx, shop, "equipment", equipment[0].ID, "owner@example.test", input); !errors.Is(err, errAssetNoteConflict) {
		t.Fatal("reused form moved to equipment")
	}
	input.Key = strings.Repeat("b", 32)
	input.Body = "Correction: small mark on rear seat"
	input.Time = ""
	input.Offset = ""
	if err := s.AddAssetNote(ctx, shop, "vehicle", vehicles[0].ID, "second-owner@example.test", input); err != nil {
		t.Fatal(err)
	}
	notes, err = s.ListAssetNotes(ctx, shop, "vehicle", vehicles[0].ID)
	if err != nil || len(notes) != 2 || notes[0].Event.At != nil || notes[0].RecordedBy != "second-owner@example.test" || notes[1].RecordedBy != "owner@example.test" || !notes[1].RecordedAt.Equal(originalTime) {
		t.Fatalf("correction/history changed: %+v %v", notes, err)
	}
	input.Key = strings.Repeat("c", 32)
	input.Body = "Truck bed inspected"
	if err := s.AddAssetNote(ctx, shop, "vehicle", vehicles[1].ID, "owner@example.test", input); err != nil {
		t.Fatal(err)
	}
	input.Key = strings.Repeat("d", 32)
	input.Body = "Filter replaced"
	if err := s.AddAssetNote(ctx, shop, "equipment", equipment[0].ID, "owner@example.test", input); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ kind, id, body string }{{"vehicle", vehicles[1].ID, "Truck bed inspected"}, {"equipment", equipment[0].ID, "Filter replaced"}} {
		notes, err := s.ListAssetNotes(ctx, shop, target.kind, target.id)
		if err != nil || len(notes) != 1 || notes[0].Body != target.body {
			t.Fatal("asset histories mixed")
		}
		if foreign, err := s.ListAssetNotes(ctx, other, target.kind, target.id); err != nil || len(foreign) != 0 {
			t.Fatal("foreign asset history readable")
		}
	}
	got, err := s.ListCustomerVehicles(ctx, shop, customer)
	if err != nil || got[0].Notes != "Standing detail" {
		t.Fatal("history rewrote standing vehicle details")
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO asset_notes(shop_id,vehicle_id,submission_key,body,occurred_on,event_timezone,recorded_by) VALUES ($1,$2,repeat('f',32),'Foreign','2025-01-01','UTC','owner')`, other, vehicles[0].ID); err == nil {
		t.Fatal("database permitted foreign vehicle")
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO asset_notes(shop_id,vehicle_id,equipment_id,submission_key,body,occurred_on,event_timezone,recorded_by) VALUES ($1,$2,$3,repeat('f',32),'Two parents','2025-01-01','UTC','owner')`, shop, vehicles[0].ID, equipment[0].ID); err == nil {
		t.Fatal("database permitted two parents")
	}

	server := &Server{store: s, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	request := func(method, tenant, role, kind, id string, form url.Values) *httptest.ResponseRecorder {
		path := "/vehicles/" + id + "/history"
		if kind == "equipment" {
			path = "/equipment/" + id + "/history"
		}
		r := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", id)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: tenant, Role: role, Email: "owner@example.test"}))
		w := httptest.NewRecorder()
		server.handleAssetHistory(w, r, kind)
		return w
	}
	for _, target := range []struct{ kind, id string }{{"vehicle", vehicles[0].ID}, {"equipment", equipment[0].ID}} {
		for _, method := range []string{"GET", "POST"} {
			if w := request(method, shop, "staff", target.kind, target.id, nil); w.Code != 403 {
				t.Fatal("staff accessed asset notes")
			}
			if w := request(method, other, "owner", target.kind, target.id, nil); w.Code != 404 {
				t.Fatal("foreign asset notes handler")
			}
		}
	}
	form := url.Values{"note_key": {strings.Repeat("e", 32)}, "note_body": {"Keep <script>typed note</script>"}, "occurred_on": {"2025-11-02"}, "event_time": {"01:30"}, "event_timezone": {"America/New_York"}}
	w := request("POST", shop, "owner", "vehicle", vehicles[0].ID, form)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "Keep &lt;script&gt;typed note&lt;/script&gt;") || !strings.Contains(w.Body.String(), "-04:00") || !strings.Contains(w.Body.String(), "-05:00") || strings.Contains(w.Body.String(), `data-history-defaults="true"`) {
		t.Fatalf("ambiguous form: %d %s", w.Code, w.Body.String())
	}
	form.Set("event_offset", "-05:00")
	w = request("POST", shop, "owner", "vehicle", vehicles[0].ID, form)
	if w.Code != 303 {
		t.Fatalf("disambiguated save: %d %s", w.Code, w.Body.String())
	}
	w = request("GET", shop, "owner", "vehicle", vehicles[0].ID, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Date only") || !strings.Contains(w.Body.String(), "UTC-05:00") || strings.Contains(w.Body.String(), "<script>typed note") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("history display missing precision/privacy")
	}
	for _, body := range []string{" ", strings.Repeat("x", 2001), "bad\x00note"} {
		input.Body = body
		if err := s.AddAssetNote(ctx, shop, "vehicle", vehicles[0].ID, "owner", input); !errors.Is(err, errAssetNoteInput) {
			t.Fatal("invalid note accepted")
		}
	}
}

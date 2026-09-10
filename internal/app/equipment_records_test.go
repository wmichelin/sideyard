package app

import (
	"context"
	"errors"
	"html/template"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func TestEquipmentRecordValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*equipmentRecord)
		valid bool
	}{
		{"unknown dates", func(*equipmentRecord) {}, true},
		{"known dates", func(e *equipmentRecord) { e.InstalledOn = "2024-02-29"; e.WarrantyUntil = "2034-02-28" }, true},
		{"empty name", func(e *equipmentRecord) { e.Kind = " " }, false},
		{"invalid leap day", func(e *equipmentRecord) { e.InstalledOn = "2025-02-29" }, false},
		{"invalid warranty", func(e *equipmentRecord) { e.WarrantyUntil = "unknown" }, false},
		{"year zero", func(e *equipmentRecord) { e.InstalledOn = "0000-01-01" }, false},
		{"warranty before installation", func(e *equipmentRecord) { e.InstalledOn = "2024-01-01"; e.WarrantyUntil = "2023-01-01" }, false},
		{"long location", func(e *equipmentRecord) { e.Location = strings.Repeat("x", 121) }, false},
		{"long notes", func(e *equipmentRecord) { e.Notes = strings.Repeat("x", 2001) }, false},
		{"null byte", func(e *equipmentRecord) { e.SerialNumber = "ref\x00" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := equipmentRecord{Equipment: Equipment{Kind: "Heat pump"}}
			tc.edit(&e)
			if err := validateEquipmentRecord(e); (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestEquipmentRecordsPreserveIdentityAndRejectConflictingEdits(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testEquipmentRecordsPreserveIdentityAndRejectConflictingEdits(t, db)
}

func testEquipmentRecordsPreserveIdentityAndRejectConflictingEdits(t *testing.T, db *DB) {
	ctx := context.Background()
	s := &Store{db: db}
	stamp := time.Now().Format("150405.000000000")
	shop := createTestShop(t, ctx, db, "equipment-"+stamp)
	other := createTestShop(t, ctx, db, "equipment-other-"+stamp)
	customer := createTestCustomer(t, ctx, db, shop, "Equipment customer")
	property := createTestProperty(t, ctx, db, shop, customer, "12 First St")
	secondProperty := createTestProperty(t, ctx, db, shop, customer, "14 First St")
	for _, name := range []string{"Heat pump", "Water heater"} {
		if err := s.CreateEquipment(ctx, shop, property, name, "Existing make", "Existing model", "Existing serial"); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ListEquipment(ctx, shop, property)
	if err != nil || len(items) != 2 {
		t.Fatalf("equipment: %+v %v", items, err)
	}
	// Installation dates predate this feature (including demo-seeded records).
	if _, err := db.Pool.Exec(ctx, database.Statement(db.Pool.Dialect(), `UPDATE equipment SET installed_on='2020-02-29' WHERE id=$1`, `UPDATE dbo.equipment SET installed_on='2020-02-29' WHERE id=CONVERT(uniqueidentifier,@p1)`), items[0].ID); err != nil {
		t.Fatal(err)
	}
	e, err := s.GetEquipmentRecord(ctx, shop, items[0].ID)
	if err != nil || e.InstalledOn != "2020-02-29" || e.ID != items[0].ID || e.Revision != 1 {
		t.Fatalf("legacy record: %+v %v", e, err)
	}
	e.Location, e.WarrantyUntil, e.Notes = "Attic", "2030-02-28", "Private filter size <script>unsafe</script>"
	if _, err := s.GetEquipmentRecord(ctx, other, e.ID); !errors.Is(err, database.ErrNoRows) {
		t.Fatalf("foreign read: %v", err)
	}
	if err := s.SaveEquipmentRecord(ctx, other, e); !errors.Is(err, errEquipmentConflict) {
		t.Fatalf("foreign write: %v", err)
	}
	wrong := e
	wrong.PropertyID = secondProperty
	if err := s.SaveEquipmentRecord(ctx, shop, wrong); !errors.Is(err, errEquipmentConflict) {
		t.Fatalf("wrong property: %v", err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- s.SaveEquipmentRecord(ctx, shop, e) }()
	}
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			wins++
		case errors.Is(err, errEquipmentConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("concurrent saves: %d wins, %d conflicts", wins, conflicts)
	}
	got, err := s.GetEquipmentRecord(ctx, shop, e.ID)
	if err != nil || got.Notes != e.Notes || got.InstalledOn != e.InstalledOn || got.WarrantyUntil != e.WarrantyUntil || got.Revision != 2 || got.PropertyID != property {
		t.Fatalf("saved: %+v %v", got, err)
	}
	untouched, err := s.GetEquipmentRecord(ctx, shop, items[1].ID)
	if err != nil || untouched.Notes != "" || untouched.InstalledOn != "" || untouched.Revision != 1 {
		t.Fatalf("other equipment changed: %+v %v", untouched, err)
	}
	got.WarrantyUntil = ""
	if err := s.SaveEquipmentRecord(ctx, shop, got); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetEquipmentRecord(ctx, shop, e.ID)
	if err != nil || got.WarrantyUntil != "" || got.InstalledOn != "2020-02-29" {
		t.Fatal("optional date clearing changed installation date")
	}
	if _, err := db.Pool.Exec(ctx, database.Statement(db.Pool.Dialect(), `INSERT INTO equipment(shop_id,property_id,kind) VALUES ($1,$2,'Foreign')`, `INSERT INTO dbo.equipment(shop_id,property_id,kind) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),'Foreign')`), other, property); err == nil {
		t.Fatal("database permitted foreign property ownership")
	}

	server := &Server{store: s, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	request := func(method, tenant, role string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/equipment/"+e.ID, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", e.ID)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: tenant, Role: role}))
		w := httptest.NewRecorder()
		server.handleEquipmentRecord(w, r)
		return w
	}
	for _, method := range []string{"GET", "POST"} {
		if w := request(method, shop, "staff", nil); w.Code != 403 {
			t.Fatal("staff accessed private equipment record")
		}
		if w := request(method, other, "owner", nil); w.Code != 404 {
			t.Fatalf("foreign handler: %d", w.Code)
		}
	}
	w := request("GET", shop, "owner", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Private filter size &lt;script&gt;") || strings.Contains(w.Body.String(), "<script>unsafe") {
		t.Fatalf("owner page: %d %s", w.Code, w.Body.String())
	}
	form := url.Values{"kind": {"Edited unit"}, "location": {"Keep my typed location"}, "installed_on": {"invalid"}, "notes": {"Keep my typed notes"}, "revision": {strconv.Itoa(got.Revision)}}
	w = request("POST", shop, "owner", form)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "Keep my typed location") || !strings.Contains(w.Body.String(), "Keep my typed notes") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("invalid form lost input: %d %s", w.Code, w.Body.String())
	}
	form.Set("installed_on", "2020-02-29")
	form.Set("revision", "1")
	w = request("POST", shop, "owner", form)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Keep my typed notes") {
		t.Fatal("stale form did not retain input")
	}
	form.Set("revision", strconv.Itoa(got.Revision))
	w = request("POST", shop, "owner", form)
	if w.Code != 303 {
		t.Fatalf("valid save: %d %s", w.Code, w.Body.String())
	}
	got, err = s.GetEquipmentRecord(ctx, shop, e.ID)
	if err != nil || got.Kind != "Edited unit" || got.Location != "Keep my typed location" {
		t.Fatalf("handler save: %+v %v", got, err)
	}
	// Existing customer summaries never query or render the new private fields.
	for _, role := range []string{"staff", "owner"} {
		r := httptest.NewRequest("GET", "/customers/"+customer, nil)
		w := httptest.NewRecorder()
		server.renderCustomerEdit(w, r, &User{ShopID: shop, Role: role}, customer, customerEditState{Status: 200})
		if w.Code != 200 || strings.Contains(w.Body.String(), got.Notes) || strings.Contains(w.Body.String(), got.Location) {
			t.Fatal("private equipment fields leaked into customer summary")
		}
	}
}

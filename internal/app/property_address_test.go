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

func TestPropertyAddressValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*propertyAddress)
		valid bool
	}{
		{"optional unit", func(*propertyAddress) {}, true},
		{"missing address", func(p *propertyAddress) { p.AddressLine1 = " " }, false},
		{"missing city", func(p *propertyAddress) { p.City = "" }, false},
		{"missing state", func(p *propertyAddress) { p.State = "" }, false},
		{"missing postal code", func(p *propertyAddress) { p.PostalCode = "" }, false},
		{"null byte", func(p *propertyAddress) { p.AddressLine2 = "Unit\x00" }, false},
		{"oversized field", func(p *propertyAddress) { p.City = strings.Repeat("x", 201) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := propertyAddress{Property: Property{AddressLine1: "12 First St", City: "Cary", State: "NC", PostalCode: "27511"}}
			tc.edit(&p)
			if err := validatePropertyAddress(p); (err == nil) != tc.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestPropertyAddressCorrectionsRetainRecordsAndRejectStaleImports(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	s := &Store{db: db}
	stamp := time.Now().Format("150405.000000000")
	shop := createTestShop(t, ctx, db, "address-"+stamp)
	other := createTestShop(t, ctx, db, "address-other-"+stamp)
	customer := createTestCustomer(t, ctx, db, shop, "Address customer")
	id := createTestProperty(t, ctx, db, shop, customer, "12 Fist St")
	if err := s.CreateEquipment(ctx, shop, id, "Panel", "", "", "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePropertyDetails(ctx, shop, id, "Side gate", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPropertyNote(ctx, shop, id, propertyNote{SubmissionKey: strings.Repeat("a", 32), Body: "Existing survey", OccurredOn: time.Now().UTC(), RecordedBy: "owner@example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddPropertyMeasurement(ctx, shop, id, propertyMeasurement{Kind: "turf", Zone: "Lawn", Value: "123.125", Unit: "sq_ft", Source: "Tape", MeasuredOn: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	plan := createTestPlan(t, ctx, db, shop, "Site plan")
	agreementID, err := s.CreateAgreement(ctx, shop, CreateAgreementInput{CustomerID: customer, PropertyID: id, PlanTemplateID: plan, Status: AgreementActive, AnniversaryDate: time.Now().UTC().AddDate(1, 0, 0), VisitsEntitled: 2, PriceCents: 19900})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPropertyAddress(ctx, shop, id)
	if err != nil || p.Revision != 1 {
		t.Fatalf("initial address: %+v %v", p, err)
	}
	p.AddressLine1, p.AddressLine2 = "12 First St", "Unit 2"
	if _, err := s.GetPropertyAddress(ctx, other, id); !errors.Is(err, database.ErrNoRows) {
		t.Fatalf("foreign read: %v", err)
	}
	if err := s.SavePropertyAddress(ctx, other, p); !errors.Is(err, errPropertyAddressConflict) {
		t.Fatalf("foreign update: %v", err)
	}
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- s.SavePropertyAddress(ctx, shop, p) }()
	}
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			wins++
		case errors.Is(err, errPropertyAddressConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("race: %d wins %d conflicts", wins, conflicts)
	}
	p, err = s.GetPropertyAddress(ctx, shop, id)
	if err != nil || p.Revision != 2 || p.CustomerID != customer || p.AddressLine2 != "Unit 2" {
		t.Fatalf("corrected address: %+v %v", p, err)
	}
	_, details, _, measurements, err := s.GetPropertyRecord(ctx, shop, id)
	if err != nil || details != "Side gate" || len(measurements) != 1 || measurements[0].Value != "123.125" {
		t.Fatal("address correction changed property history")
	}
	notes, err := s.ListPropertyNotes(ctx, shop, id)
	if err != nil || len(notes) != 1 || notes[0].Body != "Existing survey" {
		t.Fatal("address correction changed notes")
	}
	equipment, err := s.ListEquipment(ctx, shop, id)
	if err != nil || len(equipment) != 1 || equipment[0].SerialNumber != "123" {
		t.Fatal("address correction changed equipment")
	}
	var retainedProperty, retainedCustomer string
	if err := db.Pool.QueryRow(ctx, `SELECT property_id,customer_id FROM agreements WHERE id=$1`, agreementID).Scan(&retainedProperty, &retainedCustomer); err != nil || retainedProperty != id || retainedCustomer != customer {
		t.Fatal("address correction changed agreement identity")
	}
	// Exercise the existing CSV import upsert, not just this editor's write path.
	if _, err := db.Pool.Exec(ctx, `UPDATE properties SET external_id='address-import' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	imported, err := s.upsertImportProperty(ctx, tx, shop, customer, ImportRow{ExternalID: "address-import", AddressLine1: "12 First Street", City: p.City, State: p.State, PostalCode: p.PostalCode})
	if err != nil || imported != id {
		t.Fatalf("import: %s %v", imported, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePropertyAddress(ctx, shop, p); !errors.Is(err, errPropertyAddressConflict) {
		t.Fatalf("import did not invalidate browser edit: %v", err)
	}
	p, err = s.GetPropertyAddress(ctx, shop, id)
	if err != nil || p.Revision != 3 || p.AddressLine1 != "12 First Street" {
		t.Fatalf("import revision: %+v %v", p, err)
	}

	server := &Server{store: s, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	request := func(method, tenant, role string, form url.Values) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/properties/"+id+"/address", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", id)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: tenant, Role: role}))
		w := httptest.NewRecorder()
		server.handlePropertyAddress(w, r)
		return w
	}
	for _, method := range []string{"GET", "POST"} {
		if w := request(method, shop, "staff", nil); w.Code != 403 {
			t.Fatal("staff edited address")
		}
		if w := request(method, other, "owner", nil); w.Code != 404 {
			t.Fatal("foreign address handler")
		}
	}
	form := url.Values{"address_line1": {"12 First Street"}, "address_line2": {"<script>Unit 3</script>"}, "city": {p.City}, "state": {p.State}, "postal_code": {p.PostalCode}, "revision": {strconv.Itoa(p.Revision)}}
	form.Set("city", "")
	w := request("POST", shop, "owner", form)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "&lt;script&gt;Unit 3&lt;/script&gt;") {
		t.Fatal("invalid address lost/failed to escape input")
	}
	form.Set("city", p.City)
	form.Set("revision", "1")
	if w := request("POST", shop, "owner", form); w.Code != 409 {
		t.Fatal("stale handler edit succeeded")
	}
	form.Set("revision", strconv.Itoa(p.Revision))
	form.Set("address_line2", "")
	if w := request("POST", shop, "owner", form); w.Code != 303 {
		t.Fatalf("valid edit: %d %s", w.Code, w.Body.String())
	}
	p, err = s.GetPropertyAddress(ctx, shop, id)
	if err != nil || p.AddressLine2 != "" || p.Revision != 4 {
		t.Fatalf("clear unit: %+v %v", p, err)
	}
}

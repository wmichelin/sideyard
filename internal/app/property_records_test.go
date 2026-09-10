package app

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func TestPropertyMeasurementValidation(t *testing.T) {
	today := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	valid := propertyMeasurement{Kind: "turf", Zone: "Front yard", Value: "1250.125", Unit: "sq_ft", Source: "Tape measure", MeasuredOn: today, RecordedBy: "owner@example.test"}
	for _, tc := range []struct {
		name  string
		edit  func(*propertyMeasurement)
		valid bool
	}{
		{"area", func(*propertyMeasurement) {}, true},
		{"length", func(m *propertyMeasurement) { m.Kind = "perimeter"; m.Unit = "ft" }, true},
		{"zero", func(m *propertyMeasurement) { m.Value = "0.000" }, false},
		{"negative", func(m *propertyMeasurement) { m.Value = "-10" }, false},
		{"nan", func(m *propertyMeasurement) { m.Value = "NaN" }, false},
		{"rounding", func(m *propertyMeasurement) { m.Value = "1.1234" }, false},
		{"too large", func(m *propertyMeasurement) { m.Value = "100000000000" }, false},
		{"unknown type", func(m *propertyMeasurement) { m.Kind = "fake" }, false},
		{"area length mismatch", func(m *propertyMeasurement) { m.Unit = "ft" }, false},
		{"length area mismatch", func(m *propertyMeasurement) { m.Kind = "perimeter" }, false},
		{"future date", func(m *propertyMeasurement) { m.MeasuredOn = today.AddDate(0, 0, 1) }, false},
		{"missing source", func(m *propertyMeasurement) { m.Source = "" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := valid
			tc.edit(&m)
			if err := validatePropertyMeasurement(m, today); (err == nil) != tc.valid {
				t.Fatalf("validation %v", err)
			}
		})
	}
}

func TestPropertyRecordReadFailureIsUnavailableWithoutLeakingDetails(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{errors.New("synthetic internal database failure"), 503},
		{database.ErrNoRows, 404},
	} {
		s := &Server{store: &Store{db: &DB{Pool: &unavailableAuthPool{err: tc.err}}}}
		r := httptest.NewRequest("GET", "/properties/12345678-1234-1234-1234-123456789012", nil)
		r.SetPathValue("id", "12345678-1234-1234-1234-123456789012")
		r = r.WithContext(contextWithUser(context.Background(), &User{ShopID: "12345678-1234-1234-1234-123456789013", Role: "owner"}))
		w := httptest.NewRecorder()
		s.handlePropertyRecord(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), tc.err.Error()) {
			t.Fatalf("property failure: %d %s", w.Code, w.Body.String())
		}
	}
}

type propertyFailingWritesPool struct {
	database.Pool
	err error
}

func (p propertyFailingWritesPool) QueryRow(ctx context.Context, q string, args ...any) database.Row {
	if strings.HasPrefix(q, "INSERT") || strings.HasPrefix(q, "UPDATE") {
		return unavailableAuthRow{p.err}
	}
	return p.Pool.QueryRow(ctx, q, args...)
}
func (p propertyFailingWritesPool) Exec(context.Context, string, ...any) (database.Result, error) {
	return nil, p.err
}
func (p propertyFailingWritesPool) Begin(context.Context) (database.Tx, error) { return nil, p.err }

func testPropertyRecordWritesReportUnavailable(t *testing.T, db *DB) {
	ctx := context.Background()
	shop := createTestShop(t, ctx, db, "property-write-outage-"+time.Now().Format("150405.000000000"))
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	id := createTestProperty(t, ctx, db, shop, customer, "Address")
	failure := errors.New("synthetic internal write failure")
	s := &Server{store: &Store{db: &DB{Pool: propertyFailingWritesPool{Pool: db.Pool, err: failure}}}}
	for _, form := range []url.Values{
		{"action": {"details"}, "revision": {"0"}, "access_notes": {"Keep this note"}},
		{"action": {"measurement"}, "kind": {"turf"}, "zone": {"Front"}, "value": {"12.500"}, "unit": {"sq_ft"}, "source": {"Survey"}, "measured_on": {"2026-01-01"}},
		{"action": {"note"}, "note_key": {strings.Repeat("a", 32)}, "note_body": {"Site visit"}, "occurred_on": {"2026-01-01"}},
	} {
		r := httptest.NewRequest(http.MethodPost, "/properties/"+id, strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", id)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "owner", Email: "owner@example.test"}))
		w := httptest.NewRecorder()
		s.handlePropertyRecord(w, r)
		if w.Code != 503 || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), failure.Error()) {
			t.Fatalf("%s failure: %d %s", form.Get("action"), w.Code, w.Body.String())
		}
	}

	s.store = &Store{db: db}
	if err := s.store.SavePropertyDetails(ctx, shop, id, "Original", 0); err != nil {
		t.Fatal(err)
	}
	note := propertyNote{SubmissionKey: strings.Repeat("b", 32), Body: "Original note", OccurredOn: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), RecordedBy: "owner@example.test"}
	if err := s.store.AddPropertyNote(ctx, shop, id, note); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		form   url.Values
		status int
	}{
		{url.Values{"action": {"details"}, "revision": {"0"}, "access_notes": {"Stale"}}, 409},
		{url.Values{"action": {"details"}, "revision": {"1"}, "access_notes": {strings.Repeat("x", 2001)}}, 422},
		{url.Values{"action": {"note"}, "note_key": {note.SubmissionKey}, "note_body": {"Changed note"}, "occurred_on": {"2026-01-01"}}, 409},
		{url.Values{"action": {"note"}, "note_key": {"invalid"}, "note_body": {"Invalid submission"}, "occurred_on": {"2026-01-01"}}, 422},
	} {
		r := httptest.NewRequest(http.MethodPost, "/properties/"+id, strings.NewReader(tc.form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", id)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "owner", Email: "owner@example.test"}))
		w := httptest.NewRecorder()
		s.handlePropertyRecord(w, r)
		if w.Code != tc.status || w.Header().Get("Location") != "" {
			t.Fatalf("input/conflict: got%d want%d", w.Code, tc.status)
		}
	}
}

func TestPropertyRecordsKeepHistoryAndTenantBoundaries(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testPropertyRecordsKeepHistoryAndTenantBoundaries(t, db)
	testPropertyRecordWritesReportUnavailable(t, db)
}

func testPropertyRecordsKeepHistoryAndTenantBoundaries(t *testing.T, db *DB) {
	ctx := context.Background()
	s := &Store{db: db}
	stamp := time.Now().Format("150405.000000000")
	shop := createTestShop(t, ctx, db, "property-record-"+stamp)
	other := createTestShop(t, ctx, db, "property-other-"+stamp)
	customer := createTestCustomer(t, ctx, db, shop, "Property owner")
	property := createTestProperty(t, ctx, db, shop, customer, "14 First St")
	if _, _, _, _, err := s.GetPropertyRecord(ctx, other, property); err == nil {
		t.Fatal("foreign property read")
	}
	if err := s.SavePropertyDetails(ctx, other, property, "foreign", 0); err == nil {
		t.Fatal("foreign property write")
	}
	if err := s.SavePropertyDetails(ctx, shop, property, "Gate beside garage", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePropertyDetails(ctx, shop, property, "stale", 0); !errors.Is(err, errPropertyConflict) {
		t.Fatalf("stale write: %v", err)
	}
	m := propertyMeasurement{Kind: "turf", Zone: "Front yard", Value: "1250.125", Unit: "sq_ft", Source: "Tape measure", MeasuredOn: time.Now().UTC(), RecordedBy: "owner@example.test"}
	if err := s.AddPropertyMeasurement(ctx, other, property, m); err == nil {
		t.Fatal("foreign measurement")
	}
	if err := s.AddPropertyMeasurement(ctx, shop, property, m); err != nil {
		t.Fatal(err)
	}
	m.Value = "1300.250"
	m.Source = "Deep Lawn (entered manually)"
	if err := s.AddPropertyMeasurement(ctx, shop, property, m); err != nil {
		t.Fatal(err)
	}
	p, notes, revision, history, err := s.GetPropertyRecord(ctx, shop, property)
	if err != nil || p.CustomerID != customer || notes != "Gate beside garage" || revision != 1 || len(history) != 2 {
		t.Fatalf("record %v %q %d %d %v", p, notes, revision, len(history), err)
	}
	if history[0].Value != "1300.250" || history[1].Value != "1250.125" {
		t.Fatalf("measurement precision/history: %+v", history)
	}
	// Database guards protect tenant ownership and dimension integrity as well.
	if _, err := db.Pool.Exec(ctx, database.Statement(db.Pool.Dialect(), `INSERT INTO property_details(shop_id,property_id) VALUES ($1,$2)`, `INSERT INTO dbo.property_details(shop_id,property_id) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2))`), other, property); err == nil {
		t.Fatal("database permitted foreign property details")
	}
	if _, err := db.Pool.Exec(ctx, database.Statement(db.Pool.Dialect(), `UPDATE property_measurements SET unit='ft' WHERE shop_id=$1`, `UPDATE dbo.property_measurements SET unit='ft' WHERE shop_id=CONVERT(uniqueidentifier,@p1)`), shop); err == nil {
		t.Fatal("database permitted area in linear feet")
	}
	server := &Server{store: s, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	for _, role := range []string{"staff", "owner"} {
		r := httptest.NewRequest("GET", "/properties/"+property, nil)
		r.SetPathValue("id", property)
		r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: role}))
		w := httptest.NewRecorder()
		server.handlePropertyRecord(w, r)
		if role == "staff" {
			if w.Code != 403 {
				t.Fatal("staff accessed owner-only record")
			}
		} else if w.Code != 200 || !strings.Contains(w.Body.String(), "1250.125") || !strings.Contains(w.Body.String(), "Gate beside garage") {
			t.Fatalf("record page: %d %s", w.Code, w.Body.String())
		}
	}
}

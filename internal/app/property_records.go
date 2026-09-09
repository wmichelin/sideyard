package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var errPropertyConflict = errors.New("These site details changed in another tab. Reload and review your changes.")

var propertyMeasurementKinds = []struct{ ID, Name string }{
	{"turf", "Lawn / turf"}, {"beds", "Landscape beds"}, {"driveway", "Driveway"},
	{"walkway", "Walkway"}, {"patio", "Patio"}, {"roof", "Roof"},
	{"building_footprint", "Building footprint"}, {"floor_area", "Floor area"},
	{"exterior_wall", "Exterior wall area"}, {"perimeter", "Perimeter / edging"},
	{"other_area", "Other area"}, {"other_length", "Other length"},
}

type propertyMeasurement struct {
	Kind, Zone, Value, Unit, Source, RecordedBy string
	MeasuredOn                                  time.Time
	Estimated                                   bool
}

func (m propertyMeasurement) KindLabel() string {
	for _, k := range propertyMeasurementKinds {
		if k.ID == m.Kind {
			return k.Name
		}
	}
	return m.Kind
}
func (m propertyMeasurement) UnitLabel() string {
	switch m.Unit {
	case "sq_ft":
		return "sq ft"
	case "sq_m":
		return "sq m"
	default:
		return m.Unit
	}
}

// Keep decimal input exact rather than rounding area through floating point.
var measurementDecimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,10})(\.[0-9]{1,3})?$`)

func validatePropertyMeasurement(m propertyMeasurement, today time.Time) error {
	known := false
	for _, k := range propertyMeasurementKinds {
		if k.ID == m.Kind {
			known = true
		}
	}
	linear := m.Kind == "perimeter" || m.Kind == "other_length"
	validUnit := (linear && (m.Unit == "ft" || m.Unit == "m")) || (!linear && (m.Unit == "sq_ft" || m.Unit == "sq_m"))
	if !known || !validUnit {
		return errors.New("Choose an area with square units, or a length with feet/meters.")
	}
	if !measurementDecimal.MatchString(m.Value) || strings.Trim(m.Value, "0.") == "" {
		return errors.New("Enter a positive measurement with up to three decimal places.")
	}
	if m.Zone == "" || len(m.Zone) > 120 || m.Source == "" || len(m.Source) > 120 || strings.ContainsRune(m.Zone+m.Source, '\x00') {
		return errors.New("Enter a zone and measurement source, each up to 120 bytes.")
	}
	if m.MeasuredOn.IsZero() || m.MeasuredOn.Format("2006-01-02") > today.Format("2006-01-02") {
		return errors.New("Choose a measurement date no later than today.")
	}
	return nil
}

func (s *Store) GetPropertyRecord(ctx context.Context, shopID, id string) (Property, string, int, []propertyMeasurement, error) {
	var p Property
	var notes string
	var revision int
	err := s.db.Pool.QueryRow(ctx, `SELECT p.id,p.customer_id,p.address_line1,COALESCE(p.address_line2,''),p.city,p.state,p.postal_code,COALESCE(d.access_notes,''),COALESCE(d.revision,0) FROM properties p LEFT JOIN property_details d ON d.property_id=p.id AND d.shop_id=p.shop_id WHERE p.shop_id=$1 AND p.id=$2`, shopID, id).Scan(&p.ID, &p.CustomerID, &p.AddressLine1, &p.AddressLine2, &p.City, &p.State, &p.PostalCode, &notes, &revision)
	if err != nil {
		return p, notes, revision, nil, err
	}
	rows, err := s.db.Pool.Query(ctx, `SELECT kind,zone,value::text,unit,source,measured_on,estimated,recorded_by FROM property_measurements WHERE shop_id=$1 AND property_id=$2 ORDER BY recorded_at DESC,id DESC LIMIT 100`, shopID, id)
	if err != nil {
		return p, notes, revision, nil, err
	}
	defer rows.Close()
	var measurements []propertyMeasurement
	for rows.Next() {
		var m propertyMeasurement
		if err := rows.Scan(&m.Kind, &m.Zone, &m.Value, &m.Unit, &m.Source, &m.MeasuredOn, &m.Estimated, &m.RecordedBy); err != nil {
			return p, notes, revision, nil, err
		}
		measurements = append(measurements, m)
	}
	return p, notes, revision, measurements, rows.Err()
}

func (s *Store) SavePropertyDetails(ctx context.Context, shopID, id, notes string, revision int) error {
	if len(notes) > 2000 || strings.ContainsRune(notes, '\x00') {
		return errors.New("Site details must be at most 2,000 bytes.")
	}
	var saved int
	var err error
	if revision == 0 {
		err = s.db.Pool.QueryRow(ctx, `INSERT INTO property_details(shop_id,property_id,access_notes) SELECT $1,id,$3 FROM properties WHERE shop_id=$1 AND id=$2 ON CONFLICT DO NOTHING RETURNING revision`, shopID, id, notes).Scan(&saved)
	} else {
		err = s.db.Pool.QueryRow(ctx, `UPDATE property_details SET access_notes=$3,revision=revision+1 WHERE shop_id=$1 AND property_id=$2 AND revision=$4 RETURNING revision`, shopID, id, notes, revision).Scan(&saved)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return errPropertyConflict
	}
	return err
}

func (s *Store) AddPropertyMeasurement(ctx context.Context, shopID, id string, m propertyMeasurement) error {
	if err := validatePropertyMeasurement(m, s.nowUTC()); err != nil {
		return err
	}
	tag, err := s.db.Pool.Exec(ctx, `INSERT INTO property_measurements(shop_id,property_id,kind,zone,value,unit,source,measured_on,estimated,recorded_by) SELECT $1,id,$3,$4,$5::numeric,$6,$7,$8,$9,$10 FROM properties WHERE shop_id=$1 AND id=$2`, shopID, id, m.Kind, m.Zone, m.Value, m.Unit, m.Source, m.MeasuredOn, m.Estimated, m.RecordedBy)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("Property not found.")
	}
	return nil
}

func (s *Server) handlePropertyRecord(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	// Staff record visibility will be introduced with explicit crew permissions.
	if u == nil || u.Role != "owner" {
		http.Error(w, "Property records are available to the owner.", 403)
		return
	}
	id := r.PathValue("id")
	p, notes, revision, measurements, err := s.store.GetPropertyRecord(r.Context(), u.ShopID, id)
	if err != nil {
		http.Error(w, "Property record unavailable.", 404)
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form.", 400)
			return
		}
		switch r.PostForm.Get("action") {
		case "note":
			date, parseErr := time.Parse("2006-01-02", r.PostForm.Get("occurred_on"))
			if parseErr != nil {
				http.Error(w, "Choose a valid note date.", 422)
				return
			}
			err = s.store.AddPropertyNote(r.Context(), u.ShopID, id, propertyNote{SubmissionKey: r.PostForm.Get("note_key"), Body: r.PostForm.Get("note_body"), OccurredOn: date, RecordedBy: u.Email})
		case "details":
			version, parseErr := strconv.Atoi(r.PostForm.Get("revision"))
			if parseErr != nil || version < 0 {
				http.Error(w, "Reload and try again.", 400)
				return
			}
			err = s.store.SavePropertyDetails(r.Context(), u.ShopID, id, strings.TrimSpace(r.PostForm.Get("access_notes")), version)
		case "measurement":
			date, parseErr := time.Parse("2006-01-02", r.PostForm.Get("measured_on"))
			if parseErr != nil {
				http.Error(w, "Choose a valid measurement date.", 422)
				return
			}
			m := propertyMeasurement{Kind: r.PostForm.Get("kind"), Zone: strings.TrimSpace(r.PostForm.Get("zone")), Value: strings.TrimSpace(r.PostForm.Get("value")), Unit: r.PostForm.Get("unit"), Source: strings.TrimSpace(r.PostForm.Get("source")), MeasuredOn: date, Estimated: r.PostForm.Get("estimated") == "1", RecordedBy: u.Email}
			if err := validatePropertyMeasurement(m, s.store.nowUTC()); err != nil {
				http.Error(w, err.Error(), 422)
				return
			}
			err = s.store.AddPropertyMeasurement(r.Context(), u.ShopID, id, m)
		default:
			http.Error(w, "Unknown action.", 400)
			return
		}
		if err != nil {
			if errors.Is(err, errPropertyConflict) {
				http.Error(w, err.Error(), 409)
			} else {
				http.Error(w, "Could not save property details. Check your entry and retry.", 422)
			}
			return
		}
		http.Redirect(w, r, "/properties/"+url.PathEscape(id)+"?saved=1", 303)
		return
	}
	address := strings.Join([]string{p.AddressLine1, p.AddressLine2, p.City, p.State, p.PostalCode}, " ")
	p.Equipment, err = s.store.ListEquipment(r.Context(), u.ShopID, id)
	if err != nil {
		http.Error(w, "Could not load equipment. Please retry.", 503)
		return
	}
	journal, err := s.store.ListPropertyNotes(r.Context(), u.ShopID, id)
	if err != nil {
		http.Error(w, "Could not load site notes. Please retry.", 503)
		return
	}
	noteKey, err := randomToken(16)
	if err != nil {
		http.Error(w, "Could not prepare the note form. Please retry.", 503)
		return
	}
	s.renderLayout(w, r, "Property record — Sideyard", "property_record_content", map[string]any{"Nav": NavData{Active: "customers", User: u}, "Property": p, "Notes": notes, "Journal": journal, "NoteKey": noteKey, "Revision": revision, "Measurements": measurements, "Kinds": propertyMeasurementKinds, "Today": s.store.nowUTC().Format("2006-01-02"), "GoogleMaps": "https://www.google.com/maps/dir/?api=1&destination=" + url.QueryEscape(address), "AppleMaps": "https://maps.apple.com/?daddr=" + url.QueryEscape(address), "Saved": r.URL.Query().Get("saved") == "1"})
}

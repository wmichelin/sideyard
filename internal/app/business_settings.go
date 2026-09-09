package app

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/wmichelin/clubdue/internal/database"
)

// Presets supply starter names, never prices, customer terms or scheduled work.
type businessPreset struct {
	ID, Name, Description string
	Services              []businessService
}

type businessService struct {
	ID, PresetKey, Name, Description string
	Enabled                          bool
	Revision                         int
}

var businessPresets = []businessPreset{
	{"auto_detailing", "Auto detailing", "Mobile and shop detailing, packages and repeat maintenance.", []businessService{
		{PresetKey: "auto_detailing/interior", Name: "Interior detail", Description: "Interior cleaning and condition review."},
		{PresetKey: "auto_detailing/exterior", Name: "Exterior detail", Description: "Exterior cleaning and finish care."},
	}},
	{"house_washing", "House washing", "House, roof, driveway and other exterior cleaning.", []businessService{
		{PresetKey: "house_washing/house", Name: "House wash", Description: "Exterior house cleaning."},
		{PresetKey: "house_washing/driveway", Name: "Driveway cleaning", Description: "Driveway surface cleaning."},
	}},
	{"landscaping", "Landscaping & lawn care", "Recurring maintenance, seasonal cleanup and treatment programs.", []businessService{
		{PresetKey: "landscaping/maintenance", Name: "Lawn maintenance", Description: "Mowing, edging and routine property care."},
		{PresetKey: "landscaping/treatment", Name: "Lawn treatment program", Description: "A configurable seasonal treatment program."},
	}},
	{"hvac", "HVAC", "Equipment maintenance, diagnosis and repair.", []businessService{
		{PresetKey: "hvac/maintenance", Name: "HVAC maintenance", Description: "Equipment inspection and maintenance."},
		{PresetKey: "hvac/repair", Name: "HVAC diagnosis and repair", Description: "Investigate and repair equipment issues."},
	}},
	{"plumbing", "Plumbing", "Service calls, repairs and installations.", []businessService{
		{PresetKey: "plumbing/repair", Name: "Plumbing service call", Description: "Diagnosis and repair of plumbing issues."},
		{PresetKey: "plumbing/water_heater", Name: "Water heater service", Description: "Water heater maintenance or replacement."},
	}},
	{"electrical", "Electrical", "Diagnosis, repairs and installation work.", []businessService{
		{PresetKey: "electrical/repair", Name: "Electrical service call", Description: "Diagnosis and repair of electrical issues."},
		{PresetKey: "electrical/installation", Name: "Electrical installation", Description: "Quoted equipment and circuit installation."},
	}},
	{"general", "General services", "Start without industry-specific suggestions.", nil},
}

type businessSettings struct {
	Primary  string
	Trades   []string
	Revision int
}

func (b businessSettings) Selected(id string) bool { return slices.Contains(b.Trades, id) }

func validateBusinessSelection(primary string, trades []string) (businessSettings, error) {
	b := businessSettings{Primary: primary}
	for _, p := range businessPresets {
		if p.ID == primary || slices.Contains(trades, p.ID) {
			b.Trades = append(b.Trades, p.ID)
		}
	}
	if !b.Selected(primary) {
		return b, errors.New("Choose a primary trade.")
	}
	for _, id := range trades {
		if !b.Selected(id) {
			return b, errors.New("Choose a listed trade.")
		}
	}
	return b, nil
}

var errBusinessConflict = errors.New("These settings changed in another tab. Reload and review your changes.")

func (s *Store) GetBusinessSettings(ctx context.Context, shopID string) (businessSettings, []businessService, error) {
	b := businessSettings{Primary: "general", Trades: []string{"general"}}
	err := s.db.Pool.QueryRow(ctx, `SELECT primary_trade, trades, revision FROM business_settings WHERE shop_id=$1`, shopID).Scan(&b.Primary, &b.Trades, &b.Revision)
	if err != nil && !errors.Is(err, database.ErrNoRows) {
		return b, nil, err
	}
	rows, err := s.db.Pool.Query(ctx, `SELECT id, preset_key, name, description, enabled, revision FROM business_services WHERE shop_id=$1 ORDER BY name, id`, shopID)
	if err != nil {
		return b, nil, err
	}
	defer rows.Close()
	var services []businessService
	for rows.Next() {
		var v businessService
		if err := rows.Scan(&v.ID, &v.PresetKey, &v.Name, &v.Description, &v.Enabled, &v.Revision); err != nil {
			return b, nil, err
		}
		services = append(services, v)
	}
	return b, services, rows.Err()
}

func (s *Store) ApplyBusinessSettings(ctx context.Context, shopID string, b businessSettings, keys []string) error {
	selection, err := validateBusinessSelection(b.Primary, b.Trades)
	if err != nil {
		return err
	}
	allowed := map[string]businessService{}
	for _, p := range businessPresets {
		if selection.Selected(p.ID) {
			for _, v := range p.Services {
				allowed[v.PresetKey] = v
			}
		}
	}
	for _, key := range keys {
		if _, ok := allowed[key]; !ok {
			return errors.New("Choose a service from the selected trades.")
		}
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// INSERT/UPDATE serialize competing saves, including first-time setup.
	var revision int
	if b.Revision == 0 {
		err = tx.QueryRow(ctx, `INSERT INTO business_settings (shop_id, primary_trade, trades) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING RETURNING revision`, shopID, selection.Primary, selection.Trades).Scan(&revision)
	} else {
		err = tx.QueryRow(ctx, `UPDATE business_settings SET primary_trade=$2,trades=$3,revision=revision+1 WHERE shop_id=$1 AND revision=$4 RETURNING revision`, shopID, selection.Primary, selection.Trades, b.Revision).Scan(&revision)
	}
	if errors.Is(err, database.ErrNoRows) {
		return errBusinessConflict
	}
	if err != nil {
		return err
	}
	for _, key := range keys {
		v := allowed[key]
		_, err = tx.Exec(ctx, `INSERT INTO business_services (shop_id,preset_key,preset_version,name,description) VALUES ($1,$2,1,$3,$4) ON CONFLICT (shop_id,preset_key) DO NOTHING`, shopID, key, v.Name, v.Description)
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateBusinessService(ctx context.Context, shopID, id, name, description string, enabled bool, revision int) error {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len(name) > 120 || len(description) > 2000 || strings.ContainsRune(name+description, '\x00') {
		return errors.New("Enter a service name up to 120 bytes and a description up to 2,000 bytes.")
	}
	tag, err := s.db.Pool.Exec(ctx, `UPDATE business_services SET name=$3,description=$4,enabled=$5,revision=revision+1 WHERE shop_id=$1 AND id=$2 AND revision=$6`, shopID, id, name, description, enabled, revision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errBusinessConflict
	}
	return nil
}

func (s *Server) handleBusinessSettings(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	if s.demo || u == nil || u.Role != "owner" {
		http.Error(w, "Business setup is available to the owner.", http.StatusForbidden)
		return
	}
	b, services, err := s.store.GetBusinessSettings(r.Context(), u.ShopID)
	if err != nil {
		http.Error(w, "Could not load business setup. Please retry.", 503)
		return
	}
	data := map[string]any{"Nav": NavData{Active: "business", User: u}, "Settings": b, "Presets": businessPresets, "Services": services, "Saved": r.URL.Query().Get("saved") == "1"}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form.", 400)
			return
		}
		revision, err := strconv.Atoi(r.PostForm.Get("revision"))
		if err != nil || revision < 0 {
			http.Error(w, "Reload the page and try again.", 400)
			return
		}
		action := r.PostForm.Get("action")
		if action == "service" {
			err = s.store.UpdateBusinessService(r.Context(), u.ShopID, r.PostForm.Get("id"), r.PostForm.Get("name"), r.PostForm.Get("description"), r.PostForm.Get("enabled") == "1", revision)
		} else if action == "preview" || action == "apply" {
			b, err = validateBusinessSelection(r.PostForm.Get("primary"), r.PostForm["trade"])
			b.Revision = revision
			if err == nil && action == "apply" {
				err = s.store.ApplyBusinessSettings(r.Context(), u.ShopID, b, r.PostForm["service"])
			}
			if err == nil && action == "preview" {
				if revision != data["Settings"].(businessSettings).Revision {
					err = errBusinessConflict
				} else {
					existing := map[string]bool{}
					for _, v := range services {
						existing[v.PresetKey] = true
					}
					var suggested []businessService
					for _, p := range businessPresets {
						if b.Selected(p.ID) {
							for _, v := range p.Services {
								if !existing[v.PresetKey] {
									suggested = append(suggested, v)
								}
							}
						}
					}
					data["Preview"], data["Suggested"], data["Settings"] = true, suggested, b
				}
			}
		} else {
			http.Error(w, "Unknown action.", 400)
			return
		}
		if err != nil {
			if errors.Is(err, errBusinessConflict) {
				http.Error(w, err.Error(), 409)
				return
			}
			// Do not expose database errors or submitted values.
			http.Error(w, "Could not save. Check the selected trades and service details, then retry.", 422)
			return
		}
		if action != "preview" {
			http.Redirect(w, r, "/business?saved=1", http.StatusSeeOther)
			return
		}
	}
	s.renderLayout(w, r, "Business setup — Sideyard", "business_settings_content", data)
}

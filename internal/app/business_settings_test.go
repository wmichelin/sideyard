package app

import (
	"context"
	"errors"
	"html/template"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBusinessSelection(t *testing.T) {
	for _, tc := range []struct {
		primary string
		trades  []string
		valid   bool
		count   int
	}{
		{"landscaping", []string{"house_washing", "landscaping", "house_washing"}, true, 2},
		{"auto_detailing", nil, true, 1},
		{"general", nil, true, 1},
		{"missing", nil, false, 0},
		{"hvac", []string{"missing"}, false, 0},
	} {
		b, err := validateBusinessSelection(tc.primary, tc.trades)
		if (err == nil) != tc.valid {
			t.Fatalf("selection %q: %v", tc.primary, err)
		}
		if tc.valid && (len(b.Trades) != tc.count || !b.Selected(tc.primary)) {
			t.Fatalf("selection: %+v", b)
		}
	}
}

func TestBusinessSetupPreservesEditsAndSerializesSaves(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "business-"+time.Now().Format("150405.000000000"))
	other := createTestShop(t, ctx, db, "business-other-"+time.Now().Format("150405.000000000"))
	b, services, err := s.GetBusinessSettings(ctx, shop)
	if err != nil || b.Revision != 0 || len(services) != 0 {
		t.Fatalf("initial: %+v %v", b, err)
	}
	var rows int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM business_settings WHERE shop_id=$1`, shop).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("reading setup wrote data: %v", err)
	}
	b, _ = validateBusinessSelection("landscaping", []string{"house_washing"})
	keys := []string{"landscaping/treatment", "house_washing/house"}
	// A failed selection cannot partially write business settings.
	if err := s.ApplyBusinessSettings(ctx, shop, b, []string{"hvac/repair"}); err == nil {
		t.Fatal("foreign preset accepted")
	}
	if err := s.ApplyBusinessSettings(ctx, shop, b, keys); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyBusinessSettings(ctx, shop, b, keys); !errors.Is(err, errBusinessConflict) {
		t.Fatalf("stale initial save: %v", err)
	}
	b, services, err = s.GetBusinessSettings(ctx, shop)
	if err != nil || len(services) != 2 || b.Revision != 1 {
		t.Fatalf("saved: %+v %v", b, err)
	}
	v := services[0]
	if err := s.UpdateBusinessService(ctx, other, v.ID, "foreign", "", true, v.Revision); !errors.Is(err, errBusinessConflict) {
		t.Fatalf("tenant guard: %v", err)
	}
	if err := s.UpdateBusinessService(ctx, shop, v.ID, "My custom service", "Owner wording", false, v.Revision); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateBusinessService(ctx, shop, v.ID, "Stale overwrite", "", true, v.Revision); !errors.Is(err, errBusinessConflict) {
		t.Fatalf("stale service edit: %v", err)
	}
	// Reapplying a preset never overwrites the owner's edited/disabled service.
	if err := s.ApplyBusinessSettings(ctx, shop, b, keys); err != nil {
		t.Fatal(err)
	}
	b, services, err = s.GetBusinessSettings(ctx, shop)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, got := range services {
		if got.ID == v.ID {
			found = got.Name == "My custom service" && got.Description == "Owner wording" && !got.Enabled
		}
	}
	if !found || len(services) != 2 {
		t.Fatal("reapply overwrote/duplicated service")
	}
	// Exactly one concurrent settings revision can win.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.ApplyBusinessSettings(ctx, shop, b, nil) }()
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, errBusinessConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("concurrent results %d/%d", wins, conflicts)
	}
	b, _, _ = s.GetBusinessSettings(ctx, shop)
	b.Primary, b.Trades = "general", []string{"general"}
	if err := s.ApplyBusinessSettings(ctx, shop, b, nil); err != nil {
		t.Fatal(err)
	}
	_, services, err = s.GetBusinessSettings(ctx, shop)
	if err != nil || len(services) != 2 {
		t.Fatal("changing trade removed services")
	}
	_, otherServices, err := s.GetBusinessSettings(ctx, other)
	if err != nil || len(otherServices) != 0 {
		t.Fatal("tenant leaked services")
	}
	for _, table := range []string{"agreements", "plan_templates", "service_schedules"} {
		if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE shop_id=$1`, shop).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("setup changed %s: %v", table, err)
		}
	}
}

func TestBusinessSetupPreviewIsReadOnlyAndOwnerOnly(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	shop := createTestShop(t, ctx, db, "business-preview-"+time.Now().Format("150405.000000000"))
	s := &Server{store: &Store{db: db}, tmpl: template.Must(template.ParseFS(templateFS, "templates/*.html"))}
	values := url.Values{"action": {"preview"}, "primary": {"auto_detailing"}, "trade": {"house_washing"}, "revision": {"0"}}
	for _, role := range []string{"staff", "owner"} {
		r := httptest.NewRequest("POST", "/business", strings.NewReader(values.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r = r.WithContext(contextWithUser(r.Context(), &User{ShopID: shop, Role: role}))
		w := httptest.NewRecorder()
		s.handleBusinessSettings(w, r)
		if role == "staff" {
			if w.Code != 403 {
				t.Fatalf("staff status %d", w.Code)
			}
		} else if w.Code != 200 || !strings.Contains(w.Body.String(), "Interior detail") || !strings.Contains(w.Body.String(), "House wash") {
			t.Fatalf("preview: %d %s", w.Code, w.Body.String())
		}
	}
	b, services, err := s.store.GetBusinessSettings(ctx, shop)
	if err != nil || b.Revision != 0 || len(services) != 0 {
		t.Fatal("preview changed records")
	}
}

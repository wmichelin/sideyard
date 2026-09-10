package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func TestSQLServerCustomerAssetsPreserveExistingBehavior(t *testing.T) {
	t.Run("vehicles", func(t *testing.T) { testCustomerVehiclesRemainSeparateAndScoped(t, sqlServerTestDB(t)) })
	t.Run("equipment", func(t *testing.T) {
		testEquipmentRecordsPreserveIdentityAndRejectConflictingEdits(t, sqlServerTestDB(t))
	})
}

func TestSQLServerCustomerBookScopesDataAndRetainsExactText(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "customer-book")
	other := createTestShop(t, ctx, db, "customer-book-other")
	name := strings.Repeat("Large 🔧 customer ", 300)
	first, err := s.CreateCustomer(ctx, shop, name, " ", "  ")
	if err != nil {
		t.Fatal(err)
	}
	second := createTestCustomer(t, ctx, db, shop, "Second")
	foreign := createTestCustomer(t, ctx, db, other, "Foreign")
	got, err := s.GetCustomer(ctx, shop, first)
	if err != nil || got.Name != name || got.Email != " " || got.Phone != "  " {
		t.Fatalf("customer text changed: %v", err)
	}
	if _, err := s.GetCustomer(ctx, other, first); err == nil {
		t.Fatal("foreign customer readable")
	}
	if err := s.UpdateCustomer(ctx, other, first, "Wrong", "", ""); err == nil {
		t.Fatal("foreign customer changed")
	}
	property, err := s.CreateProperty(ctx, shop, first, strings.Repeat("A", 2100), " ", "City", "State", "Postal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProperty(ctx, shop, foreign, "Wrong", "", "City", "State", "Postal"); err == nil {
		t.Fatal("cross-tenant property created")
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.properties(shop_id,customer_id,address_line1,city,state,postal_code) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),'X','X','X','X')`, shop, foreign); err == nil {
		t.Fatal("database accepted cross-tenant property")
	}
	if err := s.CreateEquipment(ctx, shop, property, strings.Repeat("Long equipment ", 200), " ", "  ", ""); err != nil {
		t.Fatal(err)
	}
	properties, err := s.ListProperties(ctx, shop, first)
	if err != nil || len(properties) != 1 || properties[0].AddressLine2 != " " || len(properties[0].Equipment) != 1 || properties[0].Equipment[0].Make != " " || properties[0].Equipment[0].Model != "  " {
		t.Fatalf("property/equipment text changed: %v", err)
	}
	if err := s.SaveCustomerVehicle(ctx, shop, first, customerVehicle{Name: "Work truck"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCustomer(ctx, other, first); err == nil {
		t.Fatal("foreign delete succeeded")
	}
	if err := s.DeleteCustomer(ctx, shop, first); err != nil {
		t.Fatal(err)
	}
	var children int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.properties)+(SELECT COUNT(*) FROM dbo.equipment)+(SELECT COUNT(*) FROM dbo.customer_vehicles)`).Scan(&children); err != nil || children != 0 {
		t.Fatalf("customer cascade: %d %v", children, err)
	}
	for _, id := range []string{second, foreign} {
		tenant := shop
		if id == foreign {
			tenant = other
		}
		if _, err := s.GetCustomer(ctx, tenant, id); err != nil {
			t.Fatal("unrelated customer changed")
		}
	}
	for _, invalid := range []string{second + "suffix", second + " ", strings.ReplaceAll(second, "-", "")} {
		if _, err := s.GetCustomer(ctx, shop, invalid); err == nil {
			t.Fatal("malformed UUID aliased an existing record")
		}
		if err := s.UpdateCustomer(ctx, shop, invalid, "Wrong", "", ""); err == nil {
			t.Fatal("malformed UUID updated a record")
		}
	}
	for _, invalid := range []string{"Bad\x00name", "Bad\xffname"} {
		if _, err := s.CreateCustomer(ctx, shop, invalid, "", ""); err == nil {
			t.Fatal("SQL accepted invalid PostgreSQL text")
		}
	}
}

func TestSQLServerCustomerExternalIdentitiesPreserveNullsAndExactValues(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "external-ids")
	ids := []string{createTestCustomer(t, ctx, db, shop, "First"), createTestCustomer(t, ctx, db, shop, "Second"), createTestCustomer(t, ctx, db, shop, "Third")}
	key := strings.Repeat("External 🔧", 300)
	update := func(id, value string) error {
		_, err := db.Pool.Exec(ctx, `UPDATE dbo.customers SET external_id=@p2 WHERE id=CONVERT(uniqueidentifier,@p1)`, id, value)
		return err
	}
	if err := update(ids[0], key); err != nil {
		t.Fatal(err)
	}
	if err := update(ids[1], key); err == nil {
		t.Fatal("duplicate external identity allowed")
	}
	if err := update(ids[1], key+" "); err != nil {
		t.Fatal("trailing-space identity merged")
	}
	if err := update(ids[2], strings.ToLower(key)); err != nil {
		t.Fatal("case-distinct identity merged")
	}
	got, err := s.GetCustomer(ctx, shop, ids[1])
	if err != nil || got.ExternalID != key+" " {
		t.Fatalf("external text changed: %v", err)
	}
}

func TestSQLServerPropertyAddressRevisionTracksExactMultirowChanges(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "addresses")
	customer := createTestCustomer(t, ctx, db, shop, "Address owner")
	first := createTestProperty(t, ctx, db, shop, customer, "First")
	second := createTestProperty(t, ctx, db, shop, customer, "Second")
	p, err := s.GetPropertyAddress(ctx, shop, first)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SavePropertyAddress(ctx, shop, p); err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.GetPropertyAddress(ctx, shop, first)
	if err != nil || unchanged.Revision != 1 {
		t.Fatal("unchanged address advanced revision")
	}
	p.AddressLine2 = " "
	if err := s.SavePropertyAddress(ctx, shop, p); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePropertyAddress(ctx, shop, p); !errors.Is(err, errPropertyAddressConflict) {
		t.Fatalf("stale address accepted: %v", err)
	}
	p, err = s.GetPropertyAddress(ctx, shop, first)
	if err != nil || p.Revision != 2 || p.AddressLine2 != " " {
		t.Fatal("NULL-to-space change lost")
	}
	r, err := db.Pool.Exec(ctx, `UPDATE dbo.properties SET city=city+N' ' WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop)
	if err != nil || r.RowsAffected() != 2 {
		t.Fatalf("multirow update: %v %v", r, err)
	}
	for _, tc := range []struct {
		id       string
		revision int
	}{{first, 3}, {second, 2}} {
		got, err := s.GetPropertyAddress(ctx, shop, tc.id)
		if err != nil || got.Revision != tc.revision || got.City != "Cary " {
			t.Fatalf("multirow revision: %+v %v", got, err)
		}
	}
}

func TestSQLServerPropertyAddressCompetingEditsAndFailedRevisionAreAtomic(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "address-race")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	id := createTestProperty(t, ctx, db, shop, customer, "Original")
	p, err := s.GetPropertyAddress(ctx, shop, id)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, address := range []string{"First edit", "Second edit"} {
		go func(address string) {
			edit := p
			edit.AddressLine1 = address
			<-start
			results <- s.SavePropertyAddress(ctx, shop, edit)
		}(address)
	}
	close(start)
	var saved, conflicted int
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			saved++
		case errors.Is(err, errPropertyAddressConflict):
			conflicted++
		default:
			t.Fatal(err)
		}
	}
	got, err := s.GetPropertyAddress(ctx, shop, id)
	if err != nil || saved != 1 || conflicted != 1 || got.Revision != 2 || (got.AddressLine1 != "First edit" && got.AddressLine1 != "Second edit") {
		t.Fatalf("competing address edits: %+v, saved=%d conflicts=%d err=%v", got, saved, conflicted, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.properties SET address_revision=2147483647 WHERE id=CONVERT(uniqueidentifier,@p1)`, id); err != nil {
		t.Fatal(err)
	}
	edit := got
	edit.Revision = 2147483647
	edit.AddressLine1 = "Must roll back"
	if err := s.SavePropertyAddress(ctx, shop, edit); err == nil {
		t.Fatal("revision overflow succeeded")
	}
	after, err := s.GetPropertyAddress(ctx, shop, id)
	if err != nil || after.AddressLine1 != got.AddressLine1 || after.Revision != 2147483647 {
		t.Fatalf("failed trigger left a partial update: %+v %v", after, err)
	}
}

// Hold each first property row until all four native pool connections are in
// use. Nested equipment queries would then wait on those same four requests.
type propertyBarrierPool struct {
	database.Pool
	mu      sync.Mutex
	arrived int
	ready   chan struct{}
}

func (p *propertyBarrierPool) Query(ctx context.Context, query string, args ...any) (database.Rows, error) {
	rows, err := p.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if strings.Contains(query, "FROM dbo.properties") {
		return &propertyBarrierRows{Rows: rows, pool: p, ctx: ctx}, nil
	}
	return rows, nil
}

type propertyBarrierRows struct {
	database.Rows
	pool   *propertyBarrierPool
	ctx    context.Context
	waited bool
}

func (r *propertyBarrierRows) Next() bool {
	ok := r.Rows.Next()
	if ok && !r.waited {
		r.waited = true
		r.pool.mu.Lock()
		r.pool.arrived++
		if r.pool.arrived == 4 {
			close(r.pool.ready)
		}
		r.pool.mu.Unlock()
		select {
		case <-r.pool.ready:
		case <-r.ctx.Done():
		}
	}
	return ok
}

func TestSQLServerConcurrentCustomerDetailsReleasePropertyConnections(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shop := createTestShop(t, ctx, db, "connection-budget")
	customer := createTestCustomer(t, ctx, db, shop, "Customer")
	property := createTestProperty(t, ctx, db, shop, customer, "Address")
	s := &Store{db: db}
	if err := s.CreateEquipment(ctx, shop, property, "Unit", "", "", ""); err != nil {
		t.Fatal(err)
	}
	s = &Store{db: &DB{Pool: &propertyBarrierPool{Pool: db.Pool, ready: make(chan struct{})}}}
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			properties, err := s.ListProperties(ctx, shop, customer)
			if err == nil && (len(properties) != 1 || len(properties[0].Equipment) != 1) {
				err = errors.New("missing property equipment")
			}
			results <- err
		}()
	}
	for i := 0; i < 4; i++ {
		if err := <-results; err != nil {
			t.Fatalf("customer detail exhausted its connection budget: %v", err)
		}
	}
}

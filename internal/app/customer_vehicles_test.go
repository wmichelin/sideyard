package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func TestCustomerVehicleValidation(t *testing.T) {
	for _, tc := range []struct {
		name, year string
		valid      bool
	}{
		{"Blue sedan", "2024", true}, {"Work truck", "", true}, {"  ", "2024", false}, {"Car", "abc", false}, {"Car", "1700", false}, {"Car", "10000", false},
	} {
		if err := validateCustomerVehicle(customerVehicle{Name: tc.name, Year: tc.year}); (err == nil) != tc.valid {
			t.Fatalf("%q/%q: %v", tc.name, tc.year, err)
		}
	}
}

func TestCustomerVehiclesRemainSeparateAndScoped(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	testCustomerVehiclesRemainSeparateAndScoped(t, db)
}

func testCustomerVehiclesRemainSeparateAndScoped(t *testing.T, db *DB) {
	ctx := context.Background()
	s := &Store{db: db}
	stamp := time.Now().Format("150405.000000000")
	shop := createTestShop(t, ctx, db, "vehicles-"+stamp)
	other := createTestShop(t, ctx, db, "vehicles-other-"+stamp)
	customer := createTestCustomer(t, ctx, db, shop, "Vehicle customer")
	second := createTestCustomer(t, ctx, db, shop, "Different customer")
	foreign := createTestCustomer(t, ctx, db, other, "Foreign customer")
	for _, v := range []customerVehicle{{Name: "Blue sedan", Make: "Toyota", Model: "Camry", Year: "2024", Notes: "Seat stain"}, {Name: "Work truck", Make: "Ford", Model: "F-150", Year: "2020", Notes: "Bed cleanup"}} {
		if err := s.SaveCustomerVehicle(ctx, shop, customer, v); err != nil {
			t.Fatal(err)
		}
	}
	vehicles, err := s.ListCustomerVehicles(ctx, shop, customer)
	if err != nil || len(vehicles) != 2 {
		t.Fatalf("vehicles %d %v", len(vehicles), err)
	}
	v := vehicles[0]
	v.Notes = "Updated sedan condition"
	if err := s.SaveCustomerVehicle(ctx, other, customer, v); !errors.Is(err, errVehicleConflict) {
		t.Fatalf("foreign update: %v", err)
	}
	if err := s.SaveCustomerVehicle(ctx, shop, second, v); !errors.Is(err, errVehicleConflict) {
		t.Fatalf("wrong customer update: %v", err)
	}
	if err := s.SaveCustomerVehicle(ctx, shop, customer, v); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCustomerVehicle(ctx, shop, customer, v); !errors.Is(err, errVehicleConflict) {
		t.Fatalf("stale update: %v", err)
	}
	got, err := s.ListCustomerVehicles(ctx, shop, customer)
	if err != nil || got[0].Notes != "Updated sedan condition" || got[1].Notes != "Bed cleanup" {
		t.Fatalf("vehicle records mixed: %+v %v", got, err)
	}
	if got, err := s.ListCustomerVehicles(ctx, other, customer); err != nil || len(got) != 0 {
		t.Fatal("foreign vehicles readable")
	}
	if err := s.SaveCustomerVehicle(ctx, shop, foreign, customerVehicle{Name: "Foreign"}); !errors.Is(err, errVehicleConflict) {
		t.Fatalf("foreign insert: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, database.Statement(db.Pool.Dialect(), `INSERT INTO customer_vehicles(shop_id,customer_id,name) VALUES ($1,$2,'Foreign')`, `INSERT INTO dbo.customer_vehicles(shop_id,customer_id,name) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),'Foreign')`), shop, foreign); err == nil {
		t.Fatal("database accepted foreign customer")
	}
	// Neither vehicle needs a property, and a later location does not move it.
	createTestProperty(t, ctx, db, shop, customer, "Shop address")
	got, err = s.ListCustomerVehicles(ctx, shop, customer)
	if err != nil || len(got) != 2 || got[0].ID != v.ID {
		t.Fatal("property changed vehicle identity")
	}
	r := httptest.NewRequest("POST", "/customers/"+customer+"/vehicles", nil)
	r = r.WithContext(contextWithUser(ctx, &User{ShopID: shop, Role: "staff"}))
	w := httptest.NewRecorder()
	(&Server{store: s}).handleCustomerVehicle(w, r)
	if w.Code != 403 {
		t.Fatal("staff changed vehicle")
	}
}

package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type customerVehicle struct {
	ID, Name, Make, Model, Year, Identifier, Notes string
	Revision                                       int
}

func validateCustomerVehicle(v customerVehicle) error {
	if strings.TrimSpace(v.Name) == "" {
		return errors.New("Enter a vehicle name.")
	}
	for _, value := range []string{v.Name, v.Make, v.Model, v.Identifier} {
		if len(value) > 120 || strings.ContainsRune(value, '\x00') {
			return errors.New("Vehicle fields must be at most 120 bytes.")
		}
	}
	if len(v.Notes) > 2000 || strings.ContainsRune(v.Notes, '\x00') {
		return errors.New("Internal notes must be at most 2,000 bytes.")
	}
	if v.Year != "" {
		year, err := strconv.Atoi(v.Year)
		if err != nil || year < 1886 || year > 9999 {
			return errors.New("Enter a four-digit model year or leave it blank.")
		}
	}
	return nil
}

func (s *Store) ListCustomerVehicles(ctx context.Context, shopID, customerID string) ([]customerVehicle, error) {
	rows, err := s.db.Pool.Query(ctx, `SELECT id,name,make,model,COALESCE(model_year::text,''),identifier,internal_notes,revision FROM customer_vehicles WHERE shop_id=$1 AND customer_id=$2 ORDER BY name,id`, shopID, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vehicles []customerVehicle
	for rows.Next() {
		var v customerVehicle
		if err := rows.Scan(&v.ID, &v.Name, &v.Make, &v.Model, &v.Year, &v.Identifier, &v.Notes, &v.Revision); err != nil {
			return nil, err
		}
		vehicles = append(vehicles, v)
	}
	return vehicles, rows.Err()
}

var errVehicleConflict = errors.New("This vehicle changed or is unavailable. Reload the customer and review your changes.")

func (s *Store) SaveCustomerVehicle(ctx context.Context, shopID, customerID string, v customerVehicle) error {
	if err := validateCustomerVehicle(v); err != nil {
		return err
	}
	if v.ID == "" {
		tag, err := s.db.Pool.Exec(ctx, `INSERT INTO customer_vehicles(shop_id,customer_id,name,make,model,model_year,identifier,internal_notes) SELECT $1,id,$3,$4,$5,NULLIF($6,'')::int,$7,$8 FROM customers WHERE shop_id=$1 AND id=$2`, shopID, customerID, v.Name, v.Make, v.Model, v.Year, v.Identifier, v.Notes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errVehicleConflict
		}
		return nil
	}
	tag, err := s.db.Pool.Exec(ctx, `UPDATE customer_vehicles SET name=$4,make=$5,model=$6,model_year=NULLIF($7,'')::int,identifier=$8,internal_notes=$9,revision=revision+1 WHERE shop_id=$1 AND customer_id=$2 AND id=$3 AND revision=$10`, shopID, customerID, v.ID, v.Name, v.Make, v.Model, v.Year, v.Identifier, v.Notes, v.Revision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errVehicleConflict
	}
	return nil
}

func (s *Server) handleCustomerVehicle(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	if u == nil || u.Role != "owner" {
		http.Error(w, "Vehicle records are available to the owner.", 403)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form.", 400)
		return
	}
	v := customerVehicle{ID: r.PostForm.Get("vehicle_id"), Name: strings.TrimSpace(r.PostForm.Get("name")), Make: strings.TrimSpace(r.PostForm.Get("make")), Model: strings.TrimSpace(r.PostForm.Get("model")), Year: strings.TrimSpace(r.PostForm.Get("year")), Identifier: strings.TrimSpace(r.PostForm.Get("identifier")), Notes: strings.TrimSpace(r.PostForm.Get("notes"))}
	if v.ID != "" {
		var err error
		v.Revision, err = strconv.Atoi(r.PostForm.Get("revision"))
		if err != nil || v.Revision < 1 {
			http.Error(w, "Reload and try again.", 400)
			return
		}
	}
	if err := validateCustomerVehicle(v); err != nil {
		http.Error(w, err.Error(), 422)
		return
	}
	if err := s.store.SaveCustomerVehicle(r.Context(), u.ShopID, r.PathValue("id"), v); err != nil {
		if errors.Is(err, errVehicleConflict) {
			http.Error(w, err.Error(), 409)
		} else {
			http.Error(w, "Could not save the vehicle. Please retry.", 503)
		}
		return
	}
	http.Redirect(w, r, "/customers/"+url.PathEscape(r.PathValue("id"))+"?vehicle_saved=1#vehicles", 303)
}

package app

import (
	"log"
	"net/http"
	"strings"
)

func (s *Server) handleCustomerEdit(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	id := r.PathValue("id")
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if name != "" {
			if err := s.store.UpdateCustomer(r.Context(), user.ShopID, id, name, r.FormValue("email"), r.FormValue("phone")); err == nil {
				http.Redirect(w, r, "/customers/"+id, http.StatusSeeOther)
				return
			}
		}
	}
	s.renderCustomerEdit(w, r, user, id)
}

func (s *Server) handleCustomerDelete(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if err := s.store.DeleteCustomer(r.Context(), user.ShopID, r.PathValue("id")); err != nil {
		log.Printf("delete customer: %v", err)
		http.Error(w, "Customer cannot be deleted while agreements or jobs exist.", http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/customers", http.StatusSeeOther)
}

func (s *Server) handlePropertyCreate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	customerID := r.PathValue("id")
	if err := r.ParseForm(); err == nil {
		address, city := strings.TrimSpace(r.FormValue("address_line1")), strings.TrimSpace(r.FormValue("city"))
		state, postal := strings.TrimSpace(r.FormValue("state")), strings.TrimSpace(r.FormValue("postal_code"))
		if address != "" && city != "" && state != "" && postal != "" {
			if _, err := s.store.CreateProperty(r.Context(), user.ShopID, customerID, address, r.FormValue("address_line2"), city, state, postal); err != nil {
				log.Printf("create property: %v", err)
			}
		}
	}
	http.Redirect(w, r, "/customers/"+customerID, http.StatusSeeOther)
}

func (s *Server) handleEquipmentCreate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	propertyID := r.PathValue("id")
	if err := r.ParseForm(); err == nil {
		if kind := strings.TrimSpace(r.FormValue("kind")); kind != "" {
			if err := s.store.CreateEquipment(r.Context(), user.ShopID, propertyID, kind, r.FormValue("make"), r.FormValue("model"), r.FormValue("serial_number")); err != nil {
				log.Printf("create equipment: %v", err)
			}
		}
	}
	var customerID string
	if err := s.store.db.Pool.QueryRow(r.Context(), `SELECT customer_id FROM properties WHERE shop_id = $1 AND id = $2`, user.ShopID, propertyID).Scan(&customerID); err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/customers/"+customerID, http.StatusSeeOther)
}

func (s *Server) renderCustomerEdit(w http.ResponseWriter, r *http.Request, user *User, customerID string) {
	customer, err := s.store.GetCustomer(r.Context(), user.ShopID, customerID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	properties, err := s.store.ListProperties(r.Context(), user.ShopID, customerID)
	if err != nil {
		log.Printf("list properties: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	var vehicles []customerVehicle
	if user.Role == "owner" {
		vehicles, err = s.store.ListCustomerVehicles(r.Context(), user.ShopID, customerID)
		if err != nil {
			http.Error(w, "Could not load vehicle records. Please retry.", http.StatusServiceUnavailable)
			return
		}
	}
	s.renderLayout(w, r, "Edit customer — Sideyard", "customer_form_content", map[string]any{
		"Nav": NavData{Active: "customers", User: user}, "Customer": customer, "Properties": properties,
		"UpdateError": r.Method == http.MethodPost,
		"Vehicles":    vehicles, "VehicleSaved": r.URL.Query().Get("vehicle_saved") == "1",
	})
}

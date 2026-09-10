package app

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/wmichelin/clubdue/internal/database"
)

type customerDraft struct{ Name, Email, Phone string }

type propertyCreateDraft struct {
	AddressLine1, AddressLine2, City, State, PostalCode string
}

type equipmentCreateDraft struct {
	PropertyID, Kind, Make, Model, SerialNumber string
}

type customerEditState struct {
	Customer            *Customer
	UpdateError         string
	PropertyDraft       *propertyCreateDraft
	PropertyError       string
	EquipmentDraft      *equipmentCreateDraft
	EquipmentError      string
	VehicleDraft        *customerVehicle
	VehicleError        string
	Status              int
	CustomerUnavailable bool
	RelatedUnavailable  bool
}

func customerDraftFromRequest(r *http.Request) customerDraft {
	return customerDraft{Name: strings.TrimSpace(r.FormValue("name")), Email: r.FormValue("email"), Phone: r.FormValue("phone")}
}

func (s *Server) renderCustomersPage(w http.ResponseWriter, r *http.Request, user *User, draft customerDraft, message string, status int) {
	customers, err := s.store.ListCustomers(r.Context(), user.ShopID)
	unavailable := err != nil
	if unavailable && status == http.StatusOK {
		log.Printf("list customers unavailable request_id=%s", requestIDFromContext(r.Context()))
		http.Error(w, "Customers are temporarily unavailable. Please retry.", http.StatusServiceUnavailable)
		return
	}
	if status != http.StatusOK {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.renderLayout(w, r, "Customers — Sideyard", "customers_content", map[string]any{
		"Nav": NavData{Active: "customers", User: user}, "Customers": customers,
		"Draft": draft, "Error": message, "CustomersUnavailable": unavailable,
	})
}

func (s *Server) handleCustomerEdit(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	id := r.PathValue("id")
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		draft := customerDraftFromRequest(r)
		state := customerEditState{Customer: &Customer{ID: id, Name: draft.Name, Email: draft.Email, Phone: draft.Phone}}
		if draft.Name == "" || !validStoredText(draft.Name, draft.Email, draft.Phone) {
			state.UpdateError, state.Status = "Enter a customer name and valid contact details.", http.StatusUnprocessableEntity
			s.renderCustomerEdit(w, r, user, id, state)
			return
		}
		if err := s.store.UpdateCustomer(r.Context(), user.ShopID, id, draft.Name, draft.Email, draft.Phone); err != nil {
			if errors.Is(err, database.ErrNoRows) || errors.Is(err, errInvalidRecordID) {
				http.NotFound(w, r)
				return
			}
			log.Printf("update customer unavailable request_id=%s", requestIDFromContext(r.Context()))
			state.UpdateError, state.Status = "Could not confirm whether these customer changes were saved. Check the current customer before submitting these retained details again.", http.StatusServiceUnavailable
			s.renderCustomerEdit(w, r, user, id, state)
			return
		}
		http.Redirect(w, r, "/customers/"+url.PathEscape(id), http.StatusSeeOther)
		return
	}
	s.renderCustomerEdit(w, r, user, id, customerEditState{Status: http.StatusOK})
}

func (s *Server) handleCustomerDelete(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if err := s.store.DeleteCustomer(r.Context(), user.ShopID, r.PathValue("id")); err != nil {
		switch {
		case errors.Is(err, errCustomerDeleteConflict):
			http.Error(w, "Customer cannot be deleted while agreements or jobs exist.", http.StatusConflict)
		case errors.Is(err, database.ErrNoRows), errors.Is(err, errInvalidRecordID):
			http.NotFound(w, r)
		default:
			log.Printf("delete customer unavailable request_id=%s", requestIDFromContext(r.Context()))
			http.Error(w, "Could not confirm whether the customer was deleted. Reload the customer list before trying again.", http.StatusServiceUnavailable)
		}
		return
	}
	http.Redirect(w, r, "/customers", http.StatusSeeOther)
}

func (s *Server) handlePropertyCreate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	customerID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	draft := &propertyCreateDraft{AddressLine1: strings.TrimSpace(r.FormValue("address_line1")), AddressLine2: r.FormValue("address_line2"), City: strings.TrimSpace(r.FormValue("city")), State: strings.TrimSpace(r.FormValue("state")), PostalCode: strings.TrimSpace(r.FormValue("postal_code"))}
	state := customerEditState{PropertyDraft: draft}
	if draft.AddressLine1 == "" || draft.City == "" || draft.State == "" || draft.PostalCode == "" || !validStoredText(draft.AddressLine1, draft.AddressLine2, draft.City, draft.State, draft.PostalCode) {
		state.PropertyError, state.Status = "Enter an address, city, state / region and postal code.", http.StatusUnprocessableEntity
		s.renderCustomerEdit(w, r, user, customerID, state)
		return
	}
	if _, err := s.store.CreateProperty(r.Context(), user.ShopID, customerID, draft.AddressLine1, draft.AddressLine2, draft.City, draft.State, draft.PostalCode); err != nil {
		if errors.Is(err, database.ErrNoRows) || errors.Is(err, errInvalidRecordID) {
			http.NotFound(w, r)
			return
		}
		log.Printf("create property unavailable request_id=%s", requestIDFromContext(r.Context()))
		state.PropertyError, state.Status = "Could not confirm whether this property was saved. Check the current properties before submitting these retained details again.", http.StatusServiceUnavailable
		s.renderCustomerEdit(w, r, user, customerID, state)
		return
	}
	http.Redirect(w, r, "/customers/"+customerID, http.StatusSeeOther)
}

func (s *Server) handleEquipmentCreate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	propertyID := r.PathValue("id")
	if err := validateRecordIDs(user.ShopID, propertyID); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	draft := &equipmentCreateDraft{PropertyID: propertyID, Kind: strings.TrimSpace(r.FormValue("kind")), Make: r.FormValue("make"), Model: r.FormValue("model"), SerialNumber: r.FormValue("serial_number")}
	var customerID string
	if err := s.store.db.Pool.QueryRow(r.Context(), database.Statement(s.store.db.Pool.Dialect(), `SELECT customer_id FROM properties WHERE shop_id = $1 AND id = $2`, `SELECT LOWER(CONVERT(char(36),customer_id)) FROM dbo.properties WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), user.ShopID, propertyID).Scan(&customerID); err != nil {
		if errors.Is(err, database.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			log.Printf("load equipment parent unavailable request_id=%s", requestIDFromContext(r.Context()))
			s.renderCustomerEdit(w, r, user, "", customerEditState{EquipmentDraft: draft, EquipmentError: "The property is temporarily unavailable. These site-detail fields are retained below.", Status: http.StatusServiceUnavailable, CustomerUnavailable: true, RelatedUnavailable: true})
		}
		return
	}
	state := customerEditState{EquipmentDraft: draft}
	if draft.Kind == "" || !validStoredText(draft.Kind, draft.Make, draft.Model, draft.SerialNumber) {
		state.EquipmentError, state.Status = "Enter a valid site asset or detail.", http.StatusUnprocessableEntity
		s.renderCustomerEdit(w, r, user, customerID, state)
		return
	}
	if err := s.store.CreateEquipment(r.Context(), user.ShopID, propertyID, draft.Kind, draft.Make, draft.Model, draft.SerialNumber); err != nil {
		if errors.Is(err, database.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		log.Printf("create equipment unavailable request_id=%s", requestIDFromContext(r.Context()))
		state.EquipmentError, state.Status = "Could not confirm whether this site detail was saved. Check the current property before submitting these retained details again.", http.StatusServiceUnavailable
		s.renderCustomerEdit(w, r, user, customerID, state)
		return
	}
	http.Redirect(w, r, "/customers/"+customerID, http.StatusSeeOther)
}

func (s *Server) renderCustomerEdit(w http.ResponseWriter, r *http.Request, user *User, customerID string, state customerEditState) {
	if state.Status == 0 {
		state.Status = http.StatusOK
	}
	customer := state.Customer
	var err error
	if customer == nil && !state.CustomerUnavailable {
		customer, err = s.store.GetCustomer(r.Context(), user.ShopID, customerID)
		if err != nil {
			if state.Status == http.StatusOK {
				if errors.Is(err, database.ErrNoRows) || errors.Is(err, errInvalidRecordID) {
					http.NotFound(w, r)
				} else {
					http.Error(w, "Customer unavailable. Please retry.", http.StatusServiceUnavailable)
				}
				return
			}
			state.CustomerUnavailable, state.RelatedUnavailable = true, true
		}
	}
	if customer == nil {
		customer = &Customer{ID: customerID}
	}
	var properties []Property
	if !state.RelatedUnavailable {
		properties, err = s.store.ListProperties(r.Context(), user.ShopID, customerID)
		if err != nil {
			log.Printf("list properties unavailable request_id=%s", requestIDFromContext(r.Context()))
			if state.Status == http.StatusOK {
				http.Error(w, "Customer details are temporarily unavailable. Please retry.", http.StatusServiceUnavailable)
				return
			}
			state.RelatedUnavailable = true
		}
	}
	var vehicles []customerVehicle
	if user.Role == "owner" && !state.RelatedUnavailable {
		vehicles, err = s.store.ListCustomerVehicles(r.Context(), user.ShopID, customerID)
		if err != nil {
			if state.Status == http.StatusOK {
				http.Error(w, "Could not load vehicle records. Please retry.", http.StatusServiceUnavailable)
				return
			}
			state.RelatedUnavailable = true
		}
	}
	if state.Status != http.StatusOK {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(state.Status)
	}
	s.renderLayout(w, r, "Edit customer — Sideyard", "customer_form_content", map[string]any{
		"Nav": NavData{Active: "customers", User: user}, "Customer": customer, "Properties": properties,
		"UpdateError": state.UpdateError, "PropertyDraft": state.PropertyDraft, "PropertyError": state.PropertyError,
		"EquipmentDraft": state.EquipmentDraft, "EquipmentError": state.EquipmentError,
		"VehicleDraft": state.VehicleDraft, "VehicleError": state.VehicleError,
		"CustomerUnavailable": state.CustomerUnavailable, "RelatedUnavailable": state.RelatedUnavailable,
		"Vehicles": vehicles, "VehicleSaved": r.URL.Query().Get("vehicle_saved") == "1",
	})
}

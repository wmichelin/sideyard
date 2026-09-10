package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/wmichelin/clubdue/internal/database"
)

type propertyAddress struct {
	Property
	Revision int
}

var errPropertyAddressConflict = errors.New("This address changed in another tab or import. Reload the address and review your changes before saving.")

func validatePropertyAddress(p propertyAddress) error {
	if !validStoredText(p.AddressLine1, p.AddressLine2, p.City, p.State, p.PostalCode) {
		return errInvalidStoredText
	}

	for _, v := range []string{p.AddressLine1, p.City, p.State, p.PostalCode} {
		if strings.TrimSpace(v) == "" {
			return errors.New("Enter an address, city, state / region and postal code.")
		}
	}
	for _, v := range []string{p.AddressLine1, p.AddressLine2, p.City, p.State, p.PostalCode} {
		if len(v) > 200 || strings.ContainsRune(v, '\x00') {
			return errors.New("Each address field must be at most 200 bytes.")
		}
	}
	return nil
}

func (s *Store) GetPropertyAddress(ctx context.Context, shopID, id string) (propertyAddress, error) {
	if err := validateRecordIDs(shopID, id); err != nil {
		return propertyAddress{}, err
	}

	var p propertyAddress
	err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT id,customer_id,address_line1,COALESCE(address_line2,''),city,state,postal_code,address_revision FROM properties WHERE shop_id=$1 AND id=$2`, `SELECT LOWER(CONVERT(char(36),id)),LOWER(CONVERT(char(36),customer_id)),address_line1,COALESCE(address_line2,N''),city,state,postal_code,address_revision FROM dbo.properties WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, id).Scan(&p.ID, &p.CustomerID, &p.AddressLine1, &p.AddressLine2, &p.City, &p.State, &p.PostalCode, &p.Revision)
	return p, err
}

func (s *Store) SavePropertyAddress(ctx context.Context, shopID string, p propertyAddress) error {
	if err := validateRecordIDs(shopID, p.ID); err != nil {
		return err
	}

	if err := validatePropertyAddress(p); err != nil {
		return err
	}
	tag, err := s.db.Pool.Exec(ctx, database.Statement(s.db.Pool.Dialect(), `UPDATE properties SET address_line1=$3,address_line2=NULLIF($4,''),city=$5,state=$6,postal_code=$7,updated_at=now() WHERE shop_id=$1 AND id=$2 AND address_revision=$8`, `UPDATE dbo.properties SET address_line1=@p3,address_line2=CASE WHEN DATALENGTH(@p4)=0 THEN NULL ELSE @p4 END,city=@p5,state=@p6,postal_code=@p7,updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2) AND address_revision=@p8`), shopID, p.ID, p.AddressLine1, p.AddressLine2, p.City, p.State, p.PostalCode, p.Revision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errPropertyAddressConflict
	}
	return nil
}

func (s *Server) handlePropertyAddress(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	if u == nil || u.Role != "owner" {
		http.Error(w, "Address editing is available to the owner.", http.StatusForbidden)
		return
	}
	p, err := s.store.GetPropertyAddress(r.Context(), u.ShopID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, database.ErrNoRows) || errors.Is(err, errInvalidRecordID) {
			http.NotFound(w, r)
		} else {
			log.Printf("load property address: %v", err)
			http.Error(w, "Address unavailable. Please retry.", http.StatusServiceUnavailable)
		}
		return
	}
	message, status := "", http.StatusOK
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form.", http.StatusBadRequest)
			return
		}
		p.AddressLine1 = strings.TrimSpace(r.PostForm.Get("address_line1"))
		p.AddressLine2 = strings.TrimSpace(r.PostForm.Get("address_line2"))
		p.City = strings.TrimSpace(r.PostForm.Get("city"))
		p.State = strings.TrimSpace(r.PostForm.Get("state"))
		p.PostalCode = strings.TrimSpace(r.PostForm.Get("postal_code"))
		p.Revision, err = strconv.Atoi(r.PostForm.Get("revision"))
		if err != nil || p.Revision < 1 {
			message, status = "Reload the address before saving.", http.StatusBadRequest
		} else if err := validatePropertyAddress(p); err != nil {
			message, status = err.Error(), http.StatusUnprocessableEntity
		} else if err := s.store.SavePropertyAddress(r.Context(), u.ShopID, p); err != nil {
			if errors.Is(err, errPropertyAddressConflict) {
				message, status = err.Error(), http.StatusConflict
			} else {
				log.Printf("save property address: %v", err)
				message, status = "Could not save the address. Please retry.", http.StatusServiceUnavailable
			}
		} else {
			http.Redirect(w, r, "/properties/"+url.PathEscape(p.ID)+"/address?saved=1", http.StatusSeeOther)
			return
		}
	}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.renderLayout(w, r, "Correct property address — Sideyard", "property_address_content", map[string]any{"Nav": NavData{Active: "customers", User: u}, "Property": p, "Error": message, "Saved": r.Method == http.MethodGet && r.URL.Query().Get("saved") == "1"})
}

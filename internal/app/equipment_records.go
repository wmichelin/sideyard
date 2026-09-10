package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

type equipmentRecord struct {
	Equipment
	CustomerID, Address, Location, InstalledOn, WarrantyUntil, Notes string
	Revision                                                         int
}

var errEquipmentConflict = errors.New("This equipment changed in another tab. Reload the record and review your changes before saving.")

func validateEquipmentRecord(e equipmentRecord) error {
	if !validStoredText(e.Kind, e.Make, e.Model, e.SerialNumber, e.Location, e.Notes) {
		return errInvalidStoredText
	}

	if strings.TrimSpace(e.Kind) == "" {
		return errors.New("Enter an equipment name or type.")
	}
	for _, v := range []string{e.Kind, e.Make, e.Model, e.SerialNumber, e.Location} {
		if len(v) > 120 || strings.ContainsRune(v, '\x00') {
			return errors.New("Equipment fields must be at most 120 bytes.")
		}
	}
	if len(e.Notes) > 2000 || strings.ContainsRune(e.Notes, '\x00') {
		return errors.New("Internal service details must be at most 2,000 bytes.")
	}
	for _, date := range []string{e.InstalledOn, e.WarrantyUntil} {
		if date != "" {
			parsed, err := time.Parse("2006-01-02", date)
			if err != nil || parsed.Year() < 1 || parsed.Format("2006-01-02") != date {
				return errors.New("Enter a valid installation and warranty date, or leave them blank.")
			}
		}
	}
	if e.InstalledOn != "" && e.WarrantyUntil != "" && e.WarrantyUntil < e.InstalledOn {
		return errors.New("The warranty end date must be on or after the installation date.")
	}
	return nil
}

func (s *Store) GetEquipmentRecord(ctx context.Context, shopID, id string) (equipmentRecord, error) {
	if err := validateRecordIDs(shopID, id); err != nil {
		return equipmentRecord{}, err
	}

	var e equipmentRecord
	err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `SELECT e.id,e.property_id,e.kind,COALESCE(e.make,''),COALESCE(e.model,''),COALESCE(e.serial_number,''),e.location,COALESCE(to_char(e.installed_on,'YYYY-MM-DD'),''),COALESCE(to_char(e.warranty_until,'YYYY-MM-DD'),''),e.internal_notes,e.revision,p.customer_id,p.address_line1
		FROM equipment e JOIN properties p ON p.id=e.property_id AND p.shop_id=e.shop_id
		WHERE e.shop_id=$1 AND e.id=$2`, `SELECT LOWER(CONVERT(char(36),e.id)),LOWER(CONVERT(char(36),e.property_id)),e.kind,COALESCE(e.make,N''),COALESCE(e.model,N''),COALESCE(e.serial_number,N''),e.location,COALESCE(CONVERT(char(10),e.installed_on,23),''),COALESCE(CONVERT(char(10),e.warranty_until,23),''),e.internal_notes,e.revision,LOWER(CONVERT(char(36),p.customer_id)),p.address_line1 FROM dbo.equipment e JOIN dbo.properties p ON p.id=e.property_id AND p.shop_id=e.shop_id WHERE e.shop_id=CONVERT(uniqueidentifier,@p1) AND e.id=CONVERT(uniqueidentifier,@p2)`), shopID, id).Scan(&e.ID, &e.PropertyID, &e.Kind, &e.Make, &e.Model, &e.SerialNumber, &e.Location, &e.InstalledOn, &e.WarrantyUntil, &e.Notes, &e.Revision, &e.CustomerID, &e.Address)
	return e, err
}

func (s *Store) SaveEquipmentRecord(ctx context.Context, shopID string, e equipmentRecord) error {
	if err := validateRecordIDs(shopID, e.ID, e.PropertyID); err != nil {
		return err
	}

	if err := validateEquipmentRecord(e); err != nil {
		return err
	}
	tag, err := s.db.Pool.Exec(ctx, database.Statement(s.db.Pool.Dialect(), `UPDATE equipment SET kind=$4,make=NULLIF($5,''),model=NULLIF($6,''),serial_number=NULLIF($7,''),location=$8,installed_on=NULLIF($9,'')::date,warranty_until=NULLIF($10,'')::date,internal_notes=$11,revision=revision+1
		WHERE shop_id=$1 AND id=$2 AND property_id=$3 AND revision=$12`, `UPDATE dbo.equipment SET kind=@p4,make=CASE WHEN DATALENGTH(@p5)=0 THEN NULL ELSE @p5 END,model=CASE WHEN DATALENGTH(@p6)=0 THEN NULL ELSE @p6 END,serial_number=CASE WHEN DATALENGTH(@p7)=0 THEN NULL ELSE @p7 END,location=@p8,installed_on=CONVERT(date,NULLIF(@p9,'')),warranty_until=CONVERT(date,NULLIF(@p10,'')),internal_notes=@p11,revision=revision+1 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2) AND property_id=CONVERT(uniqueidentifier,@p3) AND revision=@p12`), shopID, e.ID, e.PropertyID, e.Kind, e.Make, e.Model, e.SerialNumber, e.Location, e.InstalledOn, e.WarrantyUntil, e.Notes, e.Revision)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errEquipmentConflict
	}
	return nil
}

func (s *Server) handleEquipmentRecord(w http.ResponseWriter, r *http.Request) {
	u := userFromContext(r.Context())
	if u == nil || u.Role != "owner" {
		http.Error(w, "Equipment records are available to the owner.", http.StatusForbidden)
		return
	}
	e, err := s.store.GetEquipmentRecord(r.Context(), u.ShopID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, database.ErrNoRows) || errors.Is(err, errInvalidRecordID) {
			http.NotFound(w, r)
		} else {
			log.Printf("load equipment record: %v", err)
			http.Error(w, "Equipment record unavailable. Please retry.", http.StatusServiceUnavailable)
		}
		return
	}
	var message string
	status := http.StatusOK
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form.", http.StatusBadRequest)
			return
		}
		e.Kind = strings.TrimSpace(r.PostForm.Get("kind"))
		e.Make = strings.TrimSpace(r.PostForm.Get("make"))
		e.Model = strings.TrimSpace(r.PostForm.Get("model"))
		e.SerialNumber = strings.TrimSpace(r.PostForm.Get("serial_number"))
		e.Location = strings.TrimSpace(r.PostForm.Get("location"))
		e.InstalledOn = r.PostForm.Get("installed_on")
		e.WarrantyUntil = r.PostForm.Get("warranty_until")
		e.Notes = strings.TrimSpace(r.PostForm.Get("notes"))
		e.Revision, err = strconv.Atoi(r.PostForm.Get("revision"))
		if err != nil || e.Revision < 1 {
			message, status = "Reload the record before saving.", http.StatusBadRequest
		} else if err := validateEquipmentRecord(e); err != nil {
			message, status = err.Error(), http.StatusUnprocessableEntity
		} else if err := s.store.SaveEquipmentRecord(r.Context(), u.ShopID, e); err != nil {
			if errors.Is(err, errEquipmentConflict) {
				message, status = err.Error(), http.StatusConflict
			} else {
				log.Printf("save equipment record: %v", err)
				message, status = "Could not save the equipment. Please retry.", http.StatusServiceUnavailable
			}
		} else {
			http.Redirect(w, r, "/equipment/"+url.PathEscape(e.ID)+"?saved=1", http.StatusSeeOther)
			return
		}
	}
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.renderLayout(w, r, "Equipment record — Sideyard", "equipment_record_content", map[string]any{"Nav": NavData{Active: "customers", User: u}, "Equipment": e, "Error": message, "Saved": r.Method == http.MethodGet && r.URL.Query().Get("saved") == "1"})
}

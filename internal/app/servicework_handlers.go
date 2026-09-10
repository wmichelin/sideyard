package app

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func (s *Server) handleServiceScheduleCreate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	agreementID := r.PathValue("id")
	agreement, err := s.store.GetAgreement(r.Context(), user.ShopID, agreementID)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) || errors.Is(err, errInvalidRecordID) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "Agreement details are temporarily unavailable. Please retry.", http.StatusServiceUnavailable)
		}
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	parsedInterval, intervalErr := strconv.ParseInt(r.FormValue("cadence_interval"), 10, 32)
	interval := int(parsedInterval)
	startsOn, startErr := time.Parse("2006-01-02", r.FormValue("starts_on"))
	endsOn, endErr := serviceScheduleEndDate(r.FormValue("ends_on"))
	input := ServiceScheduleInput{
		AgreementID:             agreementID,
		ExternalID:              strings.TrimSpace(r.FormValue("external_id")),
		ServiceName:             strings.TrimSpace(r.FormValue("service_name")),
		CadenceUnit:             strings.TrimSpace(r.FormValue("cadence_unit")),
		CadenceInterval:         interval,
		StartsOn:                startsOn,
		EndsOn:                  endsOn,
		CountsTowardEntitlement: r.FormValue("counts_toward_entitlement") == "1",
	}
	if startErr != nil || endErr != nil || intervalErr != nil {
		err = errInvalidServiceSchedule
	} else {
		err = validateServiceSchedule(input)
	}
	if err == nil {
		_, err = s.store.CreateServiceSchedule(r.Context(), user.ShopID, input)
		if err != nil {
			log.Printf("create service schedule: %v", err)
			if errors.Is(err, database.ErrNoRows) {
				http.Error(w, "This agreement is no longer available for new recurring services. Reload it to check its status.", http.StatusConflict)
			} else {
				http.Error(w, "Could not confirm the new recurring service. Check the agreement's recurring services before trying again.", http.StatusServiceUnavailable)
			}
			return
		}
	}
	if err != nil {
		log.Printf("create service schedule: %v", err)
		s.renderAgreementFormStatus(w, r, user, agreementFormData{
			ID: agreement.ID, CustomerID: agreement.CustomerID, PropertyID: agreement.PropertyID,
			PlanTemplateID: agreement.PlanTemplateID, PlanName: agreement.PlanName,
			Status: string(agreement.Status), AnniversaryDate: agreement.AnniversaryDate.Format("2006-01-02"),
			VisitsEntitled: agreement.VisitsEntitled, VisitsUsed: agreement.VisitsUsed,
			PriceCents: agreement.PriceCents, Notes: agreement.Notes,
			Error:        "Could not add that recurring service. Check its name, cadence, and dates.",
			ServiceDraft: &serviceScheduleFormData{Name: r.FormValue("service_name"), Unit: r.FormValue("cadence_unit"), Interval: r.FormValue("cadence_interval"), StartsOn: r.FormValue("starts_on"), EndsOn: r.FormValue("ends_on"), Counts: input.CountsTowardEntitlement},
		}, http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/agreements/"+agreementID, http.StatusSeeOther)
}

func (s *Server) handleServiceScheduleUpdate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var active bool
	switch r.FormValue("action") {
	case "pause":
		active = false
	case "resume":
		active = true
	default:
		http.Error(w, "invalid schedule action", http.StatusBadRequest)
		return
	}
	if err := s.store.SetServiceScheduleActive(r.Context(), user.ShopID, r.PathValue("id"), active); err != nil {
		if errors.Is(err, database.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			log.Printf("change service schedule: %v", err)
			http.Error(w, "Could not confirm the schedule change. Reload the agreement to check whether this service is paused or active before retrying.", http.StatusServiceUnavailable)
		}
		return
	}
	agreementID := r.FormValue("agreement_id")
	http.Redirect(w, r, "/agreements/"+agreementID, http.StatusSeeOther)
}

var errInvalidServiceSchedule = &serviceScheduleInputError{}

type serviceScheduleInputError struct{}

func (*serviceScheduleInputError) Error() string { return "invalid service schedule" }

type serviceScheduleFormData struct {
	Name, Unit, Interval, StartsOn, EndsOn string
	Counts                                 bool
}

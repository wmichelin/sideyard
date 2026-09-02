package app

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (s *Server) handleServiceScheduleCreate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	agreementID := r.PathValue("id")
	agreement, err := s.store.GetAgreement(r.Context(), user.ShopID, agreementID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	interval, _ := strconv.Atoi(r.FormValue("cadence_interval"))
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
	if startErr != nil || endErr != nil {
		err = errInvalidServiceSchedule
	} else {
		err = validateServiceSchedule(input)
	}
	if err == nil {
		_, err = s.store.CreateServiceSchedule(r.Context(), user.ShopID, input)
	}
	if err != nil {
		log.Printf("create service schedule: %v", err)
		s.renderAgreementForm(w, r, user, agreementFormData{
			ID: agreement.ID, CustomerID: agreement.CustomerID, PropertyID: agreement.PropertyID,
			PlanTemplateID: agreement.PlanTemplateID, PlanName: agreement.PlanName,
			Status: string(agreement.Status), AnniversaryDate: agreement.AnniversaryDate.Format("2006-01-02"),
			VisitsEntitled: agreement.VisitsEntitled, VisitsUsed: agreement.VisitsUsed,
			PriceCents: agreement.PriceCents, Notes: agreement.Notes,
			Error: "Could not add that recurring service. Check its name, cadence, and dates.",
		})
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
		http.NotFound(w, r)
		return
	}
	agreementID := r.FormValue("agreement_id")
	http.Redirect(w, r, "/agreements/"+agreementID, http.StatusSeeOther)
}

var errInvalidServiceSchedule = &serviceScheduleInputError{}

type serviceScheduleInputError struct{}

func (*serviceScheduleInputError) Error() string { return "invalid service schedule" }

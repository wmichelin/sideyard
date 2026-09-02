package app

import (
	"log"
	"net/http"
)

func (s *Server) handleVisits(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	filter := r.URL.Query().Get("filter")
	dueOnly := filter != "all"

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		action := r.FormValue("action")
		occurrenceIDs := r.Form["occurrence_id"]
		if (action == "completed" || action == "skipped") && len(occurrenceIDs) > 0 {
			if err := s.store.UpdateServiceOccurrences(r.Context(), user.ShopID, occurrenceIDs, action, r.FormValue("notes")); err != nil {
				log.Printf("service occurrence update: %v", err)
				http.Error(w, "Could not save the service outcome.", http.StatusBadRequest)
				return
			}
		}
		visitIDs := r.Form["visit_id"]
		if (action == "scheduled" || action == "completed") && len(visitIDs) > 0 {
			if err := s.store.BulkUpdateVisitStatus(r.Context(), user.ShopID, visitIDs, action); err != nil {
				log.Printf("bulk visit update: %v", err)
				http.Error(w, "server error", http.StatusInternalServerError)
				return
			}
		}
		redirectFilter := "due"
		if !dueOnly {
			redirectFilter = "all"
		}
		if f := r.FormValue("filter"); f != "" {
			redirectFilter = f
		}
		http.Redirect(w, r, "/visits?filter="+redirectFilter, http.StatusSeeOther)
		return
	}

	visits, err := s.store.ListVisitDues(r.Context(), user.ShopID, dueOnly)
	if err != nil {
		log.Printf("list visits: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	serviceOccurrences, err := s.store.ListServiceOccurrences(r.Context(), user.ShopID, dueOnly)
	if err != nil {
		log.Printf("list recurring service occurrences: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	type row struct {
		VisitDueRow
		StatusLabel string
	}
	rows := make([]row, len(visits))
	for i, v := range visits {
		rows[i] = row{VisitDueRow: v, StatusLabel: visitStatusLabel(v.Status)}
	}

	s.renderLayout(w, r, "Service work — ClubDue", "visits_content", map[string]any{
		"Nav":                NavData{Active: "visits", User: user},
		"Visits":             rows,
		"ServiceOccurrences": serviceOccurrences,
		"Filter":             filter,
		"DueOnly":            dueOnly,
	})
}

func (s *Server) handleVisitsExport(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	dueOnly := r.URL.Query().Get("filter") != "all"

	rows, err := s.store.VisitExportRows(r.Context(), user.ShopID, dueOnly)
	if err != nil {
		log.Printf("visit export: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	serviceRows, err := s.store.ServiceOccurrenceExportRows(r.Context(), user.ShopID, dueOnly)
	if err != nil {
		log.Printf("recurring service export: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	for _, serviceRow := range serviceRows {
		rows = append(rows, VisitExportRow{
			CustomerName: serviceRow.CustomerName,
			Phone:        serviceRow.Phone,
			Address:      serviceRow.Address,
			ServiceType:  serviceRow.ServiceType,
			Notes:        serviceRow.Notes,
		})
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="visit-export.csv"`)
	if err := writeVisitExportCSV(w, rows); err != nil {
		log.Printf("write csv: %v", err)
	}
}

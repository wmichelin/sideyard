package main

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
		visitIDs := r.Form["visit_id"]
		if action != "" && len(visitIDs) > 0 {
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

	type row struct {
		VisitDueRow
		StatusLabel string
	}
	rows := make([]row, len(visits))
	for i, v := range visits {
		rows[i] = row{VisitDueRow: v, StatusLabel: visitStatusLabel(v.Status)}
	}

	s.renderLayout(w, "Visits — ClubDue", "visits_content", map[string]any{
		"Nav":     NavData{Active: "visits", User: user},
		"Visits":  rows,
		"Filter":  filter,
		"DueOnly": dueOnly,
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

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="visit-export.csv"`)
	if err := writeVisitExportCSV(w, rows); err != nil {
		log.Printf("write csv: %v", err)
	}
}

package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/servicework"
	visitdomain "github.com/wmichelin/clubdue/internal/visits"
	"log"
	"net/http"
	"sort"
	"strings"
)

type workSubmission struct {
	kind, action, filter string
	ids                  []string
}

func parseWorkSubmission(r *http.Request) (workSubmission, error) {
	submission := workSubmission{action: r.PostForm.Get("action"), filter: "due"}
	filter := r.PostForm.Get("filter")
	if filter == "" {
		filter = r.URL.Query().Get("filter")
	}
	if filter == "all" {
		submission.filter = "all"
	}
	recurring, seasonal := r.PostForm["occurrence_id"], r.PostForm["visit_id"]
	if (len(recurring) == 0 && len(seasonal) == 0) || (len(recurring) > 0 && len(seasonal) > 0) {
		return submission, fmt.Errorf("Select visits from one work group before saving.")
	}
	submission.kind, submission.ids = "recurring", recurring
	if len(seasonal) > 0 {
		submission.kind, submission.ids = "seasonal", seasonal
	}
	if submission.action != "completed" &&
		!(submission.kind == "recurring" && submission.action == "skipped") &&
		!(submission.kind == "seasonal" && submission.action == "scheduled") {
		return submission, fmt.Errorf("Choose a valid outcome for the selected work.")
	}
	unique := make(map[string]bool, len(submission.ids))
	ids := make([]string, 0, len(submission.ids))
	for _, id := range submission.ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return submission, fmt.Errorf("Select valid visits before saving.")
		}
		if !unique[id] {
			unique[id] = true
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	submission.ids = ids
	return submission, nil
}

func workSuccessMessage(submission workSubmission) string {
	label := "recurring service"
	if submission.kind == "seasonal" {
		label = "seasonal visit"
	}
	if len(submission.ids) != 1 {
		label += "s"
	}
	outcome := submission.action
	if outcome == "scheduled" {
		outcome = "handed off"
	}
	return fmt.Sprintf("Saved: %d %s marked %s.", len(submission.ids), label, outcome)
}

func (s *Server) handleVisits(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	dueOnly := r.URL.Query().Get("filter") != "all"
	if r.Method != http.MethodPost {
		s.renderVisitsPage(w, r, user, dueOnly, http.StatusOK, "")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	submission, err := parseWorkSubmission(r)
	dueOnly = submission.filter != "all"
	if err != nil {
		s.workSaveError(w, r, user, dueOnly, err.Error())
		return
	}
	if submission.kind == "recurring" {
		err = s.store.UpdateServiceOccurrences(r.Context(), user.ShopID, submission.ids, submission.action, r.PostForm.Get("notes"))
	} else {
		err = s.store.BulkUpdateVisitStatus(r.Context(), user.ShopID, submission.ids, submission.action)
	}
	if err != nil {
		log.Printf("work outcome request_id=%s kind=%s error=%v", requestIDFromContext(r.Context()), submission.kind, err)
		if !errors.Is(err, database.ErrNoRows) && !errors.Is(err, servicework.ErrOutcomeInput) && !errors.Is(err, servicework.ErrOutcomeConflict) && !errors.Is(err, visitdomain.ErrOutcomeInput) && !errors.Is(err, visitdomain.ErrOutcomeConflict) {
			s.workSaveErrorStatus(w, r, user, dueOnly, "Could not confirm this save. Reload to check the recorded outcomes before retrying.", http.StatusServiceUnavailable)
			return
		}
		s.workSaveError(w, r, user, dueOnly, "Could not save this selection. A visit may have changed or reached its service limit. Review the selected visits and try again.")
		return
	}
	if !strings.Contains(r.Header.Get("Accept"), "application/json") {
		http.Redirect(w, r, "/visits?filter="+submission.filter+"&saved="+submission.kind+"-"+submission.action, http.StatusSeeOther)
		return
	}

	// Refresh only the saved group so selections in the other group survive.
	message := workSuccessMessage(submission)
	data, loadErr := s.visitsPageData(r, user, dueOnly)
	var html bytes.Buffer
	if loadErr == nil {
		loadErr = s.tmpl.ExecuteTemplate(&html, submission.kind+"_work_form", data)
	}
	if loadErr != nil {
		// The mutation committed. Do not describe a failed refresh as a failed save.
		log.Printf("work refresh request_id=%s error=%v", requestIDFromContext(r.Context()), loadErr)
		html.Reset()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(map[string]any{"saved": true, "message": message, "html": html.String()}); err != nil {
		log.Printf("work response request_id=%s error=%v", requestIDFromContext(r.Context()), err)
	}
}

func (s *Server) workSaveError(w http.ResponseWriter, r *http.Request, user *User, dueOnly bool, message string) {
	s.workSaveErrorStatus(w, r, user, dueOnly, message, http.StatusBadRequest)
}
func (s *Server) workSaveErrorStatus(w http.ResponseWriter, r *http.Request, user *User, dueOnly bool, message string, status int) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(map[string]string{"error": message}); err != nil {
			log.Printf("work error response: %v", err)
		}
		return
	}
	s.renderVisitsPage(w, r, user, dueOnly, status, message)
}

func (s *Server) visitsPageData(r *http.Request, user *User, dueOnly bool) (map[string]any, error) {
	visits, err := s.store.ListVisitDues(r.Context(), user.ShopID, dueOnly)
	if err != nil {
		return nil, err
	}
	occurrences, err := s.store.ListServiceOccurrences(r.Context(), user.ShopID, dueOnly)
	if err != nil {
		return nil, err
	}
	type row struct {
		VisitDueRow
		StatusLabel string
	}
	rows := make([]row, len(visits))
	for i, v := range visits {
		rows[i] = row{VisitDueRow: v, StatusLabel: visitStatusLabel(v.Status)}
	}
	return map[string]any{
		"Nav": NavData{Active: "visits", User: user}, "CSRFToken": csrfTokenFromContext(r.Context()),
		"Visits": rows, "ServiceOccurrences": occurrences, "DueOnly": dueOnly,
		"SelectedRecurring": map[string]bool{}, "SelectedSeasonal": map[string]bool{},
	}, nil
}

func (s *Server) renderVisitsPage(w http.ResponseWriter, r *http.Request, user *User, dueOnly bool, status int, message string) {
	data, err := s.visitsPageData(r, user, dueOnly)
	if err != nil {
		log.Printf("list work request_id=%s error=%v", requestIDFromContext(r.Context()), err)
		feedback := "Could not load service work. Reload this page to try again."
		if message != "" {
			feedback = message + " The work list is temporarily unavailable."
		}
		http.Error(w, feedback, http.StatusServiceUnavailable)
		return
	}
	if message != "" {
		data["WorkError"] = message
		for _, id := range r.PostForm["occurrence_id"] {
			data["SelectedRecurring"].(map[string]bool)[id] = true
		}
		for _, id := range r.PostForm["visit_id"] {
			data["SelectedSeasonal"].(map[string]bool)[id] = true
		}
	} else {
		switch r.URL.Query().Get("saved") {
		case "recurring-completed":
			data["WorkSuccess"] = "Selected recurring services marked completed."
		case "recurring-skipped":
			data["WorkSuccess"] = "Selected recurring services marked skipped."
		case "seasonal-completed":
			data["WorkSuccess"] = "Selected seasonal visits marked completed."
		case "seasonal-scheduled":
			data["WorkSuccess"] = "Selected seasonal visits marked handed off."
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	s.renderLayout(w, r, "Service work — Sideyard", "visits_content", data)
}

func (s *Server) handleVisitsExport(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	dueOnly := r.URL.Query().Get("filter") != "all"

	rows, err := s.store.VisitExportRows(r.Context(), user.ShopID, dueOnly)
	if err != nil {
		log.Printf("visit export: %v", err)
		http.Error(w, "server error", http.StatusServiceUnavailable)
		return
	}
	serviceRows, err := s.store.ServiceOccurrenceExportRows(r.Context(), user.ShopID, dueOnly)
	if err != nil {
		log.Printf("recurring service export: %v", err)
		http.Error(w, "server error", http.StatusServiceUnavailable)
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

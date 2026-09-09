package app

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/wmichelin/clubdue/internal/database"
)

func (s *Server) handleVehicleHistory(w http.ResponseWriter, r *http.Request) {
	s.handleAssetHistory(w, r, "vehicle")
}
func (s *Server) handleEquipmentHistory(w http.ResponseWriter, r *http.Request) {
	s.handleAssetHistory(w, r, "equipment")
}

func (s *Server) handleAssetHistory(w http.ResponseWriter, r *http.Request, kind string) {
	s.handleInternalHistory(w, r, kind)
}

func (s *Server) handleRecurringVisitHistory(w http.ResponseWriter, r *http.Request) {
	s.handleInternalHistory(w, r, "recurring")
}

func (s *Server) handleSeasonalVisitHistory(w http.ResponseWriter, r *http.Request) {
	s.handleInternalHistory(w, r, "seasonal")
}

func (s *Server) handleInternalHistory(w http.ResponseWriter, r *http.Request, kind string) {
	w.Header().Set("Cache-Control", "private, no-store")
	u := userFromContext(r.Context())
	if u == nil || u.Role != "owner" {
		http.Error(w, "Service histories are available to the owner.", 403)
		return
	}
	getTarget, addNote, listNotes := s.store.GetAssetHistoryTarget, s.store.AddAssetNote, s.store.ListAssetNotes
	nav, backLabel := "customers", "Back to asset"
	description := "Record inspections, service work and follow-ups for this asset."
	if kind == "recurring" || kind == "seasonal" {
		getTarget, addNote, listNotes = s.store.GetVisitHistoryTarget, s.store.AddVisitNote, s.store.ListVisitNotes
		nav, backLabel = "visits", "Back to service work"
		description = "Record observations for this visit. Saving a note leaves the visit's status and billing unchanged."
	}
	target, err := getTarget(r.Context(), u.ShopID, kind, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			log.Printf("load internal history kind=%s: %v", kind, err)
			http.Error(w, "History unavailable. Please retry.", 503)
		}
		return
	}
	input := internalNoteInput{Date: s.store.nowUTC().Format("2006-01-02"), Zone: "UTC"}
	message, status := "", 200
	var choices []eventTimeChoice
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form.", 400)
			return
		}
		input = internalNoteInput{Key: r.PostForm.Get("note_key"), Body: r.PostForm.Get("note_body"), Date: r.PostForm.Get("occurred_on"), Time: r.PostForm.Get("event_time"), Zone: strings.TrimSpace(r.PostForm.Get("event_timezone")), Offset: r.PostForm.Get("event_offset")}
		if _, err := validateInternalNote(input, s.store.nowUTC()); err != nil {
			message, status = err.Error(), 422
			var ambiguous *ambiguousEventTime
			if errors.As(err, &ambiguous) {
				choices = ambiguous.Choices
			}
		} else if err := addNote(r.Context(), u.ShopID, kind, target.ID, u.Email, input); err != nil {
			if errors.Is(err, errAssetNoteConflict) || errors.Is(err, errVisitNoteConflict) {
				message, status = err.Error(), 409
			} else {
				log.Printf("save internal note kind=%s: %v", kind, err)
				message, status = "Could not confirm the save. Retry this same note.", 503
			}
		} else {
			http.Redirect(w, r, r.URL.Path+"?saved=1", 303)
			return
		}
	}
	if !noteSubmissionKey.MatchString(input.Key) {
		input.Key, err = randomToken(16)
		if err != nil {
			http.Error(w, "Could not prepare a note. Reload and retry.", 503)
			return
		}
	}
	notes, err := listNotes(r.Context(), u.ShopID, kind, target.ID)
	if err != nil {
		http.Error(w, "Could not load notes. Reload and retry.", 503)
		return
	}
	if status != 200 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.renderLayout(w, r, "Service history — Sideyard", "asset_history_content", map[string]any{"Nav": NavData{Active: nav, User: u}, "Target": target, "BackLabel": backLabel, "Description": description, "Input": input, "Notes": notes, "Choices": choices, "Error": message, "Saved": r.Method == http.MethodGet && r.URL.Query().Get("saved") == "1", "Initial": r.Method == http.MethodGet, "HistoryURL": r.URL.Path})
}

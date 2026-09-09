package app

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
)

func (s *Server) handleVehicleHistory(w http.ResponseWriter, r *http.Request) {
	s.handleAssetHistory(w, r, "vehicle")
}
func (s *Server) handleEquipmentHistory(w http.ResponseWriter, r *http.Request) {
	s.handleAssetHistory(w, r, "equipment")
}

func (s *Server) handleAssetHistory(w http.ResponseWriter, r *http.Request, kind string) {
	w.Header().Set("Cache-Control", "private, no-store")
	u := userFromContext(r.Context())
	if u == nil || u.Role != "owner" {
		http.Error(w, "Service histories are available to the owner.", 403)
		return
	}
	target, err := s.store.GetAssetHistoryTarget(r.Context(), u.ShopID, kind, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			log.Printf("load asset history: %v", err)
			http.Error(w, "History unavailable. Please retry.", 503)
		}
		return
	}
	input := assetNoteInput{Date: s.store.nowUTC().Format("2006-01-02"), Zone: "UTC"}
	message, status := "", 200
	var choices []eventTimeChoice
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form.", 400)
			return
		}
		input = assetNoteInput{Key: r.PostForm.Get("note_key"), Body: r.PostForm.Get("note_body"), Date: r.PostForm.Get("occurred_on"), Time: r.PostForm.Get("event_time"), Zone: strings.TrimSpace(r.PostForm.Get("event_timezone")), Offset: r.PostForm.Get("event_offset")}
		if _, err := validateAssetNote(kind, input, s.store.nowUTC()); err != nil {
			message, status = err.Error(), 422
			var ambiguous *ambiguousEventTime
			if errors.As(err, &ambiguous) {
				choices = ambiguous.Choices
			}
		} else if err := s.store.AddAssetNote(r.Context(), u.ShopID, kind, target.ID, u.Email, input); err != nil {
			if errors.Is(err, errAssetNoteConflict) {
				message, status = err.Error(), 409
			} else {
				log.Printf("save asset note: %v", err)
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
	notes, err := s.store.ListAssetNotes(r.Context(), u.ShopID, kind, target.ID)
	if err != nil {
		http.Error(w, "Could not load notes. Reload and retry.", 503)
		return
	}
	if status != 200 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.renderLayout(w, r, "Service history — Sideyard", "asset_history_content", map[string]any{"Nav": NavData{Active: "customers", User: u}, "Target": target, "Input": input, "Notes": notes, "Choices": choices, "Error": message, "Saved": r.Method == http.MethodGet && r.URL.Query().Get("saved") == "1", "Initial": r.Method == http.MethodGet, "HistoryURL": r.URL.Path})
}

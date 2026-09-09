package app

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
	"github.com/wmichelin/clubdue/internal/servicework"
)

func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	schedules, err := s.store.ListServiceScheduleSummaries(r.Context(), user.ShopID)
	if err != nil {
		log.Printf("schedule list request_id=%s error=%v", requestIDFromContext(r.Context()), err)
		http.Error(w, "Could not load the schedule. Reload to try again.", http.StatusInternalServerError)
		return
	}
	s.renderLayout(w, r, "Schedule — Sideyard", "schedule_content", map[string]any{
		"Nav": NavData{Active: "schedule", User: user}, "Schedules": schedules,
	})
}

func (s *Server) handleVisitDate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	id := r.PathValue("id")
	visit, err := s.store.GetServiceVisitDate(r.Context(), user.ShopID, id)
	if err != nil {
		if errors.Is(err, database.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			log.Printf("visit date load request_id=%s error=%v", requestIDFromContext(r.Context()), err)
			http.Error(w, "Could not load this visit.", http.StatusInternalServerError)
		}
		return
	}
	value := visit.CurrentOn.Format("2006-01-02")
	calendarContext := r.URL.Query().Get("calendar_day")
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Could not read the date change.", http.StatusBadRequest)
			return
		}
		calendarContext = r.PostForm.Get("calendar_day")
	}
	calendarDay, hasCalendar := calendarDate(calendarContext)
	returnCalendar, savedCalendar := "", ""
	if hasCalendar {
		calendarContext = calendarDay.Format("2006-01-02")
		returnCalendar = calendarURL(calendarDay, 1) + "#calendar-agenda"
		savedCalendar = calendarURL(visit.CurrentOn, 1) + "#calendar-agenda"
	} else {
		calendarContext = ""
	}
	message, saved := "", false
	status := http.StatusOK
	if r.Method == http.MethodPost {
		value = r.PostForm.Get("scheduled_on")
		date, dateErr := time.Parse("2006-01-02", value)
		revision, revisionErr := strconv.Atoi(r.PostForm.Get("revision"))
		if dateErr != nil || revisionErr != nil || revision < 0 {
			message = "Choose a valid service date, then save the change."
			status = http.StatusBadRequest
		} else {
			err = s.store.MoveServiceVisit(r.Context(), user.ShopID, id, date, revision)
			if err == nil {
				query := url.Values{"saved": {date.Format("2006-01-02")}}
				if hasCalendar {
					query.Set("calendar_day", calendarContext)
				}
				http.Redirect(w, r, "/service-occurrences/"+id+"/date?"+query.Encode(), http.StatusSeeOther)
				return
			}
			switch {
			case errors.Is(err, servicework.ErrDateChanged), errors.Is(err, servicework.ErrVisitUnavailable):
				message, status = err.Error(), http.StatusConflict
			case errors.Is(err, servicework.ErrDateConflict), errors.Is(err, servicework.ErrDateOutsideSchedule):
				message, status = err.Error(), http.StatusBadRequest
			default:
				log.Printf("visit date save request_id=%s error=%v", requestIDFromContext(r.Context()), err)
				message, status = "Could not confirm the date change. Your chosen date is kept below; check the current date before retrying.", http.StatusInternalServerError
			}
			// Refresh the revision after a conflict but retain the user's chosen date.
			if current, loadErr := s.store.GetServiceVisitDate(r.Context(), user.ShopID, id); loadErr == nil {
				visit = current
			}
		}
	} else if r.URL.Query().Get("saved") == value {
		saved = true
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	s.renderLayout(w, r, "Change service date — Sideyard", "visit_date_content", map[string]any{
		"Nav": NavData{Active: "schedule", User: user}, "Visit": visit, "DateValue": value, "Error": message, "Saved": saved,
		"CalendarDay": calendarContext, "CalendarURL": returnCalendar, "SavedCalendarURL": savedCalendar,
	})
}

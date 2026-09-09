package app

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/wmichelin/clubdue/internal/jobs"
)

func (s *Store) jobsService() *jobs.Service { return jobs.New(s.db.Pool, s.nowUTC) }
func jobOwner(w http.ResponseWriter, r *http.Request) *User {
	u := userFromContext(r.Context())
	if u == nil || u.Role != "owner" {
		http.Error(w, "Jobs are available to the owner.", 403)
		return nil
	}
	w.Header().Set("Cache-Control", "private, no-store")
	return u
}
func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	u := jobOwner(w, r)
	if u == nil {
		return
	}
	filter := r.URL.Query().Get("filter")
	if filter != "all" {
		filter = "open"
	}
	records, err := s.store.jobsService().List(r.Context(), u.ShopID, filter)
	if err != nil {
		log.Printf("list jobs: %v", err)
		http.Error(w, "Jobs unavailable. Please retry.", 503)
		return
	}
	s.renderLayout(w, r, "Jobs — Sideyard", "jobs_content", map[string]any{"Nav": NavData{Active: "jobs", User: u}, "Jobs": records, "Filter": filter})
}
func jobInput(r *http.Request) jobs.Input {
	return jobs.Input{CustomerID: r.PostForm.Get("customer_id"), PropertyID: r.PostForm.Get("property_id"), Kind: r.PostForm.Get("kind"), Title: r.PostForm.Get("title"), Details: r.PostForm.Get("details"), Date: r.PostForm.Get("scheduled_on")}
}

func (s *Server) handleJobNew(w http.ResponseWriter, r *http.Request) {
	u := jobOwner(w, r)
	if u == nil {
		return
	}
	in := jobs.Input{CustomerID: r.URL.Query().Get("customer_id"), Kind: "job"}
	key, message, status := "", "", 200
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form.", 400)
			return
		}
		in = jobInput(r)
		key = r.PostForm.Get("job_key")
		normalized, err := jobs.Validate(in)
		if err != nil {
			message, status = err.Error(), 422
		} else {
			id, err := s.store.jobsService().Create(r.Context(), u.ShopID, key, u.Email, normalized)
			if err == nil {
				http.Redirect(w, r, "/jobs/"+id+"?saved=1", 303)
				return
			}
			switch {
			case errors.Is(err, jobs.ErrSubmission):
				message, status = err.Error(), 409
			case errors.Is(err, jobs.ErrInput):
				message, status = err.Error(), 422
			default:
				log.Printf("create job: %v", err)
				message, status = "Could not confirm the save. Retry this same form.", 503
			}
		}
	}
	if in.CustomerID == "" {
		customers, err := s.store.ListCustomers(r.Context(), u.ShopID)
		if err != nil {
			http.Error(w, "Customers unavailable. Please retry.", 503)
			return
		}
		if status != 200 {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(status)
		}
		s.renderLayout(w, r, "New job — Sideyard", "job_customer_content", map[string]any{"Nav": NavData{Active: "jobs", User: u}, "Customers": customers, "Error": message})
		return
	}
	if !jobs.ValidID(in.CustomerID) {
		http.NotFound(w, r)
		return
	}
	customer, err := s.store.GetCustomer(r.Context(), u.ShopID, in.CustomerID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if key == "" {
		key, err = randomToken(16)
		if err != nil {
			http.Error(w, "Reload to prepare a job.", 503)
			return
		}
	}
	record := jobs.Record{CustomerID: customer.ID, CustomerName: customer.Name, PropertyID: in.PropertyID, Kind: in.Kind, Title: in.Title, Details: in.Details, Date: in.Date, Status: "planned"}
	s.renderJob(w, r, u, record, key, status, message, false)
}

func (s *Server) handleJob(w http.ResponseWriter, r *http.Request) {
	u := jobOwner(w, r)
	if u == nil {
		return
	}
	service := s.store.jobsService()
	record, err := service.Get(r.Context(), u.ShopID, r.PathValue("id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			log.Printf("load job: %v", err)
			http.Error(w, "Job unavailable. Please retry.", 503)
		}
		return
	}
	message, status := "", 200
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form.", 400)
			return
		}
		revision, parseErr := strconv.Atoi(r.PostForm.Get("revision"))
		action := r.PostForm.Get("action")
		switch {
		case parseErr != nil || revision < 1:
			err = jobs.ErrConflict
		case action == "edit":
			in := jobInput(r)
			in.CustomerID = record.CustomerID
			record.PropertyID, record.Kind, record.Title, record.Details, record.Date = in.PropertyID, in.Kind, in.Title, in.Details, in.Date
			record.Revision = revision
			err = service.Edit(r.Context(), u.ShopID, record.ID, u.Email, revision, in)
		case action == "completed" || action == "cancelled" || action == "reopened":
			err = service.SetOutcome(r.Context(), u.ShopID, record.ID, u.Email, action, revision)
		default:
			err = jobs.ErrInput
		}
		if err == nil {
			http.Redirect(w, r, "/jobs/"+record.ID+"?saved=1", 303)
			return
		}
		if errors.Is(err, jobs.ErrConflict) {
			message, status = err.Error(), 409
		} else if errors.Is(err, jobs.ErrInput) || errors.Is(err, jobs.ErrDate) {
			message, status = err.Error(), 422
		} else {
			log.Printf("save job: %v", err)
			message, status = "Could not confirm the save. Reload the job to check its current state.", 503
		}
	}
	s.renderJob(w, r, u, record, "", status, message, true)
}
func (s *Server) renderJob(w http.ResponseWriter, r *http.Request, u *User, record jobs.Record, key string, status int, message string, edit bool) {
	properties, err := s.store.ListProperties(r.Context(), u.ShopID, record.CustomerID)
	if err != nil {
		http.Error(w, "Properties unavailable. Please retry.", 503)
		return
	}
	var history []jobs.Event
	if edit {
		history, err = s.store.jobsService().History(r.Context(), u.ShopID, record.ID)
		if err != nil {
			http.Error(w, "Job history unavailable. Please retry.", 503)
			return
		}
	}
	if status != 200 {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.renderLayout(w, r, "Job — Sideyard", "job_form_content", map[string]any{"Nav": NavData{Active: "jobs", User: u}, "Job": record, "Properties": properties, "History": history, "Key": key, "IsEdit": edit, "Error": message, "Saved": r.Method == http.MethodGet && r.URL.Query().Get("saved") == "1", "ReloadURL": "/jobs/" + url.PathEscape(record.ID)})
}

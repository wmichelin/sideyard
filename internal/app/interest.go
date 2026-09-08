package app

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type interestSignup struct {
	Name, Email, Business, Trade, Message string
}

type interestRecorder interface {
	SaveInterest(context.Context, interestSignup) error
}

type postgresInterestRecorder struct{ db *DB }

func (s *postgresInterestRecorder) SaveInterest(ctx context.Context, in interestSignup) error {
	_, err := s.db.Pool.Exec(ctx, `INSERT INTO interest_signups (name, email, business, trade, message)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (email) DO NOTHING`,
		in.Name, in.Email, in.Business, in.Trade, in.Message)
	return err
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	s.renderLanding(w, r, interestSignup{}, "", http.StatusOK)
}

func (s *Server) renderLanding(w http.ResponseWriter, r *http.Request, in interestSignup, message string, status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if s.config.Environment != EnvironmentProduction {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	}
	w.WriteHeader(status)
	s.render(w, r, "landing.html", map[string]any{
		"Form": in, "Error": message, "PublicDemo": s.config.EnablePublicDemo && !s.demo,
	})
}

func (s *Server) handleInterest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	s.interestThrottleOnce.Do(func() {
		// A global budget also bounds storage writes behind reverse proxies.
		// Do not trust client-supplied forwarding headers to identify visitors.
		s.interestThrottle = newAuthThrottle(RuntimeLimits{AuthMaxAttempts: 20, AuthThrottleWindow: time.Minute})
	})
	if !s.interestThrottle.take("interest") {
		w.Header().Set("Retry-After", "60")
		s.renderLanding(w, r, interestSignup{}, "We've received several requests. Please try again in a minute.", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeRequestParseError(w, err)
		return
	}
	if r.PostForm.Get("website") != "" {
		http.Redirect(w, r, "/interest/thanks", http.StatusSeeOther)
		return
	}
	in := interestSignup{
		Name: strings.TrimSpace(r.PostForm.Get("name")), Email: strings.ToLower(strings.TrimSpace(r.PostForm.Get("email"))),
		Business: strings.TrimSpace(r.PostForm.Get("business")), Trade: strings.TrimSpace(r.PostForm.Get("trade")),
		Message: strings.TrimSpace(r.PostForm.Get("message")),
	}
	if message := validateInterest(in); message != "" {
		s.renderLanding(w, r, in, message, http.StatusUnprocessableEntity)
		return
	}
	if s.interest == nil {
		s.renderLanding(w, r, in, "We couldn't save your interest right now. Please try again shortly.", http.StatusServiceUnavailable)
		return
	}
	if err := s.interest.SaveInterest(r.Context(), in); err != nil {
		// Database errors can contain submitted values; keep contact details out of logs.
		log.Printf("interest signup persistence failed request_id=%s", w.Header().Get("X-Request-ID"))
		s.renderLanding(w, r, in, "We couldn't save your interest right now. Please try again shortly.", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/interest/thanks", http.StatusSeeOther)
}

func validateInterest(in interestSignup) string {
	if in.Name == "" || in.Email == "" {
		return "Please enter your name and email address."
	}
	for _, f := range []struct {
		value string
		max   int
	}{{in.Name, 120}, {in.Email, 254}, {in.Business, 160}, {in.Trade, 80}, {in.Message, 2000}} {
		if !utf8.ValidString(f.value) || utf8.RuneCountInString(f.value) > f.max || strings.ContainsRune(f.value, 0) {
			return "Please shorten your details to fit the field limits and remove any unsupported characters."
		}
	}
	address, err := mail.ParseAddress(in.Email)
	if err != nil || address.Address != in.Email || !strings.Contains(strings.SplitN(in.Email, "@", 2)[1], ".") {
		return "Please enter a valid email address, such as you@yourbusiness.com."
	}
	return ""
}

func (s *Server) handleInterestThanks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	s.render(w, r, "interest_thanks.html", map[string]any{"PublicDemo": s.config.EnablePublicDemo && !s.demo})
}

func exportInterest(ctx context.Context, db *DB, out io.Writer) error {
	rows, err := db.Pool.Query(ctx, `SELECT created_at, name, email, business, trade, message FROM interest_signups ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	w := csv.NewWriter(out)
	if err := w.Write([]string{"Submitted (UTC)", "Name", "Email", "Business", "Trade", "Message"}); err != nil {
		return err
	}
	for rows.Next() {
		var created time.Time
		var in interestSignup
		if err := rows.Scan(&created, &in.Name, &in.Email, &in.Business, &in.Trade, &in.Message); err != nil {
			return err
		}
		record := []string{created.UTC().Format(time.RFC3339), in.Name, in.Email, in.Business, in.Trade, in.Message}
		for i := range record {
			record[i] = safeInterestCSVCell(record[i])
		}
		if err := w.Write(record); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	w.Flush()
	return w.Error()
}

func safeInterestCSVCell(value string) string {
	trimmed := strings.TrimLeftFunc(value, unicode.IsSpace)
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func runExportInterest(config Config, out io.Writer) error {
	if config.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required for export-interest")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db, err := openDB(ctx, config.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	return exportInterest(ctx, db, out)
}

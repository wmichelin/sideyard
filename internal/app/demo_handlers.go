package app

import (
	"context"
	"log"
	"net/http"
	"time"
)

func (s *Server) handlePublicDemoGet(w http.ResponseWriter, r *http.Request) {
	s.setPublicDemoHeaders(w)
	if user, _ := s.auth.userFromRequest(r.Context(), r); user != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	s.render(w, r, "demo_entry.html", map[string]any{"Error": ""})
}

func (s *Server) handlePublicDemoPost(w http.ResponseWriter, r *http.Request) {
	s.setPublicDemoHeaders(w)
	if user, _ := s.auth.userFromRequest(r.Context(), r); user != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	limiter := s.publicDemoLimiter()
	const limiterKey = "public-demo:global"
	if !limiter.take(limiterKey) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "The demo is receiving too many new visitors. Please try again in a minute.", http.StatusTooManyRequests)
		return
	}
	result, err := s.store.SeedPublicDemo(r.Context())
	if err != nil {
		log.Printf("public demo workspace creation failed request_id=%s error=%v", requestIDFromContext(r.Context()), err)
		w.WriteHeader(http.StatusServiceUnavailable)
		s.render(w, r, "demo_entry.html", map[string]any{
			"Error": "The demo is temporarily at capacity. Please try again in a few minutes.",
		})
		return
	}
	token, err := s.auth.createSessionUntil(r.Context(), result.UserID, result.ExpiresAt)
	if err != nil {
		_, _ = s.store.db.Pool.Exec(context.WithoutCancel(r.Context()),
			`DELETE FROM shops WHERE id = $1 AND kind = 'demo_sandbox'`, result.ShopID)
		log.Printf("public demo session creation failed request_id=%s", requestIDFromContext(r.Context()))
		http.Error(w, "The demo could not start. Please try again.", http.StatusServiceUnavailable)
		return
	}
	ttl := time.Until(result.ExpiresAt)
	if s.clock != nil {
		ttl = result.ExpiresAt.Sub(s.nowUTC())
	}
	s.auth.setSessionCookieFor(w, token, ttl)
	log.Printf("public demo workspace created request_id=%s", requestIDFromContext(r.Context()))
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) setPublicDemoHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
}

package app

import (
	"errors"
	"log"
	"net/http"
	"time"
)

func (s *Server) handlePublicDemoGet(w http.ResponseWriter, r *http.Request) {
	s.setPublicDemoHeaders(w)
	s.render(w, r, "demo_entry.html", map[string]any{"Error": ""})
}

func (s *Server) handlePublicDemoPost(w http.ResponseWriter, r *http.Request) {
	s.setPublicDemoHeaders(w)
	user, err := s.auth.userFromRequest(r.Context(), r)
	if err != nil && !errors.Is(err, errUnauthenticated) {
		w.Header().Set("Retry-After", "15")
		http.Error(w, "The demo is temporarily unavailable. Please try again shortly.", http.StatusServiceUnavailable)
		return
	}
	if user != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	limiter := s.publicDemoLimiter()
	const limiterKey = "public-demo:global"
	ticket, admitted := limiter.takeWithTicket(limiterKey)
	if !admitted {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "The demo is receiving too many new visitors. Please try again in a minute.", http.StatusTooManyRequests)
		return
	}
	seed := s.store.SeedPublicDemo
	if s.publicDemoSeed != nil {
		seed = s.publicDemoSeed
	}
	result, err := seed(r.Context())
	if err != nil {
		log.Printf("public demo workspace creation failed request_id=%s error=%v", requestIDFromContext(r.Context()), err)
		if !errors.Is(err, errPublicDemoCapacityReached) {
			limiter.refund(ticket)
		}
		status := http.StatusServiceUnavailable
		message := "The demo is temporarily unavailable. Please try again shortly."
		if errors.Is(err, errPublicDemoCapacityReached) {
			status = http.StatusTooManyRequests
			w.Header().Set("Retry-After", "300")
			message = "The demo is temporarily at capacity. Please try again in a few minutes."
		} else {
			w.Header().Set("Retry-After", "15")
		}
		w.WriteHeader(status)
		s.render(w, r, "demo_entry.html", map[string]any{
			"Error": message,
		})
		return
	}
	token, err := s.auth.createSessionUntil(r.Context(), result.UserID, result.ExpiresAt)
	if err != nil {
		limiter.refund(ticket)
		if s.publicDemoCleanup != nil {
			s.publicDemoCleanup(r.Context(), result.ShopID)
		} else {
			s.store.cleanupFailedPublicDemo(r.Context(), result.ShopID)
		}
		log.Printf("public demo session creation failed request_id=%s", requestIDFromContext(r.Context()))
		w.Header().Set("Retry-After", "15")
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

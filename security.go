package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const csrfCookieName = "clubdue_csrf"

type csrfContextKey struct{}

type authAttempt struct {
	failures int
	resetAt  time.Time
}

// authThrottle is intentionally process-local: it bounds pilot login/signup
// attempts without trusting forwarded headers or introducing another service.
type authThrottle struct {
	mu          sync.Mutex
	now         func() time.Time
	maxAttempts int
	window      time.Duration
	attempts    map[string]authAttempt
}

func newAuthThrottle(limits RuntimeLimits) *authThrottle {
	return &authThrottle{
		now:         time.Now,
		maxAttempts: limits.AuthMaxAttempts,
		window:      limits.AuthThrottleWindow,
		attempts:    make(map[string]authAttempt),
	}
}

func (t *authThrottle) allowed(key string) bool {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(now)
	attempt, ok := t.attempts[key]
	if !ok || !now.Before(attempt.resetAt) {
		return true
	}
	return attempt.failures < t.maxAttempts
}

func (t *authThrottle) failure(key string) {
	now := t.now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prune(now)
	attempt := t.attempts[key]
	if attempt.resetAt.IsZero() || !now.Before(attempt.resetAt) {
		attempt = authAttempt{resetAt: now.Add(t.window)}
	}
	attempt.failures++
	t.attempts[key] = attempt
}

func (t *authThrottle) success(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.attempts, key)
}

func (t *authThrottle) prune(now time.Time) {
	for key, attempt := range t.attempts {
		if !now.Before(attempt.resetAt) {
			delete(t.attempts, key)
		}
	}
	// Keep an attacker from growing this process-local pilot control without bound.
	if len(t.attempts) < 1024 {
		return
	}
	for key := range t.attempts {
		delete(t.attempts, key)
		if len(t.attempts) < 768 {
			return
		}
	}
}

func authThrottleKey(r *http.Request, route string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		host = "unknown"
	}
	return route + ":" + host
}

func (s *Server) authLimiter() *authThrottle {
	if s.throttle != nil {
		return s.throttle
	}
	return newAuthThrottle(s.config.RuntimeLimits)
}

func (s *Server) secureCookies() bool {
	return s.config.Environment != EnvironmentDevelopment
}

func (s *Server) securityHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w.Header())

		if isStateChangingMethod(r.Method) {
			if r.ContentLength > s.config.RuntimeLimits.MaxRequestBodyBytes {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, s.config.RuntimeLimits.MaxRequestBodyBytes)
		}

		csrfToken, err := s.csrfToken(w, r)
		if err != nil {
			http.Error(w, "could not establish request protection", http.StatusInternalServerError)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), csrfContextKey{}, csrfToken))

		if isStateChangingMethod(r.Method) && !s.validCSRFToken(w, r, csrfToken) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'")
	header.Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
	header.Set("Referrer-Policy", "same-origin")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func isStateChangingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *Server) csrfToken(w http.ResponseWriter, r *http.Request) (string, error) {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		return cookie.Value, nil
	}
	token, err := randomToken(32)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	return token, nil
}

func csrfTokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(csrfContextKey{}).(string)
	return token
}

func (s *Server) validCSRFToken(w http.ResponseWriter, r *http.Request, expected string) bool {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(s.config.RuntimeLimits.MaxMultipartMemory); err != nil {
			writeRequestParseError(w, err)
			return false
		}
	} else if err := r.ParseForm(); err != nil {
		writeRequestParseError(w, err)
		return false
	}
	provided := r.PostForm.Get("csrf_token")
	if provided == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) != 1 {
		http.Error(w, "invalid request token", http.StatusForbidden)
		return false
	}
	return true
}

func writeRequestParseError(w http.ResponseWriter, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "bad form", http.StatusBadRequest)
}

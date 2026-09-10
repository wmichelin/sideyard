package app

import (
	"encoding/json"
	"errors"
	"github.com/wmichelin/clubdue/internal/database"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const renewalLinkTTL = 30 * 24 * time.Hour

func (s *Server) handleAgreementRenewalLink(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	user := userFromContext(r.Context())
	token, err := randomToken(32)
	if err != nil {
		http.Error(w, "could not create renewal link", http.StatusServiceUnavailable)
		return
	}
	if err := s.store.CreateRenewalLink(r.Context(), user.ShopID, r.PathValue("id"),
		hashToken(token, s.auth.sessionSecret), s.renewalLinkExpiry()); err != nil {
		log.Printf("create renewal link: %v", err)
		if errors.Is(err, database.ErrNoRows) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "Could not confirm the renewal link. Reload the agreement before trying again.", http.StatusServiceUnavailable)
		}
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(struct {
			RenewalURL string `json:"renewal_url"`
		}{RenewalURL: renewalURL(token)})
		return
	}
	http.Redirect(w, r, "/agreements/"+r.PathValue("id")+"?renewal_token="+url.QueryEscape(token), http.StatusSeeOther)
}

func (s *Server) handleHomeownerRenewal(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if s.demo {
		http.NotFound(w, r)
		return
	}
	renewal, err := s.store.GetHomeownerRenewal(r.Context(), hashToken(r.PathValue("token"), s.auth.sessionSecret))
	if err != nil {
		renewalReadError(w, r, err)
		return
	}
	s.render(w, r, "renewal.html", map[string]any{
		"Renewal":         renewal,
		"PriceFmt":        formatCents(renewal.PriceCents),
		"AnniversaryFmt":  renewal.AnniversaryDate.Format("January 2, 2006"),
		"TestPaymentStub": s.config.TestPaymentStubEnabled(),
		"Paid":            s.config.TestPaymentStubEnabled() && r.URL.Query().Get("payment") == "stub",
	})
}

func (s *Server) handleHomeownerRenewalPayment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	// Deliberately a non-charging placeholder until Stripe Checkout and webhook
	// verification are configured. Revalidate the expiring token before replying.
	if s.demo || !s.config.TestPaymentStubEnabled() {
		http.NotFound(w, r)
		return
	}
	if _, err := s.store.GetHomeownerRenewal(r.Context(), hashToken(r.PathValue("token"), s.auth.sessionSecret)); err != nil {
		renewalReadError(w, r, err)
		return
	}
	http.Redirect(w, r, "/renew/"+url.PathEscape(r.PathValue("token"))+"?payment=stub", http.StatusSeeOther)
}

func renewalReadError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, database.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	// Never include the bearer token or customer details in feedback or logs.
	http.Error(w, "This renewal page is temporarily unavailable. Please retry.", http.StatusServiceUnavailable)
}

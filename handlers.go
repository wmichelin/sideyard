package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Server struct {
	tmpl  *template.Template
	auth  *Auth
	store *Store
	demo  bool
}

type NavData struct {
	Active string
	User   *User
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.demo {
			if r.URL.Path != "/" && r.URL.Path != "/login" {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			next(w, r.WithContext(contextWithUser(r.Context(), demoUser())))
			return
		}
		user, err := s.auth.userFromRequest(r.Context(), r)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r.WithContext(contextWithUser(r.Context(), user)))
	}
}

type ctxKey int

const userCtxKey ctxKey = 1

func contextWithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

func userFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(userCtxKey).(*User)
	return u
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleSignupGet(w http.ResponseWriter, r *http.Request) {
	if s.demo {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if u, _ := s.auth.userFromRequest(r.Context(), r); u != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}
	s.render(w, "signup.html", map[string]any{
		"Error":    "",
		"ShopName": "",
		"Email":    "",
	})
}

func (s *Server) handleSignupPost(w http.ResponseWriter, r *http.Request) {
	if s.demo {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	shopName := r.FormValue("shop_name")
	email := r.FormValue("email")
	password := r.FormValue("password")

	token, err := s.auth.signup(r.Context(), shopName, email, password)
	if err != nil {
		s.render(w, "signup.html", map[string]any{
			"Error":    err.Error(),
			"ShopName": shopName,
			"Email":    email,
		})
		return
	}
	s.auth.setSessionCookie(w, token)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleDevSeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, err := s.store.SeedDemo(r.Context())
	if err != nil {
		log.Printf("seed: %v", err)
		http.Error(w, "seed failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"shop_id":%q,"email":%q,"password":%q,"message":%q}`,
		result.ShopID, result.Email, result.Password, result.Message)
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	if !s.demo {
		if u, _ := s.auth.userFromRequest(r.Context(), r); u != nil {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
	}
	s.render(w, "login.html", map[string]any{
		"Error": "",
		"Email": "",
		"Demo":  s.demo,
	})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if s.demo {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")
	token, err := s.auth.login(r.Context(), email, password)
	if err != nil {
		s.render(w, "login.html", map[string]any{
			"Error": err.Error(),
			"Email": email,
			"Demo":  false,
		})
		return
	}
	s.auth.setSessionCookie(w, token)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.demo {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			s.auth.logout(r.Context(), cookie.Value)
		}
		s.auth.clearSessionCookie(w)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) renderLayout(w http.ResponseWriter, title, contentTemplate string, data map[string]any) {
	var body bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&body, contentTemplate, data); err != nil {
		log.Printf("render content %s: %v", contentTemplate, err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	data["Title"] = title
	data["Body"] = template.HTML(body.String())
	s.render(w, "layout.html", data)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if s.demo {
		s.render(w, "demo.html", buildPageData(sampleMembers))
		return
	}

	stats, err := s.store.DashboardStats(r.Context(), user.ShopID)
	if err != nil {
		log.Printf("dashboard stats: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	s.renderLayout(w, "Dashboard — ClubDue", "dashboard_content", map[string]any{
		"Nav": NavData{Active: "dashboard", User: user},
		"Stats": map[string]any{
			"ActiveCount":      stats.ActiveCount,
			"RenewalsDueMonth": stats.RenewalsDueMonth,
			"RenewalsDue90":    stats.RenewalsDue90,
			"LapsedMTD":        stats.LapsedMTD,
			"MRRFmt":           formatCents(stats.MRRCents),
			"VisitsOverdue":    stats.VisitsOverdue,
		},
	})
}

func (s *Server) handleAgreementsList(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	agreements, err := s.store.ListAgreements(r.Context(), user.ShopID)
	if err != nil {
		log.Printf("list agreements: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	type row struct {
		Agreement
		StatusLabel    string
		PriceFmt       string
		AnniversaryFmt string
		VisitsLabel    string
	}
	rows := make([]row, len(agreements))
	for i, a := range agreements {
		rows[i] = row{
			Agreement:      a,
			StatusLabel:    agreementStatusLabel(a.Status),
			PriceFmt:       formatCents(a.PriceCents),
			AnniversaryFmt: a.AnniversaryDate.Format("Jan 2, 2006"),
			VisitsLabel:    strconv.Itoa(a.VisitsUsed) + " / " + strconv.Itoa(a.VisitsEntitled),
		}
	}

	s.renderLayout(w, "Agreements — ClubDue", "agreements_content", map[string]any{
		"Nav":        NavData{Active: "agreements", User: user},
		"Agreements": rows,
	})
}

func (s *Server) handleAgreementNew(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	s.renderAgreementForm(w, r, user, agreementFormData{
		Status:          string(AgreementActive),
		AnniversaryDate: time.Now().UTC().Format("2006-01-02"),
		VisitsEntitled:  2,
	})
}

func (s *Server) handleAgreementEdit(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	id := r.PathValue("id")
	a, err := s.store.GetAgreement(r.Context(), user.ShopID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.renderAgreementForm(w, r, user, agreementFormData{
		ID:              a.ID,
		CustomerID:      a.CustomerID,
		PlanTemplateID:  a.PlanTemplateID,
		Status:          string(a.Status),
		AnniversaryDate: a.AnniversaryDate.Format("2006-01-02"),
		VisitsEntitled:  a.VisitsEntitled,
		VisitsUsed:      a.VisitsUsed,
		PriceCents:      a.PriceCents,
		Notes:           a.Notes,
	})
}

type agreementFormData struct {
	ID              string
	CustomerID      string
	PlanTemplateID  string
	Status          string
	AnniversaryDate string
	VisitsEntitled  int
	VisitsUsed      int
	PriceCents      int
	Notes           string
	Error           string
}

func (s *Server) renderAgreementForm(w http.ResponseWriter, r *http.Request, user *User, form agreementFormData) {
	customers, err := s.store.ListCustomers(r.Context(), user.ShopID)
	if err != nil {
		log.Printf("list customers: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	plans, err := s.store.ListPlanTemplates(r.Context(), user.ShopID)
	if err != nil {
		log.Printf("list plans: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	title := "New agreement — ClubDue"
	if form.ID != "" {
		title = "Edit agreement — ClubDue"
	}
	s.renderLayout(w, title, "agreement_form_content", map[string]any{
		"Nav":       NavData{Active: "agreements", User: user},
		"Form":      form,
		"Customers": customers,
		"Plans":     plans,
		"IsEdit":    form.ID != "",
	})
}

func (s *Server) handleAgreementCreate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	form, input, ok := s.parseAgreementForm(w, r, user, "")
	if !ok {
		return
	}
	id, err := s.store.CreateAgreement(r.Context(), user.ShopID, input)
	if err != nil {
		form.Error = "Could not create agreement."
		s.renderAgreementForm(w, r, user, form)
		return
	}
	http.Redirect(w, r, "/agreements/"+id, http.StatusSeeOther)
}

func (s *Server) handleAgreementUpdate(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	id := r.PathValue("id")
	form, input, ok := s.parseAgreementForm(w, r, user, id)
	if !ok {
		return
	}
	if err := s.store.UpdateAgreement(r.Context(), user.ShopID, id, input); err != nil {
		form.Error = "Could not update agreement."
		s.renderAgreementForm(w, r, user, form)
		return
	}
	http.Redirect(w, r, "/agreements/"+id, http.StatusSeeOther)
}

func (s *Server) parseAgreementForm(w http.ResponseWriter, r *http.Request, user *User, id string) (agreementFormData, CreateAgreementInput, bool) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return agreementFormData{}, CreateAgreementInput{}, false
	}

	form := agreementFormData{
		ID:              id,
		CustomerID:      r.FormValue("customer_id"),
		PlanTemplateID:  r.FormValue("plan_template_id"),
		Status:          r.FormValue("status"),
		AnniversaryDate: r.FormValue("anniversary_date"),
		Notes:           r.FormValue("notes"),
	}
	form.VisitsEntitled, _ = strconv.Atoi(r.FormValue("visits_entitled"))
	form.VisitsUsed, _ = strconv.Atoi(r.FormValue("visits_used"))
	form.PriceCents, _ = strconv.Atoi(r.FormValue("price_cents"))

	if form.CustomerID == "" {
		form.Error = "Customer is required."
		s.renderAgreementForm(w, r, user, form)
		return form, CreateAgreementInput{}, false
	}

	anniversary, err := time.Parse("2006-01-02", form.AnniversaryDate)
	if err != nil {
		form.Error = "Invalid anniversary date."
		s.renderAgreementForm(w, r, user, form)
		return form, CreateAgreementInput{}, false
	}

	status := AgreementStatus(form.Status)
	if status == "" {
		status = AgreementActive
	}

	input := CreateAgreementInput{
		CustomerID:      form.CustomerID,
		PlanTemplateID:  form.PlanTemplateID,
		Status:          status,
		AnniversaryDate: anniversary,
		VisitsEntitled:  form.VisitsEntitled,
		VisitsUsed:      form.VisitsUsed,
		PriceCents:      form.PriceCents,
		Notes:           form.Notes,
	}
	return form, input, true
}

func (s *Server) handleRenewals(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	buckets, err := s.store.RenewalQueue(r.Context(), user.ShopID)
	if err != nil {
		log.Printf("renewal queue: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	total := 0
	for _, b := range buckets {
		total += len(b.Items)
	}

	s.renderLayout(w, "Renewals — ClubDue", "renewals_content", map[string]any{
		"Nav":     NavData{Active: "renewals", User: user},
		"Buckets": buckets,
		"Total":   total,
	})
}

func (s *Server) handleCustomers(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if name != "" {
			_, err := s.store.CreateCustomer(r.Context(), user.ShopID, name,
				r.FormValue("email"), r.FormValue("phone"))
			if err != nil {
				log.Printf("create customer: %v", err)
			}
		}
		http.Redirect(w, r, "/customers", http.StatusSeeOther)
		return
	}

	customers, err := s.store.ListCustomers(r.Context(), user.ShopID)
	if err != nil {
		log.Printf("list customers: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	s.renderLayout(w, "Customers — ClubDue", "customers_content", map[string]any{
		"Nav":       NavData{Active: "customers", User: user},
		"Customers": customers,
	})
}

func (s *Server) handlePlanTemplates(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		if name != "" {
			price, _ := strconv.Atoi(r.FormValue("price_cents"))
			visits, _ := strconv.Atoi(r.FormValue("visits_per_year"))
			if visits == 0 {
				visits = 2
			}
			_, err := s.store.CreatePlanTemplate(r.Context(), user.ShopID, name,
				r.FormValue("description"), price, visits)
			if err != nil {
				log.Printf("create plan: %v", err)
			}
		}
		http.Redirect(w, r, "/plan-templates", http.StatusSeeOther)
		return
	}

	plans, err := s.store.ListPlanTemplates(r.Context(), user.ShopID)
	if err != nil {
		log.Printf("list plans: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	type row struct {
		PlanTemplate
		PriceFmt string
	}
	rows := make([]row, len(plans))
	for i, p := range plans {
		rows[i] = row{PlanTemplate: p, PriceFmt: formatCents(p.PriceCents)}
	}

	s.renderLayout(w, "Plan templates — ClubDue", "plan_templates_content", map[string]any{
		"Nav":   NavData{Active: "plans", User: user},
		"Plans": rows,
	})
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}

func demoUser() *User {
	return &User{
		ID:       "demo",
		ShopID:   "demo",
		Email:    "demo@clubdue.local",
		Role:     "owner",
		ShopName: "Demo Shop",
	}
}

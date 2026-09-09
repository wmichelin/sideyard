package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Server struct {
	tmpl                 *template.Template
	auth                 *Auth
	store                *Store
	config               Config
	demo                 bool
	throttle             *authThrottle
	demoThrottle         *authThrottle
	demoThrottleOnce     sync.Once
	interest             interestRecorder
	interestThrottle     *authThrottle
	interestThrottleOnce sync.Once
	clock                Clock
}

func (s *Server) nowUTC() time.Time {
	return utcNow(s.clock)
}

func (s *Server) renewalLinkExpiry() time.Time {
	return s.nowUTC().Add(renewalLinkTTL)
}

func agreementIsWithinRenewalWindow(agreement Agreement, now time.Time, days int) bool {
	return agreement.Status == AgreementActive &&
		!agreement.AnniversaryDate.Before(now) &&
		!agreement.AnniversaryDate.After(now.AddDate(0, 0, days))
}

type NavData struct {
	Active string
	User   *User
}

const (
	publicDemoMaxMutationFormBytes = 16 << 10
	publicDemoMaxFormValues        = 256
	publicDemoMaxFieldBytes        = 2000
)

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
		if user.IsDemo {
			s.setPublicDemoHeaders(w)
		}
		if user.IsDemo && isStateChangingMethod(r.Method) {
			if !s.config.EnablePublicDemo || !publicDemoMutationAllowed(r) {
				http.Error(w, "This action is unavailable in the public demo.", http.StatusForbidden)
				return
			}
			if !publicDemoMutationWithinLimits(r) {
				if publicDemoImportPreviewRequest(r) {
					http.Error(w, "Demo CSV files must be at most 128 KiB with no more than 100 rows and 2,000 bytes per cell.", http.StatusRequestEntityTooLarge)
				} else {
					http.Error(w, "Demo changes must be smaller than 16 KiB with fields no longer than 2,000 bytes.", http.StatusRequestEntityTooLarge)
				}
				return
			}
			if err := s.store.ReservePublicDemoMutation(r.Context(), user.ShopID); err != nil {
				if errors.Is(err, errPublicDemoMutationBudgetReached) {
					http.Error(w, "This demo used its 30 actions. Start a fresh demo to keep exploring.", http.StatusTooManyRequests)
					return
				}
				log.Printf("reserve public demo mutation: %v", err)
				http.Error(w, "The demo could not save that change.", http.StatusServiceUnavailable)
				return
			}
		}
		if err := s.store.RecordReturningPilotUse(r.Context(), user.ShopID); err != nil {
			log.Printf("pilot activation returning use: %v", err)
		}
		next(w, r.WithContext(contextWithUser(r.Context(), user)))
	}
}

func publicDemoMutationWithinLimits(r *http.Request) bool {
	maxBytes := int64(publicDemoMaxMutationFormBytes)
	if publicDemoImportPreviewRequest(r) {
		maxBytes = publicDemoMaxImportRequestBytes
		if r.MultipartForm == nil {
			return false
		}
		files := r.MultipartForm.File["csv"]
		if len(files) != 1 || files[0].Size > publicDemoMaxImportCSVBytes {
			return false
		}
	}
	if r.ContentLength > maxBytes {
		return false
	}
	totalValues := 0
	for _, values := range r.Form {
		for _, value := range values {
			totalValues++
			if len(value) > publicDemoMaxFieldBytes {
				return false
			}
		}
	}
	return len(r.Form.Encode()) <= publicDemoMaxMutationFormBytes && totalValues <= publicDemoMaxFormValues
}

func publicDemoMutationAllowed(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	segments := publicDemoPathSegments(r.URL.Path)
	if len(segments) == 0 {
		return false
	}
	mediaType, ok := normalizedMediaType(r.Header.Get("Content-Type"))
	if !ok {
		return false
	}
	if segments[0] == "import" {
		return len(segments) == 1 && publicDemoImportRequestAllowed(r, mediaType)
	}
	if mediaType != "application/x-www-form-urlencoded" {
		return false
	}
	switch segments[0] {
	case "business":
		return len(segments) == 1
	case "agreements":
		return len(segments) == 1 ||
			len(segments) == 2 ||
			(len(segments) == 3 && (segments[2] == "renewal-link" || segments[2] == "service-schedules"))
	case "service-schedules":
		if len(segments) != 2 {
			return false
		}
		action := r.PostForm.Get("action")
		return action == "pause" || action == "resume"
	case "service-occurrences":
		fields := map[string]string{
			"csrf_token": "", "scheduled_on": "", "revision": "",
		}
		if _, present := r.PostForm["calendar_day"]; present {
			if _, valid := calendarDate(r.PostForm.Get("calendar_day")); !valid {
				return false
			}
			fields["calendar_day"] = ""
		}
		return len(segments) == 3 && segments[2] == "date" && len(r.URL.Query()) == 0 && exactPostForm(r, fields)
	case "renewals":
		if len(segments) != 1 {
			return false
		}
		status := r.PostForm.Get("status")
		return status == "open" || status == "contacted" || status == "renewed" || status == "lost"
	case "customers":
		return len(segments) == 1 ||
			len(segments) == 2 ||
			(len(segments) == 3 && (segments[2] == "delete" || segments[2] == "properties" || segments[2] == "vehicles"))
	case "properties":
		return len(segments) == 2 || (len(segments) == 3 && (segments[2] == "equipment" || segments[2] == "address"))
	case "equipment":
		return len(segments) == 2
	case "visits":
		if len(segments) != 1 {
			return false
		}
		action := r.PostForm.Get("action")
		return action == "completed" || action == "skipped" || action == "scheduled"
	case "plan-templates":
		return len(segments) == 1 || len(segments) == 2
	default:
		return false
	}
}

func normalizedMediaType(contentType string) (string, bool) {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", false
	}
	return strings.ToLower(mediaType), true
}

func publicDemoImportRequestAllowed(r *http.Request, mediaType string) bool {
	if len(r.URL.Query()) != 0 {
		return false
	}
	switch mediaType {
	case "multipart/form-data":
		return publicDemoImportPreviewRequest(r) && publicDemoImportFileShapeAllowed(r)
	case "application/x-www-form-urlencoded":
		return exactPostForm(r, map[string]string{
			"csrf_token":    "",
			"step":          "commit",
			"preview_token": "",
		}) && r.PostForm.Get("csrf_token") != "" && r.PostForm.Get("preview_token") != ""
	default:
		return false
	}
}

func publicDemoImportPreviewRequest(r *http.Request) bool {
	mediaType, ok := normalizedMediaType(r.Header.Get("Content-Type"))
	if !ok || mediaType != "multipart/form-data" || r.Method != http.MethodPost || r.URL.Path != "/import" {
		return false
	}
	return exactPostForm(r, map[string]string{
		"csrf_token": "",
		"step":       "dry_run",
	}) && r.PostForm.Get("csrf_token") != ""
}

func publicDemoImportFileShapeAllowed(r *http.Request) bool {
	if r.MultipartForm == nil || len(r.MultipartForm.File) != 1 {
		return false
	}
	files, ok := r.MultipartForm.File["csv"]
	if !ok || len(files) != 1 || files[0].Size < 1 {
		return false
	}
	return strings.EqualFold(filepath.Ext(files[0].Filename), ".csv")
}

// exactPostForm rejects duplicate, missing, and unexpected body fields. An
// empty expected value means the field may contain any single nonempty value;
// callers perform any route-specific nonempty check explicitly.
func exactPostForm(r *http.Request, expected map[string]string) bool {
	if len(r.PostForm) != len(expected) {
		return false
	}
	for name, want := range expected {
		values, ok := r.PostForm[name]
		if !ok || len(values) != 1 || (want != "" && values[0] != want) {
			return false
		}
	}
	return true
}

func publicDemoPathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" {
			return nil
		}
	}
	return segments
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
	s.handleLivez(w, nil)
}

func (s *Server) handleLivez(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if s.demo || s.store == nil || s.store.db == nil || s.store.db.Pool == nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.db.Pool.Ping(ctx); err != nil {
		log.Printf("readiness check failed request_id=%s", requestIDFromContext(r.Context()))
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
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
	s.render(w, r, "signup.html", map[string]any{
		"Error":    "",
		"ShopName": "",
		"Email":    "",
		"Closed":   !s.config.SignupOpen(),
	})
}

func (s *Server) handleSignupPost(w http.ResponseWriter, r *http.Request) {
	if s.demo {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if user, _ := s.auth.userFromRequest(r.Context(), r); user != nil {
		http.Error(w, "Sign out before creating another approved account.", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	shopName := r.FormValue("shop_name")
	email := r.FormValue("email")
	password := r.FormValue("password")
	throttle := s.authLimiter()
	throttleKey := authThrottleKey(r, "signup")
	if !throttle.allowed(throttleKey) {
		s.render(w, r, "signup.html", map[string]any{
			"Error": "Too many attempts. Please try again later.", "ShopName": shopName, "Email": email, "Closed": !s.config.SignupOpen(),
		})
		return
	}
	if !s.config.SignupAllowed(email) {
		throttle.failure(throttleKey)
		s.render(w, r, "signup.html", map[string]any{
			"Error":    "This email is not approved for pilot enrollment.",
			"ShopName": shopName,
			"Email":    email,
			"Closed":   !s.config.SignupOpen(),
		})
		return
	}
	token, err := s.auth.signup(r.Context(), shopName, email, password)
	if err != nil {
		throttle.failure(throttleKey)
		s.render(w, r, "signup.html", map[string]any{
			"Error":    err.Error(),
			"ShopName": shopName,
			"Email":    email,
			"Closed":   !s.config.SignupOpen(),
		})
		return
	}
	throttle.success(throttleKey)
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
	s.render(w, r, "login.html", map[string]any{
		"Error":      "",
		"Email":      "",
		"Demo":       s.demo,
		"PublicDemo": s.config.EnablePublicDemo,
		"SignupOpen": s.config.SignupOpen(),
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
	throttle := s.authLimiter()
	throttleKey := authThrottleKey(r, "login")
	if !throttle.allowed(throttleKey) {
		s.render(w, r, "login.html", map[string]any{
			"Error": "Too many attempts. Please try again later.", "Email": email, "Demo": false, "PublicDemo": s.config.EnablePublicDemo, "SignupOpen": s.config.SignupOpen(),
		})
		return
	}
	token, err := s.auth.login(r.Context(), email, password)
	if err != nil {
		throttle.failure(throttleKey)
		s.render(w, r, "login.html", map[string]any{
			"Error": "Invalid email or password.",
			"Email": email, "Demo": false, "PublicDemo": s.config.EnablePublicDemo, "SignupOpen": s.config.SignupOpen(),
		})
		return
	}
	throttle.success(throttleKey)
	s.auth.setSessionCookie(w, token)
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.demo {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if err := s.auth.logout(r.Context(), cookie.Value); err != nil {
				log.Printf("logout failed request_id=%s error=%v", requestIDFromContext(r.Context()), err)
				http.Error(w, "Could not sign out safely. Please try again.", http.StatusServiceUnavailable)
				return
			}
		}
		s.auth.clearSessionCookie(w)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) renderLayout(w http.ResponseWriter, r *http.Request, title, contentTemplate string, data map[string]any) {
	data["CSRFToken"] = csrfTokenFromContext(r.Context())
	var body bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&body, contentTemplate, data); err != nil {
		log.Printf("render content %s: %v", contentTemplate, err)
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}
	data["Title"] = title
	data["Body"] = template.HTML(body.String())
	s.render(w, r, "layout.html", data)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if s.demo {
		s.render(w, r, "demo.html", buildPageData(sampleMembers))
		return
	}

	stats, err := s.store.DashboardStats(r.Context(), user.ShopID)
	if err != nil {
		log.Printf("dashboard stats: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	calendar := calendarSelection(s.nowUTC(), r.URL.Query())
	page, _ := strconv.Atoi(r.URL.Query().Get("calendar_page"))
	work, err := s.store.ServiceCalendar(r.Context(), user.ShopID, calendar.Month, calendar.End, calendar.SelectedDay, page)
	if err != nil {
		log.Printf("dashboard calendar request_id=%s error=%v", requestIDFromContext(r.Context()), err)
		http.Error(w, "Could not load the service calendar. Reload to try again.", http.StatusInternalServerError)
		return
	}
	calendar.populate(work)

	s.renderLayout(w, r, "Dashboard — Sideyard", "dashboard_content", map[string]any{
		"Nav":      NavData{Active: "dashboard", User: user},
		"Calendar": calendar,
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
	filter, days := r.URL.Query().Get("filter"), 0
	if raw := r.URL.Query().Get("renew_days"); raw != "" {
		days, _ = strconv.Atoi(raw)
	}
	rows := make([]row, 0, len(agreements))
	now := s.nowUTC()
	for _, a := range agreements {
		if filter == "active" && a.Status != AgreementActive {
			continue
		}
		if days > 0 && !agreementIsWithinRenewalWindow(a, now, days) {
			continue
		}
		rows = append(rows, row{
			Agreement:      a,
			StatusLabel:    agreementStatusLabel(a.Status),
			PriceFmt:       formatCents(a.PriceCents),
			AnniversaryFmt: a.AnniversaryDate.Format("Jan 2, 2006"),
			VisitsLabel:    strconv.Itoa(a.VisitsUsed) + " / " + strconv.Itoa(a.VisitsEntitled),
		})
	}

	s.renderLayout(w, r, "Agreements — Sideyard", "agreements_content", map[string]any{
		"Nav":        NavData{Active: "agreements", User: user},
		"Agreements": rows,
		"Filter":     filter, "RenewDays": days,
	})
}

func (s *Server) handleAgreementNew(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	s.renderAgreementForm(w, r, user, agreementFormData{
		Status:          string(AgreementActive),
		AnniversaryDate: s.nowUTC().Format("2006-01-02"),
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
		PropertyID:      a.PropertyID,
		PlanTemplateID:  a.PlanTemplateID,
		PlanName:        a.PlanName,
		Status:          string(a.Status),
		AnniversaryDate: a.AnniversaryDate.Format("2006-01-02"),
		VisitsEntitled:  a.VisitsEntitled,
		VisitsUsed:      a.VisitsUsed,
		PriceCents:      a.PriceCents,
		Notes:           a.Notes,
		RenewalURL:      renewalURL(r.URL.Query().Get("renewal_token")),
	})
}

type agreementFormData struct {
	ID              string
	CustomerID      string
	PropertyID      string
	PlanTemplateID  string
	PlanName        string
	Status          string
	AnniversaryDate string
	VisitsEntitled  int
	VisitsUsed      int
	PriceCents      int
	Notes           string
	Error           string
	RenewalURL      string
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
	properties, err := s.store.ListAllProperties(r.Context(), user.ShopID)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	var schedules []ServiceSchedule
	if form.ID != "" {
		schedules, err = s.store.ListServiceSchedules(r.Context(), user.ShopID, form.ID)
		if err != nil {
			log.Printf("list service schedules: %v", err)
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
	}

	title := "New agreement — Sideyard"
	if form.ID != "" {
		title = "Edit agreement — Sideyard"
	}
	s.renderLayout(w, r, title, "agreement_form_content", map[string]any{
		"Nav":              NavData{Active: "agreements", User: user},
		"Form":             form,
		"Customers":        customers,
		"Plans":            plans,
		"Properties":       properties,
		"Schedules":        schedules,
		"ServiceStartDate": s.nowUTC().Format("2006-01-02"),
		"IsEdit":           form.ID != "",
	})
}

func renewalURL(token string) string {
	if token == "" {
		return ""
	}
	return "/renew/" + token
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
		PropertyID:      r.FormValue("property_id"),
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
		PropertyID:      form.PropertyID,
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
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if err := s.store.UpdateRenewalTask(r.Context(), user.ShopID, r.FormValue("task_id"), r.FormValue("status"), r.FormValue("notes")); err != nil {
			log.Printf("renewal update: %v", err)
			s.renderRenewalQueue(w, r, user, "Could not save this renewal outcome. It may already be final or no longer available.", http.StatusBadRequest)
			return
		}
		http.Redirect(w, r, "/renewals", http.StatusSeeOther)
		return
	}
	s.renderRenewalQueue(w, r, user, "", http.StatusOK)
}

func (s *Server) renderRenewalQueue(w http.ResponseWriter, r *http.Request, user *User, updateError string, status int) {
	tasks, err := s.store.ListRenewalTasks(r.Context(), user.ShopID)
	if err != nil {
		log.Printf("renewal queue: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if updateError != "" {
		w.WriteHeader(status)
	}
	s.renderLayout(w, r, "Renewals — Sideyard", "renewals_content", map[string]any{
		"Nav": NavData{Active: "renewals", User: user}, "Tasks": tasks, "UpdateError": updateError,
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

	s.renderLayout(w, r, "Customers — Sideyard", "customers_content", map[string]any{
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
			if visits < 0 {
				visits = 0
			}
			termMonths, _ := strconv.Atoi(r.FormValue("term_months"))
			if termMonths <= 0 {
				termMonths = 12
			}
			_, err := s.store.CreatePlanTemplate(r.Context(), user.ShopID, name,
				r.FormValue("description"), price, termMonths, visits)
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

	s.renderLayout(w, r, "Plan templates — Sideyard", "plan_templates_content", map[string]any{
		"Nav":   NavData{Active: "plans", User: user},
		"Plans": rows,
	})
}

func (s *Server) handlePlanTemplateEdit(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	id := r.PathValue("id")
	plan, err := s.store.GetPlanTemplate(r.Context(), user.ShopID, id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(r.FormValue("name"))
		price, _ := strconv.Atoi(r.FormValue("price_cents"))
		termMonths, _ := strconv.Atoi(r.FormValue("term_months"))
		visits, _ := strconv.Atoi(r.FormValue("visits_per_year"))
		if name != "" && price >= 0 && termMonths > 0 && visits >= 0 {
			err = s.store.UpdatePlanTemplate(r.Context(), user.ShopID, id, name,
				r.FormValue("description"), price, termMonths, visits)
			if err == nil {
				http.Redirect(w, r, "/plan-templates", http.StatusSeeOther)
				return
			}
		}
		plan = &PlanTemplate{ID: id, Name: name, Description: r.FormValue("description"), PriceCents: price, TermMonths: termMonths, VisitsPerYear: visits}
	}

	s.renderLayout(w, r, "Edit plan template — Sideyard", "plan_template_form_content", map[string]any{
		"Nav":   NavData{Active: "plans", User: user},
		"Plan":  plan,
		"Error": err != nil || r.Method == http.MethodPost,
	})
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data any) {
	if pageData, ok := data.(map[string]any); ok {
		pageData["CSRFToken"] = csrfTokenFromContext(r.Context())
	}
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

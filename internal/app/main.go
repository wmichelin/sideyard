package app

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Run starts the Sideyard application or executes a supported operator command.
func Run(args []string) error {
	loadEnvFile(".env")
	config, err := LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	return run(config, args)
}

func run(config Config, args []string) error {
	if len(args) > 0 {
		if len(args) == 1 && args[0] == "export-interest" {
			return runExportInterest(config, os.Stdout)
		}
		if len(args) != 1 || args[0] != "provision-owner" {
			return fmt.Errorf("unsupported command")
		}
		return runProvisionOwner(config)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return err
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return err
	}

	srv := &Server{tmpl: tmpl, config: config, demo: config.DemoMode(), throttle: newAuthThrottle(config.RuntimeLimits)}
	closeResources := func() {}

	if !srv.demo {
		db, err := openDB(ctx, config.DatabaseURL)
		if err != nil {
			return err
		}
		closeResources = db.Close

		srv.store = &Store{db: db}
		srv.interest = &postgresInterestRecorder{db: db}
		if config.Environment == EnvironmentStaging {
			retired, err := srv.store.RetireLegacySharedDemo(ctx)
			if err != nil {
				closeResources()
				return fmt.Errorf("retire legacy staging demo: %w", err)
			}
			if retired > 0 {
				log.Print("Retired legacy shared staging demo tenant")
			}
		}

		auth := newAuth(db, config.SessionSecret, srv.secureCookies())
		if err := auth.bootstrapShop(ctx, config.BootstrapOwner); err != nil {
			closeResources()
			return fmt.Errorf("bootstrap shop: %w", err)
		}

		srv.auth = auth
		if err := srv.store.SyncAllVisitDues(ctx); err != nil {
			log.Printf("renewal maintenance failed phase=startup operation=sync_visit_dues error=%v", err)
		}
		runRenewalMaintenance(ctx, srv.store, time.Now, "startup")
		go runDailyRenewalGeneration(ctx, srv.store, 24*time.Hour, time.Now)
		if config.InterestNotifications.APIKey != "" {
			notificationCtx, cancelNotifications := context.WithCancel(ctx)
			done := make(chan struct{})
			sender := &resendInterestSender{
				client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
				config: config.InterestNotifications, environment: config.Environment,
			}
			go func() {
				defer close(done)
				runInterestNotifications(notificationCtx, db, sender, time.Now)
			}()
			closeResources = func() { cancelNotifications(); <-done; db.Close() }
			log.Print("Sideyard signup email notifications enabled")
		} else {
			log.Print("Sideyard signup email notifications unconfigured; alerts remain queued")
		}
		log.Print("Sideyard running with Postgres")
	} else {
		log.Print("Sideyard running in demo mode (set DATABASE_URL for Postgres)")
	}

	listener, err := net.Listen("tcp", config.Address)
	if err != nil {
		closeResources()
		return fmt.Errorf("listen on %s: %w", config.Address, err)
	}
	log.Printf("Sideyard listening on %s", listener.Addr())
	return serveHTTP(ctx, config, srv.handler(static), listener, closeResources)
}

func (s *Server) handler(static fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /livez", s.handleLivez)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /rollback-safetyz", s.handleRollbackSafety)
	mux.HandleFunc("GET /login", s.handleLoginGet)
	mux.HandleFunc("POST /login", s.handleLoginPost)
	mux.HandleFunc("GET /signup", s.handleSignupGet)
	mux.HandleFunc("POST /signup", s.handleSignupPost)
	mux.HandleFunc("POST /logout", s.handleLogout)
	mux.HandleFunc("POST /interest", s.handleInterest)
	mux.HandleFunc("GET /interest/thanks", s.handleInterestThanks)
	if s.config.EnablePublicDemo && !s.demo {
		mux.HandleFunc("GET /demo", s.handlePublicDemoGet)
		mux.HandleFunc("POST /demo/session", s.handlePublicDemoPost)
	}
	mux.HandleFunc("GET /renew/{token}", s.handleHomeownerRenewal)
	mux.HandleFunc("POST /renew/{token}", s.handleHomeownerRenewalPayment)
	if s.config.EnableDevRoutes && !s.demo {
		mux.HandleFunc("POST /dev/seed", s.handleDevSeed)
		log.Print("Dev routes enabled: POST /dev/seed")
	}

	auth := s.requireAuth
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if s.demo {
			auth(s.handleDashboard)(w, r)
			return
		}
		s.handleLanding(w, r)
	})
	mux.HandleFunc("GET /dashboard", auth(s.handleDashboard))
	mux.HandleFunc("GET /business", auth(s.handleBusinessSettings))
	mux.HandleFunc("POST /business", auth(s.handleBusinessSettings))
	mux.HandleFunc("GET /agreements", auth(s.handleAgreementsList))
	mux.HandleFunc("GET /agreements/new", auth(s.handleAgreementNew))
	mux.HandleFunc("POST /agreements", auth(s.handleAgreementCreate))
	mux.HandleFunc("GET /agreements/{id}", auth(s.handleAgreementEdit))
	mux.HandleFunc("POST /agreements/{id}", auth(s.handleAgreementUpdate))
	mux.HandleFunc("POST /agreements/{id}/renewal-link", auth(s.handleAgreementRenewalLink))
	mux.HandleFunc("POST /agreements/{id}/service-schedules", auth(s.handleServiceScheduleCreate))
	mux.HandleFunc("POST /service-schedules/{id}", auth(s.handleServiceScheduleUpdate))
	mux.HandleFunc("GET /schedule", auth(s.handleSchedule))
	mux.HandleFunc("GET /jobs", auth(s.handleJobs))
	mux.HandleFunc("GET /jobs/new", auth(s.handleJobNew))
	mux.HandleFunc("POST /jobs", auth(s.handleJobNew))
	mux.HandleFunc("GET /jobs/{id}", auth(s.handleJob))
	mux.HandleFunc("POST /jobs/{id}", auth(s.handleJob))
	mux.HandleFunc("GET /service-occurrences/{id}/date", auth(s.handleVisitDate))
	mux.HandleFunc("POST /service-occurrences/{id}/date", auth(s.handleVisitDate))
	mux.HandleFunc("GET /renewals", auth(s.handleRenewals))
	mux.HandleFunc("POST /renewals", auth(s.handleRenewals))
	mux.HandleFunc("GET /customers", auth(s.handleCustomers))
	mux.HandleFunc("POST /customers", auth(s.handleCustomers))
	mux.HandleFunc("GET /customers/{id}", auth(s.handleCustomerEdit))
	mux.HandleFunc("POST /customers/{id}", auth(s.handleCustomerEdit))
	mux.HandleFunc("POST /customers/{id}/delete", auth(s.handleCustomerDelete))
	mux.HandleFunc("POST /customers/{id}/properties", auth(s.handlePropertyCreate))
	mux.HandleFunc("POST /customers/{id}/vehicles", auth(s.handleCustomerVehicle))
	mux.HandleFunc("POST /properties/{id}/equipment", auth(s.handleEquipmentCreate))
	mux.HandleFunc("GET /equipment/{id}", auth(s.handleEquipmentRecord))
	mux.HandleFunc("POST /equipment/{id}", auth(s.handleEquipmentRecord))
	mux.HandleFunc("GET /equipment/{id}/history", auth(s.handleEquipmentHistory))
	mux.HandleFunc("POST /equipment/{id}/history", auth(s.handleEquipmentHistory))
	mux.HandleFunc("GET /vehicles/{id}/history", auth(s.handleVehicleHistory))
	mux.HandleFunc("POST /vehicles/{id}/history", auth(s.handleVehicleHistory))
	mux.HandleFunc("GET /service-occurrences/{id}/history", auth(s.handleRecurringVisitHistory))
	mux.HandleFunc("POST /service-occurrences/{id}/history", auth(s.handleRecurringVisitHistory))
	mux.HandleFunc("GET /visits/{id}/history", auth(s.handleSeasonalVisitHistory))
	mux.HandleFunc("POST /visits/{id}/history", auth(s.handleSeasonalVisitHistory))
	mux.HandleFunc("GET /properties/{id}", auth(s.handlePropertyRecord))
	mux.HandleFunc("POST /properties/{id}", auth(s.handlePropertyRecord))
	mux.HandleFunc("GET /properties/{id}/address", auth(s.handlePropertyAddress))
	mux.HandleFunc("POST /properties/{id}/address", auth(s.handlePropertyAddress))
	mux.HandleFunc("GET /properties/{id}/photos", auth(s.handlePropertyPhotos))
	mux.HandleFunc("POST /properties/{id}/photos", auth(s.handlePropertyPhotos))
	mux.HandleFunc("GET /property-photos/{id}", auth(s.handlePropertyPhotoImage))
	mux.HandleFunc("POST /property-photos/{id}/delete", auth(s.handlePropertyPhotoDelete))
	mux.HandleFunc("GET /import", auth(s.handleImport))
	mux.HandleFunc("POST /import", auth(s.handleImport))
	mux.HandleFunc("GET /visits/export.csv", auth(s.handleVisitsExport))
	mux.HandleFunc("GET /visits", auth(s.handleVisits))
	mux.HandleFunc("POST /visits", auth(s.handleVisits))
	mux.HandleFunc("GET /plan-templates", auth(s.handlePlanTemplates))
	mux.HandleFunc("POST /plan-templates", auth(s.handlePlanTemplates))
	mux.HandleFunc("GET /plan-templates/{id}", auth(s.handlePlanTemplateEdit))
	mux.HandleFunc("POST /plan-templates/{id}", auth(s.handlePlanTemplateEdit))

	return s.requestIDHandler(s.securityHandler(s.completionLogHandler(mux)))
}

func (s *Server) handleRollbackSafety(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("demo-sandbox-v1\n"))
}

func runProvisionOwner(config Config) error {
	credentials, err := config.ValidateProvisionOwner()
	if err != nil {
		return err
	}
	if config.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL is required for provision-owner")
	}
	db, err := openDB(context.Background(), config.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	auth := newAuth(db, config.SessionSecret, config.Environment != EnvironmentDevelopment)
	action, err := auth.provisionOwner(context.Background(), credentials)
	if err != nil {
		return fmt.Errorf("provision owner: %w", err)
	}
	log.Printf("owner %s completed", action)
	return nil
}

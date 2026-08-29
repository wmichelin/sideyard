package main

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

func main() {
	loadEnvFile(".env")
	config, err := LoadConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	if err := run(config, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(config Config, args []string) error {
	if len(args) > 0 {
		if len(args) != 1 || args[0] != "provision-owner" {
			return fmt.Errorf("unsupported command")
		}
		return runProvisionOwner(config)
	}

	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return err
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return err
	}

	srv := &Server{tmpl: tmpl, config: config, demo: config.DemoMode(), throttle: newAuthThrottle(config.RuntimeLimits)}

	if !srv.demo {
		ctx := context.Background()
		db, err := openDB(ctx, config.DatabaseURL)
		if err != nil {
			return err
		}
		defer db.Close()

		auth := newAuth(db, config.SessionSecret, srv.secureCookies())
		if err := auth.bootstrapShop(ctx, config.BootstrapOwner); err != nil {
			return fmt.Errorf("bootstrap shop: %w", err)
		}

		srv.auth = auth
		srv.store = &Store{db: db}
		if err := srv.store.SyncAllVisitDues(ctx); err != nil {
			log.Printf("sync visit dues: %v", err)
		}
		if err := srv.store.GenerateAllRenewalTasks(ctx, time.Now()); err != nil {
			log.Printf("generate renewal tasks: %v", err)
		}
		if err := srv.store.QueueRenewalDigests(ctx, time.Now()); err != nil {
			log.Printf("queue renewal digests: %v", err)
		}
		go runDailyRenewalGeneration(srv.store)
		log.Print("ClubDue running with Postgres")
	} else {
		log.Print("ClubDue running in demo mode (set DATABASE_URL for Postgres)")
	}

	log.Printf("ClubDue listening on %s", config.Address)
	return http.ListenAndServe(config.Address, srv.handler(static))
}

func (s *Server) handler(static fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /login", s.handleLoginGet)
	mux.HandleFunc("POST /login", s.handleLoginPost)
	mux.HandleFunc("GET /signup", s.handleSignupGet)
	mux.HandleFunc("POST /signup", s.handleSignupPost)
	mux.HandleFunc("POST /logout", s.handleLogout)
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
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /dashboard", auth(s.handleDashboard))
	mux.HandleFunc("GET /agreements", auth(s.handleAgreementsList))
	mux.HandleFunc("GET /agreements/new", auth(s.handleAgreementNew))
	mux.HandleFunc("POST /agreements", auth(s.handleAgreementCreate))
	mux.HandleFunc("GET /agreements/{id}", auth(s.handleAgreementEdit))
	mux.HandleFunc("POST /agreements/{id}", auth(s.handleAgreementUpdate))
	mux.HandleFunc("POST /agreements/{id}/renewal-link", auth(s.handleAgreementRenewalLink))
	mux.HandleFunc("GET /renewals", auth(s.handleRenewals))
	mux.HandleFunc("POST /renewals", auth(s.handleRenewals))
	mux.HandleFunc("GET /customers", auth(s.handleCustomers))
	mux.HandleFunc("POST /customers", auth(s.handleCustomers))
	mux.HandleFunc("GET /customers/{id}", auth(s.handleCustomerEdit))
	mux.HandleFunc("POST /customers/{id}", auth(s.handleCustomerEdit))
	mux.HandleFunc("POST /customers/{id}/delete", auth(s.handleCustomerDelete))
	mux.HandleFunc("POST /customers/{id}/properties", auth(s.handlePropertyCreate))
	mux.HandleFunc("POST /properties/{id}/equipment", auth(s.handleEquipmentCreate))
	mux.HandleFunc("GET /import", auth(s.handleImport))
	mux.HandleFunc("POST /import", auth(s.handleImport))
	mux.HandleFunc("GET /visits/export.csv", auth(s.handleVisitsExport))
	mux.HandleFunc("GET /visits", auth(s.handleVisits))
	mux.HandleFunc("POST /visits", auth(s.handleVisits))
	mux.HandleFunc("GET /plan-templates", auth(s.handlePlanTemplates))
	mux.HandleFunc("POST /plan-templates", auth(s.handlePlanTemplates))
	mux.HandleFunc("GET /plan-templates/{id}", auth(s.handlePlanTemplateEdit))
	mux.HandleFunc("POST /plan-templates/{id}", auth(s.handlePlanTemplateEdit))

	return s.securityHandler(mux)
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

func runDailyRenewalGeneration(store *Store) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		if err := store.GenerateAllRenewalTasks(context.Background(), time.Now()); err != nil {
			log.Printf("generate renewal tasks: %v", err)
		}
		if err := store.QueueRenewalDigests(context.Background(), time.Now()); err != nil {
			log.Printf("queue renewal digests: %v", err)
		}
	}
}

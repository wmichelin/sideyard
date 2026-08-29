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

	srv := &Server{tmpl: tmpl, config: config, demo: config.DemoMode()}

	if !srv.demo {
		ctx := context.Background()
		db, err := openDB(ctx, config.DatabaseURL)
		if err != nil {
			return err
		}
		defer db.Close()

		auth := newAuth(db, config.SessionSecret)
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

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /healthz", srv.handleHealthz)
	mux.HandleFunc("GET /login", srv.handleLoginGet)
	mux.HandleFunc("POST /login", srv.handleLoginPost)
	mux.HandleFunc("GET /signup", srv.handleSignupGet)
	mux.HandleFunc("POST /signup", srv.handleSignupPost)
	mux.HandleFunc("POST /logout", srv.handleLogout)
	mux.HandleFunc("GET /renew/{token}", srv.handleHomeownerRenewal)
	mux.HandleFunc("POST /renew/{token}", srv.handleHomeownerRenewalPayment)
	if config.EnableDevRoutes && !srv.demo {
		mux.HandleFunc("POST /dev/seed", srv.handleDevSeed)
		log.Print("Dev routes enabled: POST /dev/seed")
	}

	auth := srv.requireAuth
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if srv.demo {
			auth(srv.handleDashboard)(w, r)
			return
		}
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /dashboard", auth(srv.handleDashboard))
	mux.HandleFunc("GET /agreements", auth(srv.handleAgreementsList))
	mux.HandleFunc("GET /agreements/new", auth(srv.handleAgreementNew))
	mux.HandleFunc("POST /agreements", auth(srv.handleAgreementCreate))
	mux.HandleFunc("GET /agreements/{id}", auth(srv.handleAgreementEdit))
	mux.HandleFunc("POST /agreements/{id}", auth(srv.handleAgreementUpdate))
	mux.HandleFunc("POST /agreements/{id}/renewal-link", auth(srv.handleAgreementRenewalLink))
	mux.HandleFunc("GET /renewals", auth(srv.handleRenewals))
	mux.HandleFunc("POST /renewals", auth(srv.handleRenewals))
	mux.HandleFunc("GET /customers", auth(srv.handleCustomers))
	mux.HandleFunc("POST /customers", auth(srv.handleCustomers))
	mux.HandleFunc("GET /customers/{id}", auth(srv.handleCustomerEdit))
	mux.HandleFunc("POST /customers/{id}", auth(srv.handleCustomerEdit))
	mux.HandleFunc("POST /customers/{id}/delete", auth(srv.handleCustomerDelete))
	mux.HandleFunc("POST /customers/{id}/properties", auth(srv.handlePropertyCreate))
	mux.HandleFunc("POST /properties/{id}/equipment", auth(srv.handleEquipmentCreate))
	mux.HandleFunc("GET /import", auth(srv.handleImport))
	mux.HandleFunc("POST /import", auth(srv.handleImport))
	mux.HandleFunc("GET /visits/export.csv", auth(srv.handleVisitsExport))
	mux.HandleFunc("GET /visits", auth(srv.handleVisits))
	mux.HandleFunc("POST /visits", auth(srv.handleVisits))
	mux.HandleFunc("GET /plan-templates", auth(srv.handlePlanTemplates))
	mux.HandleFunc("POST /plan-templates", auth(srv.handlePlanTemplates))
	mux.HandleFunc("GET /plan-templates/{id}", auth(srv.handlePlanTemplateEdit))
	mux.HandleFunc("POST /plan-templates/{id}", auth(srv.handlePlanTemplateEdit))

	log.Printf("ClubDue listening on %s", config.Address)
	return http.ListenAndServe(config.Address, mux)
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
	auth := newAuth(db, config.SessionSecret)
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

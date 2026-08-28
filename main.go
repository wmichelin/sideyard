package main

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
)

//go:embed templates/*
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

func main() {
	loadEnvFile(".env")

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}

	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		log.Fatal(err)
	}

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal(err)
	}

	databaseURL := os.Getenv("DATABASE_URL")
	srv := &Server{tmpl: tmpl, demo: databaseURL == ""}

	if databaseURL != "" {
		ctx := context.Background()
		db, err := openDB(ctx, databaseURL)
		if err != nil {
			log.Fatal(err)
		}
		defer db.Close()

		auth := newAuth(db)
		if err := auth.bootstrapShop(ctx); err != nil {
			log.Fatalf("bootstrap shop: %v", err)
		}

		srv.auth = auth
		srv.store = &Store{db: db}
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
	if devRoutesEnabled() && !srv.demo {
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
	mux.HandleFunc("GET /renewals", auth(srv.handleRenewals))
	mux.HandleFunc("GET /customers", auth(srv.handleCustomers))
	mux.HandleFunc("POST /customers", auth(srv.handleCustomers))
	mux.HandleFunc("GET /import", auth(srv.handleImport))
	mux.HandleFunc("POST /import", auth(srv.handleImport))
	mux.HandleFunc("GET /plan-templates", auth(srv.handlePlanTemplates))
	mux.HandleFunc("POST /plan-templates", auth(srv.handlePlanTemplates))

	log.Printf("ClubDue listening on %s", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

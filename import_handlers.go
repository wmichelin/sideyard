package main

import (
	"encoding/base64"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
)

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if r.Method == http.MethodGet {
		s.renderImportUpload(w, r, user, "")
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	step := r.FormValue("step")
	switch step {
	case "commit":
		s.handleImportCommit(w, r, user)
	default:
		s.handleImportDryRun(w, r, user)
	}
}

func (s *Server) renderImportUpload(w http.ResponseWriter, r *http.Request, user *User, errMsg string) {
	s.renderLayout(w, r, "Import — ClubDue", "import_content", map[string]any{
		"Nav":   NavData{Active: "import", User: user},
		"Step":  "upload",
		"Error": errMsg,
	})
}

func (s *Server) handleImportDryRun(w http.ResponseWriter, r *http.Request, user *User) {
	if err := r.ParseMultipartForm(s.config.RuntimeLimits.MaxMultipartMemory); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		s.renderImportUpload(w, r, user, "Could not read upload.")
		return
	}

	file, _, err := r.FormFile("csv")
	if err != nil {
		s.renderImportUpload(w, r, user, "Please choose a CSV file.")
		return
	}
	defer file.Close()

	raw, err := io.ReadAll(file)
	if err != nil {
		s.renderImportUpload(w, r, user, "Could not read file.")
		return
	}

	rows, err := parseImportCSV(strings.NewReader(string(raw)))
	if err != nil {
		s.renderImportUpload(w, r, user, err.Error())
		return
	}

	analysis, err := s.store.AnalyzeImport(r.Context(), user.ShopID, rows)
	if err != nil {
		log.Printf("import analyze: %v", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	preview := analysis.Rows
	if len(preview) > 20 {
		preview = preview[:20]
	}

	s.renderLayout(w, r, "Import preview — ClubDue", "import_content", map[string]any{
		"Nav":       NavData{Active: "import", User: user},
		"Step":      "preview",
		"Summary":   analysis.Summary,
		"Preview":   preview,
		"TotalRows": len(analysis.Rows),
		"CSVData":   base64.StdEncoding.EncodeToString(raw),
		"CanCommit": analysis.Summary.Errors == 0 && len(analysis.Rows) > 0,
	})
}

func (s *Server) handleImportCommit(w http.ResponseWriter, r *http.Request, user *User) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	encoded := r.FormValue("csv_data")
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(raw) == 0 {
		s.renderImportUpload(w, r, user, "Import session expired — upload the file again.")
		return
	}

	rows, err := parseImportCSV(strings.NewReader(string(raw)))
	if err != nil {
		s.renderImportUpload(w, r, user, err.Error())
		return
	}

	result, err := s.store.CommitImport(r.Context(), user.ShopID, rows)
	if err != nil {
		log.Printf("import commit: %v", err)
		s.renderImportUpload(w, r, user, "Import failed: "+err.Error())
		return
	}

	s.renderLayout(w, r, "Import complete — ClubDue", "import_content", map[string]any{
		"Nav":     NavData{Active: "import", User: user},
		"Step":    "done",
		"Summary": result.Summary,
	})
}

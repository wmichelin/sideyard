package app

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
	if user.IsDemo {
		http.Error(w, "File uploads are disabled in the public demo.", http.StatusForbidden)
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
		"Nav":    NavData{Active: "import", User: user},
		"Step":   "upload",
		"Error":  errMsg,
		"IsDemo": user.IsDemo,
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

	// Preview currently carries its exact source payload into the commit form.
	// Base64 plus form encoding expands it, so use a conservative half-request
	// raw limit and fail during preview rather than unpredictably at commit.
	maxRawBytes := s.config.RuntimeLimits.MaxRequestBodyBytes / 2
	raw, err := io.ReadAll(io.LimitReader(file, maxRawBytes+1))
	if err != nil {
		s.renderImportUpload(w, r, user, "Could not read file.")
		return
	}
	if int64(len(raw)) > maxRawBytes {
		s.renderImportUpload(w, r, user, "CSV is too large for a reliable preview and commit. Split it into smaller files.")
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
	if err := s.store.RecordPilotActivation(r.Context(), user.ShopID, pilotEventImportPreviewed); err != nil {
		log.Printf("pilot activation import preview: %v", err)
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

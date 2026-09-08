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
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	step := r.PostForm.Get("step")
	switch step {
	case "commit":
		s.handleImportCommit(w, r, user)
	default:
		s.handleImportDryRun(w, r, user)
	}
}

func (s *Server) renderImportUpload(w http.ResponseWriter, r *http.Request, user *User, errMsg string) {
	s.renderLayout(w, r, "Import — Sideyard", "import_content", map[string]any{
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

	maxRawBytes := s.config.RuntimeLimits.MaxRequestBodyBytes / 2
	maxRows := maxImportRows
	maxCellBytes := 0
	if user.IsDemo {
		maxRawBytes = publicDemoMaxImportCSVBytes
		maxRows = publicDemoMaxImportRows
		maxCellBytes = publicDemoMaxImportCellBytes
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxRawBytes+1))
	if err != nil {
		s.renderImportUpload(w, r, user, "Could not read file.")
		return
	}
	if int64(len(raw)) > maxRawBytes {
		if user.IsDemo {
			s.renderImportUpload(w, r, user, "CSV exceeds the 128 KiB public-demo limit.")
		} else {
			s.renderImportUpload(w, r, user, "CSV is too large for a reliable preview and commit. Split it into smaller files.")
		}
		return
	}

	rows, err := parseImportCSVWithLimits(strings.NewReader(string(raw)), maxRows, maxCellBytes)
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
	var errorRows []ImportPreviewRow
	for _, row := range analysis.Rows {
		if row.Error != "" {
			errorRows = append(errorRows, row)
		}
	}

	canCommit := analysis.Summary.Errors == 0 && len(analysis.Rows) > 0
	previewData := map[string]any{
		"Nav":       NavData{Active: "import", User: user},
		"Step":      "preview",
		"Summary":   analysis.Summary,
		"Preview":   preview,
		"ErrorRows": errorRows,
		"TotalRows": len(analysis.Rows),
		"CanCommit": canCommit,
		"IsDemo":    user.IsDemo,
	}
	if canCommit && user.IsDemo {
		projectionDate := s.nowUTC()
		projectedOccurrences, err := projectedImportOccurrenceCount(rows, projectionDate)
		if err != nil {
			log.Printf("public demo import projection request_id=%s", requestIDFromContext(r.Context()))
			http.Error(w, "The demo could not safely preview recurring work.", http.StatusServiceUnavailable)
			return
		}
		token, err := randomToken(32)
		if err != nil {
			log.Printf("public demo import preview token: %v", err)
			http.Error(w, "The demo could not protect this preview.", http.StatusServiceUnavailable)
			return
		}
		err = s.store.CreatePublicDemoImportPreview(
			r.Context(), user.ShopID, hashToken(token, s.auth.sessionSecret), raw,
			len(rows), projectedOccurrences, projectionDate,
		)
		if errors.Is(err, errPublicDemoImportRowBudgetReached) {
			s.renderImportUpload(w, r, user, "This demo used its 500-row CSV allowance. Start a fresh demo to import more fictional data.")
			return
		}
		if errors.Is(err, errPublicDemoOccurrenceBudgetReached) {
			s.renderImportUpload(w, r, user, "This CSV would generate too many demo visits. Use fewer daily services, a less frequent cadence, or an end date.")
			return
		}
		if err != nil {
			log.Printf("public demo import preview store: %v", err)
			http.Error(w, "The demo could not protect this preview.", http.StatusServiceUnavailable)
			return
		}
		previewData["PreviewToken"] = token
	} else if canCommit {
		// Customer imports retain the existing stateless preview/commit flow.
		previewData["CSVData"] = base64.StdEncoding.EncodeToString(raw)
	}
	s.renderLayout(w, r, "Import preview — Sideyard", "import_content", previewData)
}

func (s *Server) handleImportCommit(w http.ResponseWriter, r *http.Request, user *User) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	var raw []byte
	var demoPayload PublicDemoImportPayload
	maxRows := maxImportRows
	maxCellBytes := 0
	if user.IsDemo {
		var err error
		demoPayload, err = s.store.ConsumePublicDemoImportPreview(
			r.Context(), user.ShopID, hashToken(r.PostForm.Get("preview_token"), s.auth.sessionSecret),
		)
		if errors.Is(err, errPublicDemoImportRowBudgetReached) {
			s.renderImportUpload(w, r, user, "This demo used its 500-row CSV allowance. Start a fresh demo to import more fictional data.")
			return
		}
		if errors.Is(err, errPublicDemoImportPreviewInvalid) {
			s.renderImportUpload(w, r, user, "Import preview expired or was already used — upload the file again.")
			return
		}
		if errors.Is(err, errPublicDemoOccurrenceBudgetReached) {
			s.renderImportUpload(w, r, user, "This CSV would generate too many demo visits. Use fewer daily services, a less frequent cadence, or an end date.")
			return
		}
		if err != nil {
			log.Printf("public demo import preview consume: %v", err)
			http.Error(w, "The demo could not commit that preview.", http.StatusServiceUnavailable)
			return
		}
		raw = demoPayload.CSV
		maxRows = publicDemoMaxImportRows
		maxCellBytes = publicDemoMaxImportCellBytes
	} else {
		encoded := r.PostForm.Get("csv_data")
		var err error
		raw, err = base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(raw) == 0 || int64(len(raw)) > s.config.RuntimeLimits.MaxRequestBodyBytes/2 {
			s.renderImportUpload(w, r, user, "Import session expired — upload the file again.")
			return
		}
	}

	rows, err := parseImportCSVWithLimits(strings.NewReader(string(raw)), maxRows, maxCellBytes)
	if err != nil {
		s.renderImportUpload(w, r, user, err.Error())
		return
	}
	if user.IsDemo {
		projectedOccurrences, projectionErr := projectedImportOccurrenceCount(rows, demoPayload.ProjectionDate)
		if projectionErr != nil || len(rows) != demoPayload.RowCount || projectedOccurrences != demoPayload.ProjectedOccurrences {
			log.Printf("public demo import control mismatch request_id=%s", requestIDFromContext(r.Context()))
			http.Error(w, "The demo could not verify that preview. Upload the file again.", http.StatusServiceUnavailable)
			return
		}
	}

	result, err := s.store.CommitImport(r.Context(), user.ShopID, rows)
	if err != nil {
		if user.IsDemo {
			log.Printf("public demo import commit failed request_id=%s", requestIDFromContext(r.Context()))
			s.renderImportUpload(w, r, user, "Import failed safely. Preview the file again and retry.")
		} else {
			log.Printf("import commit: %v", err)
			s.renderImportUpload(w, r, user, "Import failed: "+err.Error())
		}
		return
	}

	s.renderLayout(w, r, "Import complete — Sideyard", "import_content", map[string]any{
		"Nav":     NavData{Active: "import", User: user},
		"Step":    "done",
		"Summary": result.Summary,
	})
}

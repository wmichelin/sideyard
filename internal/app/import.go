package app

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	importSourceCSV     = "csv"
	maxImportRows       = 10000
	maxImportPriceCents = int64(1<<31 - 1)
)

var importCSVHeaders = []string{
	"external_id",
	"customer_name",
	"email",
	"phone",
	"address_line1",
	"city",
	"state",
	"postal_code",
	"plan_name",
	"anniversary_date",
	"status",
	"price_cents",
	"price",
	"service_external_id",
	"service_name",
	"cadence_interval",
	"cadence_unit",
	"service_start_date",
	"service_end_date",
}

var requiredImportCSVHeaders = []string{
	"external_id", "customer_name", "address_line1", "city", "state", "postal_code", "anniversary_date",
}

type ImportRow struct {
	LineNum           int
	ExternalID        string
	CustomerName      string
	Email             string
	Phone             string
	AddressLine1      string
	City              string
	State             string
	PostalCode        string
	PlanName          string
	AnniversaryDate   time.Time
	Status            AgreementStatus
	PriceCents        int
	ServiceExternalID string
	ServiceName       string
	CadenceInterval   int
	CadenceUnit       string
	ServiceStartsOn   time.Time
	ServiceEndsOn     *time.Time
	Error             string
}

type ImportPreviewRow struct {
	ImportRow
	Action string
}

type ImportSummary struct {
	NewCustomers      int
	UpdatedCustomers  int
	NewProperties     int
	UpdatedProperties int
	NewAgreements     int
	UpdatedAgreements int
	NewSchedules      int
	UpdatedSchedules  int
	Skipped           int
	Errors            int
}

type ImportAnalysis struct {
	Rows    []ImportPreviewRow
	Summary ImportSummary
}

type ImportCommitResult struct {
	Summary ImportSummary
}

func parseImportCSV(r io.Reader) ([]ImportRow, error) {
	return parseImportCSVWithLimits(r, maxImportRows, 0)
}

func parseImportCSVWithLimits(r io.Reader, maxRows, maxCellBytes int) ([]ImportRow, error) {
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read csv: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("csv file is empty")
	}
	if len(records)-1 > maxRows {
		return nil, fmt.Errorf("csv file exceeds the %d-row limit", maxRows)
	}
	if maxCellBytes > 0 {
		for _, record := range records {
			for _, cell := range record {
				if len(cell) > maxCellBytes {
					return nil, fmt.Errorf("csv cell exceeds the %d-byte limit", maxCellBytes)
				}
			}
		}
	}

	header := records[0]
	colIndex, err := mapImportColumns(header)
	if err != nil {
		return nil, err
	}

	var rows []ImportRow
	seenExternalIDs := make(map[string]struct{})
	seenServiceExternalIDs := make(map[string]struct{})
	for i, record := range records[1:] {
		lineNum := i + 2
		row := ImportRow{LineNum: lineNum}
		if len(record) < len(importCSVHeaders) {
			padded := make([]string, len(importCSVHeaders))
			copy(padded, record)
			record = padded
		}

		get := func(name string) string {
			idx := colIndex[name]
			if idx < 0 || idx >= len(record) {
				return ""
			}
			return strings.TrimSpace(record[idx])
		}

		row.ExternalID = get("external_id")
		row.CustomerName = get("customer_name")
		row.Email = get("email")
		row.Phone = get("phone")
		row.AddressLine1 = get("address_line1")
		row.City = get("city")
		row.State = get("state")
		row.PostalCode = get("postal_code")
		row.PlanName = get("plan_name")
		serviceExternalID := get("service_external_id")
		row.ServiceExternalID = serviceExternalID
		row.ServiceName = get("service_name")
		if row.ServiceExternalID == "" && row.ExternalID != "" {
			row.ServiceExternalID = row.ExternalID + "-service"
		}

		if row.ExternalID == "" {
			row.Error = "external_id is required"
		} else if _, exists := seenExternalIDs[row.ExternalID]; exists {
			row.Error = "duplicate external_id in file"
		} else if row.CustomerName == "" {
			row.Error = "customer_name is required"
		} else if row.AddressLine1 == "" || row.City == "" || row.State == "" || row.PostalCode == "" {
			row.Error = "address_line1, city, state, and postal_code are required"
		} else {
			dateStr := get("anniversary_date")
			if dateStr == "" {
				row.Error = "anniversary_date is required"
			} else {
				t, err := parseImportDate(dateStr)
				if err != nil {
					row.Error = "anniversary_date must be YYYY-MM-DD or M/D/YYYY"
				} else {
					row.AnniversaryDate = t
				}
			}
		}

		if row.Error == "" {
			status := get("status")
			if status == "" {
				status = string(AgreementActive)
			}
			status = strings.ToLower(status)
			switch AgreementStatus(status) {
			case AgreementActive, AgreementPastDue, AgreementLapsed, AgreementCancelled, AgreementPendingRenewal:
				row.Status = AgreementStatus(status)
			default:
				row.Error = "invalid status"
			}
		}

		if row.Error == "" {
			priceCents := get("price_cents")
			priceDollars := get("price")
			if priceCents == "" && priceDollars == "" {
				row.PriceCents = 0
			} else if priceCents != "" {
				price, err := strconv.ParseInt(priceCents, 10, 32)
				if err != nil || price < 0 {
					row.Error = "price_cents must be between 0 and 2147483647"
				} else {
					row.PriceCents = int(price)
				}
			} else {
				price, err := parseImportDollarCents(priceDollars)
				if err != nil {
					row.Error = "price must be a non-negative dollar amount"
				} else {
					row.PriceCents = price
				}
			}
		}
		if row.Error == "" {
			cadenceInterval := get("cadence_interval")
			cadenceUnit := get("cadence_unit")
			startsOn := get("service_start_date")
			endsOn := get("service_end_date")
			hasServiceFields := row.ServiceName != "" || serviceExternalID != "" || cadenceInterval != "" || cadenceUnit != "" || startsOn != "" || endsOn != ""
			if row.ServiceName == "" && hasServiceFields {
				row.Error = "service_name is required when recurrence fields are present"
			} else if row.ServiceName != "" {
				row.CadenceInterval = 1
				if cadenceInterval != "" {
					interval, err := strconv.Atoi(cadenceInterval)
					if err != nil || interval <= 0 || interval > 365 {
						row.Error = "cadence_interval must be between 1 and 365"
					} else {
						row.CadenceInterval = interval
					}
				}
				if row.Error == "" {
					unit, impliedInterval, err := normalizeImportCadence(cadenceUnit)
					if err != nil {
						row.Error = err.Error()
					} else {
						row.CadenceUnit = unit
						if impliedInterval > 1 && cadenceInterval != "" && row.CadenceInterval != impliedInterval {
							row.Error = fmt.Sprintf("cadence_interval must be %d when cadence_unit is %s", impliedInterval, cadenceUnit)
						} else if cadenceInterval == "" && impliedInterval > 0 {
							row.CadenceInterval = impliedInterval
						}
					}
				}
				if row.Error == "" {
					if startsOn == "" {
						row.Error = "service_start_date is required when service_name is present"
					} else if parsed, err := parseImportDate(startsOn); err != nil {
						row.Error = "service_start_date must be YYYY-MM-DD or M/D/YYYY"
					} else {
						row.ServiceStartsOn = parsed
					}
				}
				if row.Error == "" && endsOn != "" {
					parsed, err := parseImportDate(endsOn)
					if err != nil {
						row.Error = "service_end_date must be YYYY-MM-DD or M/D/YYYY"
					} else if parsed.Before(row.ServiceStartsOn) {
						row.Error = "service_end_date must not precede service_start_date"
					} else {
						row.ServiceEndsOn = &parsed
					}
				}
			}
		}
		if row.ServiceName != "" && row.ServiceExternalID != "" {
			if _, exists := seenServiceExternalIDs[row.ServiceExternalID]; exists && row.Error == "" {
				row.Error = "duplicate service_external_id in file"
			}
			seenServiceExternalIDs[row.ServiceExternalID] = struct{}{}
		}
		if row.ExternalID != "" {
			seenExternalIDs[row.ExternalID] = struct{}{}
		}

		rows = append(rows, row)
	}
	return rows, nil
}

func mapImportColumns(header []string) (map[string]int, error) {
	index := make(map[string]int, len(importCSVHeaders))
	for i, name := range importCSVHeaders {
		index[name] = -1
		_ = i
	}
	for i, col := range header {
		col = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(col)), "\ufeff")
		if _, ok := index[col]; ok {
			index[col] = i
		}
	}
	for _, name := range requiredImportCSVHeaders {
		if index[name] < 0 {
			return nil, fmt.Errorf("missing required column: %s", name)
		}
	}
	return index, nil
}

func parseImportDate(raw string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "1/2/2006", "01/02/2006"} {
		if value, err := time.Parse(layout, strings.TrimSpace(raw)); err == nil {
			return value, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported date")
}

func normalizeImportCadence(raw string) (string, int, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "day", "days", "daily":
		return "day", 1, nil
	case "week", "weeks", "weekly":
		return "week", 1, nil
	case "biweekly", "bi-weekly":
		return "week", 2, nil
	case "month", "months", "monthly":
		return "month", 1, nil
	default:
		return "", 0, fmt.Errorf("cadence_unit must be day, week, month, daily, weekly, biweekly, or monthly")
	}
}

func parseImportDollarCents(raw string) (int, error) {
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "$"))
	value = strings.ReplaceAll(value, ",", "")
	if value == "" || strings.HasPrefix(value, "-") || strings.Count(value, ".") > 1 {
		return 0, fmt.Errorf("invalid dollar amount")
	}
	parts := strings.SplitN(value, ".", 2)
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole < 0 {
		return 0, fmt.Errorf("invalid dollar amount")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 {
		return 0, fmt.Errorf("invalid dollar amount")
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	cents := 0
	if fraction != "" {
		cents, err = strconv.Atoi(fraction)
		if err != nil {
			return 0, fmt.Errorf("invalid dollar amount")
		}
	}
	if whole > maxImportPriceCents/100 {
		return 0, fmt.Errorf("invalid dollar amount")
	}
	total := whole*100 + int64(cents)
	if total > maxImportPriceCents {
		return 0, fmt.Errorf("invalid dollar amount")
	}
	return int(total), nil
}

func (row ImportRow) contentHash() string {
	payload := strings.Join([]string{
		row.ExternalID,
		row.CustomerName,
		row.Email,
		row.Phone,
		row.AddressLine1,
		row.City,
		row.State,
		row.PostalCode,
		row.PlanName,
		row.AnniversaryDate.Format("2006-01-02"),
		string(row.Status),
		strconv.Itoa(row.PriceCents),
		row.ServiceExternalID,
		row.ServiceName,
		row.CadenceUnit,
		strconv.Itoa(row.CadenceInterval),
		row.ServiceStartsOn.Format("2006-01-02"),
		formatOptionalImportDate(row.ServiceEndsOn),
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func formatOptionalImportDate(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format("2006-01-02")
}

func (s *Store) AnalyzeImport(ctx context.Context, shopID string, rows []ImportRow) (ImportAnalysis, error) {
	plans, err := s.ListPlanTemplates(ctx, shopID)
	if err != nil {
		return ImportAnalysis{}, err
	}
	planByName := make(map[string]string, len(plans))
	for _, p := range plans {
		planByName[p.Name] = p.ID
	}

	out := ImportAnalysis{
		Rows: make([]ImportPreviewRow, 0, len(rows)),
	}

	for _, row := range rows {
		preview := ImportPreviewRow{ImportRow: row}
		reject := func(message string) {
			preview.Error = message
			preview.Action = "error"
			out.Summary.Skipped++
			out.Summary.Errors++
			out.Rows = append(out.Rows, preview)
		}
		if row.Error != "" {
			reject(row.Error)
			continue
		}
		if row.PlanName != "" {
			if _, ok := planByName[row.PlanName]; !ok {
				reject(fmt.Sprintf("unknown plan template: %s", row.PlanName))
				continue
			}
		}

		agrID, agrExists, err := s.findAgreementByExternalID(ctx, shopID, row.ExternalID)
		if err != nil {
			return ImportAnalysis{}, err
		}
		if agrExists {
			if row.PlanName != "" {
				capturedPlan, err := s.findAgreementCapturedPlan(ctx, shopID, agrID)
				if err != nil {
					return ImportAnalysis{}, err
				}
				if capturedPlan != row.PlanName {
					reject("existing agreement plan terms are immutable; leave plan_name unchanged or edit the agreement separately")
					continue
				}
			}
			existingServiceIDs, err := s.findAgreementExternalServiceIDs(ctx, shopID, agrID)
			if err != nil {
				return ImportAnalysis{}, err
			}
			if len(existingServiceIDs) > 1 {
				reject("existing agreement has multiple imported recurring services; edit it in ClubDue instead of re-importing this row")
				continue
			}
			if len(existingServiceIDs) == 1 && row.ServiceName == "" {
				reject("existing recurring service is omitted; keep its service columns or pause it in ClubDue")
				continue
			}
			if len(existingServiceIDs) == 1 && existingServiceIDs[0] != row.ServiceExternalID {
				reject("service_external_id is immutable after import; keep the existing value or edit the service in ClubDue")
				continue
			}
		}
		if row.ServiceName != "" {
			ownerAgreementID, scheduleExists, err := s.findServiceScheduleOwnerByExternalID(ctx, shopID, row.ServiceExternalID)
			if err != nil {
				return ImportAnalysis{}, err
			}
			if scheduleExists && (!agrExists || ownerAgreementID != agrID) {
				reject(fmt.Sprintf("service_external_id %s already belongs to another agreement", row.ServiceExternalID))
				continue
			}
		}

		customerID, customerExists, err := s.findImportCustomer(ctx, shopID, row)
		if err != nil {
			return ImportAnalysis{}, err
		}
		if customerExists {
			out.Summary.UpdatedCustomers++
			preview.Action = "update customer"
		} else {
			out.Summary.NewCustomers++
			preview.Action = "new customer"
		}

		propID, propExists, err := s.findPropertyByExternalID(ctx, shopID, row.ExternalID)
		if err != nil {
			return ImportAnalysis{}, err
		}
		if propExists {
			out.Summary.UpdatedProperties++
		} else {
			out.Summary.NewProperties++
		}
		_ = propID

		if agrExists {
			out.Summary.UpdatedAgreements++
			if preview.Action == "new customer" {
				preview.Action = "new customer, update agreement"
			} else {
				preview.Action = "update agreement"
			}
		} else {
			out.Summary.NewAgreements++
			if preview.Action == "update customer" {
				preview.Action = "update customer, new agreement"
			} else {
				preview.Action = "new"
			}
		}
		_ = customerID

		if row.ServiceName != "" {
			_, scheduleExists, err := s.findServiceScheduleByExternalID(ctx, shopID, row.ServiceExternalID)
			if err != nil {
				return ImportAnalysis{}, err
			}
			if scheduleExists {
				out.Summary.UpdatedSchedules++
				preview.Action += ", update recurring service"
			} else {
				out.Summary.NewSchedules++
				preview.Action += ", new recurring service"
			}
		}

		out.Rows = append(out.Rows, preview)
	}
	return out, nil
}

func (s *Store) CommitImport(ctx context.Context, shopID string, rows []ImportRow) (ImportCommitResult, error) {
	analysis, err := s.AnalyzeImport(ctx, shopID, rows)
	if err != nil {
		return ImportCommitResult{}, err
	}
	if analysis.Summary.Errors > 0 {
		return ImportCommitResult{}, fmt.Errorf("cannot commit import with %d errors", analysis.Summary.Errors)
	}

	plans, err := s.ListPlanTemplates(ctx, shopID)
	if err != nil {
		return ImportCommitResult{}, err
	}
	planByName := make(map[string]string, len(plans))
	visitsByPlan := make(map[string]int, len(plans))
	for _, p := range plans {
		planByName[p.Name] = p.ID
		visitsByPlan[p.Name] = p.VisitsPerYear
	}

	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return ImportCommitResult{}, err
	}
	defer tx.Rollback(ctx)

	result := ImportCommitResult{Summary: analysis.Summary}

	for _, row := range rows {
		if row.Error != "" {
			continue
		}

		customerID, err := s.upsertImportCustomer(ctx, tx, shopID, row)
		if err != nil {
			return ImportCommitResult{}, err
		}
		if err := s.recordImportRow(ctx, tx, shopID, importSourceCSV, row.ExternalID, "customer", customerID, row.contentHash()); err != nil {
			return ImportCommitResult{}, err
		}

		propertyID, err := s.upsertImportProperty(ctx, tx, shopID, customerID, row)
		if err != nil {
			return ImportCommitResult{}, err
		}
		if err := s.recordImportRow(ctx, tx, shopID, importSourceCSV, row.ExternalID, "property", propertyID, row.contentHash()); err != nil {
			return ImportCommitResult{}, err
		}

		planID := ""
		visitsEntitled := 2
		if row.PlanName != "" {
			planID = planByName[row.PlanName]
			visitsEntitled = visitsByPlan[row.PlanName]
		} else if row.ServiceName != "" {
			visitsEntitled = 0
		}

		agreementID, err := s.upsertImportAgreement(ctx, tx, shopID, customerID, propertyID, planID, row, visitsEntitled)
		if err != nil {
			return ImportCommitResult{}, err
		}
		if err := s.recordImportRow(ctx, tx, shopID, importSourceCSV, row.ExternalID, "agreement", agreementID, row.contentHash()); err != nil {
			return ImportCommitResult{}, err
		}
		if row.ServiceName != "" {
			scheduleID, err := s.upsertImportServiceSchedule(ctx, tx, shopID, agreementID, row)
			if err != nil {
				return ImportCommitResult{}, err
			}
			if err := s.recordImportRow(ctx, tx, shopID, importSourceCSV, row.ServiceExternalID, "service_schedule", scheduleID, row.contentHash()); err != nil {
				return ImportCommitResult{}, err
			}
		}
	}
	if err := recordPilotActivationTx(ctx, tx, shopID, pilotEventImportCommitted); err != nil {
		return ImportCommitResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ImportCommitResult{}, err
	}
	return result, nil
}

func (s *Store) findImportCustomer(ctx context.Context, shopID string, row ImportRow) (string, bool, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id FROM customers
		WHERE shop_id = $1 AND external_id = $2`, shopID, row.ExternalID,
	).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if err != pgx.ErrNoRows {
		return "", false, err
	}

	err = s.db.Pool.QueryRow(ctx, `
		SELECT c.id
		FROM customers c
		JOIN properties p ON p.customer_id = c.id AND p.shop_id = c.shop_id
		WHERE c.shop_id = $1
		  AND c.name = $2
		  AND p.address_line1 = $3
		  AND p.city = $4
		  AND p.state = $5
		  AND p.postal_code = $6
		  AND c.external_id IS NULL
		LIMIT 1`,
		shopID, row.CustomerName, row.AddressLine1, row.City, row.State, row.PostalCode,
	).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func (s *Store) findPropertyByExternalID(ctx context.Context, shopID, externalID string) (string, bool, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id FROM properties
		WHERE shop_id = $1 AND external_id = $2`, shopID, externalID,
	).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func (s *Store) findAgreementByExternalID(ctx context.Context, shopID, externalID string) (string, bool, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id FROM agreements
		WHERE shop_id = $1 AND external_id = $2`, shopID, externalID,
	).Scan(&id)
	if err == pgx.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return id, true, nil
}

func (s *Store) findServiceScheduleByExternalID(ctx context.Context, shopID, externalID string) (string, bool, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id FROM service_schedules
		WHERE shop_id = $1 AND external_id = $2`, shopID, externalID,
	).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if err != pgx.ErrNoRows {
		return "", false, err
	}
	return "", false, nil
}

func (s *Store) findServiceScheduleOwnerByExternalID(ctx context.Context, shopID, externalID string) (string, bool, error) {
	var agreementID string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT agreement_id FROM service_schedules
		WHERE shop_id = $1 AND external_id = $2`, shopID, externalID,
	).Scan(&agreementID)
	if err == nil {
		return agreementID, true, nil
	}
	if err != pgx.ErrNoRows {
		return "", false, err
	}
	return "", false, nil
}

func (s *Store) findAgreementCapturedPlan(ctx context.Context, shopID, agreementID string) (string, error) {
	var planName string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT plan_snapshot_name FROM agreements
		WHERE shop_id = $1 AND id = $2`, shopID, agreementID,
	).Scan(&planName)
	return planName, err
}

func (s *Store) findAgreementExternalServiceIDs(ctx context.Context, shopID, agreementID string) ([]string, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT external_id FROM service_schedules
		WHERE shop_id = $1 AND agreement_id = $2 AND external_id IS NOT NULL
		ORDER BY external_id`, shopID, agreementID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var externalIDs []string
	for rows.Next() {
		var externalID string
		if err := rows.Scan(&externalID); err != nil {
			return nil, err
		}
		externalIDs = append(externalIDs, externalID)
	}
	return externalIDs, rows.Err()
}

func (s *Store) upsertImportCustomer(ctx context.Context, tx pgx.Tx, shopID string, row ImportRow) (string, error) {
	var existingID string
	err := tx.QueryRow(ctx, `
		SELECT c.id
		FROM customers c
		JOIN properties p ON p.customer_id = c.id AND p.shop_id = c.shop_id
		WHERE c.shop_id = $1 AND c.external_id IS NULL
		  AND c.name = $2 AND p.address_line1 = $3 AND p.city = $4
		  AND p.state = $5 AND p.postal_code = $6
		LIMIT 1
		FOR UPDATE OF c`, shopID, row.CustomerName, row.AddressLine1, row.City, row.State, row.PostalCode,
	).Scan(&existingID)
	if err == nil {
		_, err = tx.Exec(ctx, `
			UPDATE customers SET external_id = $3, name = $4,
				email = NULLIF($5, ''), phone = NULLIF($6, ''), updated_at = now()
			WHERE shop_id = $1 AND id = $2`, shopID, existingID, row.ExternalID,
			row.CustomerName, row.Email, row.Phone)
		return existingID, err
	}
	if err != pgx.ErrNoRows {
		return "", err
	}

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO customers (shop_id, external_id, name, email, phone)
		VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''))
		ON CONFLICT (shop_id, external_id) DO UPDATE SET
			name = EXCLUDED.name,
			email = EXCLUDED.email,
			phone = EXCLUDED.phone,
			updated_at = now()
		RETURNING id`,
		shopID, row.ExternalID, row.CustomerName, row.Email, row.Phone,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("upsert customer %s: %w", row.ExternalID, err)
	}
	return id, nil
}

func (s *Store) upsertImportProperty(ctx context.Context, tx pgx.Tx, shopID, customerID string, row ImportRow) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO properties (shop_id, customer_id, external_id, address_line1, city, state, postal_code)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (shop_id, external_id) DO UPDATE SET
			customer_id = EXCLUDED.customer_id,
			address_line1 = EXCLUDED.address_line1,
			city = EXCLUDED.city,
			state = EXCLUDED.state,
			postal_code = EXCLUDED.postal_code,
			updated_at = now()
		RETURNING id`,
		shopID, customerID, row.ExternalID, row.AddressLine1, row.City, row.State, row.PostalCode,
	).Scan(&id)
	return id, err
}

func (s *Store) upsertImportAgreement(
	ctx context.Context,
	tx pgx.Tx,
	shopID, customerID, propertyID, planID string,
	row ImportRow,
	visitsEntitled int,
) (string, error) {
	var planPtr *string
	if planID != "" {
		planPtr = &planID
	}

	var id string
	err := tx.QueryRow(ctx, `
		INSERT INTO agreements (
			shop_id, customer_id, property_id, plan_template_id, external_id,
			status, anniversary_date, visits_entitled, price_cents,
			plan_snapshot_name, plan_snapshot_description, plan_snapshot_billing_interval,
			plan_snapshot_term_months, plan_snapshot_visits_per_year, plan_snapshot_visit_windows
		)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9,
			COALESCE(pt.name, 'Custom plan'), COALESCE(pt.description, ''),
			COALESCE(pt.billing_interval, 'annual'), COALESCE(pt.term_months, 12),
			COALESCE(pt.visits_per_year, $8), COALESCE(pt.visit_windows, '{spring,fall}')
		FROM (SELECT $4::uuid AS plan_id) selected
		LEFT JOIN plan_templates pt ON pt.id = selected.plan_id AND pt.shop_id = $1
		ON CONFLICT (shop_id, external_id) DO UPDATE SET
			customer_id = EXCLUDED.customer_id,
			property_id = EXCLUDED.property_id,
			status = EXCLUDED.status,
			anniversary_date = EXCLUDED.anniversary_date,
			price_cents = EXCLUDED.price_cents,
			updated_at = now()
		RETURNING id`,
		shopID, customerID, propertyID, planPtr, row.ExternalID,
		string(row.Status), row.AnniversaryDate, visitsEntitled, row.PriceCents,
	).Scan(&id)
	return id, err
}

func (s *Store) upsertImportServiceSchedule(
	ctx context.Context,
	tx pgx.Tx,
	shopID, agreementID string,
	row ImportRow,
) (string, error) {
	var id, existingAgreementID, serviceName, cadenceUnit string
	var cadenceInterval int
	var startsOn time.Time
	var endsOn *time.Time
	var generatedThrough *time.Time
	err := tx.QueryRow(ctx, `
		SELECT id, agreement_id, service_name, cadence_unit, cadence_interval, starts_on, ends_on, generated_through
		FROM service_schedules
		WHERE shop_id = $1 AND external_id = $2
		FOR UPDATE`, shopID, row.ServiceExternalID,
	).Scan(&id, &existingAgreementID, &serviceName, &cadenceUnit, &cadenceInterval, &startsOn, &endsOn, &generatedThrough)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(ctx, `
			INSERT INTO service_schedules (
				shop_id, agreement_id, external_id, service_name, cadence_unit,
				cadence_interval, starts_on, ends_on
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			RETURNING id`, shopID, agreementID, row.ServiceExternalID, row.ServiceName,
			row.CadenceUnit, row.CadenceInterval, row.ServiceStartsOn, row.ServiceEndsOn,
		).Scan(&id)
		return id, err
	}
	if err != nil {
		return "", err
	}
	if existingAgreementID != agreementID {
		return "", fmt.Errorf("service_external_id %s already belongs to another agreement", row.ServiceExternalID)
	}
	unchanged := serviceName == row.ServiceName &&
		cadenceUnit == row.CadenceUnit && cadenceInterval == row.CadenceInterval &&
		dateEqual(startsOn, row.ServiceStartsOn) && optionalDateEqual(endsOn, row.ServiceEndsOn)
	if unchanged {
		return id, nil
	}
	newGeneratedThrough := generatedThrough
	if generatedThrough != nil {
		yesterday := s.nowUTC().AddDate(0, 0, -1)
		newGeneratedThrough = &yesterday
	}
	if _, err := tx.Exec(ctx, `
		UPDATE service_schedules SET
			agreement_id = $3, service_name = $4, cadence_unit = $5,
			cadence_interval = $6, starts_on = $7, ends_on = $8,
			generated_through = $9, updated_at = now()
		WHERE shop_id = $1 AND id = $2`, shopID, id, agreementID, row.ServiceName,
		row.CadenceUnit, row.CadenceInterval, row.ServiceStartsOn, row.ServiceEndsOn, newGeneratedThrough); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		DELETE FROM service_occurrences
		WHERE shop_id = $1 AND schedule_id = $2 AND status = 'due'
		  AND (due_on >= $3::date OR COALESCE(scheduled_on, due_on) >= $3::date)`,
		shopID, id, s.nowUTC()); err != nil {
		return "", err
	}
	return id, nil
}

func dateEqual(left, right time.Time) bool {
	return left.Format("2006-01-02") == right.Format("2006-01-02")
}

func optionalDateEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return dateEqual(*left, *right)
}

func (s *Store) recordImportRow(ctx context.Context, tx pgx.Tx, shopID, source, externalID, entityType, entityID, contentHash string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO import_rows (shop_id, source, external_id, entity_type, entity_id, content_hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (shop_id, source, external_id, entity_type) DO UPDATE SET
			entity_id = EXCLUDED.entity_id,
			content_hash = EXCLUDED.content_hash,
			imported_at = now()`,
		shopID, source, externalID, entityType, entityID, contentHash,
	)
	return err
}

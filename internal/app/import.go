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

const importSourceCSV = "csv"

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
}

type ImportRow struct {
	LineNum         int
	ExternalID      string
	CustomerName    string
	Email           string
	Phone           string
	AddressLine1    string
	City            string
	State           string
	PostalCode      string
	PlanName        string
	AnniversaryDate time.Time
	Status          AgreementStatus
	PriceCents      int
	Error           string
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
	reader := csv.NewReader(r)
	reader.TrimLeadingSpace = true
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("read csv: %w", err)
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("csv file is empty")
	}

	header := records[0]
	colIndex, err := mapImportColumns(header)
	if err != nil {
		return nil, err
	}

	var rows []ImportRow
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

		if row.ExternalID == "" {
			row.Error = "external_id is required"
		} else if row.CustomerName == "" {
			row.Error = "customer_name is required"
		} else if row.AddressLine1 == "" || row.City == "" || row.State == "" || row.PostalCode == "" {
			row.Error = "address_line1, city, state, and postal_code are required"
		} else {
			dateStr := get("anniversary_date")
			if dateStr == "" {
				row.Error = "anniversary_date is required"
			} else {
				t, err := time.Parse("2006-01-02", dateStr)
				if err != nil {
					row.Error = "anniversary_date must be YYYY-MM-DD"
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
			switch AgreementStatus(status) {
			case AgreementActive, AgreementLapsed, AgreementCancelled, AgreementPendingRenewal:
				row.Status = AgreementStatus(status)
			default:
				row.Error = "invalid status"
			}
		}

		if row.Error == "" {
			priceStr := get("price_cents")
			if priceStr == "" {
				row.PriceCents = 0
			} else {
				price, err := strconv.Atoi(priceStr)
				if err != nil || price < 0 {
					row.Error = "price_cents must be a non-negative integer"
				} else {
					row.PriceCents = price
				}
			}
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
		col = strings.TrimSpace(strings.ToLower(col))
		if _, ok := index[col]; ok {
			index[col] = i
		}
	}
	for _, name := range importCSVHeaders {
		if index[name] < 0 {
			return nil, fmt.Errorf("missing required column: %s", name)
		}
	}
	return index, nil
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
	}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
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
		if row.Error != "" {
			preview.Action = "error"
			out.Summary.Skipped++
			out.Summary.Errors++
			out.Rows = append(out.Rows, preview)
			continue
		}
		if row.PlanName != "" {
			if _, ok := planByName[row.PlanName]; !ok {
				preview.Error = fmt.Sprintf("unknown plan template: %s", row.PlanName)
				preview.Action = "error"
				out.Summary.Skipped++
				out.Summary.Errors++
				out.Rows = append(out.Rows, preview)
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

		agrID, agrExists, err := s.findAgreementByExternalID(ctx, shopID, row.ExternalID)
		if err != nil {
			return ImportAnalysis{}, err
		}
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
		_ = agrID

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
		}

		agreementID, err := s.upsertImportAgreement(ctx, tx, shopID, customerID, propertyID, planID, row, visitsEntitled)
		if err != nil {
			return ImportCommitResult{}, err
		}
		if err := s.recordImportRow(ctx, tx, shopID, importSourceCSV, row.ExternalID, "agreement", agreementID, row.contentHash()); err != nil {
			return ImportCommitResult{}, err
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
		LIMIT 1`,
		shopID, row.CustomerName, row.AddressLine1, row.City, row.State, row.PostalCode,
	).Scan(&id)
	if err != nil {
		return "", false, nil
	}
	return id, true, nil
}

func (s *Store) findPropertyByExternalID(ctx context.Context, shopID, externalID string) (string, bool, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id FROM properties
		WHERE shop_id = $1 AND external_id = $2`, shopID, externalID,
	).Scan(&id)
	if err != nil {
		return "", false, nil
	}
	return id, true, nil
}

func (s *Store) findAgreementByExternalID(ctx context.Context, shopID, externalID string) (string, bool, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id FROM agreements
		WHERE shop_id = $1 AND external_id = $2`, shopID, externalID,
	).Scan(&id)
	if err != nil {
		return "", false, nil
	}
	return id, true, nil
}

func (s *Store) upsertImportCustomer(ctx context.Context, tx pgx.Tx, shopID string, row ImportRow) (string, error) {
	var id string
	err := tx.QueryRow(ctx, `
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
	if err == nil {
		return id, nil
	}

	existingID, found, err := s.findImportCustomer(ctx, shopID, row)
	if err != nil || !found {
		return "", fmt.Errorf("upsert customer %s: %w", row.ExternalID, err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE customers SET
			external_id = COALESCE(external_id, $3),
			name = $4,
			email = NULLIF($5, ''),
			phone = NULLIF($6, ''),
			updated_at = now()
		WHERE shop_id = $1 AND id = $2`,
		shopID, existingID, row.ExternalID, row.CustomerName, row.Email, row.Phone,
	)
	return existingID, err
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
			visits_entitled = EXCLUDED.visits_entitled,
			price_cents = EXCLUDED.price_cents,
			updated_at = now()
		RETURNING id`,
		shopID, customerID, propertyID, planPtr, row.ExternalID,
		string(row.Status), row.AnniversaryDate, visitsEntitled, row.PriceCents,
	).Scan(&id)
	return id, err
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

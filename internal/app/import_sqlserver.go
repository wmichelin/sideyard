package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

type importRowError struct {
	Line     int
	Message  string
	Conflict bool
}

func (e *importRowError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("CSV row %d: %s", e.Line, e.Message)
	}
	return e.Message
}

func importRowInputError(row ImportRow) string {
	if row.Error != "" {
		return row.Error
	}
	if !validStoredText(row.ExternalID, row.CustomerName, row.Email, row.Phone, row.AddressLine1, row.City, row.State, row.PostalCode, row.PlanName, row.ServiceExternalID, row.ServiceName, row.CadenceUnit) {
		return "Text must be valid UTF-8 without null characters."
	}
	if row.ExternalID == "" || row.CustomerName == "" || row.AddressLine1 == "" || row.City == "" || row.State == "" || row.PostalCode == "" {
		return "Complete the required customer and address fields."
	}
	if row.AnniversaryDate.Year() < 1 || row.AnniversaryDate.Year() > 9999 || !validSQLInt(row.PriceCents) || row.PriceCents < 0 {
		return "Check the anniversary date and price."
	}
	switch row.Status {
	case AgreementActive, AgreementPastDue, AgreementLapsed, AgreementCancelled, AgreementPendingRenewal:
	default:
		return "Check the agreement status."
	}
	if row.ServiceName != "" {
		if row.ServiceExternalID == "" || row.CadenceInterval < 1 || row.CadenceInterval > 365 || row.ServiceStartsOn.Year() < 1 || row.ServiceStartsOn.Year() > 9999 {
			return "Check the recurring service identity, cadence and dates."
		}
		switch row.CadenceUnit {
		case "day", "week", "month":
		default:
			return "Check the recurring service cadence."
		}
		if row.ServiceEndsOn != nil && (row.ServiceEndsOn.Year() < 1 || row.ServiceEndsOn.Year() > 9999 || row.ServiceEndsOn.Before(row.ServiceStartsOn)) {
			return "Check the recurring service end date."
		}
	}
	return ""
}
func validateImportRows(shopID string, rows []ImportRow) error {
	if err := validateRecordIDs(shopID); err != nil {
		return err
	}
	if len(rows) == 0 || len(rows) > maxImportRows {
		return &importRowError{Message: "Import between 1 and 10,000 rows."}
	}
	keys, services := map[string]bool{}, map[string]bool{}
	for _, row := range rows {
		if msg := importRowInputError(row); msg != "" {
			return &importRowError{Line: row.LineNum, Message: msg}
		}
		if keys[row.ExternalID] {
			return &importRowError{Line: row.LineNum, Message: "Duplicate external_id."}
		}
		keys[row.ExternalID] = true
		if row.ServiceName != "" {
			if services[row.ServiceExternalID] {
				return &importRowError{Line: row.LineNum, Message: "Duplicate service_external_id."}
			}
			services[row.ServiceExternalID] = true
		}
	}
	return nil
}
func firstImportAnalysisError(a ImportAnalysis) error {
	for _, row := range a.Rows {
		if row.Error != "" {
			return &importRowError{Line: row.LineNum, Message: row.Error, Conflict: true}
		}
	}
	return &importRowError{Message: "Preview the file again before committing.", Conflict: true}
}

// Hash keys narrow index reads; full binary text and byte length preserve exact
// identities. HOLDLOCK also retains missing keys throughout a native import.
const sqlServerFindImportCustomerExternal = `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.customers WITH(UPDLOCK,HOLDLOCK)
 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND external_id_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),@p2))
 AND external_id=@p2 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(external_id)=DATALENGTH(@p2)`
const sqlServerFindImportManualCustomer = `SELECT TOP(1) LOWER(CONVERT(char(36),c.id))
 FROM dbo.customers c WITH(UPDLOCK,HOLDLOCK) JOIN dbo.properties p WITH(HOLDLOCK) ON p.customer_id=c.id AND p.shop_id=c.shop_id
 WHERE c.shop_id=CONVERT(uniqueidentifier,@p1) AND c.external_id IS NULL
 AND c.name=@p2 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(c.name)=DATALENGTH(@p2)
 AND p.address_line1=@p3 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(p.address_line1)=DATALENGTH(@p3)
 AND p.city=@p4 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(p.city)=DATALENGTH(@p4)
 AND p.state=@p5 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(p.state)=DATALENGTH(@p5)
 AND p.postal_code=@p6 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(p.postal_code)=DATALENGTH(@p6)
 ORDER BY CONVERT(char(36),c.id)`
const sqlServerFindImportProperty = `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.properties WITH(UPDLOCK,HOLDLOCK)
 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND external_id_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),@p2))
 AND external_id=@p2 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(external_id)=DATALENGTH(@p2)`
const sqlServerFindImportAgreement = `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK)
 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND external_id_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),@p2))
 AND external_id=@p2 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(external_id)=DATALENGTH(@p2)`
const sqlServerFindImportSchedule = `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.service_schedules WITH(UPDLOCK,HOLDLOCK)
 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND external_id_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),@p2))
 AND external_id=@p2 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(external_id)=DATALENGTH(@p2)`
const sqlServerFindImportScheduleOwner = `SELECT LOWER(CONVERT(char(36),agreement_id)) FROM dbo.service_schedules WITH(UPDLOCK,HOLDLOCK)
 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND external_id_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),@p2))
 AND external_id=@p2 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(external_id)=DATALENGTH(@p2)`
const sqlServerFindImportCapturedPlan = `SELECT plan_snapshot_name FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK)
 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`
const sqlServerFindImportServiceIDs = `SELECT external_id FROM dbo.service_schedules WITH(UPDLOCK,HOLDLOCK)
 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND agreement_id=CONVERT(uniqueidentifier,@p2) AND external_id IS NOT NULL ORDER BY external_id`

func (s *Store) commitImportSQLServer(ctx context.Context, shopID string, rows []ImportRow) (ImportCommitResult, error) {
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return ImportCommitResult{}, err
	}
	defer tx.Rollback(ctx)
	// Work transactions retain a shared shop gate. An exclusive import gate avoids
	// acquiring several agreements in the opposite order to concurrent work batches.
	var found int
	if err = tx.QueryRow(ctx, `SELECT 1 FROM dbo.shops WITH(XLOCK,HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, shopID).Scan(&found); err != nil {
		return ImportCommitResult{}, err
	}
	now := s.nowUTC().Truncate(time.Microsecond)
	if now.Year() < 1 || now.Year() > 9999 {
		return ImportCommitResult{}, errors.New("import clock outside supported range")
	}
	plans, err := listImportPlansSQLServer(ctx, tx, shopID)
	if err != nil {
		return ImportCommitResult{}, err
	}
	analysis, err := analyzeImport(ctx, tx, shopID, rows, plans)
	if err != nil {
		return ImportCommitResult{}, err
	}
	if analysis.Summary.Errors > 0 {
		return ImportCommitResult{}, firstImportAnalysisError(analysis)
	}
	planByName := map[string]PlanTemplate{}
	for _, p := range plans {
		planByName[p.Name] = p
	}
	result := ImportCommitResult{}
	type mappedRow struct {
		row                           ImportRow
		customer, property, agreement string
	}
	mapped := make([]mappedRow, 0, len(rows))
	included := map[string]bool{}
	for _, row := range rows {
		customer, exists, err := findImportCustomer(ctx, tx, shopID, row)
		if err != nil {
			return ImportCommitResult{}, err
		}
		if exists {
			result.Summary.UpdatedCustomers++
			_, err = tx.Exec(ctx, `UPDATE dbo.customers SET external_id=@p3,name=@p4,email=CASE WHEN DATALENGTH(@p5)=0 THEN NULL ELSE @p5 END,phone=CASE WHEN DATALENGTH(@p6)=0 THEN NULL ELSE @p6 END,updated_at=@p7 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, customer, row.ExternalID, row.CustomerName, row.Email, row.Phone, now)
		} else {
			result.Summary.NewCustomers++
			err = tx.QueryRow(ctx, `DECLARE @created TABLE(id uniqueidentifier); INSERT INTO dbo.customers(shop_id,external_id,name,email,phone,created_at,updated_at) OUTPUT inserted.id INTO @created VALUES(CONVERT(uniqueidentifier,@p1),@p2,@p3,CASE WHEN DATALENGTH(@p4)=0 THEN NULL ELSE @p4 END,CASE WHEN DATALENGTH(@p5)=0 THEN NULL ELSE @p5 END,@p6,@p6); SELECT LOWER(CONVERT(char(36),id)) FROM @created;`, shopID, row.ExternalID, row.CustomerName, row.Email, row.Phone, now).Scan(&customer)
		}
		if err != nil {
			return ImportCommitResult{}, err
		}
		property, exists, err := findPropertyByExternalID(ctx, tx, shopID, row.ExternalID)
		if err != nil {
			return ImportCommitResult{}, err
		}
		if exists {
			result.Summary.UpdatedProperties++
		} else {
			result.Summary.NewProperties++
		}
		agreement, exists, err := findAgreementByExternalID(ctx, tx, shopID, row.ExternalID)
		if err != nil {
			return ImportCommitResult{}, err
		}
		if exists {
			result.Summary.UpdatedAgreements++
			included[agreement] = true
		} else {
			result.Summary.NewAgreements++
		}
		mapped = append(mapped, mappedRow{row, customer, property, agreement})
	}
	// SQL Server has immediate ownership guards. Temporarily detach only CSV-owned
	// agreements; the same transaction must restore their final valid references.
	for _, m := range mapped {
		if m.property == "" {
			continue
		}
		var owner string
		if err := tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),customer_id)) FROM dbo.properties WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, m.property).Scan(&owner); err != nil {
			return ImportCommitResult{}, err
		}
		if owner == m.customer {
			continue
		}
		linked, err := importPropertyAgreementIDs(ctx, tx, shopID, m.property)
		if err != nil {
			return ImportCommitResult{}, err
		}
		for _, id := range linked {
			if !included[id] {
				return ImportCommitResult{}, &importRowError{Line: m.row.LineNum, Message: "This property has another agreement outside this file. Correct its customer in Sideyard first.", Conflict: true}
			}
		}
		var jobs int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.jobs WITH(HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND property_id=CONVERT(uniqueidentifier,@p2)`, shopID, m.property).Scan(&jobs); err != nil {
			return ImportCommitResult{}, err
		}
		if jobs > 0 {
			return ImportCommitResult{}, &importRowError{Line: m.row.LineNum, Message: "This property has jobs for its current customer. Correct the customer relationship before importing.", Conflict: true}
		}
		for _, id := range linked {
			if _, err := tx.Exec(ctx, `UPDATE dbo.agreements SET property_id=NULL WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, id); err != nil {
				return ImportCommitResult{}, err
			}
		}
	}
	// Move all properties before reattaching any agreement, including cross-row moves.
	for i := range mapped {
		m := &mapped[i]
		m.property, err = upsertImportPropertySQLServer(ctx, tx, shopID, m.customer, m.row, now)
		if err != nil {
			return ImportCommitResult{}, err
		}
	}
	for _, m := range mapped {
		row := m.row
		agreement := m.agreement
		if agreement == "" {
			var planID *string
			visits := 2
			if row.PlanName != "" {
				p := planByName[row.PlanName]
				planID = &p.ID
				visits = p.VisitsPerYear
			} else if row.ServiceName != "" {
				visits = 0
			}
			err = tx.QueryRow(ctx, sqlServerCreateAgreement, shopID, m.customer, m.property, planID, row.ExternalID, string(row.Status), row.AnniversaryDate.Format("2006-01-02"), visits, 0, row.PriceCents, "").Scan(&agreement)
		} else {
			_, err = tx.Exec(ctx, `UPDATE dbo.agreements SET customer_id=CONVERT(uniqueidentifier,@p3),property_id=CONVERT(uniqueidentifier,@p4),status=@p5,anniversary_date=CONVERT(date,@p6),price_cents=@p7,updated_at=@p8 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, agreement, m.customer, m.property, string(row.Status), row.AnniversaryDate.Format("2006-01-02"), row.PriceCents, now)
		}
		if err != nil {
			return ImportCommitResult{}, err
		}
		records := []struct{ external, kind, id string }{{row.ExternalID, "customer", m.customer}, {row.ExternalID, "property", m.property}, {row.ExternalID, "agreement", agreement}}
		if row.ServiceName != "" {
			schedule, exists, err := upsertImportScheduleSQLServer(ctx, tx, shopID, agreement, row, now)
			if err != nil {
				return ImportCommitResult{}, err
			}
			if exists {
				result.Summary.UpdatedSchedules++
			} else {
				result.Summary.NewSchedules++
			}
			records = append(records, struct{ external, kind, id string }{row.ServiceExternalID, "service_schedule", schedule})
		}
		for _, record := range records {
			if err := recordImportRowSQLServer(ctx, tx, shopID, importSourceCSV, record.external, record.kind, record.id, row.contentHash(), now); err != nil {
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

func listImportPlansSQLServer(ctx context.Context, q database.Queryer, shopID string) ([]PlanTemplate, error) {
	rows, err := q.Query(ctx, `SELECT LOWER(CONVERT(char(36),id)),name,visits_per_year FROM dbo.plan_templates WITH(HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlanTemplate
	for rows.Next() {
		var p PlanTemplate
		if err := rows.Scan(&p.ID, &p.Name, &p.VisitsPerYear); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func importPropertyAgreementIDs(ctx context.Context, tx database.Tx, shopID, propertyID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND property_id=CONVERT(uniqueidentifier,@p2)`, shopID, propertyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
func upsertImportPropertySQLServer(ctx context.Context, tx database.Tx, shopID, customerID string, row ImportRow, now time.Time) (string, error) {
	id, exists, err := findPropertyByExternalID(ctx, tx, shopID, row.ExternalID)
	if err != nil {
		return "", err
	}
	if exists {
		_, err = tx.Exec(ctx, `UPDATE dbo.properties SET customer_id=CONVERT(uniqueidentifier,@p3),address_line1=@p4,city=@p5,state=@p6,postal_code=@p7,updated_at=@p8 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, id, customerID, row.AddressLine1, row.City, row.State, row.PostalCode, now)
	} else {
		err = tx.QueryRow(ctx, `DECLARE @created TABLE(id uniqueidentifier); INSERT INTO dbo.properties(shop_id,customer_id,external_id,address_line1,city,state,postal_code,created_at,updated_at) OUTPUT inserted.id INTO @created VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,@p4,@p5,@p6,@p7,@p8,@p8); SELECT LOWER(CONVERT(char(36),id)) FROM @created;`, shopID, customerID, row.ExternalID, row.AddressLine1, row.City, row.State, row.PostalCode, now).Scan(&id)
	}
	return id, err
}

func upsertImportScheduleSQLServer(ctx context.Context, tx database.Tx, shopID, agreementID string, row ImportRow, now time.Time) (string, bool, error) {
	id, exists, err := findServiceScheduleByExternalID(ctx, tx, shopID, row.ServiceExternalID)
	if err != nil {
		return "", false, err
	}
	var end any
	if row.ServiceEndsOn != nil {
		end = row.ServiceEndsOn.Format("2006-01-02")
	}
	if !exists {
		err = tx.QueryRow(ctx, `DECLARE @created TABLE(id uniqueidentifier); INSERT INTO dbo.service_schedules(shop_id,agreement_id,external_id,service_name,cadence_unit,cadence_interval,starts_on,ends_on,created_at,updated_at) OUTPUT inserted.id INTO @created VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,@p4,@p5,@p6,CONVERT(date,@p7),CONVERT(date,@p8),@p9,@p9); SELECT LOWER(CONVERT(char(36),id)) FROM @created;`, shopID, agreementID, row.ServiceExternalID, row.ServiceName, row.CadenceUnit, row.CadenceInterval, row.ServiceStartsOn.Format("2006-01-02"), end, now).Scan(&id)
		return id, false, err
	}
	var owner, name, unit string
	var interval int
	var start time.Time
	var ends, generated *time.Time
	err = tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),agreement_id)),service_name,cadence_unit,cadence_interval,starts_on,ends_on,generated_through FROM dbo.service_schedules WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, id).Scan(&owner, &name, &unit, &interval, &start, &ends, &generated)
	if err != nil {
		return "", true, err
	}
	if owner != agreementID {
		return "", true, &importRowError{Line: row.LineNum, Message: "The recurring service belongs to another agreement.", Conflict: true}
	}
	if name == row.ServiceName && unit == row.CadenceUnit && interval == row.CadenceInterval && dateEqual(start, row.ServiceStartsOn) && optionalDateEqual(ends, row.ServiceEndsOn) {
		return id, true, nil
	}
	var checkpoint any
	if generated != nil {
		yesterday := now.AddDate(0, 0, -1)
		if yesterday.Year() < 1 {
			return "", true, &importRowError{Line: row.LineNum, Message: "The recurrence checkpoint is outside the supported date range."}
		}
		checkpoint = yesterday.Format("2006-01-02")
	}
	_, err = tx.Exec(ctx, `UPDATE dbo.service_schedules SET service_name=@p3,cadence_unit=@p4,cadence_interval=@p5,starts_on=CONVERT(date,@p6),ends_on=CONVERT(date,@p7),generated_through=CONVERT(date,@p8),updated_at=@p9 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, id, row.ServiceName, row.CadenceUnit, row.CadenceInterval, row.ServiceStartsOn.Format("2006-01-02"), end, checkpoint, now)
	if err != nil {
		return "", true, err
	}
	_, err = tx.Exec(ctx, `DELETE FROM dbo.service_occurrences WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND schedule_id=CONVERT(uniqueidentifier,@p2) AND status=N'due' AND (due_on>=CONVERT(date,@p3) OR effective_on>=CONVERT(date,@p3))`, shopID, id, now.Format("2006-01-02"))
	return id, true, err
}

func recordImportRowSQLServer(ctx context.Context, tx database.Tx, shopID, source, externalID, entityType, entityID, contentHash string, now time.Time) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.import_rows WITH(UPDLOCK,HOLDLOCK)
 WHERE shop_id=CONVERT(uniqueidentifier,@p1)
 AND source_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),@p2)) AND source=@p2 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(source)=DATALENGTH(@p2)
 AND external_id_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),@p3)) AND external_id=@p3 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(external_id)=DATALENGTH(@p3)
 AND entity_type_key=HASHBYTES('SHA2_256',CONVERT(varbinary(max),@p4)) AND entity_type=@p4 COLLATE Latin1_General_100_BIN2 AND DATALENGTH(entity_type)=DATALENGTH(@p4)`, shopID, source, externalID, entityType).Scan(&id)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE dbo.import_rows SET entity_id=CONVERT(uniqueidentifier,@p3),content_hash=@p4,imported_at=@p5 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, id, entityID, contentHash, now)
	} else if errors.Is(err, database.ErrNoRows) {
		_, err = tx.Exec(ctx, `INSERT INTO dbo.import_rows(shop_id,source,external_id,entity_type,entity_id,content_hash,imported_at) VALUES(CONVERT(uniqueidentifier,@p1),@p2,@p3,@p4,CONVERT(uniqueidentifier,@p5),@p6,@p7)`, shopID, source, externalID, entityType, entityID, contentHash, now)
	}
	return err
}

package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/wmichelin/clubdue/internal/database"
)

type AgreementStatus string

const (
	AgreementActive         AgreementStatus = "active"
	AgreementPastDue        AgreementStatus = "past_due"
	AgreementLapsed         AgreementStatus = "lapsed"
	AgreementCancelled      AgreementStatus = "cancelled"
	AgreementPendingRenewal AgreementStatus = "pending_renewal"
)

type Customer struct {
	ID         string
	ExternalID string
	Name       string
	Email      string
	Phone      string
}

type Property struct {
	ID           string
	CustomerID   string
	AddressLine1 string
	AddressLine2 string
	City         string
	State        string
	PostalCode   string
	Equipment    []Equipment
}

type Equipment struct {
	ID           string
	PropertyID   string
	Kind         string
	Make         string
	Model        string
	SerialNumber string
}

type PlanTemplate struct {
	ID              string
	Name            string
	Description     string
	PriceCents      int
	BillingInterval string
	TermMonths      int
	VisitsPerYear   int
}

type Agreement struct {
	ID              string
	CustomerID      string
	CustomerName    string
	PropertyID      string
	PropertyAddress string
	PlanTemplateID  string
	PlanName        string
	ExternalID      string
	Status          AgreementStatus
	AnniversaryDate time.Time
	VisitsEntitled  int
	VisitsUsed      int
	PriceCents      int
	Notes           string
}

type DashboardStats struct {
	ActiveCount      int
	RenewalsDueMonth int
	RenewalsDue90    int
	LapsedMTD        int
	MRRCents         int
	VisitsOverdue    int
}

type RenewalBucket struct {
	Label string
	Days  int
	Items []RenewalItem
}

type RenewalItem struct {
	AgreementID     string
	CustomerName    string
	PropertyAddress string
	PlanName        string
	AnniversaryDate time.Time
	DaysUntil       int
	PriceCents      int
	PriceFmt        string
	AnniversaryFmt  string
}

type Store struct {
	db                 *DB
	clock              Clock
	publicDemoSeedOnce sync.Once
	publicDemoSeedGate chan struct{}
	photoObjects       photoObjectStorage
}

var errCustomerDeleteConflict = errors.New("customer has agreements or jobs")

func isCustomerDeleteForeignKeyConflict(err error) bool {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23503" {
		return true
	}
	var sqlServerError mssql.Error
	return errors.As(err, &sqlServerError) && sqlServerError.Number == 547
}

func (s *Store) nowUTC() time.Time {
	return utcNow(s.clock)
}

func (s *Store) ListCustomers(ctx context.Context, shopID string) ([]Customer, error) {
	if err := validateRecordIDs(shopID); err != nil {
		return nil, err
	}

	rows, err := s.db.Pool.Query(ctx, database.Statement(s.db.Pool.Dialect(), `
		SELECT id, COALESCE(external_id, ''), name, COALESCE(email, ''), COALESCE(phone, '')
		FROM customers
		WHERE shop_id = $1
		ORDER BY name`, `SELECT LOWER(CONVERT(char(36),id)),COALESCE(external_id,N''),name,COALESCE(email,N''),COALESCE(phone,N'') FROM dbo.customers WHERE shop_id=CONVERT(uniqueidentifier,@p1) ORDER BY name`), shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Customer
	for rows.Next() {
		var c Customer
		if err := rows.Scan(&c.ID, &c.ExternalID, &c.Name, &c.Email, &c.Phone); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) CreateCustomer(ctx context.Context, shopID, name, email, phone string) (string, error) {
	if !validStoredText(name, email, phone) {
		return "", errInvalidStoredText
	}

	if err := validateRecordIDs(shopID); err != nil {
		return "", err
	}

	var id string
	err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `
		INSERT INTO customers (shop_id, name, email, phone)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''))
		RETURNING id`, `DECLARE @created TABLE(id uniqueidentifier);
INSERT INTO dbo.customers(shop_id,name,email,phone) OUTPUT inserted.id INTO @created
VALUES(CONVERT(uniqueidentifier,@p1),@p2,CASE WHEN DATALENGTH(@p3)=0 THEN NULL ELSE @p3 END,CASE WHEN DATALENGTH(@p4)=0 THEN NULL ELSE @p4 END);
SELECT LOWER(CONVERT(char(36),id)) FROM @created;`), shopID, name, email, phone).Scan(&id)
	return id, err
}

func (s *Store) GetCustomer(ctx context.Context, shopID, customerID string) (*Customer, error) {
	if err := validateRecordIDs(shopID, customerID); err != nil {
		return nil, err
	}

	var c Customer
	err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `
		SELECT id, COALESCE(external_id, ''), name, COALESCE(email, ''), COALESCE(phone, '')
		FROM customers WHERE shop_id = $1 AND id = $2`, `SELECT LOWER(CONVERT(char(36),id)),COALESCE(external_id,N''),name,COALESCE(email,N''),COALESCE(phone,N'') FROM dbo.customers WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, customerID,
	).Scan(&c.ID, &c.ExternalID, &c.Name, &c.Email, &c.Phone)
	if err != nil {
		return nil, fmt.Errorf("load customer: %w", err)
	}
	return &c, nil
}

func (s *Store) UpdateCustomer(ctx context.Context, shopID, customerID, name, email, phone string) error {
	if !validStoredText(name, email, phone) {
		return errInvalidStoredText
	}

	if err := validateRecordIDs(shopID, customerID); err != nil {
		return err
	}

	tag, err := s.db.Pool.Exec(ctx, database.Statement(s.db.Pool.Dialect(), `
		UPDATE customers SET name = $3, email = NULLIF($4, ''), phone = NULLIF($5, ''), updated_at = now()
		WHERE shop_id = $1 AND id = $2`, `UPDATE dbo.customers SET name=@p3,email=CASE WHEN DATALENGTH(@p4)=0 THEN NULL ELSE @p4 END,phone=CASE WHEN DATALENGTH(@p5)=0 THEN NULL ELSE @p5 END,updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, customerID, name, email, phone)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteCustomer(ctx context.Context, shopID, customerID string) error {
	if err := validateRecordIDs(shopID, customerID); err != nil {
		return err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var lockedID string
	if err := tx.QueryRow(ctx, database.Statement(tx.Dialect(), `SELECT id FROM customers WHERE shop_id=$1 AND id=$2 FOR UPDATE`, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.customers WITH(XLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, customerID).Scan(&lockedID); err != nil {
		return err
	}

	var dependent int
	if err := tx.QueryRow(ctx, database.Statement(tx.Dialect(), `SELECT CASE WHEN EXISTS(SELECT 1 FROM agreements WHERE shop_id=$1 AND customer_id=$2) OR EXISTS(SELECT 1 FROM jobs WHERE shop_id=$1 AND customer_id=$2) THEN 1 ELSE 0 END`, `SELECT CASE WHEN EXISTS(SELECT 1 FROM dbo.agreements WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND customer_id=CONVERT(uniqueidentifier,@p2)) OR EXISTS(SELECT 1 FROM dbo.jobs WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND customer_id=CONVERT(uniqueidentifier,@p2)) THEN 1 ELSE 0 END`), shopID, customerID).Scan(&dependent); err != nil {
		return err
	}
	if dependent == 1 {
		return errCustomerDeleteConflict
	}
	tag, err := tx.Exec(ctx, database.Statement(tx.Dialect(), `DELETE FROM customers WHERE shop_id = $1 AND id = $2`, `DELETE FROM dbo.customers WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, customerID)
	if err != nil {
		if isCustomerDeleteForeignKeyConflict(err) {
			return errCustomerDeleteConflict
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNoRows
	}
	return tx.Commit(ctx)
}

func (s *Store) ListProperties(ctx context.Context, shopID, customerID string) ([]Property, error) {
	if err := validateRecordIDs(shopID, customerID); err != nil {
		return nil, err
	}

	rows, err := s.db.Pool.Query(ctx, database.Statement(s.db.Pool.Dialect(), `
		SELECT id, customer_id, address_line1, COALESCE(address_line2, ''), city, state, postal_code
		FROM properties WHERE shop_id = $1 AND customer_id = $2 ORDER BY address_line1`, `SELECT LOWER(CONVERT(char(36),id)),LOWER(CONVERT(char(36),customer_id)),address_line1,COALESCE(address_line2,N''),city,state,postal_code FROM dbo.properties WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND customer_id=CONVERT(uniqueidentifier,@p2) ORDER BY address_line1`), shopID, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var properties []Property
	for rows.Next() {
		var p Property
		if err := rows.Scan(&p.ID, &p.CustomerID, &p.AddressLine1, &p.AddressLine2, &p.City, &p.State, &p.PostalCode); err != nil {
			return nil, err
		}
		properties = append(properties, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// Release the property query before borrowing another pool connection.
	for i := range properties {
		equipment, err := s.ListEquipment(ctx, shopID, properties[i].ID)
		if err != nil {
			return nil, err
		}
		properties[i].Equipment = equipment
	}
	return properties, nil
}

func (s *Store) ListAllProperties(ctx context.Context, shopID string) ([]Property, error) {
	if err := validateRecordIDs(shopID); err != nil {
		return nil, err
	}

	rows, err := s.db.Pool.Query(ctx, database.Statement(s.db.Pool.Dialect(), `
		SELECT id, customer_id, address_line1, COALESCE(address_line2, ''), city, state, postal_code
		FROM properties WHERE shop_id = $1 ORDER BY address_line1`, `SELECT LOWER(CONVERT(char(36),id)),LOWER(CONVERT(char(36),customer_id)),address_line1,COALESCE(address_line2,N''),city,state,postal_code FROM dbo.properties WHERE shop_id=CONVERT(uniqueidentifier,@p1) ORDER BY address_line1`), shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Property
	for rows.Next() {
		var p Property
		if err := rows.Scan(&p.ID, &p.CustomerID, &p.AddressLine1, &p.AddressLine2, &p.City, &p.State, &p.PostalCode); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreateProperty(ctx context.Context, shopID, customerID, address1, address2, city, state, postal string) (string, error) {
	if !validStoredText(address1, address2, city, state, postal) {
		return "", errInvalidStoredText
	}

	if err := validateRecordIDs(shopID, customerID); err != nil {
		return "", err
	}

	var id string
	err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `
		INSERT INTO properties (shop_id, customer_id, address_line1, address_line2, city, state, postal_code)
		SELECT $1, id, $3, NULLIF($4, ''), $5, $6, $7 FROM customers WHERE shop_id = $1 AND id = $2
		RETURNING id`, `DECLARE @created TABLE(id uniqueidentifier);
INSERT INTO dbo.properties(shop_id,customer_id,address_line1,address_line2,city,state,postal_code) OUTPUT inserted.id INTO @created
SELECT CONVERT(uniqueidentifier,@p1),id,@p3,CASE WHEN DATALENGTH(@p4)=0 THEN NULL ELSE @p4 END,@p5,@p6,@p7 FROM dbo.customers WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2);
SELECT LOWER(CONVERT(char(36),id)) FROM @created;`), shopID, customerID, address1, address2, city, state, postal).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create property: %w", err)
	}
	return id, nil
}

func (s *Store) ListEquipment(ctx context.Context, shopID, propertyID string) ([]Equipment, error) {
	if err := validateRecordIDs(shopID, propertyID); err != nil {
		return nil, err
	}

	rows, err := s.db.Pool.Query(ctx, database.Statement(s.db.Pool.Dialect(), `
		SELECT id, property_id, kind, COALESCE(make, ''), COALESCE(model, ''), COALESCE(serial_number, '')
		FROM equipment WHERE shop_id = $1 AND property_id = $2 ORDER BY kind`, `SELECT LOWER(CONVERT(char(36),id)),LOWER(CONVERT(char(36),property_id)),kind,COALESCE(make,N''),COALESCE(model,N''),COALESCE(serial_number,N'') FROM dbo.equipment WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND property_id=CONVERT(uniqueidentifier,@p2) ORDER BY kind`), shopID, propertyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var equipment []Equipment
	for rows.Next() {
		var e Equipment
		if err := rows.Scan(&e.ID, &e.PropertyID, &e.Kind, &e.Make, &e.Model, &e.SerialNumber); err != nil {
			return nil, err
		}
		equipment = append(equipment, e)
	}
	return equipment, rows.Err()
}

func (s *Store) CreateEquipment(ctx context.Context, shopID, propertyID, kind, make, model, serial string) error {
	if !validStoredText(kind, make, model, serial) {
		return errInvalidStoredText
	}

	if err := validateRecordIDs(shopID, propertyID); err != nil {
		return err
	}

	tag, err := s.db.Pool.Exec(ctx, database.Statement(s.db.Pool.Dialect(), `
		INSERT INTO equipment (shop_id, property_id, kind, make, model, serial_number)
		SELECT $1, id, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, '')
		FROM properties WHERE shop_id = $1 AND id = $2`, `INSERT INTO dbo.equipment(shop_id,property_id,kind,make,model,serial_number)
SELECT CONVERT(uniqueidentifier,@p1),id,@p3,CASE WHEN DATALENGTH(@p4)=0 THEN NULL ELSE @p4 END,CASE WHEN DATALENGTH(@p5)=0 THEN NULL ELSE @p5 END,CASE WHEN DATALENGTH(@p6)=0 THEN NULL ELSE @p6 END FROM dbo.properties WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, propertyID, kind, make, model, serial)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNoRows
	}
	return nil
}

func (s *Store) ListPlanTemplates(ctx context.Context, shopID string) ([]PlanTemplate, error) {
	if err := validateRecordIDs(shopID); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, database.Statement(s.db.Pool.Dialect(), `
		SELECT id, name, COALESCE(description, ''), price_cents, billing_interval, term_months, visits_per_year
		FROM plan_templates
		WHERE shop_id = $1
		ORDER BY name`, `SELECT LOWER(CONVERT(char(36),id)),name,COALESCE(description,N''),price_cents,billing_interval,term_months,visits_per_year FROM dbo.plan_templates WHERE shop_id=CONVERT(uniqueidentifier,@p1)`), shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PlanTemplate
	for rows.Next() {
		var p PlanTemplate
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.BillingInterval, &p.TermMonths, &p.VisitsPerYear); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if s.db.Pool.Dialect() == database.SQLServer {
		slices.SortFunc(out, func(a, b PlanTemplate) int { return cmp.Compare(a.Name, b.Name) })
	}
	return out, rows.Err()
}

func (s *Store) CreatePlanTemplate(ctx context.Context, shopID, name, description string, priceCents, termMonths, visitsPerYear int) (string, error) {
	if err := validateRecordIDs(shopID); err != nil {
		return "", err
	}
	if !validStoredText(name, description) || !validSQLInt(priceCents, termMonths, visitsPerYear) || termMonths <= 0 {
		return "", errPlanInput
	}
	var id string
	err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `
		INSERT INTO plan_templates (shop_id, name, description, price_cents, term_months, visits_per_year)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6)
		RETURNING id`, `DECLARE @created TABLE(id uniqueidentifier);
INSERT INTO dbo.plan_templates(shop_id,name,description,price_cents,term_months,visits_per_year) OUTPUT inserted.id INTO @created
VALUES(CONVERT(uniqueidentifier,@p1),@p2,CASE WHEN DATALENGTH(@p3)=0 THEN NULL ELSE @p3 END,@p4,@p5,@p6);
SELECT LOWER(CONVERT(char(36),id)) FROM @created;`), shopID, name, description, priceCents, termMonths, visitsPerYear).Scan(&id)
	return id, err
}

func (s *Store) GetPlanTemplate(ctx context.Context, shopID, planID string) (*PlanTemplate, error) {
	if err := validateRecordIDs(shopID, planID); err != nil {
		return nil, err
	}
	var p PlanTemplate
	err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `
		SELECT id, name, COALESCE(description, ''), price_cents, billing_interval, term_months, visits_per_year
		FROM plan_templates WHERE shop_id = $1 AND id = $2`, `SELECT LOWER(CONVERT(char(36),id)),name,COALESCE(description,N''),price_cents,billing_interval,term_months,visits_per_year FROM dbo.plan_templates WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`), shopID, planID,
	).Scan(&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.BillingInterval, &p.TermMonths, &p.VisitsPerYear)
	if err != nil {
		return nil, fmt.Errorf("get plan template: %w", err)
	}
	return &p, nil
}

func (s *Store) UpdatePlanTemplate(ctx context.Context, shopID, planID, name, description string, priceCents, termMonths, visitsPerYear int) error {
	if err := validateRecordIDs(shopID, planID); err != nil {
		return err
	}
	if !validStoredText(name, description) || !validSQLInt(priceCents, termMonths, visitsPerYear) || termMonths <= 0 {
		return errPlanInput
	}
	tag, err := s.db.Pool.Exec(ctx, database.Statement(s.db.Pool.Dialect(), `
		UPDATE plan_templates
		SET name = $3, description = NULLIF($4, ''), price_cents = $5, term_months = $6, visits_per_year = $7
		WHERE shop_id = $1 AND id = $2`, `UPDATE dbo.plan_templates SET name=@p3,description=CASE WHEN DATALENGTH(@p4)=0 THEN NULL ELSE @p4 END,price_cents=@p5,term_months=@p6,visits_per_year=@p7 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`),
		shopID, planID, name, description, priceCents, termMonths, visitsPerYear,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNoRows
	}
	return nil
}

func (s *Store) ListAgreements(ctx context.Context, shopID string) ([]Agreement, error) {
	if err := validateRecordIDs(shopID); err != nil {
		return nil, err
	}
	rows, err := s.db.Pool.Query(ctx, database.Statement(s.db.Pool.Dialect(), `
		SELECT
			a.id,
			a.customer_id,
			c.name,
			COALESCE(a.property_id::text, ''),
			COALESCE(p.address_line1 || ', ' || p.city || ', ' || p.state, ''),
			COALESCE(a.plan_template_id::text, ''),
			a.plan_snapshot_name,
			COALESCE(a.external_id, ''),
			a.status,
			a.anniversary_date,
			a.visits_entitled,
			a.visits_used,
			a.price_cents,
			COALESCE(a.notes, '')
		FROM agreements a
		JOIN customers c ON c.id = a.customer_id
		LEFT JOIN properties p ON p.id = a.property_id
		WHERE a.shop_id = $1
		ORDER BY a.anniversary_date`, `SELECT `+sqlServerAgreementColumns+sqlServerAgreementFrom+`WHERE a.shop_id=CONVERT(uniqueidentifier,@p1) ORDER BY a.anniversary_date`), shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAgreements(rows)
}

func (s *Store) GetAgreement(ctx context.Context, shopID, agreementID string) (*Agreement, error) {
	if err := validateRecordIDs(shopID, agreementID); err != nil {
		return nil, err
	}
	var a Agreement
	var status string
	err := s.db.Pool.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `
		SELECT
			a.id,
			a.customer_id,
			c.name,
			COALESCE(a.property_id::text, ''),
			COALESCE(p.address_line1 || ', ' || p.city || ', ' || p.state, ''),
			COALESCE(a.plan_template_id::text, ''),
			a.plan_snapshot_name,
			COALESCE(a.external_id, ''),
			a.status,
			a.anniversary_date,
			a.visits_entitled,
			a.visits_used,
			a.price_cents,
			COALESCE(a.notes, '')
		FROM agreements a
		JOIN customers c ON c.id = a.customer_id
		LEFT JOIN properties p ON p.id = a.property_id
		WHERE a.shop_id = $1 AND a.id = $2`, `SELECT `+sqlServerAgreementColumns+sqlServerAgreementFrom+`WHERE a.shop_id=CONVERT(uniqueidentifier,@p1) AND a.id=CONVERT(uniqueidentifier,@p2)`), shopID, agreementID).Scan(
		&a.ID, &a.CustomerID, &a.CustomerName,
		&a.PropertyID, &a.PropertyAddress,
		&a.PlanTemplateID, &a.PlanName,
		&a.ExternalID, &status,
		&a.AnniversaryDate, &a.VisitsEntitled, &a.VisitsUsed,
		&a.PriceCents, &a.Notes,
	)
	if err != nil {
		return nil, fmt.Errorf("get agreement: %w", err)
	}
	a.Status = AgreementStatus(status)
	return &a, nil
}

type agreementRows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}

func scanAgreements(rows agreementRows) ([]Agreement, error) {
	var out []Agreement
	for rows.Next() {
		var a Agreement
		var status string
		if err := rows.Scan(
			&a.ID, &a.CustomerID, &a.CustomerName,
			&a.PropertyID, &a.PropertyAddress,
			&a.PlanTemplateID, &a.PlanName,
			&a.ExternalID, &status,
			&a.AnniversaryDate, &a.VisitsEntitled, &a.VisitsUsed,
			&a.PriceCents, &a.Notes,
		); err != nil {
			return nil, err
		}
		a.Status = AgreementStatus(status)
		out = append(out, a)
	}
	return out, rows.Err()
}

type CreateAgreementInput struct {
	CustomerID      string
	PropertyID      string
	PlanTemplateID  string
	ExternalID      string
	Status          AgreementStatus
	AnniversaryDate time.Time
	VisitsEntitled  int
	VisitsUsed      int
	PriceCents      int
	Notes           string
}

func (s *Store) CreateAgreement(ctx context.Context, shopID string, in CreateAgreementInput) (string, error) {
	if err := validateAgreementInput(shopID, in); err != nil {
		return "", err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if err := lockSQLServerAgreementParents(ctx, tx, shopID, in, false); err != nil {
		return "", err
	}
	var id string
	var anniversaryValue any = in.AnniversaryDate
	if s.db.Pool.Dialect() == database.SQLServer {
		anniversaryValue = in.AnniversaryDate.Format("2006-01-02")
	}
	var planID *string
	if in.PlanTemplateID != "" {
		planID = &in.PlanTemplateID
	}
	var propertyID *string
	if in.PropertyID != "" {
		propertyID = &in.PropertyID
	}
	err = tx.QueryRow(ctx, database.Statement(s.db.Pool.Dialect(), `
		INSERT INTO agreements (
			shop_id, customer_id, property_id, plan_template_id, external_id, status,
			anniversary_date, visits_entitled, visits_used, price_cents, notes,
			plan_snapshot_name, plan_snapshot_description, plan_snapshot_billing_interval,
			plan_snapshot_term_months, plan_snapshot_visits_per_year, plan_snapshot_visit_windows
		)
		SELECT $1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, $10, NULLIF($11, ''),
			COALESCE(pt.name, 'Custom plan'), COALESCE(pt.description, ''),
			COALESCE(pt.billing_interval, 'annual'), COALESCE(pt.term_months, 12),
			COALESCE(pt.visits_per_year, $8), COALESCE(pt.visit_windows, '{spring,fall}')
		FROM (SELECT $4::uuid AS plan_id) selected
		LEFT JOIN plan_templates pt ON pt.id = selected.plan_id AND pt.shop_id = $1
		RETURNING id`, sqlServerCreateAgreement),
		shopID, in.CustomerID, propertyID, planID, in.ExternalID, string(in.Status),
		anniversaryValue, in.VisitsEntitled, in.VisitsUsed, in.PriceCents, in.Notes,
	).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, tx.Commit(ctx)
}

func (s *Store) UpdateAgreement(ctx context.Context, shopID, agreementID string, in CreateAgreementInput) error {
	if err := validateRecordIDs(agreementID); err != nil {
		return err
	}
	if err := validateAgreementInput(shopID, in); err != nil {
		return err
	}
	tx, err := s.db.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockSQLServerAgreementParents(ctx, tx, shopID, in, true); err != nil {
		return err
	}
	var anniversaryValue any = in.AnniversaryDate
	if s.db.Pool.Dialect() == database.SQLServer {
		anniversaryValue = in.AnniversaryDate.Format("2006-01-02")
	}
	var planID *string
	if in.PlanTemplateID != "" {
		planID = &in.PlanTemplateID
	}
	var propertyID *string
	if in.PropertyID != "" {
		propertyID = &in.PropertyID
	}
	tag, err := tx.Exec(ctx, database.Statement(s.db.Pool.Dialect(), `
		UPDATE agreements SET
			customer_id = $3,
			property_id = $4,
			status = $6,
			anniversary_date = $7,
			visits_entitled = $8,
			visits_used = $9,
			price_cents = $10,
			notes = NULLIF($11, ''),
			updated_at = now()
		WHERE shop_id = $1 AND id = $2
		  AND plan_template_id IS NOT DISTINCT FROM $5`, sqlServerUpdateAgreement),
		shopID, agreementID, in.CustomerID, propertyID, planID, string(in.Status),
		anniversaryValue, in.VisitsEntitled, in.VisitsUsed, in.PriceCents, in.Notes,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return database.ErrNoRows
	}
	return tx.Commit(ctx)
}

func (s *Store) DashboardStats(ctx context.Context, shopID string) (DashboardStats, error) {
	var stats DashboardStats
	if err := validateRecordIDs(shopID); err != nil {
		return stats, err
	}
	asOf := s.nowUTC()
	var err error
	if s.db.Pool.Dialect() == database.SQLServer {
		stats, err = s.sqlServerDashboardCounts(ctx, shopID, asOf)
	} else {
		err = s.db.Pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE a.status = 'active'),
			COUNT(*) FILTER (WHERE a.status = 'active'
				AND a.anniversary_date >= $2::date
				AND a.anniversary_date < date_trunc('month', $2::date) + INTERVAL '1 month'),
			COUNT(*) FILTER (WHERE a.status = 'active'
				AND a.anniversary_date BETWEEN $2::date AND $2::date + 90),
			COUNT(*) FILTER (WHERE a.status = 'lapsed'
				AND a.updated_at >= date_trunc('month', $2::date)),
			COALESCE(SUM(
				CASE WHEN a.status = 'active' THEN
					CASE WHEN a.plan_snapshot_billing_interval = 'monthly' THEN a.price_cents ELSE a.price_cents / 12 END
				ELSE 0 END
			), 0)
		FROM agreements a
		WHERE a.shop_id = $1`, shopID, asOf).Scan(
			&stats.ActiveCount,
			&stats.RenewalsDueMonth,
			&stats.RenewalsDue90,
			&stats.LapsedMTD,
			&stats.MRRCents,
		)
	}
	if err != nil {
		return stats, err
	}
	stats.VisitsOverdue, err = s.CountVisitsOverdue(ctx, shopID)
	if err != nil {
		return stats, err
	}
	serviceOverdue, err := s.CountServiceOccurrencesOverdue(ctx, shopID)
	stats.VisitsOverdue += serviceOverdue
	return stats, err
}

func (s *Store) RenewalQueue(ctx context.Context, shopID string) ([]RenewalBucket, error) {
	if err := validateRecordIDs(shopID); err != nil {
		return nil, err
	}
	asOf := s.nowUTC()
	query := `
		SELECT
			a.id,
			c.name,
			COALESCE(p.address_line1 || ', ' || p.city || ', ' || p.state, ''),
			a.plan_snapshot_name,
			a.anniversary_date,
			(a.anniversary_date - $2::date)::int,
			a.price_cents
		FROM agreements a
		JOIN customers c ON c.id = a.customer_id
		LEFT JOIN properties p ON p.id = a.property_id
		WHERE a.shop_id = $1
		  AND a.status = 'active'
		  AND a.anniversary_date BETWEEN $2::date AND $2::date + 90
		ORDER BY a.anniversary_date`
	args := []any{shopID, asOf}
	if s.db.Pool.Dialect() == database.SQLServer {
		query = sqlServerRenewalQueue
		horizon := asOf.UTC().AddDate(0, 0, 90)
		if horizon.Year() > 9999 {
			horizon = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
		}
		args = []any{shopID, asOf.UTC().Format("2006-01-02"), horizon.Format("2006-01-02")}
	}
	rows, err := s.db.Pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bucketDefs := []struct {
		label string
		max   int
	}{
		{"7 days", 7},
		{"14 days", 14},
		{"30 days", 30},
		{"60 days", 60},
		{"90 days", 90},
	}

	buckets := make([]RenewalBucket, len(bucketDefs))
	for i, def := range bucketDefs {
		buckets[i] = RenewalBucket{Label: def.label, Days: def.max}
	}

	for rows.Next() {
		var item RenewalItem
		if err := rows.Scan(
			&item.AgreementID, &item.CustomerName, &item.PropertyAddress,
			&item.PlanName, &item.AnniversaryDate, &item.DaysUntil, &item.PriceCents,
		); err != nil {
			return nil, err
		}
		item.PriceFmt = formatCents(item.PriceCents)
		item.AnniversaryFmt = item.AnniversaryDate.Format("Jan 2, 2006")

		for i, def := range bucketDefs {
			if item.DaysUntil <= def.max {
				buckets[i].Items = append(buckets[i].Items, item)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buckets, nil
}

func formatCents(cents int) string {
	return fmt.Sprintf("$%.2f", float64(cents)/100)
}

func agreementStatusLabel(s AgreementStatus) string {
	switch s {
	case AgreementActive:
		return "Active"
	case AgreementPastDue:
		return "Past due"
	case AgreementLapsed:
		return "Lapsed"
	case AgreementCancelled:
		return "Cancelled"
	case AgreementPendingRenewal:
		return "Pending renewal"
	default:
		return string(s)
	}
}

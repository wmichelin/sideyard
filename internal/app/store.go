package app

import (
	"context"
	"fmt"
	"sync"
	"time"
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
	db               *DB
	clock            Clock
	publicDemoSeedMu sync.Mutex
}

func (s *Store) nowUTC() time.Time {
	return utcNow(s.clock)
}

func (s *Store) ListCustomers(ctx context.Context, shopID string) ([]Customer, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, COALESCE(external_id, ''), name, COALESCE(email, ''), COALESCE(phone, '')
		FROM customers
		WHERE shop_id = $1
		ORDER BY name`, shopID)
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
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO customers (shop_id, name, email, phone)
		VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, ''))
		RETURNING id`, shopID, name, email, phone).Scan(&id)
	return id, err
}

func (s *Store) GetCustomer(ctx context.Context, shopID, customerID string) (*Customer, error) {
	var c Customer
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, COALESCE(external_id, ''), name, COALESCE(email, ''), COALESCE(phone, '')
		FROM customers WHERE shop_id = $1 AND id = $2`, shopID, customerID,
	).Scan(&c.ID, &c.ExternalID, &c.Name, &c.Email, &c.Phone)
	if err != nil {
		return nil, fmt.Errorf("customer not found")
	}
	return &c, nil
}

func (s *Store) UpdateCustomer(ctx context.Context, shopID, customerID, name, email, phone string) error {
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE customers SET name = $3, email = NULLIF($4, ''), phone = NULLIF($5, ''), updated_at = now()
		WHERE shop_id = $1 AND id = $2`, shopID, customerID, name, email, phone)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("customer not found")
	}
	return nil
}

func (s *Store) DeleteCustomer(ctx context.Context, shopID, customerID string) error {
	tag, err := s.db.Pool.Exec(ctx, `DELETE FROM customers WHERE shop_id = $1 AND id = $2`, shopID, customerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("customer not found")
	}
	return nil
}

func (s *Store) ListProperties(ctx context.Context, shopID, customerID string) ([]Property, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, customer_id, address_line1, COALESCE(address_line2, ''), city, state, postal_code
		FROM properties WHERE shop_id = $1 AND customer_id = $2 ORDER BY address_line1`, shopID, customerID)
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
		equipment, err := s.ListEquipment(ctx, shopID, p.ID)
		if err != nil {
			return nil, err
		}
		p.Equipment = equipment
		properties = append(properties, p)
	}
	return properties, rows.Err()
}

func (s *Store) ListAllProperties(ctx context.Context, shopID string) ([]Property, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, customer_id, address_line1, COALESCE(address_line2, ''), city, state, postal_code
		FROM properties WHERE shop_id = $1 ORDER BY address_line1`, shopID)
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
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO properties (shop_id, customer_id, address_line1, address_line2, city, state, postal_code)
		SELECT $1, id, $3, NULLIF($4, ''), $5, $6, $7 FROM customers WHERE shop_id = $1 AND id = $2
		RETURNING id`, shopID, customerID, address1, address2, city, state, postal).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create property: %w", err)
	}
	return id, nil
}

func (s *Store) ListEquipment(ctx context.Context, shopID, propertyID string) ([]Equipment, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, property_id, kind, COALESCE(make, ''), COALESCE(model, ''), COALESCE(serial_number, '')
		FROM equipment WHERE shop_id = $1 AND property_id = $2 ORDER BY kind`, shopID, propertyID)
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
	tag, err := s.db.Pool.Exec(ctx, `
		INSERT INTO equipment (shop_id, property_id, kind, make, model, serial_number)
		SELECT $1, id, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, '')
		FROM properties WHERE shop_id = $1 AND id = $2`, shopID, propertyID, kind, make, model, serial)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("property not found")
	}
	return nil
}

func (s *Store) ListPlanTemplates(ctx context.Context, shopID string) ([]PlanTemplate, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, name, COALESCE(description, ''), price_cents, billing_interval, term_months, visits_per_year
		FROM plan_templates
		WHERE shop_id = $1
		ORDER BY name`, shopID)
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
	return out, rows.Err()
}

func (s *Store) CreatePlanTemplate(ctx context.Context, shopID, name, description string, priceCents, termMonths, visitsPerYear int) (string, error) {
	var id string
	err := s.db.Pool.QueryRow(ctx, `
		INSERT INTO plan_templates (shop_id, name, description, price_cents, term_months, visits_per_year)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6)
		RETURNING id`, shopID, name, description, priceCents, termMonths, visitsPerYear).Scan(&id)
	return id, err
}

func (s *Store) GetPlanTemplate(ctx context.Context, shopID, planID string) (*PlanTemplate, error) {
	var p PlanTemplate
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, name, COALESCE(description, ''), price_cents, billing_interval, term_months, visits_per_year
		FROM plan_templates WHERE shop_id = $1 AND id = $2`, shopID, planID,
	).Scan(&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.BillingInterval, &p.TermMonths, &p.VisitsPerYear)
	if err != nil {
		return nil, fmt.Errorf("plan template not found")
	}
	return &p, nil
}

func (s *Store) UpdatePlanTemplate(ctx context.Context, shopID, planID, name, description string, priceCents, termMonths, visitsPerYear int) error {
	tag, err := s.db.Pool.Exec(ctx, `
		UPDATE plan_templates
		SET name = $3, description = NULLIF($4, ''), price_cents = $5, term_months = $6, visits_per_year = $7
		WHERE shop_id = $1 AND id = $2`,
		shopID, planID, name, description, priceCents, termMonths, visitsPerYear,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("plan template not found")
	}
	return nil
}

func (s *Store) ListAgreements(ctx context.Context, shopID string) ([]Agreement, error) {
	rows, err := s.db.Pool.Query(ctx, `
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
		ORDER BY a.anniversary_date`, shopID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanAgreements(rows)
}

func (s *Store) GetAgreement(ctx context.Context, shopID, agreementID string) (*Agreement, error) {
	var a Agreement
	var status string
	err := s.db.Pool.QueryRow(ctx, `
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
		WHERE a.shop_id = $1 AND a.id = $2`, shopID, agreementID).Scan(
		&a.ID, &a.CustomerID, &a.CustomerName,
		&a.PropertyID, &a.PropertyAddress,
		&a.PlanTemplateID, &a.PlanName,
		&a.ExternalID, &status,
		&a.AnniversaryDate, &a.VisitsEntitled, &a.VisitsUsed,
		&a.PriceCents, &a.Notes,
	)
	if err != nil {
		return nil, fmt.Errorf("agreement not found")
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
	var id string
	var planID *string
	if in.PlanTemplateID != "" {
		planID = &in.PlanTemplateID
	}
	var propertyID *string
	if in.PropertyID != "" {
		propertyID = &in.PropertyID
	}
	err := s.db.Pool.QueryRow(ctx, `
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
		RETURNING id`,
		shopID, in.CustomerID, propertyID, planID, in.ExternalID, string(in.Status),
		in.AnniversaryDate, in.VisitsEntitled, in.VisitsUsed, in.PriceCents, in.Notes,
	).Scan(&id)
	return id, err
}

func (s *Store) UpdateAgreement(ctx context.Context, shopID, agreementID string, in CreateAgreementInput) error {
	var planID *string
	if in.PlanTemplateID != "" {
		planID = &in.PlanTemplateID
	}
	var propertyID *string
	if in.PropertyID != "" {
		propertyID = &in.PropertyID
	}
	tag, err := s.db.Pool.Exec(ctx, `
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
		  AND plan_template_id IS NOT DISTINCT FROM $5`,
		shopID, agreementID, in.CustomerID, propertyID, planID, string(in.Status),
		in.AnniversaryDate, in.VisitsEntitled, in.VisitsUsed, in.PriceCents, in.Notes,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("agreement not found")
	}
	return nil
}

func (s *Store) DashboardStats(ctx context.Context, shopID string) (DashboardStats, error) {
	var stats DashboardStats
	asOf := s.nowUTC()
	err := s.db.Pool.QueryRow(ctx, `
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
	asOf := s.nowUTC()
	rows, err := s.db.Pool.Query(ctx, `
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
		ORDER BY a.anniversary_date`, shopID, asOf)
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

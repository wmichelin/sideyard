package app

import (
	"context"
	"errors"

	"github.com/wmichelin/clubdue/internal/database"
)

var errAgreementInput = errors.New("Check the agreement references, dates and values.")
var errPlanInput = errors.New("Check the plan name, term and values.")

func validSQLInt(values ...int) bool {
	for _, v := range values {
		if int64(v) < -2147483648 || int64(v) > 2147483647 {
			return false
		}
	}
	return true
}

func validateAgreementInput(shopID string, in CreateAgreementInput) error {
	if err := validateRecordIDs(shopID, in.CustomerID); err != nil {
		return err
	}
	for _, id := range []string{in.PropertyID, in.PlanTemplateID} {
		if id != "" {
			if err := validateRecordIDs(id); err != nil {
				return err
			}
		}
	}
	if !validStoredText(in.ExternalID, in.Notes) || in.AnniversaryDate.Year() < 1 || in.AnniversaryDate.Year() > 9999 || !validSQLInt(in.VisitsEntitled, in.VisitsUsed, in.PriceCents) {
		return errAgreementInput
	}
	switch in.Status {
	case AgreementActive, AgreementPastDue, AgreementLapsed, AgreementCancelled, AgreementPendingRenewal:
		return nil
	default:
		return errAgreementInput
	}
}

// Retain parents before agreement writes, matching delete/update lock order.
// PostgreSQL retains its existing native statement/foreign-key behavior.
func lockSQLServerAgreementParents(ctx context.Context, tx database.Tx, shopID string, in CreateAgreementInput, allowMissingPlan bool) error {
	if tx.Dialect() != database.SQLServer {
		return nil
	}
	checks := []struct {
		query string
		args  []any
	}{
		{`SELECT 1 FROM dbo.shops WITH(HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, []any{shopID}},
		{`SELECT 1 FROM dbo.customers WITH(HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, []any{shopID, in.CustomerID}},
	}
	if in.PropertyID != "" {
		checks = append(checks, struct {
			query string
			args  []any
		}{`SELECT 1 FROM dbo.properties WITH(HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND customer_id=CONVERT(uniqueidentifier,@p2) AND id=CONVERT(uniqueidentifier,@p3)`, []any{shopID, in.CustomerID, in.PropertyID}})
	}
	if in.PlanTemplateID != "" {
		checks = append(checks, struct {
			query string
			args  []any
		}{`SELECT 1 FROM dbo.plan_templates WITH(HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, []any{shopID, in.PlanTemplateID}})
	}
	for i, check := range checks {
		var found int
		if err := tx.QueryRow(ctx, check.query, check.args...).Scan(&found); err != nil {
			if errors.Is(err, database.ErrNoRows) {
				// Preserve the update's NULL-safe stale-plan guard after deletion.
				// The serializable lookup still holds the missing plan key range.
				if allowMissingPlan && in.PlanTemplateID != "" && i == len(checks)-1 {
					continue
				}
				return errAgreementInput
			}
			return err
		}
	}
	return nil
}

const sqlServerAgreementColumns = `LOWER(CONVERT(char(36),a.id)),LOWER(CONVERT(char(36),a.customer_id)),c.name,
 COALESCE(LOWER(CONVERT(char(36),a.property_id)),''),
 CASE WHEN p.id IS NULL THEN N'' ELSE p.address_line1+N', '+p.city+N', '+p.state END,
 COALESCE(LOWER(CONVERT(char(36),a.plan_template_id)),''),a.plan_snapshot_name,
 COALESCE(a.external_id,N''),a.status,a.anniversary_date,a.visits_entitled,a.visits_used,a.price_cents,COALESCE(a.notes,N'')`

const sqlServerAgreementFrom = ` FROM dbo.agreements a
 JOIN dbo.customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
 LEFT JOIN dbo.properties p ON p.id=a.property_id AND p.shop_id=a.shop_id AND p.customer_id=a.customer_id `

const sqlServerCreateAgreement = `DECLARE @created TABLE(id uniqueidentifier);
INSERT INTO dbo.agreements(shop_id,customer_id,property_id,plan_template_id,external_id,status,anniversary_date,
 visits_entitled,visits_used,price_cents,notes,plan_snapshot_name,plan_snapshot_description,plan_snapshot_billing_interval,
 plan_snapshot_term_months,plan_snapshot_visits_per_year,plan_snapshot_visit_windows) OUTPUT inserted.id INTO @created
SELECT CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CONVERT(uniqueidentifier,@p3),CONVERT(uniqueidentifier,@p4),
 CASE WHEN DATALENGTH(@p5)=0 THEN NULL ELSE @p5 END,@p6,CONVERT(date,@p7),@p8,@p9,@p10,CASE WHEN DATALENGTH(@p11)=0 THEN NULL ELSE @p11 END,
 COALESCE(pt.name,N'Custom plan'),COALESCE(pt.description,N''),COALESCE(pt.billing_interval,N'annual'),
 COALESCE(pt.term_months,12),COALESCE(pt.visits_per_year,@p8),COALESCE(pt.visit_windows,N'["spring","fall"]')
FROM (SELECT CONVERT(uniqueidentifier,@p4) AS plan_id) selected
LEFT JOIN dbo.plan_templates pt ON pt.id=selected.plan_id AND pt.shop_id=CONVERT(uniqueidentifier,@p1);
SELECT LOWER(CONVERT(char(36),id)) FROM @created;`

const sqlServerUpdateAgreement = `UPDATE dbo.agreements SET customer_id=CONVERT(uniqueidentifier,@p3),property_id=CONVERT(uniqueidentifier,@p4),status=@p6,
 anniversary_date=CONVERT(date,@p7),visits_entitled=@p8,visits_used=@p9,price_cents=@p10,
 notes=CASE WHEN DATALENGTH(@p11)=0 THEN NULL ELSE @p11 END,updated_at=SYSUTCDATETIME()
WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)
 AND (plan_template_id=CONVERT(uniqueidentifier,@p5) OR (plan_template_id IS NULL AND @p5 IS NULL));`

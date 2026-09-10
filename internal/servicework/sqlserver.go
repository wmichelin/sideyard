package servicework

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wmichelin/clubdue/internal/database"
)

var recordIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validateIDs(ids ...string) error {
	for _, id := range ids {
		if !recordIDPattern.MatchString(id) {
			return fmt.Errorf("service record not found: %w", database.ErrNoRows)
		}
	}
	return nil
}
func validText(value string) bool { return utf8.ValidString(value) && !strings.ContainsRune(value, 0) }
func validDate(value time.Time) bool {
	year := dateOnly(value).Year()
	return year >= 1 && year <= 9999
}
func dateArgument(dialect database.Dialect, value time.Time) any {
	if dialect == database.SQLServer {
		return dateOnly(value).Format("2006-01-02")
	}
	return dateOnly(value)
}
func optionalDateArgument(dialect database.Dialect, value *time.Time) any {
	if value == nil {
		return nil
	}
	return dateArgument(dialect, *value)
}
func planningWindow(today time.Time) (time.Time, time.Time) {
	first, last := today.AddDate(0, 0, -lookBehindDays), today.AddDate(0, 0, lookAheadDays)
	if first.Year() < 1 {
		first = time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	if last.Year() > 9999 {
		last = time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	return first, last
}

func lockSQLServerShop(ctx context.Context, tx database.Tx, shopID string) error {
	var found int
	return tx.QueryRow(ctx, `SELECT 1 FROM dbo.shops WITH(HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, shopID).Scan(&found)
}
func lockSQLServerAgreement(ctx context.Context, tx database.Tx, shopID, agreementID string, requireActive bool) error {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, agreementID).Scan(&status)
	if err != nil {
		return err
	}
	if requireActive && status != "active" {
		return database.ErrNoRows
	}
	return nil
}

// Discovery takes no retained child lock. Hold the original parent before the
// schedule and recheck its identity, so direct reparenting cannot invert locks.
func lockSQLServerSchedule(ctx context.Context, tx database.Tx, shopID, scheduleID string, requireActiveAgreement bool) error {
	if err := lockSQLServerShop(ctx, tx, shopID); err != nil {
		return err
	}
	var agreement string
	if err := tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),agreement_id)) FROM dbo.service_schedules WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, scheduleID).Scan(&agreement); err != nil {
		return err
	}
	if err := lockSQLServerAgreement(ctx, tx, shopID, agreement, requireActiveAgreement); err != nil {
		return err
	}
	var current string
	if err := tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),agreement_id)) FROM dbo.service_schedules WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, scheduleID).Scan(&current); err != nil {
		return err
	}
	if current != agreement {
		return fmt.Errorf("service schedule parent changed: %w", database.ErrNoRows)
	}
	return nil
}

func insertOccurrences(ctx context.Context, tx database.Tx, shopID, scheduleID string, dates []time.Time) error {
	if len(dates) == 0 {
		return nil
	}
	var value any = dates
	if tx.Dialect() == database.SQLServer {
		encodedDates := make([]string, len(dates))
		for i, date := range dates {
			encodedDates[i] = date.Format("2006-01-02")
		}
		encoded, err := json.Marshal(encodedDates)
		if err != nil {
			return err
		}
		value = string(encoded)
	}
	_, err := tx.Exec(ctx, database.Statement(tx.Dialect(), `
 INSERT INTO service_occurrences(shop_id,schedule_id,due_on)
 SELECT $1,$2,due_on FROM unnest($3::date[]) AS generated(due_on)
 ON CONFLICT(shop_id,schedule_id,due_on) DO NOTHING`, `
 INSERT INTO dbo.service_occurrences(shop_id,schedule_id,due_on)
 SELECT CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CONVERT(date,j.value)
 FROM OPENJSON(@p3) j WHERE NOT EXISTS(SELECT 1 FROM dbo.service_occurrences WITH(UPDLOCK,HOLDLOCK)
 WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND schedule_id=CONVERT(uniqueidentifier,@p2) AND due_on=CONVERT(date,j.value))`), shopID, scheduleID, value)
	return err
}

// The shared order matches seasonal completion: all agreements before schedules
// and occurrences. The returned mappings are rechecked under their child locks.
type resolvedOccurrence struct {
	id, schedule, agreement string
	counts                  bool
}

func prepareSQLServerOccurrences(ctx context.Context, tx database.Tx, shopID string, ids []string) ([]resolvedOccurrence, error) {
	if err := lockSQLServerShop(ctx, tx, shopID); err != nil {
		return nil, err
	}
	canonical := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.ToLower(id)
		if !seen[id] {
			canonical = append(canonical, id)
			seen[id] = true
		}
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT LOWER(CONVERT(char(36),o.id)),LOWER(CONVERT(char(36),sc.id)),LOWER(CONVERT(char(36),sc.agreement_id))
 FROM dbo.service_occurrences o JOIN dbo.service_schedules sc ON sc.shop_id=o.shop_id AND sc.id=o.schedule_id
 JOIN OPENJSON(@p2) j ON o.id=CONVERT(uniqueidentifier,j.value) WHERE o.shop_id=CONVERT(uniqueidentifier,@p1)`, shopID, string(encoded))
	if err != nil {
		return nil, err
	}
	var resolved []resolvedOccurrence
	for rows.Next() {
		var v resolvedOccurrence
		if err := rows.Scan(&v.id, &v.schedule, &v.agreement); err != nil {
			rows.Close()
			return nil, err
		}
		resolved = append(resolved, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(resolved) != len(canonical) {
		return nil, database.ErrNoRows
	}
	slices.SortFunc(resolved, func(a, b resolvedOccurrence) int {
		if n := strings.Compare(a.agreement, b.agreement); n != 0 {
			return n
		}
		if n := strings.Compare(a.schedule, b.schedule); n != 0 {
			return n
		}
		return strings.Compare(a.id, b.id)
	})
	prior := ""
	for _, v := range resolved {
		if v.agreement != prior {
			if err := lockSQLServerAgreement(ctx, tx, shopID, v.agreement, true); err != nil {
				return nil, err
			}
			prior = v.agreement
		}
	}
	prior = ""
	var counts bool
	for i, v := range resolved {
		if v.schedule != prior {
			var agreement string
			var active bool
			err := tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),agreement_id)),active,counts_toward_entitlement FROM dbo.service_schedules WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, v.schedule).Scan(&agreement, &active, &counts)
			if err != nil {
				return nil, err
			}
			if agreement != v.agreement || !active {
				return nil, database.ErrNoRows
			}
			prior = v.schedule
		}
		resolved[i].counts = counts
	}
	return resolved, nil
}

// SQL's default text and uniqueidentifier ordering differ from PostgreSQL. Use
// UTF-8 bytes and canonical UUID text BEFORE paginating; Go sorts full lists.
const sqlServerNameOrder = `CONVERT(varbinary(max),CONVERT(varchar(max),c.name COLLATE Latin1_General_100_BIN2_UTF8)),CONVERT(varbinary(max),CONVERT(varchar(max),sc.service_name COLLATE Latin1_General_100_BIN2_UTF8))`
const sqlServerOccurrenceJoins = ` FROM dbo.service_occurrences o
 JOIN dbo.service_schedules sc ON sc.id=o.schedule_id AND sc.shop_id=o.shop_id
 JOIN dbo.agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
 JOIN dbo.customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
 LEFT JOIN dbo.properties p ON p.id=a.property_id AND p.shop_id=a.shop_id AND p.customer_id=a.customer_id`
const sqlServerPropertyAddress = `CASE WHEN p.id IS NULL THEN N'' ELSE p.address_line1+N', '+p.city+N', '+p.state+N' '+p.postal_code END`
const sqlServerListOccurrences = `SELECT LOWER(CONVERT(char(36),o.id)),LOWER(CONVERT(char(36),o.schedule_id)),LOWER(CONVERT(char(36),sc.agreement_id)),c.name,COALESCE(c.phone,N''),` + sqlServerPropertyAddress + `,sc.service_name,o.effective_on,o.status,COALESCE(o.notes,N''),sc.active,CONVERT(bit,CASE WHEN a.status=N'active' THEN 1 ELSE 0 END),o.due_on,o.schedule_revision` + sqlServerOccurrenceJoins + ` WHERE o.shop_id=CONVERT(uniqueidentifier,@p1)`
const sqlServerListSummaries = `SELECT LOWER(CONVERT(char(36),sc.id)),LOWER(CONVERT(char(36),sc.agreement_id)),c.name,` + sqlServerPropertyAddress + `,sc.service_name,sc.cadence_unit,sc.cadence_interval,sc.active,CONVERT(bit,CASE WHEN a.status=N'active' THEN 1 ELSE 0 END),sc.ends_on,history.last_completed,COALESCE(LOWER(CONVERT(char(36),upcoming.id)),''),upcoming.next_on
 FROM dbo.service_schedules sc JOIN dbo.agreements a ON a.id=sc.agreement_id AND a.shop_id=sc.shop_id
 JOIN dbo.customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
 LEFT JOIN dbo.properties p ON p.id=a.property_id AND p.shop_id=a.shop_id AND p.customer_id=a.customer_id
 OUTER APPLY(SELECT MAX(completed_at) AS last_completed FROM dbo.service_occurrences WHERE shop_id=sc.shop_id AND schedule_id=sc.id AND status=N'completed') history
 OUTER APPLY(SELECT TOP(1) id,effective_on AS next_on FROM dbo.service_occurrences WHERE shop_id=sc.shop_id AND schedule_id=sc.id AND status=N'due' AND sc.active=1 AND a.status=N'active' ORDER BY effective_on,due_on,LOWER(CONVERT(char(36),id)) COLLATE Latin1_General_100_BIN2) upcoming
 WHERE sc.shop_id=CONVERT(uniqueidentifier,@p1) AND (@p2=N'' OR sc.agreement_id=CONVERT(uniqueidentifier,NULLIF(@p2,N'')))`

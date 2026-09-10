package renewals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wmichelin/clubdue/internal/activation"
	"github.com/wmichelin/clubdue/internal/database"
)

var recordIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var ErrInput = errors.New("invalid renewal input")
var ErrConflict = errors.New("renewal outcome conflict")

func validateIDs(ids ...string) error {
	for _, id := range ids {
		if !recordIDPattern.MatchString(id) {
			return database.ErrNoRows
		}
	}
	return nil
}
func validText(s string) bool { return utf8.ValidString(s) && !strings.ContainsRune(s, 0) }
func dateOnly(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
func validDate(t time.Time) bool { year := t.UTC().Year(); return year >= 1 && year <= 9999 }
func lockShop(ctx context.Context, tx database.Tx, shop string) error {
	var found int
	return tx.QueryRow(ctx, `SELECT 1 FROM dbo.shops WITH(HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&found)
}
func lockAgreement(ctx context.Context, tx database.Tx, shop, id string) (time.Time, int, string, error) {
	var anniversary time.Time
	var term int
	var status string
	err := tx.QueryRow(ctx, `SELECT anniversary_date,plan_snapshot_term_months,status FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shop, id).Scan(&anniversary, &term, &status)
	return anniversary, term, status, err
}

var milestones = []int{90, 60, 30, 14, 7}

func validateMilestoneDates(cycle time.Time) error {
	if !validDate(cycle) || !validDate(cycle.AddDate(0, 0, -90)) {
		return fmt.Errorf("renewal cycle or milestone is outside years 0001 through 9999: %w", ErrInput)
	}
	return nil
}
func insertSQLServerMilestones(ctx context.Context, tx database.Tx, shop, id string, cycle time.Time) error {
	if err := validateMilestoneDates(cycle); err != nil {
		return err
	}
	for _, days := range milestones {
		_, err := tx.Exec(ctx, `INSERT INTO dbo.renewal_tasks(shop_id,agreement_id,cycle_start_date,due_at,milestone_days)
 SELECT CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),CONVERT(date,@p3),CONVERT(date,@p4),@p5
 WHERE NOT EXISTS(SELECT 1 FROM dbo.renewal_tasks WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND agreement_id=CONVERT(uniqueidentifier,@p2) AND cycle_start_date=CONVERT(date,@p3) AND milestone_days=@p5)`, shop, id, cycle.Format("2006-01-02"), cycle.AddDate(0, 0, -days).Format("2006-01-02"), days)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) generateSQLServerTasks(ctx context.Context, shop string, asOf time.Time) error {
	rows, err := s.pool.Query(ctx, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.agreements WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND status=N'active'`, shop)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.generateSQLServerAgreementTasks(ctx, shop, id, asOf); err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) generateSQLServerAgreementTasks(ctx context.Context, shop, id string, asOf time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockShop(ctx, tx, shop); err != nil {
		return err
	}
	anniversary, term, status, err := lockAgreement(ctx, tx, shop, id)
	if errors.Is(err, database.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if status != "active" {
		return nil
	}
	cycle, err := renewalCycleStart(anniversary, asOf, term)
	if err != nil {
		return err
	}
	if cycle.After(dateOnly(asOf).AddDate(0, 0, 90)) {
		return nil
	}
	if err := insertSQLServerMilestones(ctx, tx, shop, id, cycle); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Service) updateSQLServerTask(ctx context.Context, shop, id, status, notes string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := lockShop(ctx, tx, shop); err != nil {
		return err
	}
	var agreement string
	if err := tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),agreement_id)) FROM dbo.renewal_tasks WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shop, id).Scan(&agreement); err != nil {
		return err
	}
	_, term, _, err := lockAgreement(ctx, tx, shop, agreement)
	if err != nil {
		return err
	}
	var parent, current string
	var cycle time.Time
	if err := tx.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),agreement_id)),cycle_start_date,status FROM dbo.renewal_tasks WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shop, id).Scan(&parent, &cycle, &current); err != nil {
		return err
	}
	if parent != agreement {
		return ErrConflict
	}
	if current == "renewed" || current == "lost" {
		if current == status {
			return tx.Commit(ctx)
		}
		return ErrConflict
	}
	// Preserve the selected task's cycle even if the agreement was edited later.
	next := cycle.AddDate(0, term, 0)
	if status == "renewed" {
		if err := validateMilestoneDates(next); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE dbo.renewal_tasks SET status=@p3,notes=CASE WHEN DATALENGTH(@p4)=0 THEN NULL ELSE @p4 END,updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shop, id, status, notes); err != nil {
		return err
	}
	if err := activation.RecordTx(ctx, tx, shop, activation.EventFirstRenewalAction); err != nil {
		return err
	}
	switch status {
	case "renewed":
		if _, err := tx.Exec(ctx, `UPDATE dbo.agreements SET anniversary_date=CONVERT(date,@p3),visits_used=0,status=N'active',updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shop, agreement, next.Format("2006-01-02")); err != nil {
			return err
		}
	case "lost":
		if _, err := tx.Exec(ctx, `UPDATE dbo.agreements SET status=N'lapsed',updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shop, agreement); err != nil {
			return err
		}
	}
	if status == "renewed" || status == "lost" {
		if _, err := tx.Exec(ctx, `UPDATE dbo.renewal_tasks SET status=@p5,updated_at=SYSUTCDATETIME() WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND agreement_id=CONVERT(uniqueidentifier,@p2) AND cycle_start_date=CONVERT(date,@p3) AND id<>CONVERT(uniqueidentifier,@p4) AND status IN(N'open',N'contacted')`, shop, agreement, cycle.Format("2006-01-02"), id, status); err != nil {
			return err
		}
	}
	if status == "renewed" {
		if err := insertSQLServerMilestones(ctx, tx, shop, agreement, next); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Service) queueSQLServerDigests(ctx context.Context, day time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Capture membership in one RCSI statement, as the PostgreSQL INSERT SELECT
	// does. Close rows before taking shop/day locks; IDs are historical references.
	rows, err := tx.Query(ctx, `SELECT LOWER(CONVERT(char(36),t.shop_id)),LOWER(CONVERT(char(36),t.id)),t.due_at FROM dbo.renewal_tasks t JOIN dbo.shops sh ON sh.id=t.shop_id AND sh.kind<>N'demo_sandbox' WHERE t.due_at<=CONVERT(date,@p1) AND t.status IN(N'open',N'contacted')`, day.Format("2006-01-02"))
	if err != nil {
		return err
	}
	type member struct {
		id  string
		due time.Time
	}
	groups := map[string][]member{}
	for rows.Next() {
		var shop string
		var m member
		if err := rows.Scan(&shop, &m.id, &m.due); err != nil {
			rows.Close()
			return err
		}
		groups[shop] = append(groups[shop], m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	shops := make([]string, 0, len(groups))
	for shop := range groups {
		shops = append(shops, shop)
	}
	slices.Sort(shops)
	for _, shop := range shops {
		members := groups[shop]
		slices.SortFunc(members, func(a, b member) int {
			if n := a.due.Compare(b.due); n != 0 {
				return n
			}
			return strings.Compare(a.id, b.id)
		})
		ids := make([]string, len(members))
		for i, m := range members {
			ids[i] = m.id
		}
		encoded, err := json.Marshal(ids)
		if err != nil {
			return err
		}
		if err := lockShop(ctx, tx, shop); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO dbo.renewal_digest_outbox(shop_id,digest_date,task_ids) SELECT CONVERT(uniqueidentifier,@p1),CONVERT(date,@p2),@p3 WHERE NOT EXISTS(SELECT 1 FROM dbo.renewal_digest_outbox WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND digest_date=CONVERT(date,@p2))`, shop, day.Format("2006-01-02"), string(encoded)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

const sqlServerListTasks = `SELECT LOWER(CONVERT(char(36),t.id)),LOWER(CONVERT(char(36),a.id)),LOWER(CONVERT(char(36),c.id)),c.name,COALESCE(c.email,N''),COALESCE(c.phone,N''),
 CASE WHEN p.id IS NULL THEN N'' ELSE p.address_line1+N', '+p.city+N', '+p.state+N' '+p.postal_code END,
 a.plan_snapshot_name,a.anniversary_date,t.due_at,t.milestone_days,DATEDIFF(day,CONVERT(date,@p2),a.anniversary_date),t.status,COALESCE(t.notes,N'')
 FROM dbo.renewal_tasks t JOIN dbo.agreements a ON a.id=t.agreement_id AND a.shop_id=t.shop_id
 JOIN dbo.customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
 LEFT JOIN dbo.properties p ON p.id=a.property_id AND p.shop_id=a.shop_id AND p.customer_id=a.customer_id
 WHERE t.shop_id=CONVERT(uniqueidentifier,@p1) AND (t.due_at<=CONVERT(date,@p2) OR t.status<>N'open')
 ORDER BY t.due_at,a.anniversary_date,t.milestone_days DESC`

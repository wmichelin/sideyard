package visits

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/wmichelin/clubdue/internal/database"
)

var recordIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func (s *Service) insertSQLServerVisit(ctx context.Context, shopID, agreementID, season string, year int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var found int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.shops WITH(HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, shopID).Scan(&found); err != nil {
		return err
	}
	// Sync and completion use the same update lock before either touches a visit
	// key; a shared agreement lock could deadlock with completion's counter update.
	if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, agreementID).Scan(&found); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO dbo.visit_dues(shop_id,agreement_id,season,due_year,status)
 SELECT CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p3,@p4,N'due'
 WHERE NOT EXISTS(SELECT 1 FROM dbo.visit_dues WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND agreement_id=CONVERT(uniqueidentifier,@p2) AND season=@p3 AND due_year=@p4)`, shopID, agreementID, season, year); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type resolvedVisit struct{ id, agreement string }

// Discover without retained child locks, then lock parents in a stable order.
// The caller locks each visit and rechecks the mapping before changing anything.
func prepareSQLServerVisits(ctx context.Context, tx database.Tx, shopID string, ids []string) ([]resolvedVisit, error) {
	var found int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.shops WITH(HOLDLOCK) WHERE id=CONVERT(uniqueidentifier,@p1)`, shopID).Scan(&found); err != nil {
		return nil, err
	}
	canonical := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.ToLower(id)
		if !seen[id] {
			seen[id] = true
			canonical = append(canonical, id)
		}
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT LOWER(CONVERT(char(36),v.id)),LOWER(CONVERT(char(36),v.agreement_id)) FROM dbo.visit_dues v
 JOIN OPENJSON(@p2) j ON v.id=CONVERT(uniqueidentifier,j.value) WHERE v.shop_id=CONVERT(uniqueidentifier,@p1)`, shopID, string(encoded))
	if err != nil {
		return nil, err
	}
	var resolved []resolvedVisit
	for rows.Next() {
		var v resolvedVisit
		if err := rows.Scan(&v.id, &v.agreement); err != nil {
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
		return nil, fmt.Errorf("visit not found: %w", database.ErrNoRows)
	}
	slices.SortFunc(resolved, func(a, b resolvedVisit) int {
		if n := strings.Compare(a.agreement, b.agreement); n != 0 {
			return n
		}
		return strings.Compare(a.id, b.id)
	})
	previous := ""
	for _, v := range resolved {
		if v.agreement == previous {
			continue
		}
		previous = v.agreement
		if err := tx.QueryRow(ctx, `SELECT 1 FROM dbo.agreements WITH(UPDLOCK,HOLDLOCK) WHERE shop_id=CONVERT(uniqueidentifier,@p1) AND id=CONVERT(uniqueidentifier,@p2)`, shopID, v.agreement).Scan(&found); err != nil {
			return nil, err
		}
	}
	return resolved, nil
}

const sqlServerListVisits = `SELECT LOWER(CONVERT(char(36),vd.id)),LOWER(CONVERT(char(36),vd.agreement_id)),c.name,COALESCE(c.phone,N''),
 CASE WHEN p.id IS NULL THEN N'' ELSE p.address_line1+N', '+p.city+N', '+p.state+N' '+p.postal_code END,
 a.plan_snapshot_name,vd.season,vd.due_year,vd.status,vd.scheduled_at,vd.completed_at,COALESCE(vd.notes,N'')
 FROM dbo.visit_dues vd JOIN dbo.agreements a ON a.id=vd.agreement_id AND a.shop_id=vd.shop_id
 JOIN dbo.customers c ON c.id=a.customer_id AND c.shop_id=a.shop_id
 LEFT JOIN dbo.properties p ON p.id=a.property_id AND p.shop_id=a.shop_id AND p.customer_id=a.customer_id
 WHERE vd.shop_id=CONVERT(uniqueidentifier,@p1)`

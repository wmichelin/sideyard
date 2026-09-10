package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wmichelin/clubdue/internal/database"
)

func TestSQLServerRenewalLinksKeepExactIdentityAndLiveTerms(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, "renewal-token")
	a := createSeasonalTestAgreement(t, db, s, shop, []string{}, 5, 0)
	other := createTestShop(t, ctx, db, "renewal-other")
	if err := s.CreateRenewalLink(ctx, other, a, "foreign", now.Add(time.Hour)); err == nil {
		t.Fatal("cross-shop link accepted")
	}
	offsetExpiry := time.Date(2034, 9, 9, 23, 0, 0, 999, time.FixedZone("west", -4*3600))
	if err := s.CreateRenewalLink(ctx, shop, a, "offset", offsetExpiry); err != nil {
		t.Fatal(err)
	}
	s.clock = fixedClock{now: offsetExpiry.UTC().Truncate(time.Microsecond).Add(-time.Microsecond)}
	if _, err := s.GetHomeownerRenewal(ctx, "offset"); err != nil {
		t.Fatal("offset expired early", err)
	}
	s.clock = fixedClock{now: offsetExpiry.UTC().Truncate(time.Microsecond)}
	if _, err := s.GetHomeownerRenewal(ctx, "offset"); !errors.Is(err, database.ErrNoRows) {
		t.Fatal("offset expiry", err)
	}
	s.clock = fixedClock{now: now}
	tokens := []string{"Token", "token", "token ", "\u2060", strings.Repeat("界", 3000)}
	for _, token := range tokens {
		if err := s.CreateRenewalLink(ctx, shop, a, token, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetHomeownerRenewal(ctx, token); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateRenewalLink(ctx, shop, a, token, now.Add(time.Hour)); err == nil {
			t.Fatal("duplicate hash accepted")
		}
	}
	if _, err := s.GetHomeownerRenewal(ctx, "Token "); !errors.Is(err, database.ErrNoRows) {
		t.Fatal("token identity normalized", err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET status=N'cancelled',price_cents=12345,anniversary_date=CONVERT(date,'2035-02-01') WHERE id=CONVERT(uniqueidentifier,@p1)`, a); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetHomeownerRenewal(ctx, "Token")
	if err != nil || got.PriceCents != 12345 || got.AnniversaryDate.Format("2006-01-02") != "2035-02-01" {
		t.Fatal("link stopped showing live terms", err)
	}
	s.clock = fixedClock{now: now.Add(time.Hour - time.Microsecond)}
	if _, err := s.GetHomeownerRenewal(ctx, "Token"); err != nil {
		t.Fatal("expired early", err)
	}
	s.clock = fixedClock{now: now.Add(time.Hour)}
	if _, err := s.GetHomeownerRenewal(ctx, "Token"); !errors.Is(err, database.ErrNoRows) {
		t.Fatal("expiry boundary", err)
	}
	s.clock = fixedClock{now: now}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.shops SET kind=N'demo_sandbox',expires_at=@p2 WHERE id=CONVERT(uniqueidentifier,@p1)`, shop, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetHomeownerRenewal(ctx, "token"); !errors.Is(err, database.ErrNoRows) {
		t.Fatal("demo expiry ignored", err)
	}
}
func TestSQLServerRenewalOutcomesAndDigestSnapshots(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	now := time.Date(2034, 9, 9, 12, 0, 0, 0, time.UTC)
	s := &Store{db: db, clock: fixedClock{now: now}}
	shop := createTestShop(t, ctx, db, "renewal-outcomes")
	a := createSeasonalTestAgreement(t, db, s, shop, []string{}, 5, 3)
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.agreements SET anniversary_date=CONVERT(date,'2034-10-09') WHERE id=CONVERT(uniqueidentifier,@p1)`, a); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.GenerateRenewalTasks(ctx, shop, now); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.renewal_tasks WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&count); err != nil || count != 5 {
		t.Fatal("milestones", count, err)
	}
	tasks, err := s.ListRenewalTasks(ctx, shop)
	if err != nil || len(tasks) != 3 {
		t.Fatal("visible milestones", len(tasks), err)
	}
	for _, task := range tasks {
		if task.Days != 30 || task.DueState != "Upcoming" {
			t.Fatal("wrong anniversary-based due state")
		}
	}
	if err := s.QueueRenewalDigests(ctx, now); err != nil {
		t.Fatal(err)
	}
	var snapshot string
	if err := db.Pool.QueryRow(ctx, `SELECT task_ids FROM dbo.renewal_digest_outbox WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := json.Unmarshal([]byte(snapshot), &ids); err != nil || len(ids) != 3 {
		t.Fatal("snapshot", err)
	}
	if err := s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "contacted", "first contact"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "renewed", "confirmed"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "renewed", "rewrite"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRenewalTask(ctx, shop, tasks[0].ID, "lost", ""); err == nil {
		t.Fatal("rewrote final")
	}
	agreement, err := s.GetAgreement(ctx, shop, a)
	if err != nil || agreement.VisitsUsed != 0 || agreement.VisitsEntitled != 5 || agreement.AnniversaryDate.Format("2006-01-02") != "2035-10-09" || agreement.Status != AgreementActive {
		t.Fatal("renewal did not roll terms", err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.renewal_tasks WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&count); err != nil || count != 10 {
		t.Fatal("next cycle missing", count, err)
	}
	if err := s.QueueRenewalDigests(ctx, now); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := db.Pool.QueryRow(ctx, `SELECT task_ids FROM dbo.renewal_digest_outbox WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&after); err != nil || after != snapshot {
		t.Fatal("snapshot changed", err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.agreements WHERE id=CONVERT(uniqueidentifier,@p1)`, a); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT task_ids FROM dbo.renewal_digest_outbox WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop).Scan(&after); err != nil || after != snapshot {
		t.Fatal("historical digest deleted", err)
	}
	// Normal UUID arrays permit NULLs, duplicates and references that no longer exist.
	historical := fmt.Sprintf(`[null,%q,%q]`, ids[0], ids[0])
	if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.renewal_digest_outbox(shop_id,digest_date,task_ids) VALUES(CONVERT(uniqueidentifier,@p1),'2034-01-01',@p2)`, shop, historical); err != nil {
		t.Fatal("historical membership rejected", err)
	}
	for _, bad := range []string{`[]`, `[1]`, `["not-a-uuid"]`, `[[null]]`, `[true]`, `["00000000-0000-0000-0000-000000000000suffix"]`} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.renewal_digest_outbox(shop_id,digest_date,task_ids) VALUES(CONVERT(uniqueidentifier,@p1),'2034-01-02',@p2)`, shop, bad); err == nil {
			t.Fatal("invalid membership", bad)
		}
	}
}

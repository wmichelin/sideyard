package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSQLServerOwnerIsolationAndRecovery(t *testing.T) {
	t.Run("same name is not tenant access", func(t *testing.T) { testProvisionOwnerRejectsShopCollisionsAndPreservesRecovery(t, sqlServerTestDB(t)) })
	t.Run("concurrent names create one tenant", func(t *testing.T) { testProvisionOwnerConcurrentShopCollisionCreatesOneOwner(t, sqlServerTestDB(t)) })
}

func TestSQLServerAuthUnicodeLongIdentityAndSessions(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	clock := &fixedClock{now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}
	auth := newAuthWithClock(db, "synthetic-session-secret", false, clock)
	// Existing TEXT fields have no 254-character or index-width limit. Include
	// characters for which SQL Server LOWER disagrees with Go/PostgreSQL.
	email := strings.Repeat("long", 300) + "ẞK𐐀Ა𞤀@example.test"
	name := strings.Repeat("Long business ", 300) + "🔧"
	token, err := auth.signup(ctx, name, email, "valid-password")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	user, err := auth.userFromRequest(ctx, request)
	if err != nil || user == nil {
		t.Fatalf("session: %v", err)
	}
	if user.Email != strings.ToLower(email) || user.ShopName != name || user.IsDemo || user.Role != "owner" {
		t.Fatal("account text or tenant role changed")
	}
	if _, err := auth.signup(ctx, "Different tenant", strings.ToLower(email), "valid-password"); err == nil || !strings.Contains(err.Error(), "email already registered") {
		t.Fatalf("canonical alias accepted: %v", err)
	}
	if _, err := auth.signup(ctx, name, "other@example.test", "valid-password"); err == nil || !strings.Contains(err.Error(), "shop name already taken") {
		t.Fatalf("occupied slug accepted: %v", err)
	}
	var accounts int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.users`).Scan(&accounts); err != nil || accounts != 1 {
		t.Fatalf("failed signup left identity: %d %v", accounts, err)
	}
	another, err := auth.login(ctx, email, "valid-password")
	if err != nil {
		t.Fatal(err)
	}
	if another == token {
		t.Fatal("login reused token")
	}
	if err := auth.logout(ctx, another); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.userFromRequest(ctx, request); err != nil {
		t.Fatal("logout revoked another device")
	}
	request.Header.Set("Cookie", sessionCookieName+"="+another)
	if _, err := auth.userFromRequest(ctx, request); err == nil {
		t.Fatal("revoked session authenticated")
	}
	request.Header.Set("Cookie", sessionCookieName+"="+token)
	clock.now = clock.now.Add(sessionTTL)
	if _, err := auth.userFromRequest(ctx, request); err == nil {
		t.Fatal("expired session authenticated")
	}
	if _, err := auth.createSession(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.sessions WHERE user_id=CONVERT(uniqueidentifier,@p1)`, user.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("expiry cleanup: %d %v", count, err)
	}
}

func TestSQLServerAuthConcurrentEmailAndTransactionalFailure(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	auth := newAuth(db, "synthetic-secret", false)
	start := make(chan struct{})
	results := make(chan error, 4)
	for i := 0; i < cap(results); i++ {
		go func(i int) {
			<-start
			_, err := auth.signup(ctx, fmt.Sprintf("Business %d", i), "SAME@example.test", "valid-password")
			results <- err
		}(i)
	}
	close(start)
	successes := 0
	for i := 0; i < cap(results); i++ {
		if err := <-results; err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "email already registered") {
			t.Error(err)
		}
	}
	var shops, users, events int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.shops),(SELECT COUNT(*) FROM dbo.users),(SELECT COUNT(*) FROM dbo.pilot_activation_events)`).Scan(&shops, &users, &events); err != nil || successes != 1 || shops != 1 || users != 1 || events != 1 {
		t.Fatalf("concurrent signup: %d %d %d %d %v", successes, shops, users, events, err)
	}
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_activation ON dbo.pilot_activation_events AFTER INSERT AS THROW 50001,'synthetic activation failure',1;`); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.provisionOwner(ctx, OwnerCredentials{Email: "fail@example.test", Password: "valid-password", ShopName: "Failed provision"}); err == nil {
		t.Fatal("failed activation reported provisioned")
	}
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.shops),(SELECT COUNT(*) FROM dbo.users),(SELECT COUNT(*) FROM dbo.owner_provision_events)`).Scan(&shops, &users, &events); err != nil || shops != 1 || users != 1 || events != 0 {
		t.Fatalf("rollback left partial account: %d %d %d %v", shops, users, events, err)
	}
}

func TestSQLServerAuthDoesNotRecoverDifferentStoredIdentity(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	auth := newAuth(db, "synthetic-secret", false)
	for i, email := range []string{"mixed@EXAMPLE.test", "space@example.test "} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			// Migration preserves original case/space; canonical text is derived with
			// Go lowercasing only. Login and recovery retain exact stored-email semantics.
			var shopID string
			if err := db.Pool.QueryRow(ctx, sqlServerInsertUnclaimedShop, fmt.Sprintf("Raw %d", i), fmt.Sprintf("raw-%d", i)).Scan(&shopID); err != nil {
				t.Fatal(err)
			}
			hash, err := bcryptGenerate("original-password")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.users (shop_id,email,email_normalized,password_hash) VALUES (CONVERT(uniqueidentifier,@p1),@p2,@p3,@p4)`, shopID, email, strings.ToLower(email), hash); err != nil {
				t.Fatal(err)
			}
			if _, err := auth.login(ctx, email, "original-password"); err == nil {
				t.Fatal("normalized input authenticated distinct raw identity")
			}
			action, err := auth.provisionOwner(ctx, OwnerCredentials{Email: email, Password: "replacement-password", ShopName: fmt.Sprintf("New %d", i)})
			if action == "recovered" {
				t.Fatal("recovered a different raw identity")
			}
			if i == 0 && err == nil {
				t.Fatal("normalized duplicate was inserted")
			}
			// A trailing space is a different persisted identity, as in PostgreSQL.
			if i == 1 && (err != nil || action != "provisioned") {
				t.Fatalf("distinct identity: %q %v", action, err)
			}
			var gotHash string
			if err := db.Pool.QueryRow(ctx, `SELECT password_hash FROM dbo.users WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shopID).Scan(&gotHash); err != nil || gotHash != hash {
				t.Fatal("original account changed")
			}
		})
	}
}

func TestSQLServerActivationIsIdempotentAndUsesUTCDay(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	auth := newAuth(db, "synthetic-secret", false)
	if _, err := auth.provisionOwner(ctx, OwnerCredentials{Email: "activation@example.test", Password: "valid-password", ShopName: "Activation"}); err != nil {
		t.Fatal(err)
	}
	var shopID string
	if err := db.Pool.QueryRow(ctx, `SELECT LOWER(CONVERT(char(36),id)) FROM dbo.shops`).Scan(&shopID); err != nil {
		t.Fatal(err)
	}
	store := &Store{db: db}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.RecordPilotActivation(ctx, shopID, pilotEventImportPreviewed); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if err := store.RecordReturningPilotUse(ctx, shopID); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.pilot_activation_events WHERE event_type='returning_use'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("same-day use: %d %v", count, err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.pilot_activation_events SET occurred_at=DATEADD(day,-1,SYSUTCDATETIME()) WHERE event_type='account_created'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := store.RecordReturningPilotUse(ctx, shopID); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM dbo.pilot_activation_events`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("milestone duplication: %d %v", count, err)
	}
}

func TestSQLServerRecoveryFailurePreservesPasswordAndSession(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	auth := newAuth(db, "synthetic-secret", false)
	owner := OwnerCredentials{Email: "recovery@example.test", Password: "original-password", ShopName: "Recovery"}
	if _, err := auth.provisionOwner(ctx, owner); err != nil {
		t.Fatal(err)
	}
	token, err := auth.login(ctx, owner.Email, owner.Password)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_recovery ON dbo.owner_provision_events AFTER INSERT AS THROW 50001,'synthetic recovery audit failure',1;`); err != nil {
		t.Fatal(err)
	}
	owner.Password = "replacement-password"
	if _, err := auth.provisionOwner(ctx, owner); err == nil {
		t.Fatal("failed audit reported recovered")
	}
	if _, err := auth.userFromRequest(ctx, request); err != nil {
		t.Fatalf("failed recovery revoked session: %v", err)
	}
	if _, err := auth.login(ctx, owner.Email, "original-password"); err != nil {
		t.Fatal("failed recovery changed original password")
	}
	if _, err := auth.login(ctx, owner.Email, owner.Password); err == nil {
		t.Fatal("failed recovery installed new password")
	}
}

func TestSQLServerDemoExpiryAndTenantDeletionRemainIsolated(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	clock := &fixedClock{now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}
	auth := newAuthWithClock(db, "synthetic-secret", false, clock)
	requests := make([]*http.Request, 2)
	users := make([]*User, 2)
	for i := range requests {
		owner := OwnerCredentials{Email: fmt.Sprintf("tenant%d@example.test", i), Password: "valid-password", ShopName: fmt.Sprintf("Tenant %d", i)}
		if _, err := auth.provisionOwner(ctx, owner); err != nil {
			t.Fatal(err)
		}
		token, err := auth.login(ctx, owner.Email, owner.Password)
		if err != nil {
			t.Fatal(err)
		}
		requests[i] = httptest.NewRequest(http.MethodGet, "/dashboard", nil)
		requests[i].AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
		users[i], err = auth.userFromRequest(ctx, requests[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.shops SET kind='demo_sandbox',expires_at=@p2 WHERE id=CONVERT(uniqueidentifier,@p1)`, users[0].ShopID, clock.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if user, err := auth.userFromRequest(ctx, requests[0]); err != nil || !user.IsDemo {
		t.Fatalf("active demo: %v", err)
	}
	clock.now = clock.now.Add(time.Hour)
	if _, err := auth.userFromRequest(ctx, requests[0]); err == nil {
		t.Fatal("expired shop accepted still-valid session")
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, users[0].ShopID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.userFromRequest(ctx, requests[1]); err != nil {
		t.Fatal("tenant deletion changed unrelated session")
	}
	var accounts, sessions, audits, events int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.users),(SELECT COUNT(*) FROM dbo.sessions),(SELECT COUNT(*) FROM dbo.owner_provision_events),(SELECT COUNT(*) FROM dbo.pilot_activation_events)`).Scan(&accounts, &sessions, &audits, &events); err != nil || accounts != 1 || sessions != 1 || audits != 1 || events != 2 {
		t.Fatalf("tenant cascade: %d %d %d %d %v", accounts, sessions, audits, events, err)
	}
}

func TestSQLServerAuthConstraintsRejectPaddedClosedValues(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	auth := newAuth(db, "synthetic-secret", false)
	if _, err := auth.signup(ctx, "Constraint fixture", "constraints@example.test", "valid-password"); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE dbo.shops SET kind='demo_sandbox ',expires_at=SYSUTCDATETIME()`,
		`UPDATE dbo.users SET role='owner '`,
		`UPDATE dbo.sessions SET token_hash=token_hash+' '`,
		`INSERT INTO dbo.owner_provision_events(user_id,action) SELECT id,'provisioned ' FROM dbo.users`,
		`UPDATE dbo.pilot_activation_events SET event_type='account_created '`,
	} {
		if _, err := db.Pool.Exec(ctx, statement); err == nil {
			t.Errorf("padded value accepted: %s", statement)
		}
	}
}

func TestAccountCreationRejectsInvalidTextBeforeDatabaseAccess(t *testing.T) {
	auth := newAuth(nil, "synthetic-secret", false)
	for _, input := range []struct{ name, email string }{
		{"Bad\x00name", "owner@example.test"}, {"Bad\xffname", "owner@example.test"},
		{"Valid name", "bad\x00@example.test"}, {"Valid name", "bad\xff@example.test"},
	} {
		if _, err := auth.signup(context.Background(), input.name, input.email, "valid-password"); err == nil {
			t.Fatal("invalid text reached signup storage")
		}
		if _, err := auth.provisionOwner(context.Background(), OwnerCredentials{Email: input.email, ShopName: input.name, Password: "valid-password"}); err == nil {
			t.Fatal("invalid text reached provisioning storage")
		}
	}
}

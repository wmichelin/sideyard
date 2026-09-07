package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProvisionOwnerRejectsShopCollisionsAndPreservesRecovery(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := newAuth(db, "owner-isolation-secret", false)
	stamp := time.Now().UnixNano()
	owner := OwnerCredentials{Email: fmt.Sprintf("owner-%d@example.test", stamp), Password: "original-password", ShopName: fmt.Sprintf("Shared Name %d", stamp)}
	if action, err := auth.provisionOwner(ctx, owner); err != nil || action != "provisioned" {
		t.Fatalf("provision = %q, %v", action, err)
	}
	token, err := auth.login(ctx, owner.Email, owner.Password)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	original, err := auth.userFromRequest(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{owner.ShopName, strings.ReplaceAll(strings.ToUpper(owner.ShopName), " ", "-")} {
		t.Run(fmt.Sprintf("collision-%d", i), func(t *testing.T) {
			other := OwnerCredentials{Email: fmt.Sprintf("other-%d-%d@example.test", stamp, i), Password: "unrelated-password", ShopName: name}
			if action, err := auth.provisionOwner(ctx, other); err == nil || !strings.Contains(err.Error(), "shop name already taken") || action != "" {
				t.Errorf("collision = %q, %v; want useful rejection", action, err)
			}
			var count int
			if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE email = $1`, other.Email).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Errorf("rejected email persisted %d users", count)
			}
		})
	}
	if _, err := auth.userFromRequest(ctx, request); err != nil {
		t.Fatalf("collision revoked original session: %v", err)
	}
	owner.Email = "  " + strings.ToUpper(owner.Email) + "  "
	owner.ShopName = "Different name must not move the owner"
	owner.Password = "replacement-password"
	if action, err := auth.provisionOwner(ctx, owner); err != nil || action != "recovered" {
		t.Fatalf("recovery = %q, %v", action, err)
	}
	if _, err := auth.userFromRequest(ctx, request); err == nil {
		t.Error("old session survived recovery")
	}
	if _, err := auth.login(ctx, owner.Email, "original-password"); err == nil {
		t.Error("old password survived recovery")
	}
	newToken, err := auth.login(ctx, owner.Email, owner.Password)
	if err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: newToken})
	recovered, err := auth.userFromRequest(ctx, request)
	if err != nil || recovered == nil || recovered.ShopID != original.ShopID {
		t.Fatalf("recovery changed tenant: %v, %v", recovered, err)
	}
	var provisioned, recoveries int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action = 'provisioned'), count(*) FILTER (WHERE action = 'recovered') FROM owner_provision_events WHERE user_id = $1`, original.ID).Scan(&provisioned, &recoveries); err != nil {
		t.Fatal(err)
	}
	if provisioned != 1 || recoveries != 1 {
		t.Fatalf("audit = %d provisions, %d recoveries", provisioned, recoveries)
	}
}

func TestProvisionOwnerConcurrentShopCollisionCreatesOneOwner(t *testing.T) {
	ctx := context.Background()
	db, err := openDB(ctx, integrationDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	auth := newAuth(db, "owner-concurrency-secret", false)
	stamp := time.Now().UnixNano()
	shopName := fmt.Sprintf("Concurrent Shop %d", stamp)
	start := make(chan struct{})
	results := make(chan error, 4)
	for i := 0; i < cap(results); i++ {
		go func(i int) {
			<-start
			_, err := auth.provisionOwner(ctx, OwnerCredentials{Email: fmt.Sprintf("race-%d-%d@example.test", stamp, i), Password: "concurrent-password", ShopName: shopName})
			results <- err
		}(i)
	}
	close(start)
	successes := 0
	for i := 0; i < cap(results); i++ {
		if err := <-results; err == nil {
			successes++
		} else if !strings.Contains(err.Error(), "shop name already taken") {
			t.Errorf("unexpected collision error: %v", err)
		}
	}
	var owners, audits int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM users u JOIN shops s ON s.id = u.shop_id WHERE s.slug = $1`, slugify(shopName)).Scan(&owners); err != nil {
		t.Fatal(err)
	}
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM owner_provision_events e JOIN users u ON u.id = e.user_id JOIN shops s ON s.id = u.shop_id WHERE s.slug = $1`, slugify(shopName)).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || owners != 1 || audits != 1 {
		t.Fatalf("concurrent results: successes=%d owners=%d audits=%d; want one each", successes, owners, audits)
	}
}

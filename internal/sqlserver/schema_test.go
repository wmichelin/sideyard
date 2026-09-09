package sqlserver

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSQLServerInterestCapacityUpgradePreservesRecordsAndConstraints(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()
	old := fstest.MapFS{}
	for _, path := range []string{"migrations/001_interest.sql", "migrations/002_tenant_auth.sql"} {
		body, err := migrationFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		old[path] = &fstest.MapFile{Data: body}
	}
	if err := p.Migrate(ctx, old); err != nil {
		t.Fatal(err)
	}
	name, business, trade, message, email := strings.Repeat("🔧", 120), strings.Repeat("🔧", 160), strings.Repeat("🔧", 80), strings.Repeat("🔧", 2000), strings.Repeat("🔧", 254)
	if _, err := p.Exec(ctx, `INSERT INTO dbo.interest_signups(name,email,business,trade,message) VALUES(@p1,@p2,@p3,@p4,@p5)`, name, email, business, trade, message); err != nil {
		t.Fatal(err)
	}
	if err := p.MigrateSchema(ctx); err != nil {
		t.Fatal(err)
	}
	var gotName, gotBusiness, gotTrade, gotMessage, gotEmail string
	if err := p.QueryRow(ctx, `SELECT name,email,business,trade,message FROM dbo.interest_signups`).Scan(&gotName, &gotEmail, &gotBusiness, &gotTrade, &gotMessage); err != nil || gotName != name || gotEmail != email || gotBusiness != business || gotTrade != trade || gotMessage != message {
		t.Fatalf("upgrade changed exact-limit Unicode: %v", err)
	}
	if _, err := p.Exec(ctx, `INSERT INTO dbo.interest_signups(name,email) VALUES('Duplicate',@p1)`, email); err == nil {
		t.Fatal("upgrade removed email uniqueness")
	}
	if _, err := p.Exec(ctx, `INSERT INTO dbo.interest_signups(name,email) VALUES('Defaults','defaults@example.test')`); err != nil {
		t.Fatal("upgrade lost optional defaults")
	}
	for _, column := range []string{"name", "email", "business", "trade", "message"} {
		if _, err := p.Exec(ctx, "UPDATE dbo.interest_signups SET "+column+"="+column+"+N' ' WHERE email=@p1", email); err == nil {
			t.Fatalf("%s silently trimmed over-limit space", column)
		}
	}
}

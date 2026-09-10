package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestSQLServerBusinessSetupPreservesExistingBehavior(t *testing.T) {
	t.Run("setup", func(t *testing.T) { testBusinessSetupPreservesEditsAndSerializesSaves(t, sqlServerTestDB(t)) })
	t.Run("preview", func(t *testing.T) { testBusinessSetupPreviewIsReadOnlyAndOwnerOnly(t, sqlServerTestDB(t)) })
	t.Run("availability", func(t *testing.T) { testBusinessSetupAvailability(t, sqlServerTestDB(t)) })
}

func TestSQLServerBusinessSetupAtomicFailuresAndRaces(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "business-atomic")
	b, _ := validateBusinessSelection("hvac", nil)
	// A later failed preset must also roll back the settings and earlier insert.
	if _, err := db.Pool.Exec(ctx, `CREATE TRIGGER dbo.fail_test_business_service ON dbo.business_services AFTER INSERT AS BEGIN IF EXISTS(SELECT 1 FROM inserted WHERE preset_key=N'hvac/repair') THROW 51000,'synthetic service failure',1; END`); err != nil {
		t.Fatal(err)
	}
	keys := []string{"hvac/maintenance", "hvac/repair"}
	if err := s.ApplyBusinessSettings(ctx, shop, b, keys); err == nil {
		t.Fatal("partial setup reported saved")
	}
	got, services, err := s.GetBusinessSettings(ctx, shop)
	if err != nil || got.Revision != 0 || got.Primary != "general" || len(services) != 0 {
		t.Fatal("failed setup retained records")
	}
	if _, err := db.Pool.Exec(ctx, `DROP TRIGGER dbo.fail_test_business_service`); err != nil {
		t.Fatal(err)
	}
	// Two competing first saves cannot combine their service selections.
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, key := range keys {
		go func(key string) { <-start; done <- s.ApplyBusinessSettings(ctx, shop, b, []string{key, key}) }(key)
	}
	close(start)
	wins, conflicts := 0, 0
	for i := 0; i < 2; i++ {
		switch err := <-done; {
		case err == nil:
			wins++
		case errors.Is(err, errBusinessConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	got, services, err = s.GetBusinessSettings(ctx, shop)
	if err != nil || got.Revision != 1 || len(services) != 1 || wins != 1 || conflicts != 1 {
		t.Fatal("first-save race mixed presets or revisions")
	}
	v := services[0]
	start = make(chan struct{})
	done = make(chan error, 2)
	for _, name := range []string{"Edited A", "Edited B"} {
		go func(name string) {
			<-start
			done <- s.UpdateBusinessService(ctx, shop, v.ID, name, "Description", false, 1)
		}(name)
	}
	close(start)
	wins, conflicts = 0, 0
	for i := 0; i < 2; i++ {
		switch err := <-done; {
		case err == nil:
			wins++
		case errors.Is(err, errBusinessConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatal("service revision race")
	}
	// Both revision overflows must leave the previous state and no new presets.
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.business_settings SET revision=2147483647 WHERE shop_id=CONVERT(uniqueidentifier,@p1); UPDATE dbo.business_services SET revision=2147483647 WHERE shop_id=CONVERT(uniqueidentifier,@p1);`, shop); err != nil {
		t.Fatal(err)
	}
	got.Revision = 2147483647
	if err := s.ApplyBusinessSettings(ctx, shop, got, keys); err == nil {
		t.Fatal("settings overflow accepted")
	}
	if err := s.UpdateBusinessService(ctx, shop, v.ID, "Overflow", "", true, 2147483647); err == nil {
		t.Fatal("service overflow accepted")
	}
	got, services, err = s.GetBusinessSettings(ctx, shop)
	if err != nil || got.Revision != 2147483647 || len(services) != 1 || services[0].Revision != 2147483647 || services[0].Name == "Overflow" || services[0].Enabled {
		t.Fatal("overflow changed setup")
	}
	// Services are deliberately independent of the selected settings.
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.business_settings WHERE shop_id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	got, services, err = s.GetBusinessSettings(ctx, shop)
	if err != nil || got.Primary != "general" || got.Revision != 0 || len(services) != 1 {
		t.Fatal("missing settings lost services/defaults")
	}
	if err := s.ApplyBusinessSettings(ctx, shop, b, keys); err != nil {
		t.Fatal("same revision retry failed", err)
	}
	if _, err := db.Pool.Exec(ctx, `DELETE FROM dbo.shops WHERE id=CONVERT(uniqueidentifier,@p1)`, shop); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.Pool.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM dbo.business_settings)+(SELECT COUNT(*) FROM dbo.business_services)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("shop cascade left setup")
	}
}

func TestSQLServerBusinessTradesPreserveExactHistoricalStrings(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "business-json")
	insert := func(primary, trades string) error {
		_, err := db.Pool.Exec(ctx, `INSERT INTO dbo.business_settings(shop_id,primary_trade,trades) VALUES(CONVERT(uniqueidentifier,@p1),@p2,@p3)`, shop, primary, trades)
		return err
	}
	for _, trades := range []string{`not json`, `{}`, `"hvac"`, `[]`, `["HVAC"]`, `["hvac "]`, `["hvac",null]`, `["hvac",1]`, `["hvac",{}]`, `["hvac",[]]`, `["hvac",true]`} {
		if err := insert("hvac", trades); err == nil {
			t.Fatalf("invalid trades accepted: %s", trades)
		}
	}
	long := strings.Repeat("🔧", 2200) + " \"\\\n"
	trades := []string{"unknown", long, long, "unknown "}
	encoded, _ := json.Marshal(trades)
	if err := insert(long, string(encoded)); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.GetBusinessSettings(ctx, shop)
	if err != nil || got.Primary != long {
		t.Fatal("long primary changed", err)
	}
	if !slices.Equal(trades, got.Trades) {
		t.Fatal("stored trade strings/order changed")
	}
	// Set-based validation rejects the complete statement, including valid rows.
	second := createTestShop(t, ctx, db, "business-json-other")
	if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.business_settings(shop_id,primary_trade,trades) VALUES(CONVERT(uniqueidentifier,@p1),N'hvac',N'["hvac"]')`, second); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE dbo.business_settings SET primary_trade=CASE WHEN shop_id=CONVERT(uniqueidentifier,@p1) THEN N'unknown' ELSE N'missing' END,revision=revision+1`, shop); err == nil {
		t.Fatal("multirow invalid membership accepted")
	}
	got, _, err = s.GetBusinessSettings(ctx, shop)
	if err != nil || got.Primary != long || got.Revision != 1 {
		t.Fatal("invalid second row partially updated first")
	}
}

func TestSQLServerBusinessServicesPreserveTextIdentityAndOrdering(t *testing.T) {
	db := sqlServerTestDB(t)
	ctx := context.Background()
	s := &Store{db: db}
	shop := createTestShop(t, ctx, db, "business-text")
	long := strings.Repeat("🔧", 2200)
	description := long + "  "
	for i, key := range []string{long, long + " ", "Key", "key", "key "} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.business_services(shop_id,preset_key,preset_version,name,description) VALUES(CONVERT(uniqueidentifier,@p1),@p2,7,@p3,@p4)`, shop, key, fmt.Sprintf("Historical %d", i), description); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.business_services(shop_id,preset_key,preset_version,name) VALUES(CONVERT(uniqueidentifier,@p1),@p2,1,N'Duplicate')`, shop, long); err == nil {
		t.Fatal("duplicate identity accepted")
	}
	_, services, err := s.GetBusinessSettings(ctx, shop)
	if err != nil || len(services) != 5 {
		t.Fatal("historical service lost")
	}
	for _, v := range services {
		if v.Description != description {
			t.Fatal("historical description changed")
		}
	}
	v := services[0]
	for _, name := range []string{strings.Repeat("🔧", 31), "bad\xff", "bad\x00"} {
		if err := s.UpdateBusinessService(ctx, shop, v.ID, name, "", true, 1); !errors.Is(err, errBusinessInput) {
			t.Fatal("invalid service text accepted", err)
		}
	}
	if err := s.UpdateBusinessService(ctx, shop, v.ID, strings.Repeat("🔧", 30), strings.Repeat("🔧", 500), true, 1); err != nil {
		t.Fatal("exact byte limits rejected", err)
	}
	for _, id := range []string{v.ID + "suffix", "bad"} {
		if err := s.UpdateBusinessService(ctx, shop, id, "New", "", true, 2); !errors.Is(err, errInvalidRecordID) {
			t.Fatal("UUID alias accepted")
		}
	}
	// Supplementary Unicode needs enough physical capacity to reject overflow,
	// including a trailing space that SQL otherwise truncates at the column edge.
	maxName := strings.Repeat("🔧", 120)
	if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.business_services(shop_id,preset_key,preset_version,name) VALUES(CONVERT(uniqueidentifier,@p1),N'max',1,@p2)`, shop, maxName); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.business_services(shop_id,preset_key,preset_version,name) VALUES(CONVERT(uniqueidentifier,@p1),N'overflow',1,@p2)`, shop, maxName+" "); err == nil {
		t.Fatal("oversized name truncated")
	}
	orderShop := createTestShop(t, ctx, db, "business-order")
	names := []string{"\U00010000", "ä", "a ", "A", "Á", "z", "\ue000", "a"}
	for i, name := range names {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.business_services(shop_id,preset_key,preset_version,name) VALUES(CONVERT(uniqueidentifier,@p1),@p2,1,@p3)`, orderShop, fmt.Sprint(i), name); err != nil {
			t.Fatal(err)
		}
	}
	var wantIDs []string
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("%08x-0000-0000-0000-%012x", i+1, 2-i)
		wantIDs = append(wantIDs, id)
		if _, err := db.Pool.Exec(ctx, `INSERT INTO dbo.business_services(id,shop_id,preset_key,preset_version,name) VALUES(CONVERT(uniqueidentifier,@p1),CONVERT(uniqueidentifier,@p2),@p1,1,N'tie')`, id, orderShop); err != nil {
			t.Fatal(err)
		}
	}
	_, services, err = s.GetBusinessSettings(ctx, orderShop)
	if err != nil {
		t.Fatal(err)
	}
	var gotNames, gotIDs []string
	for _, v := range services {
		if v.Name == "tie" {
			gotIDs = append(gotIDs, v.ID)
		} else {
			gotNames = append(gotNames, v.Name)
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, gotNames) {
		t.Fatalf("name order: got %q want %q", gotNames, names)
	}
	if !slices.Equal(wantIDs, gotIDs) {
		t.Fatalf("ID tie order: got %v want %v", gotIDs, wantIDs)
	}
}

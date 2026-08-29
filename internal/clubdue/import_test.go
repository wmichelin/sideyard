package clubdue

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestSampleImportFixture(t *testing.T) {
	f, err := os.Open("testdata/sample-import.csv")
	if err != nil {
		t.Fatalf("open sample fixture: %v", err)
	}
	defer f.Close()

	rows, err := parseImportCSV(f)
	if err != nil {
		t.Fatalf("parse sample fixture: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("sample rows: got %d want 5", len(rows))
	}
	for _, row := range rows {
		if row.Error != "" || row.ExternalID == "" {
			t.Fatalf("invalid sample row: %+v", row)
		}
	}
}

func TestParseImportCSV(t *testing.T) {
	csv := `external_id,customer_name,email,phone,address_line1,city,state,postal_code,plan_name,anniversary_date,status,price_cents
csv-1001,Morgan Walsh,morgan@example.com,555-0201,142 Oak Lane,Denver,CO,80202,Gold HVAC Plan,2026-09-15,active,24900
`
	rows, err := parseImportCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: got %d want 1", len(rows))
	}
	r := rows[0]
	if r.ExternalID != "csv-1001" || r.CustomerName != "Morgan Walsh" {
		t.Fatalf("unexpected row: %+v", r)
	}
	if r.Error != "" {
		t.Fatalf("unexpected error: %s", r.Error)
	}
	if !r.AnniversaryDate.Equal(time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("anniversary: %v", r.AnniversaryDate)
	}
	if r.PriceCents != 24900 || r.Status != AgreementActive {
		t.Fatalf("price/status: %d %s", r.PriceCents, r.Status)
	}
}

func TestParseImportCSV_missingColumn(t *testing.T) {
	_, err := parseImportCSV(strings.NewReader("external_id,customer_name\nx,y"))
	if err == nil || !strings.Contains(err.Error(), "missing required column") {
		t.Fatalf("expected missing column error, got %v", err)
	}
}

func TestParseImportCSV_validationError(t *testing.T) {
	csv := `external_id,customer_name,email,phone,address_line1,city,state,postal_code,plan_name,anniversary_date,status,price_cents
,Missing ID,a@b.com,,1 Main,Denver,CO,80202,Gold,2026-01-01,active,100
`
	rows, err := parseImportCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rows[0].Error == "" {
		t.Fatal("expected validation error on row")
	}
}

func TestImportRowContentHash(t *testing.T) {
	row := ImportRow{
		ExternalID:      "csv-1",
		CustomerName:    "Test",
		AnniversaryDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Status:          AgreementActive,
	}
	h1 := row.contentHash()
	h2 := row.contentHash()
	if h1 != h2 || h1 == "" {
		t.Fatalf("hash unstable: %q %q", h1, h2)
	}
}

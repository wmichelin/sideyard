package app

import (
	"fmt"
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

func TestLandscapingSheetsFixtureIncludesImportableRecurringServices(t *testing.T) {
	f, err := os.Open("testdata/landscaping-google-sheets-template.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := parseImportCSV(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("landscaping rows = %d, want 3", len(rows))
	}
	for _, row := range rows {
		if row.Error != "" || row.ServiceName == "" || row.ServiceExternalID == "" || row.ServiceStartsOn.IsZero() {
			t.Fatalf("invalid landscaping row: %+v", row)
		}
	}
	if rows[1].CadenceUnit != "week" || rows[1].CadenceInterval != 2 {
		t.Fatalf("biweekly cadence normalized to %d %s", rows[1].CadenceInterval, rows[1].CadenceUnit)
	}
}

func TestPublicDemoLandscapingFixtureFitsImportLimits(t *testing.T) {
	f, err := os.Open("static/landscaping-demo-import.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := parseImportCSVWithLimits(f, publicDemoMaxImportRows, publicDemoMaxImportCellBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("public demo sample rows = %d, want 3", len(rows))
	}
	for _, row := range rows {
		if row.Error != "" || !strings.HasPrefix(row.ExternalID, "demo-csv-") || row.ServiceName == "" {
			t.Fatalf("invalid public demo sample row: %+v", row)
		}
	}
}

func TestParseImportCSVWithPublicDemoRowAndCellLimits(t *testing.T) {
	buildCSV := func(rowCount, nameBytes int) string {
		var csv strings.Builder
		csv.WriteString("external_id,customer_name,address_line1,city,state,postal_code,anniversary_date\n")
		for i := range rowCount {
			fmt.Fprintf(&csv, "limit-%03d,%s,1 Main St,Columbus,OH,43215,2030-01-01\n", i, strings.Repeat("x", nameBytes))
		}
		return csv.String()
	}

	rows, err := parseImportCSVWithLimits(
		strings.NewReader(buildCSV(publicDemoMaxImportRows, publicDemoMaxImportCellBytes)),
		publicDemoMaxImportRows,
		publicDemoMaxImportCellBytes,
	)
	if err != nil || len(rows) != publicDemoMaxImportRows {
		t.Fatalf("exact public demo limits = %d rows, %v", len(rows), err)
	}
	if _, err := parseImportCSVWithLimits(
		strings.NewReader(buildCSV(publicDemoMaxImportRows+1, 1)),
		publicDemoMaxImportRows,
		publicDemoMaxImportCellBytes,
	); err == nil || !strings.Contains(err.Error(), "100-row limit") {
		t.Fatalf("row limit error = %v", err)
	}
	if _, err := parseImportCSVWithLimits(
		strings.NewReader(buildCSV(1, publicDemoMaxImportCellBytes+1)),
		publicDemoMaxImportRows,
		publicDemoMaxImportCellBytes,
	); err == nil || !strings.Contains(err.Error(), "2000-byte") {
		t.Fatalf("cell limit error = %v", err)
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

func TestParseImportCSVRejectsPricesOutsidePostgresIntegerRange(t *testing.T) {
	for _, tc := range []struct {
		name        string
		priceHeader string
		price       string
		wantError   bool
	}{
		{name: "max cents", priceHeader: "price_cents", price: "2147483647"},
		{name: "cents overflow", priceHeader: "price_cents", price: "2147483648", wantError: true},
		{name: "max dollars", priceHeader: "price", price: "21474836.47"},
		{name: "dollars overflow", priceHeader: "price", price: "21474836.48", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			csv := "external_id,customer_name,address_line1,city,state,postal_code,anniversary_date," + tc.priceHeader + "\n" +
				"bounded-price,Example,1 Main,Columbus,OH,43215,2030-01-01," + tc.price + "\n"
			rows, err := parseImportCSV(strings.NewReader(csv))
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("rows = %d", len(rows))
			}
			if tc.wantError && rows[0].Error == "" {
				t.Fatalf("overflowing price accepted: %+v", rows[0])
			}
			if !tc.wantError && (rows[0].Error != "" || int64(rows[0].PriceCents) != maxImportPriceCents) {
				t.Fatalf("bounded price = %+v", rows[0])
			}
		})
	}
}

func TestParseGoogleSheetsCSVWithOptionalColumnsCurrencyAndCommonDate(t *testing.T) {
	csv := "\ufeffcustomer_name,external_id,address_line1,city,state,postal_code,anniversary_date,price,extra_notes\r\n" +
		`Maya Thompson,land-001,184 Willow Lane,Columbus,OH,43215,9/15/2026,"$1,850.50",gate code kept separately` + "\r\n"
	rows, err := parseImportCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatalf("parse Sheets export: %v", err)
	}
	if len(rows) != 1 || rows[0].Error != "" {
		t.Fatalf("Sheets rows = %+v", rows)
	}
	if rows[0].PriceCents != 185050 || !rows[0].AnniversaryDate.Equal(time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("normalized Sheets row = %+v", rows[0])
	}
}

func TestParseImportCSVRejectsDuplicateExternalIDBeforeCommit(t *testing.T) {
	csv := `external_id,customer_name,address_line1,city,state,postal_code,anniversary_date
same,Maya Thompson,1 Main St,Columbus,OH,43215,2026-09-15
same,Daniel Brooks,2 Main St,Columbus,OH,43215,2026-10-15
`
	rows, err := parseImportCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Error != "" || rows[1].Error != "duplicate external_id in file" {
		t.Fatalf("duplicate rows = %+v", rows)
	}
}

func TestParseImportCSVRejectsIncompleteOrReversedServiceRecurrence(t *testing.T) {
	for _, row := range []string{
		"svc-1,Maya,1 Main,Columbus,OH,43215,2026-09-15,Weekly mowing,,week,,",
		"svc-2,Maya,1 Main,Columbus,OH,43215,2026-09-15,Weekly mowing,1,week,2026-10-01,2026-09-01",
	} {
		csv := "external_id,customer_name,address_line1,city,state,postal_code,anniversary_date,service_name,cadence_interval,cadence_unit,service_start_date,service_end_date\n" + row + "\n"
		rows, err := parseImportCSV(strings.NewReader(csv))
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Error == "" {
			t.Fatalf("unsafe recurrence accepted: %+v", rows)
		}
	}
}

func TestParseImportCSVRejectsDuplicateServiceIdentity(t *testing.T) {
	csv := `external_id,customer_name,address_line1,city,state,postal_code,anniversary_date,service_external_id,service_name,cadence_unit,service_start_date
agreement-1,Maya,1 Main,Columbus,OH,43215,2026-09-15,same-service,Mowing,week,2026-03-01
agreement-2,Daniel,2 Main,Columbus,OH,43215,2026-10-15,same-service,Edging,week,2026-03-01
`
	rows, err := parseImportCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[1].Error != "duplicate service_external_id in file" {
		t.Fatalf("duplicate service identities = %+v", rows)
	}
}

func TestParseImportCSVTreatsBiweeklyAsAnUnambiguousTwoWeekCadence(t *testing.T) {
	for _, tc := range []struct {
		name      string
		interval  string
		wantError bool
		wantEvery int
	}{
		{name: "implicit", interval: "", wantEvery: 2},
		{name: "explicit two", interval: "2", wantEvery: 2},
		{name: "conflicting one", interval: "1", wantError: true},
		{name: "conflicting three", interval: "3", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			csv := "external_id,customer_name,address_line1,city,state,postal_code,anniversary_date,service_name,cadence_interval,cadence_unit,service_start_date\n" +
				"agreement-1,Maya,1 Main,Columbus,OH,43215,2026-09-15,Mowing," + tc.interval + ",biweekly,2026-03-01\n"
			rows, err := parseImportCSV(strings.NewReader(csv))
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("rows = %d", len(rows))
			}
			if tc.wantError {
				if rows[0].Error == "" {
					t.Fatalf("conflicting biweekly interval accepted: %+v", rows[0])
				}
				return
			}
			if rows[0].Error != "" || rows[0].CadenceUnit != "week" || rows[0].CadenceInterval != tc.wantEvery {
				t.Fatalf("biweekly cadence = %+v", rows[0])
			}
		})
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

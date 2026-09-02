package visits

import (
	"testing"
	"time"
)

func TestSeasonWindowLabel(t *testing.T) {
	label := seasonWindowLabel("spring", 2026)
	if label == "" || label == "spring 2026" {
		t.Fatalf("unexpected label: %q", label)
	}
}

func TestIsVisitOverdue(t *testing.T) {
	// Spring 2020 is long past
	if !isVisitOverdue("spring", 2020, VisitDue, time.Now().UTC()) {
		t.Fatal("expected spring 2020 due visit to be overdue")
	}
	if isVisitOverdue("spring", 2020, VisitCompleted, time.Now().UTC()) {
		t.Fatal("completed visit should not be overdue")
	}
}

func TestWriteVisitExportCSV(t *testing.T) {
	var buf stringsBuilder
	rows := []VisitExportRow{{
		CustomerName: "Alex",
		Phone:        "555-0101",
		Address:      "1 Main St",
		ServiceType:  "Gold maintenance plan",
		Notes:        "agreement abc spring 2026",
	}}
	if err := writeVisitExportCSV(&buf, rows); err != nil {
		t.Fatalf("export: %v", err)
	}
	out := buf.String()
	if !stringsContains(out, "customer_name") || !stringsContains(out, "Alex") {
		t.Fatalf("unexpected csv: %q", out)
	}
}

type stringsBuilder struct {
	b []byte
}

func (s *stringsBuilder) Write(p []byte) (int, error) {
	s.b = append(s.b, p...)
	return len(p), nil
}

func (s *stringsBuilder) String() string { return string(s.b) }

func stringsContains(hay, needle string) bool {
	return len(hay) >= len(needle) && (hay == needle || len(needle) == 0 ||
		(len(hay) > 0 && containsSubstr(hay, needle)))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

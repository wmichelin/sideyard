package clubdue

import "testing"

func TestSummarize(t *testing.T) {
	s := summarize(sampleMembers)
	if s.PaidCount != 2 {
		t.Fatalf("paid count: got %d want 2", s.PaidCount)
	}
	if s.OverdueCount != 2 {
		t.Fatalf("overdue count: got %d want 2", s.OverdueCount)
	}
	if s.TotalExpected != 600 {
		t.Fatalf("expected total: got %v want 600", s.TotalExpected)
	}
	if s.TotalCollected != 300 {
		t.Fatalf("collected total: got %v want 300", s.TotalCollected)
	}
	if s.CollectionRate != 50 {
		t.Fatalf("collection rate: got %d want 50", s.CollectionRate)
	}
}

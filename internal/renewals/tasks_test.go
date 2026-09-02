package renewals

import (
	"context"
	"strings"
	"testing"
)

func TestRenewalNotesAreBoundedBeforeDatabaseWork(t *testing.T) {
	service := &Service{}
	err := service.UpdateRenewalTask(context.Background(), "shop", "task", "contacted", strings.Repeat("x", 2001))
	if err == nil || !strings.Contains(err.Error(), "2000 bytes") {
		t.Fatalf("oversized renewal notes error = %v", err)
	}
}

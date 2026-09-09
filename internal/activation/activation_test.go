package activation

import (
	"context"
	"errors"
	"testing"

	"github.com/wmichelin/clubdue/internal/database"
)

type failingEventPool struct {
	database.Pool // Unexpected calls fail instead of contacting an external DB.
	calls         int
	err           error
}

func (*failingEventPool) Dialect() database.Dialect { return database.Postgres }

func (p *failingEventPool) Exec(context.Context, string, ...any) (database.Result, error) {
	p.calls++
	return nil, p.err
}

func TestRecordReturnsDatabaseFailureAndRejectsInvalidEventBeforeIO(t *testing.T) {
	failure := errors.New("database unavailable")
	p := &failingEventPool{err: failure}
	if err := Record(context.Background(), p, "shop", EventAccountCreated); !errors.Is(err, failure) {
		t.Fatalf("storage failure lost: %v", err)
	}
	if p.calls != 1 {
		t.Fatal("unexpected retries")
	}
	if err := Record(context.Background(), p, "shop", Event("invalid")); err == nil {
		t.Fatal("invalid event accepted")
	}
	if p.calls != 1 {
		t.Fatal("invalid event reached database")
	}
}

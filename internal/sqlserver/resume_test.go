package sqlserver

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

type sequenceConnector struct {
	driver.Connector
	errors []error
	calls  int
	conn   driver.Conn
}

func (c *sequenceConnector) Connect(context.Context) (driver.Conn, error) {
	c.calls++
	if c.calls <= len(c.errors) {
		return nil, c.errors[c.calls-1]
	}
	return c.conn, nil
}

func TestAzureResumeRetriesOnlyUnavailableConnections(t *testing.T) {
	unavailable := fmt.Errorf("login: %w", mssql.Error{Number: 40613})
	conn := &failedInitConnection{}
	base := &sequenceConnector{errors: []error{unavailable, unavailable, unavailable, unavailable, unavailable}, conn: conn}
	var delays []time.Duration
	c := resumingConnector{Connector: base, budget: 80 * time.Second, wait: func(ctx context.Context, d time.Duration) error {
		if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 80*time.Second {
			t.Fatal("unbounded connection retry")
		}
		delays = append(delays, d)
		return nil
	}}
	got, err := c.Connect(context.Background())
	if err != nil || got != conn || base.calls != 6 || !reflect.DeepEqual(delays, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second}) {
		t.Fatalf("resume sequence: %d %v %v", base.calls, delays, err)
	}
	for _, failure := range []error{mssql.Error{Number: 18456}, mssql.Error{Number: 40615}, errors.New("TLS certificate invalid"), context.Canceled} {
		base = &sequenceConnector{errors: []error{failure}}
		c.Connector = base
		c.wait = func(context.Context, time.Duration) error { t.Fatal("retried a non-resume failure"); return nil }
		if _, err := c.Connect(context.Background()); !reflect.DeepEqual(err, failure) || base.calls != 1 {
			t.Fatalf("configuration failure: %v", err)
		}
	}
}

func TestAzureResumeHonorsCancellationAndTotalBudget(t *testing.T) {
	for _, budget := range []time.Duration{5 * time.Millisecond, time.Hour} {
		ctx, cancel := context.WithCancel(context.Background())
		base := &sequenceConnector{errors: []error{mssql.Error{Number: 40613}}}
		c := resumingConnector{Connector: base, budget: budget, wait: waitForResume}
		if budget == time.Hour {
			cancel()
		}
		start := time.Now()
		_, err := c.Connect(ctx)
		cancel()
		if (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) || time.Since(start) > time.Second {
			t.Fatalf("retry escaped deadline: %v", err)
		}
		if base.calls > 1 {
			t.Fatal("retried after cancellation")
		}
	}
}

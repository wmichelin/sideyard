package sqlserver

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

// Retry only connection establishment, before any application statement or
// transaction reaches the server. A database-unavailable error does not prove
// monthly quota exhaustion; keep that distinction for operational monitoring.
type resumingConnector struct {
	driver.Connector
	budget time.Duration
	wait   func(context.Context, time.Duration) error
}

func (c resumingConnector) Connect(parent context.Context) (driver.Conn, error) {
	ctx, cancel := context.WithTimeout(parent, c.budget)
	defer cancel()
	delay := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := c.Connector.Connect(ctx)
		if err == nil {
			if err := ctx.Err(); err != nil {
				if conn != nil {
					_ = conn.Close()
				}
				return nil, err
			}
			return conn, nil
		}
		var sqlErr mssql.Error
		if !errors.As(err, &sqlErr) || sqlErr.Number != 40613 {
			return nil, err
		}
		if waitErr := c.wait(ctx, delay); waitErr != nil {
			return nil, fmt.Errorf("database connection unavailable: %w", waitErr)
		}
		delay = min(delay*2, 10*time.Second)
	}
}

func waitForResume(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

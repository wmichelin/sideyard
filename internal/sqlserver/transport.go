package sqlserver

import (
	"context"
	"database/sql/driver"
	"net"
	"sync"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

// Driver 1.9.3's prelogin and TLS IO do not consistently observe Connect's
// context, and some failed handshakes lose the raw socket. Own those transports
// until native login and session initialization have both succeeded.
type nativeConnector struct{ native *mssql.Connector }

func (c nativeConnector) Driver() driver.Driver { return c.native.Driver() }
func (c nativeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	owner := &connectingTransports{}
	stop := context.AfterFunc(ctx, owner.close)
	defer stop()
	// The pinned native Connector has no mutable synchronization state. Copy
	// its immutable configuration so concurrent attempts get separate dialers.
	attempt := *c.native
	base := attempt.Dialer
	if base == nil {
		base = &net.Dialer{KeepAlive: 30 * time.Second}
	}
	attempt.Dialer = connectingDialer{base: base, owner: owner}
	conn, err := (cleanConnector{&attempt}).Connect(ctx)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil && !stop() {
		err = ctx.Err()
	}
	if err != nil {
		owner.close()
		if conn != nil {
			_ = conn.Close()
		}
		return nil, err
	}
	owner.release()
	return conn, nil
}

type connectingDialer struct {
	base  mssql.Dialer
	owner *connectingTransports
}

func (d connectingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := d.base.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if !d.owner.add(conn) {
		_ = conn.Close()
		return nil, context.Canceled
	}
	return conn, nil
}

type connectingTransports struct {
	mu       sync.Mutex
	conns    []net.Conn
	finished bool
}

func (o *connectingTransports) add(conn net.Conn) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.finished {
		return false
	}
	o.conns = append(o.conns, conn)
	return true
}
func (o *connectingTransports) close() {
	o.mu.Lock()
	conns := o.conns
	o.conns = nil
	o.finished = true
	o.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
	}
}
func (o *connectingTransports) release() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.conns = nil
	o.finished = true
}

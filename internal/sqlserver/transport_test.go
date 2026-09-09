package sqlserver

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/wmichelin/clubdue/internal/testdb"
)

type testDialer func(context.Context, string, string) (net.Conn, error)

func (d testDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return d(ctx, network, address)
}

func TestSQLServerNativeHandshakeHonorsCancellationAndReleasesSocket(t *testing.T) {
	connector, err := mssql.NewConnector("server=127.0.0.1;port=1433;database=sideyard_test;user id=synthetic;password=synthetic;connection timeout=2;disableretry=true")
	if err != nil {
		t.Fatal(err)
	}
	serverClosed := make(chan struct{})
	connector.Dialer = testDialer(func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
		go func() { _, _ = io.Copy(io.Discard, server); close(serverClosed) }()
		return client, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	conn, err := (nativeConnector{connector}).Connect(ctx)
	if conn != nil || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("native handshake escaped request deadline: %v after %v", err, time.Since(start))
	}
	select {
	case <-serverClosed:
	case <-time.After(time.Second):
		t.Fatal("canceled native handshake leaked its raw transport")
	}
}

func TestSQLServerTransportReleaseKeepsSuccessfulConnectionOpen(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	o := &connectingTransports{}
	if !o.add(client) {
		t.Fatal("new transport rejected")
	}
	o.release()
	o.close()
	write := make(chan error, 1)
	go func() { _, err := server.Write([]byte{1}); write <- err }()
	_ = client.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := client.Read(b[:]); err != nil {
		t.Fatal("released successful transport closed")
	}
	if err := <-write; err != nil {
		t.Fatal(err)
	}
}

func TestSQLServerSuccessfulNativeConnectionOutlivesConnectContext(t *testing.T) {
	connector := testdb.SQLServer(t)
	connector.SessionInitSQL = sessionInitSQL
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := (nativeConnector{connector}).Connect(ctx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Give an incorrectly retained cancellation callback a chance to run.
	time.Sleep(10 * time.Millisecond)
	statement, err := conn.(driver.ConnPrepareContext).PrepareContext(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	rows, err := statement.(driver.StmtQueryContext).QueryContext(context.Background(), nil)
	if err != nil {
		t.Fatalf("successful connection remained attached to canceled Connect context: %v", err)
	}
	defer rows.Close()
	values := make([]driver.Value, 1)
	if err := rows.Next(values); err != nil || values[0] != int64(1) {
		t.Fatalf("query after connect cancellation: %v %v", values, err)
	}
}

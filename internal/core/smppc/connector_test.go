package smppc_test

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func TestConnectorConnectionSuccess(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	cfg := smppc.Config{
		CID:      "test",
		Host:     addr.IP.String(),
		Port:     addr.Port,
		SystemID: "client",
		Password: "password",
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	connChan := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			connChan <- conn
		}
	}()

	c := smppc.NewConnector(cfg)
	if err := c.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer c.Stop()

	select {
	case conn := <-connChan:
		defer conn.Close()
		// Verify BIND PDU
		pdu, err := smppwire.Read(conn, 1024)
		if err != nil {
			t.Fatalf("Read BIND failed: %v", err)
		}
		if pdu.Header.CommandID != smppwire.CommandBindTransceiver {
			t.Errorf("expected BIND_TRANSCEIVER, got %#x", pdu.Header.CommandID)
		}
		if string(pdu.Bind.SystemID) != "client" {
			t.Errorf("expected SystemID 'client', got %q", pdu.Bind.SystemID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connection")
	}

	waitForConnectorStatus(t, c, smppc.StatusBound, 2*time.Second)
}

func waitForConnectorStatus(t *testing.T, c *smppc.Connector, want smppc.Status, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		got := c.Status()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected status %s, got %s after %s", want, got, timeout)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestConnectorReconnectLoop(t *testing.T) {
	// Start without listener
	cfg := smppc.Config{
		CID:          "test",
		Host:         "127.0.0.1",
		Port:         12345, // Likely closed
		SystemID:     "client",
		Password:     "password",
		ConFailDelay: 0.1, // Short delay for test
	}
	_ = cfg.Validate()

	c := smppc.NewConnector(cfg)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	time.Sleep(200 * time.Millisecond)
	if c.Status() != smppc.StatusConnecting {
		t.Errorf("expected status CONNECTING during retries, got %s", c.Status())
	}

	// Now start listener on that port
	ln, err := net.Listen("tcp", "127.0.0.1:12345")
	if err != nil {
		// Port might be taken, skip or use a better strategy
		t.Skip("could not bind to 12345")
	}
	defer ln.Close()

	connChan := make(chan net.Conn, 1)
	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			connChan <- conn
		}
	}()

	select {
	case conn := <-connChan:
		conn.Close()
		// Wait for connector to transition to BOUND (or at least try again)
		time.Sleep(200 * time.Millisecond)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnect")
	}
}

func TestConnectorReconnectOnConnectionLoss(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	cfg := smppc.Config{
		CID:          "test",
		Host:         addr.IP.String(),
		Port:         addr.Port,
		SystemID:     "client",
		Password:     "password",
		ConFailDelay: 0.1,
	}
	_ = cfg.Validate()

	connChan := make(chan net.Conn, 2)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			connChan <- conn
		}
	}()

	c := smppc.NewConnector(cfg)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	// First connection
	var conn1 net.Conn
	select {
	case conn1 = <-connChan:
		_ = conn1.Close() // Simulate loss
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first connection")
	}

	// Second connection (reconnect)
	select {
	case conn2 := <-connChan:
		defer conn2.Close()
		// Success
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnect")
	}
}

func TestConnectorStopClosesConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	cfg := smppc.Config{
		CID:      "test",
		Host:     addr.IP.String(),
		Port:     addr.Port,
		SystemID: "client",
		Password: "password",
	}
	_ = cfg.Validate()

	connChan := make(chan net.Conn, 1)
	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			connChan <- conn
		}
	}()

	c := smppc.NewConnector(cfg)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}

	var conn net.Conn
	select {
	case conn = <-connChan:
		// Drain BIND PDU
		_, _ = smppwire.Read(conn, 1024)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for connection")
	}

	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}

	// Check if connection is closed on the server side
	buf := make([]byte, 1)
	conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	n, err := conn.Read(buf)
	if err != io.EOF {
		t.Errorf("expected EOF, got n=%d err=%v", n, err)
	}
}

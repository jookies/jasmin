package smppc

import (
	"context"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

type timerTestAddr string

func (a timerTestAddr) Network() string { return string(a) }
func (a timerTestAddr) String() string  { return string(a) }

type closeCountingConn struct {
	mu     sync.Mutex
	closes int
}

func (c *closeCountingConn) Read([]byte) (int, error)         { return 0, net.ErrClosed }
func (c *closeCountingConn) Write(data []byte) (int, error)   { return len(data), nil }
func (c *closeCountingConn) LocalAddr() net.Addr              { return timerTestAddr("local") }
func (c *closeCountingConn) RemoteAddr() net.Addr             { return timerTestAddr("remote") }
func (c *closeCountingConn) SetDeadline(time.Time) error      { return nil }
func (c *closeCountingConn) SetReadDeadline(time.Time) error  { return nil }
func (c *closeCountingConn) SetWriteDeadline(time.Time) error { return nil }
func (c *closeCountingConn) Close() error {
	c.mu.Lock()
	c.closes++
	c.mu.Unlock()
	return nil
}
func (c *closeCountingConn) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closes
}

func TestSupersededInactivityGenerationCannotCloseConnection(t *testing.T) {
	conn := &closeCountingConn{}
	session := NewSession(conn, Config{CID: "timer-generation", PDUTimeout: 60}, nil, nil, nil)
	session.resetInactivityTimer()
	staleGeneration := session.inactivityGeneration
	session.resetInactivityTimer()
	session.expireInactivity(staleGeneration)
	if got := conn.closeCount(); got != 0 {
		t.Fatalf("stale inactivity callback closed connection %d times", got)
	}
	session.mu.Lock()
	currentGeneration := session.inactivityGeneration
	session.mu.Unlock()
	session.expireInactivity(currentGeneration)
	if got := conn.closeCount(); got != 1 {
		t.Fatalf("current inactivity callback closed connection %d times, want 1", got)
	}
}

func TestSequenceWrapStaysInRangeAndSkipsLiveCorrelations(t *testing.T) {
	session := NewSession(&closeCountingConn{}, Config{CID: "sequence-wrap"}, nil, nil, nil)
	session.mu.Lock()
	defer session.mu.Unlock()
	session.nextSeq = maxSequenceNumber - 1
	sequence, err := session.nextSequenceLocked()
	if err != nil {
		t.Fatal(err)
	}
	if sequence != maxSequenceNumber {
		t.Fatalf("sequence = %#x, want max %#x", sequence, maxSequenceNumber)
	}
	session.pendingControls[1] = nil
	session.pending[2] = &pendingRequest{}
	sequence, err = session.nextSequenceLocked()
	if err != nil {
		t.Fatal(err)
	}
	if sequence != 3 {
		t.Fatalf("wrapped sequence = %d, want 3 after live 1 and 2", sequence)
	}
	if sequence == 0 || sequence > maxSequenceNumber {
		t.Fatalf("sequence outside SMPP range: %#x", sequence)
	}
}

func TestDefaultAMQPProviderDoneClosesOnConsumerContextCancellation(t *testing.T) {
	url := os.Getenv("AMQP_URL")
	if url == "" {
		t.Skip("AMQP_URL is not set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	provider := &defaultAMQPProvider{}
	stream, err := provider.Consume(ctx, url, "test-provider-liveness")
	if err != nil {
		cancel()
		t.Fatalf("Consume: %v", err)
	}
	cancel()
	select {
	case <-stream.Done:
	case <-time.After(2 * time.Second):
		t.Fatal("default provider did not close Done after consumer cancellation")
	}
}

func TestDefaultAMQPProviderClosesRawConnectionOnTLSConfigError(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			accepted <- connection
		}
	}()

	provider := &defaultAMQPProvider{}
	url := "amqps://guest:guest@" + listener.Addr().String() + "/?cacertfile=/definitely/missing.pem"
	if _, err := provider.Consume(context.Background(), url, "tls-error"); err == nil {
		t.Fatal("Consume succeeded with a missing CA file")
	}

	var serverConnection net.Conn
	select {
	case serverConnection = <-accepted:
		defer serverConnection.Close()
	case <-time.After(time.Second):
		t.Fatal("AMQP provider did not establish the raw TCP connection")
	}
	if err := serverConnection.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := serverConnection.Read(buffer); err == nil {
		t.Fatal("raw AMQP connection remained readable after setup error")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("raw AMQP connection leaked after setup error")
	}
}

func TestAMQPTransportHandshakeDeadlineFires(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	connection, stopContextClose, err := dialAMQPTransport(
		context.Background(),
		20*time.Millisecond,
		func(context.Context, string, string) (net.Conn, error) { return client, nil },
		"tcp",
		"unused",
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	defer stopContextClose()

	started := time.Now()
	buffer := make([]byte, 1)
	_, err = connection.Read(buffer)
	if err == nil {
		t.Fatal("silent AMQP peer did not trigger the handshake deadline")
	}
	timeout, ok := err.(net.Error)
	if !ok || !timeout.Timeout() {
		t.Fatalf("handshake read error = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("handshake deadline fired after %s, want under 1s", elapsed)
	}
}

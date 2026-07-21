package smppc

import (
	"net"
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

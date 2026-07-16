package smppc_test

import (
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

func TestManagerLifecycle(t *testing.T) {
	m := smppc.NewManager()
	cfg := smppc.Config{
		CID:      "smpp-1",
		Host:     "127.0.0.1",
		Port:     2775,
		SystemID: "jookies",
	}

	// Add
	if err := m.Add(cfg); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Duplicate
	if err := m.Add(cfg); err != smppc.ErrAlreadyExists {
		t.Errorf("got error %v, want ErrAlreadyExists", err)
	}

	// Get
	c, err := m.Get("smpp-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if c.Config().CID != "smpp-1" {
		t.Errorf("got cid %s, want smpp-1", c.Config().CID)
	}

	// List
	list := m.List()
	if len(list) != 1 {
		t.Errorf("got list len %d, want 1", len(list))
	}

	// Remove (should fail if not disconnected, but it is by default)
	if err := m.Remove("smpp-1"); err != nil {
		t.Errorf("Remove: %v", err)
	}

	// Get again
	if _, err := m.Get("smpp-1"); err != smppc.ErrNotFound {
		t.Errorf("got error %v, want ErrNotFound", err)
	}
}

func TestConnectorState(t *testing.T) {
	cfg := smppc.Config{
		CID:      "smpp-1",
		Host:     "127.0.0.1",
		Port:     2775,
		SystemID: "jookies",
	}
	c := smppc.NewConnector(cfg)

	if c.Status() != smppc.StatusDisconnected {
		t.Errorf("got status %v, want DISCONNECTED", c.Status())
	}

	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if c.Status() != smppc.StatusConnecting {
		t.Errorf("got status %v, want CONNECTING", c.Status())
	}

	if err := c.Stop(); err != nil {
		t.Fatal(err)
	}
	if c.Status() != smppc.StatusDisconnected {
		t.Errorf("got status %v, want DISCONNECTED", c.Status())
	}
}

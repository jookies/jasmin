package smppc_test

import (
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
)

func TestManagerLifecycle(t *testing.T) {
	m := smppc.NewManager("amqp://guest:guest@localhost:5672/")
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

func TestManagerDefensivelyCopiesThroughput(t *testing.T) {
	throughput := 2.0
	m := smppc.NewManager("amqp://guest:guest@localhost:5672/")
	cfg := smppc.Config{
		CID:      "smpp-copy", Host: "127.0.0.1", Port: 2775, SystemID: "jookies",
		SubmitSMThroughput: &throughput,
	}
	if err := m.Add(cfg); err != nil {
		t.Fatal(err)
	}

	throughput = 7
	listed := m.List()
	if got := listed[0].EffectiveSubmitSMThroughput(); got != 2 {
		t.Fatalf("stored throughput changed through input alias: got %v, want 2", got)
	}
	*listed[0].SubmitSMThroughput = 9
	listedAgain := m.List()
	if got := listedAgain[0].EffectiveSubmitSMThroughput(); got != 2 {
		t.Fatalf("stored throughput changed through output alias: got %v, want 2", got)
	}
}

func TestConnectorState(t *testing.T) {
	cfg := smppc.Config{
		CID:      "smpp-1",
		Host:     "127.0.0.1",
		Port:     2775,
		SystemID: "jookies",
	}
	c, err := smppc.NewConnector(cfg, "amqp://guest:guest@localhost:5672/")
	if err != nil {
		t.Fatal(err)
	}

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

package smppc

import "testing"

func TestManagerAvailabilityUsesDesiredAndObservedState(t *testing.T) {
	manager := NewManagerWithFactory("", func(cfg Config, amqpURL string) (*Connector, error) {
		return NewConnector(cfg, amqpURL)
	})
	cfg := Config{CID: "connector-a", Host: "127.0.0.1", Port: 2775, SystemID: "client"}
	if err := manager.Add(cfg); err != nil {
		t.Fatal(err)
	}
	connector, _ := manager.Get(cfg.CID)
	connector.SetStatus(StatusBound)
	if manager.Available(cfg.CID) {
		t.Fatal("observed BOUND without desired start must not be available")
	}
	manager.mu.Lock()
	manager.connectors[cfg.CID].desired = true
	manager.mu.Unlock()
	if !manager.Available(cfg.CID) {
		t.Fatal("desired and observed BOUND connector must be available")
	}
	connector.SetStatus(StatusConnecting)
	if manager.Available(cfg.CID) {
		t.Fatal("desired but CONNECTING connector must not be available")
	}
	if manager.Available("missing") {
		t.Fatal("missing connector must not be available")
	}
}

func TestConfigTLSAndPrefetchValidation(t *testing.T) {
	base := Config{CID: "connector-a", Host: "127.0.0.1", Port: 2775, SystemID: "client"}
	invalidTLS := base
	invalidTLS.TLSServerName = "smsc.example"
	if err := invalidTLS.Validate(); err == nil {
		t.Fatal("TLS options without tls_enabled accepted")
	}
	invalidPrefetch := base
	invalidPrefetch.PrefetchCount = 65536
	if err := invalidPrefetch.Validate(); err == nil {
		t.Fatal("oversized prefetch accepted")
	}
	valid := base
	valid.TLSEnabled = true
	valid.TLSServerName = "smsc.example"
	valid.PrefetchCount = 32
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if valid.PrefetchCount != 32 {
		t.Fatalf("prefetch=%d", valid.PrefetchCount)
	}
}

package smppc

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func boolPointer(value bool) *bool { return &value }

func TestConnectorConnectionFailureRetryFlag(t *testing.T) {
	for _, test := range []struct {
		name  string
		retry bool
	}{
		{name: "disabled", retry: false},
		{name: "enabled", retry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			address := listener.Addr().(*net.TCPAddr)
			accepted := make(chan struct{}, 4)
			go func() {
				for {
					connection, acceptErr := listener.Accept()
					if acceptErr != nil {
						return
					}
					accepted <- struct{}{}
					_ = connection.Close()
				}
			}()
			cfg := Config{
				CID: "failure-" + test.name, Host: address.IP.String(), Port: address.Port,
				SystemID: "client", ConFailRetry: boolPointer(test.retry), ConFailDelay: 0.02,
			}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			connector, err := NewConnector(cfg, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := connector.Start(); err != nil {
				t.Fatal(err)
			}
			defer connector.Stop()
			select {
			case <-accepted:
			case <-time.After(time.Second):
				t.Fatal("initial connection was not attempted")
			}
			select {
			case <-accepted:
				if !test.retry {
					t.Fatal("connection failure retried while disabled")
				}
			case <-time.After(100 * time.Millisecond):
				if test.retry {
					t.Fatal("connection failure was not retried while enabled")
				}
			}
		})
	}
}

func TestConnectorConnectionLossRetryFlag(t *testing.T) {
	for _, test := range []struct {
		name  string
		retry bool
	}{
		{name: "disabled", retry: false},
		{name: "enabled", retry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			address := listener.Addr().(*net.TCPAddr)
			accepted := make(chan struct{}, 4)
			go func() {
				for {
					connection, acceptErr := listener.Accept()
					if acceptErr != nil {
						return
					}
					accepted <- struct{}{}
					request, readErr := smppwire.Read(connection, smppwire.DefaultMaxSize)
					if readErr == nil {
						response, encodeErr := smppwire.Encode(smppwire.PDU{
							Header:       smppwire.Header{CommandID: smppwire.CommandBindTransceiverResp, SequenceNumber: request.Header.SequenceNumber},
							BindResponse: &smppwire.BindResponseBody{SystemID: []byte("smsc")},
						})
						if encodeErr == nil {
							_, _ = connection.Write(response)
						}
					}
					_ = connection.Close()
				}
			}()
			cfg := Config{
				CID: "loss-" + test.name, Host: address.IP.String(), Port: address.Port,
				SystemID: "client", ConFailRetry: boolPointer(false),
				ConLossRetry: boolPointer(test.retry), ConLossDelay: 0.02,
			}
			if err := cfg.Validate(); err != nil {
				t.Fatal(err)
			}
			connector, err := NewConnector(cfg, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := connector.Start(); err != nil {
				t.Fatal(err)
			}
			defer connector.Stop()
			select {
			case <-accepted:
			case <-time.After(time.Second):
				t.Fatal("initial connection was not attempted")
			}
			select {
			case <-accepted:
				if !test.retry {
					t.Fatal("connection loss retried while disabled")
				}
			case <-time.After(150 * time.Millisecond):
				if test.retry {
					t.Fatal("connection loss was not retried while enabled")
				}
			}
		})
	}
}

func TestConfigReconnectFlagsDefaultTrueAndCloneIndependently(t *testing.T) {
	cfg := Config{CID: "flags", Host: "127.0.0.1", Port: 2775, SystemID: "client"}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if !cfg.ConnectionFailureRetryEnabled() || !cfg.ConnectionLossRetryEnabled() {
		t.Fatal("legacy reconnect flags must default to enabled")
	}
	clone := cfg.Clone()
	*clone.ConFailRetry = false
	*clone.ConLossRetry = false
	if !cfg.ConnectionFailureRetryEnabled() || !cfg.ConnectionLossRetryEnabled() {
		t.Fatal("clone aliases reconnect flag pointers")
	}
}

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

func TestConnectorStopHonorsConfiguredUnbindTimeout(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := NewErrorRetryPolicy(DefaultErrorRetryRules())
	readiness, _ := NewReadinessPolicy(DefaultReadinessConfig())
	session := NewSession(client, Config{TrxTimeout: 1}, retry, readiness, nil)
	go func() { _ = session.Run(context.Background()) }()
	connector := &Connector{cfg: Config{TrxTimeout: 1}, session: session, status: StatusBound}
	peerDone := make(chan error, 1)
	go func() {
		request, err := smppwire.Read(server, smppwire.DefaultMaxSize)
		if err != nil {
			peerDone <- err
			return
		}
		time.Sleep(300 * time.Millisecond)
		response, err := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{
			CommandID: smppwire.CommandUnbindResp, SequenceNumber: request.Header.SequenceNumber,
		}})
		if err == nil {
			_, err = server.Write(response)
		}
		peerDone <- err
	}()
	started := time.Now()
	if err := connector.Stop(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed < 300*time.Millisecond || elapsed >= time.Second {
		t.Fatalf("Stop elapsed=%s, want configured graceful wait", elapsed)
	}
	if err := <-peerDone; err != nil {
		t.Fatal(err)
	}
}

func TestSessionRejectsCommandMismatchedControlResponse(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	session := &Session{
		conn: client,
		pendingControls: map[uint32]*pendingControl{
			42: {expectedResponseCommand: smppwire.CommandEnquireLinkResp},
		},
	}
	if err := session.handlePDU(smppwire.PDU{Header: smppwire.Header{
		CommandID: smppwire.CommandUnbindResp, SequenceNumber: 42,
	}}); err != nil {
		t.Fatalf("mismatched response returned error: %v", err)
	}
	if _, pending := session.pendingControls[42]; !pending {
		t.Fatal("mismatched response consumed enquire_link control")
	}
}

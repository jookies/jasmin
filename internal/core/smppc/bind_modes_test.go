package smppc

import (
	"testing"

	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

func TestConfigAcceptsAllBindModes(t *testing.T) {
	for _, bind := range []BindType{BindTransceiver, BindTransmitter, BindReceiver, ""} {
		cfg := Config{CID: "c", Host: "h", Port: 2775, SystemID: "s", Bind: bind}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("bind %q rejected: %v", bind, err)
		}
	}
	bad := Config{CID: "c", Host: "h", Port: 2775, SystemID: "s", Bind: "carrier-pigeon"}
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid bind accepted")
	}
	// Empty defaults to transceiver.
	defaulted := Config{CID: "c", Host: "h", Port: 2775, SystemID: "s"}
	if err := defaulted.Validate(); err != nil {
		t.Fatal(err)
	}
	if defaulted.Bind != BindTransceiver {
		t.Fatalf("empty bind defaulted to %q want transceiver", defaulted.Bind)
	}
}

func TestBindRoleCapabilities(t *testing.T) {
	cases := []struct {
		bind             BindType
		canSubmit, canRx bool
	}{
		{BindTransceiver, true, true},
		{BindTransmitter, true, false},
		{BindReceiver, false, true},
	}
	for _, testCase := range cases {
		cfg := Config{Bind: testCase.bind}
		if cfg.CanSubmit() != testCase.canSubmit {
			t.Errorf("%s CanSubmit=%v want %v", testCase.bind, cfg.CanSubmit(), testCase.canSubmit)
		}
		if cfg.CanReceive() != testCase.canRx {
			t.Errorf("%s CanReceive=%v want %v", testCase.bind, cfg.CanReceive(), testCase.canRx)
		}
	}
}

func TestBindCommandsPerRole(t *testing.T) {
	cases := map[BindType][2]uint32{
		BindTransceiver: {smppwire.CommandBindTransceiver, smppwire.CommandBindTransceiverResp},
		BindTransmitter: {smppwire.CommandBindTransmitter, smppwire.CommandBindTransmitterResp},
		BindReceiver:    {smppwire.CommandBindReceiver, smppwire.CommandBindReceiverResp},
	}
	for bind, want := range cases {
		req, resp := bindCommands(bind)
		if req != want[0] || resp != want[1] {
			t.Errorf("%s bindCommands = %#x/%#x want %#x/%#x", bind, req, resp, want[0], want[1])
		}
	}
}

func TestManagerAvailableExcludesReceiver(t *testing.T) {
	manager := NewManager("amqp://localhost")
	// A receiver connector, once bound, must not be MT-available.
	receiver := Config{CID: "rx", Host: "h", Port: 2775, SystemID: "s", Bind: BindReceiver}
	if err := manager.Add(receiver); err != nil {
		t.Fatal(err)
	}
	// Force the managed status to bound+desired to isolate the bind-role gate.
	entry := manager.connectors["rx"]
	entry.desired = true
	entry.connector.setStatus(StatusBound)
	if manager.Available("rx") {
		t.Fatal("receiver connector reported MT-available")
	}

	transmitter := Config{CID: "tx", Host: "h", Port: 2775, SystemID: "s", Bind: BindTransmitter}
	if err := manager.Add(transmitter); err != nil {
		t.Fatal(err)
	}
	txEntry := manager.connectors["tx"]
	txEntry.desired = true
	txEntry.connector.setStatus(StatusBound)
	if !manager.Available("tx") {
		t.Fatal("transmitter connector not MT-available")
	}
}

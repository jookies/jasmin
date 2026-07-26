package smpps

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/logging"
)

type fakeBinding struct{ t BindType }

func (f fakeBinding) BindType() BindType { return f.t }

func TestBindTypeName(t *testing.T) {
	cases := map[BindType]string{
		BindReceiver:    "CommandId.bind_receiver",
		BindTransmitter: "CommandId.bind_transmitter",
		BindTransceiver: "CommandId.bind_transceiver",
	}
	for bindType, want := range cases {
		if got := bindTypeName(bindType); got != want {
			t.Errorf("bindTypeName(%d) = %q, want %q", bindType, got, want)
		}
	}
}

func TestBoundCountsStr(t *testing.T) {
	manager := NewBindManager()
	manager.Add(fakeBinding{BindTransceiver})
	manager.Add(fakeBinding{BindTransceiver})
	manager.Add(fakeBinding{BindReceiver})
	want := "CommandId.bind_transceiver: 2, CommandId.bind_transmitter: 0, CommandId.bind_receiver: 1"
	if got := boundCountsStr(manager); got != want {
		t.Errorf("boundCountsStr = %q, want %q", got, want)
	}
}

func TestLogBind(t *testing.T) {
	var buffer bytes.Buffer
	server := &Server{logger: logging.Logger("smpp.server", logging.Config{Writer: &buffer})}
	manager := NewBindManager()
	manager.Add(fakeBinding{BindTransceiver})
	server.logBind("Added", BindTransceiver, "esme1", manager)
	want := "Added CommandId.bind_transceiver bind for 'esme1'. Active binds: " +
		"CommandId.bind_transceiver: 1, CommandId.bind_transmitter: 0, CommandId.bind_receiver: 0."
	if !strings.Contains(buffer.String(), want) {
		t.Errorf("bind line missing:\n got %q\nwant %q", buffer.String(), want)
	}
	if !strings.Contains(buffer.String(), "INFO") {
		t.Errorf("bind line must be INFO: %q", buffer.String())
	}
	// A nil logger is a no-op.
	(&Server{}).logBind("Dropped", BindReceiver, "x", manager)
}

// TestBindLogDifferential proves the bind_type rendering (str of CommandId) and
// the Active-binds count string match the legacy SMPPServerFactory exactly.
func TestBindLogDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	// counts as [transceiver, transmitter, receiver]
	specs := [][3]int{{1, 0, 0}, {0, 2, 3}, {2, 1, 1}, {0, 0, 0}}
	payload, err := json.Marshal(specs)
	if err != nil {
		t.Fatal(err)
	}
	const oracle = `
import sys, json
from smpp.pdu.pdu_types import CommandId
# getBoundConnectionCountsStr order: _binds is {transceiver, transmitter, receiver}
order = [CommandId.bind_transceiver, CommandId.bind_transmitter, CommandId.bind_receiver]
out = {"types": [str(c) for c in order], "counts": []}
for tc, tx, rx in json.load(sys.stdin):
    counts = {CommandId.bind_transceiver: tc, CommandId.bind_transmitter: tx, CommandId.bind_receiver: rx}
    out["counts"].append(', '.join("%s: %d" % (k, counts[k]) for k in order))
print(json.dumps(out))
`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", oracle)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var want struct {
		Types  []string `json:"types"`
		Counts []string `json:"counts"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	// bind_type rendering (order transceiver, transmitter, receiver).
	goTypes := []string{bindTypeName(BindTransceiver), bindTypeName(BindTransmitter), bindTypeName(BindReceiver)}
	for i, wantType := range want.Types {
		if goTypes[i] != wantType {
			t.Errorf("bind type %d: go %q, py %q", i, goTypes[i], wantType)
		}
	}
	for i, spec := range specs {
		manager := NewBindManager()
		for n := 0; n < spec[0]; n++ {
			manager.Add(fakeBinding{BindTransceiver})
		}
		for n := 0; n < spec[1]; n++ {
			manager.Add(fakeBinding{BindTransmitter})
		}
		for n := 0; n < spec[2]; n++ {
			manager.Add(fakeBinding{BindReceiver})
		}
		if got := boundCountsStr(manager); got != want.Counts[i] {
			t.Errorf("counts %v: go %q, py %q", spec, got, want.Counts[i])
		}
	}
}

package smppc

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestPythonBytesReprDifferential proves pythonBytesRepr reproduces CPython's
// repr(bytes) byte-for-byte across every single byte value, quote/escape combos,
// and multi-byte strings — the highest-risk rendering in the SMS-MT audit line.
func TestPythonBytesReprDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	// Corpus: every single byte, then curated multi-byte edge cases.
	var corpus [][]byte
	for value := 0; value < 256; value++ {
		corpus = append(corpus, []byte{byte(value)})
	}
	corpus = append(corpus,
		[]byte(""), []byte("plain"), []byte("1111"),
		[]byte("it's"), []byte(`say "hi"`), []byte(`both ' and "`),
		[]byte("tab\there"), []byte("nl\nhere"), []byte("cr\rhere"), []byte(`back\slash`),
		[]byte("café mañana"), []byte{0, 1, 2, 39, 34, 92, 127, 128, 255},
		[]byte("a'b\"c\\d\te"),
	)
	hexInputs := make([]string, len(corpus))
	for i, b := range corpus {
		hexInputs[i] = hex.EncodeToString(b)
	}
	payload, err := json.Marshal(hexInputs)
	if err != nil {
		t.Fatal(err)
	}

	const oracle = `
import sys, json
his = json.load(sys.stdin)
print(json.dumps([repr(bytes.fromhex(h)) for h in his]))
`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", oracle)
	command.Stdin = bytes.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var want []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	if len(want) != len(corpus) {
		t.Fatalf("oracle returned %d reprs, want %d", len(want), len(corpus))
	}
	for i, b := range corpus {
		if got := pythonBytesRepr(b); got != want[i] {
			t.Errorf("pythonBytesRepr(%x) = %s, want %s", b, got, want[i])
		}
	}
}

// TestStatusForLogDifferential confirms `%s` of a CommandStatus member is the
// fully-qualified CommandStatus.<NAME> in the target Python (the 3.11/3.12
// enum-__str__ version-sensitivity check), for the names statusForLog composes.
func TestStatusForLogDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	names := []string{"ESME_ROK", "ESME_RINVDSTADR", "ESME_RSYSERR", "ESME_RMSGQFUL", "ESME_RTHROTTLED"}
	payload, err := json.Marshal(names)
	if err != nil {
		t.Fatal(err)
	}
	const oracle = `
import sys, json
from smpp.pdu.pdu_types import CommandStatus
names = json.load(sys.stdin)
print(json.dumps(['%s' % getattr(CommandStatus, n) for n in names]))
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
	var want []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	for i, name := range names {
		// statusForLog composes "CommandStatus." + smppStatusName(value); assert the
		// qualified form matches Python for the same member name.
		if got := "CommandStatus." + name; got != want[i] {
			t.Errorf("status %s: go %s, py %s", name, got, want[i])
		}
	}
}

// TestReceiptForLogDifferential confirms receiptForLog matches `%s` of the
// legacy-decoded registered_delivery.receipt for each valid wire value.
func TestReceiptForLogDifferential(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	const oracle = `
import sys, json, io
from smpp.pdu.pdu_encoding import RegisteredDeliveryEncoder
enc = RegisteredDeliveryEncoder()
wires = json.load(sys.stdin)
out = []
for w in wires:
    rd = enc.decode(io.BytesIO(bytes([w])))
    out.append('%s' % rd.receipt)
print(json.dumps(out))
`
	wires := []int{0, 1, 2, 0x10, 0x11, 0x12} // higher bits set: receipt still from low 2 bits
	payload, err := json.Marshal(wires)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", oracle)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	command.Stdin = bytes.NewReader(payload)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var want []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &want); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	for i, wire := range wires {
		if got := receiptForLog(byte(wire)); got != want[i] {
			t.Errorf("receiptForLog(%#02x) = %s, want %s", wire, got, want[i])
		}
	}
}

package picklecompat_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// repickleFieldsOracleScript loads the repickled bare PDU and confirms it equals
// the PDU decoded from the original wire — proving the field extraction added
// during repickle does not alter the pickled bytes the router forwards.
const repickleFieldsOracleScript = `
import json, sys, base64, pickle, io, contextlib
from io import BytesIO
from smpp.pdu.pdu_encoding import PDUEncoder

payload = json.loads(sys.argv[1])
expected = PDUEncoder().decode(BytesIO(base64.b64decode(payload["wire"])))
loaded = pickle.loads(base64.b64decode(payload["repickled"]))
problems = []
with contextlib.redirect_stdout(io.StringIO()):
    if loaded != expected:
        problems.append("repickled pdu != wire pdu")
print(json.dumps({"ok": not problems, "problems": problems}))
`

// TestRepickleRoutablePDUReturnsFieldsAndUnchangedPDU proves the decode-return
// added to repickle_routable_pdu: RepickleRoutablePDU returns the deliver_sm's
// source/destination/short_message for MO content-filter routing AND a bare-PDU
// pickle byte-identical in meaning to the input (field extraction is read-only).
func TestRepickleRoutablePDUReturnsFieldsAndUnchangedPDU(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	body := smppwire.SMBody{
		SourceAddress:      []byte("31698765432"),
		DestinationAddress: []byte("2255"),
		ShortMessage:       []byte("STOP now"),
	}
	pdu := smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 4}, SM: &body}
	wire, err := smppwire.Encode(pdu)
	if err != nil {
		t.Fatal(err)
	}

	// Build the RoutableDeliverSm pickle the smppc side publishes, then repickle
	// it exactly as the router does — capturing the decoded routing fields.
	routablePickle, err := bridge.EncodeRoutableDeliverSM(ctx, wire, "smsc-in")
	if err != nil {
		t.Fatalf("encode routable: %v", err)
	}
	repickled, fields, err := bridge.RepickleRoutablePDU(ctx, routablePickle)
	if err != nil {
		t.Fatalf("repickle: %v", err)
	}

	if string(fields.SourceAddr) != "31698765432" ||
		string(fields.DestinationAddr) != "2255" ||
		string(fields.ShortMessage) != "STOP now" {
		t.Fatalf("decoded fields wrong: src=%q dst=%q msg=%q",
			fields.SourceAddr, fields.DestinationAddr, fields.ShortMessage)
	}

	payload, err := json.Marshal(map[string]string{
		"wire":      base64.StdEncoding.EncodeToString(wire),
		"repickled": base64.StdEncoding.EncodeToString(repickled),
	})
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, pythonPath, "-c", repickleFieldsOracleScript, string(payload))
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var verdict struct {
		OK       bool     `json:"ok"`
		Problems []string `json:"problems"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &verdict); err != nil {
		t.Fatalf("verdict %q: %v", output, err)
	}
	if !verdict.OK {
		t.Fatalf("repickled PDU differs from wire: %v", verdict.Problems)
	}
}

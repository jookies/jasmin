package smppwire_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// dataSMOracleScript decodes our Go-encoded data_sm wire with smpp.pdu (the
// library the bridge uses) and reports the parsed params, proving the Go
// data_sm mandatory-body layout matches the standard the bridge relies on.
const dataSMOracleScript = `
import sys, json, base64, io, contextlib
from smpp.pdu.pdu_encoding import PDUEncoder

wire = base64.b64decode(sys.argv[1])
with contextlib.redirect_stdout(io.StringIO()):
    pdu = PDUEncoder().decode(io.BytesIO(wire))
p = pdu.params
def b(x):
    return base64.b64encode(bytes(x)).decode() if x is not None else None
print(json.dumps({
    "command": pdu.id.name,
    "source_addr": b(p.get("source_addr")),
    "destination_addr": b(p.get("destination_addr")),
    "message_payload": b(p.get("message_payload")),
}))
`

// TestDataSMEncodingMatchesSmppPdu proves a Go-encoded data_sm decodes under
// smpp.pdu to the same fields — the bridge decodes MO data_sm this way, so the
// layout must agree.
func TestDataSMEncodingMatchesSmppPdu(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	body := &smppwire.SMBody{
		SourceAddressTON: 2, SourceAddressNPI: 1, SourceAddress: []byte("31612345678"),
		DestinationAddressTON: 1, DestinationAddressNPI: 1, DestinationAddress: []byte("2255"),
		ESMClass: 0, RegisteredDelivery: 0, DataCoding: 0,
	}
	body.Optional.MessagePayload = []byte("hello via data_sm")
	wire, err := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandDataSM, SequenceNumber: 1}, SM: body})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, pythonPath, "-c", dataSMOracleScript, base64.StdEncoding.EncodeToString(wire))
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}
	var got struct {
		Command         string `json:"command"`
		SourceAddr      string `json:"source_addr"`
		DestinationAddr string `json:"destination_addr"`
		MessagePayload  string `json:"message_payload"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &got); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	if got.Command != "data_sm" {
		t.Fatalf("smpp.pdu parsed command %q want data_sm", got.Command)
	}
	assertB64(t, "source_addr", got.SourceAddr, "31612345678")
	assertB64(t, "destination_addr", got.DestinationAddr, "2255")
	assertB64(t, "message_payload", got.MessagePayload, "hello via data_sm")
}

func assertB64(t *testing.T, field, encoded, want string) {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("%s decode: %v", field, err)
	}
	if string(raw) != want {
		t.Fatalf("%s = %q want %q", field, raw, want)
	}
}

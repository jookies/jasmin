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
)

// frontDoorOracleScript builds submit_sm PDUs exactly as the HTTP API front door
// does — via SMPPOperationFactory with data_coding passed as a plain int (see
// jasmin/protocols/http/endpoints/send.py) — and pickles them at protocol 2, the
// shape that reaches the SMPPc bridge over AMQP. For each it also computes the
// wire data_coding byte the legacy client would send, replicating
// SMPPClientProtocol.preSubmitSm: a known int becomes its DataCoding object, an
// unknown int becomes None (wire 0x00).
const frontDoorOracleScript = `
import json, sys, pickle, base64
from jasmin.protocols.smpp.operations import SMPPOperationFactory
from smpp.pdu.constants import data_coding_default_value_map
from smpp.pdu.pdu_types import DataCoding, DataCodingDefault
from smpp.pdu.pdu_encoding import DataCodingEncoder

factory = SMPPOperationFactory()
cases = []
for iv in [0, 1, 3, 8, 12, 15, 245]:
    pdu = factory.SubmitSM(source_addr=b'1111', destination_addr=b'2222',
                           short_message=b'hello', data_coding=iv)
    b64 = base64.b64encode(pickle.dumps(pdu, protocol=2)).decode()
    if iv in data_coding_default_value_map:
        dc = DataCoding(schemeData=getattr(DataCodingDefault, data_coding_default_value_map[iv]))
        expected = DataCodingEncoder().encode(dc)[0]
    else:
        expected = 0
    cases.append({"dc": iv, "pickle": b64, "expected": expected})
print(json.dumps(cases))
`

// TestDecodeSubmitSMFrontDoorDataCoding proves GAP 1: a submit_sm pickled by the
// legacy front door carries data_coding as a plain int, and the bridge must
// convert it to the same wire byte the legacy client's preSubmitSm would send.
// Before the fix the bridge fed the int straight to DataCodingEncoder, which
// raised (AttributeError: 'int' object has no attribute 'scheme') and poisoned
// every HTTP-origin message on the legacy-publisher -> Go-smppc edge.
func TestDecodeSubmitSMFrontDoorDataCoding(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, pythonPath, "-c", frontDoorOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var cases []struct {
		DC       int    `json:"dc"`
		Pickle   string `json:"pickle"`
		Expected int    `json:"expected"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &cases); err != nil {
		t.Fatalf("oracle output %q: %v", output, err)
	}
	if len(cases) == 0 {
		t.Fatal("oracle produced no cases")
	}

	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	for _, testCase := range cases {
		pickled, err := base64.StdEncoding.DecodeString(testCase.Pickle)
		if err != nil {
			t.Fatalf("data_coding=%d: bad pickle: %v", testCase.DC, err)
		}
		body, _, err := bridge.DecodeSubmitSM(ctx, pickled)
		if err != nil {
			t.Errorf("data_coding=%d: decode failed (GAP 1): %v", testCase.DC, err)
			continue
		}
		if int(body.DataCoding) != testCase.Expected {
			t.Errorf("data_coding=%d: wire byte go=%d oracle=%d", testCase.DC, body.DataCoding, testCase.Expected)
		}
		// Sanity: the rest of the front-door PDU projects intact.
		if string(body.SourceAddress) != "1111" || string(body.DestinationAddress) != "2222" ||
			string(body.ShortMessage) != "hello" {
			t.Errorf("data_coding=%d: body fields diverge: %+v", testCase.DC, body)
		}
	}
}

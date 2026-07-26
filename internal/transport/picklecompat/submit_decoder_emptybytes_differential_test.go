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

// emptyBytesOracleScript pickles submit_sm PDUs whose byte params are empty.
// CPython pickles b'' at protocol 2 as a __builtin__.bytes() call (non-empty
// bytes go through _codecs.encode), so these are the exact pickles the Go
// front door publishes for a /send without a source address. Only smpp.pdu is
// imported, so the pinned bridge runtime (compat/requirements-pickle-bridge.txt)
// suffices; params are set explicitly after construction so falsy values
// cannot be dropped by constructor defaults.
const emptyBytesOracleScript = `
import json, base64, pickle
from smpp.pdu.operations import SubmitSM

cases = []

p = SubmitSM(seqNum=1, source_addr=b'x', destination_addr=b'15551234567', short_message=b'hello')
p.params['source_addr'] = b''
cases.append({"name": "empty_source_addr", "pickle": base64.b64encode(pickle.dumps(p, 2)).decode(),
              "source": "", "dest": "15551234567", "message": "hello"})

p = SubmitSM(seqNum=2, source_addr=b'1111', destination_addr=b'2222', short_message=b'x')
p.params['short_message'] = b''
cases.append({"name": "empty_short_message", "pickle": base64.b64encode(pickle.dumps(p, 2)).decode(),
              "source": "1111", "dest": "2222", "message": ""})

p = SubmitSM(seqNum=3, source_addr=b'1111', destination_addr=b'2222', short_message=b'hi')
cases.append({"name": "non_empty_guard", "pickle": base64.b64encode(pickle.dumps(p, 2)).decode(),
              "source": "1111", "dest": "2222", "message": "hi"})

print(json.dumps(cases))
`

// TestDecodeSubmitSMEmptyBytes proves the empty-bytes poison gap: a submit_sm
// with any empty byte param (source_addr b'' is the no-"from" HTTP send, the
// common shape) pickles a __builtin__.bytes global that SubmitSMUnpickler
// rejected ("forbidden global") while DeliverSMUnpickler allowlisted it — so
// the connector silently dropped every such submit (poison => reject without
// requeue, no attempt row, no log). Python's stock Unpickler accepts it, so
// the legacy listener never had this hole.
func TestDecodeSubmitSMEmptyBytes(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, pythonPath, "-c", emptyBytesOracleScript)
	command.Env = append(os.Environ(), "PYTHONPATH=../../..")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("oracle: %v (%s)", err, output)
	}
	var cases []struct {
		Name    string `json:"name"`
		Pickle  string `json:"pickle"`
		Source  string `json:"source"`
		Dest    string `json:"dest"`
		Message string `json:"message"`
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
			t.Fatalf("%s: bad pickle: %v", testCase.Name, err)
		}
		body, _, err := bridge.DecodeSubmitSM(ctx, pickled)
		if err != nil {
			t.Errorf("%s: decode failed (empty-bytes poison gap): %v", testCase.Name, err)
			continue
		}
		if string(body.SourceAddress) != testCase.Source {
			t.Errorf("%s: source_addr go=%q oracle=%q", testCase.Name, body.SourceAddress, testCase.Source)
		}
		if string(body.DestinationAddress) != testCase.Dest {
			t.Errorf("%s: destination_addr go=%q oracle=%q", testCase.Name, body.DestinationAddress, testCase.Dest)
		}
		if string(body.ShortMessage) != testCase.Message {
			t.Errorf("%s: short_message go=%q oracle=%q", testCase.Name, body.ShortMessage, testCase.Message)
		}
	}
}

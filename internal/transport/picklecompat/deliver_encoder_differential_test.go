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

// deliverRoutableOracleScript validates a Go-bridge-produced RoutableDeliverSm
// pickle against the legacy construction for the same wire frame. The
// routable's datetime field is stamped at construction (datetime.now()), so
// the comparison is semantic — the unpickled object must equal the one the
// legacy listener would build — rather than byte-exact.
const deliverRoutableOracleScript = `
import json, sys, base64, pickle, io, contextlib
from io import BytesIO
from smpp.pdu.pdu_encoding import PDUEncoder
from datetime import datetime
from jasmin.routing.Routables import RoutableDeliverSm
from jasmin.routing.jasminApi import Connector

payload = json.loads(sys.argv[1])
wire = base64.b64decode(payload["wire"])
pickled = base64.b64decode(payload["pickled"])
cid = payload["cid"]

expected_pdu = PDUEncoder().decode(BytesIO(wire))
loaded = pickle.loads(pickled)

# smpp.pdu's PDU.__eq__ prints params to stdout; keep the verdict JSON clean.
problems = []
with contextlib.redirect_stdout(io.StringIO()):
    if type(loaded).__name__ != "RoutableDeliverSm":
        problems.append("type=%s" % type(loaded).__name__)
    if loaded.connector.cid != cid:
        problems.append("cid=%r" % loaded.connector.cid)
    if not isinstance(loaded.datetime, datetime):
        problems.append("datetime type=%s" % type(loaded.datetime).__name__)
    if loaded.pdu != expected_pdu:
        problems.append("pdu mismatch: loaded=%r expected=%r" % (loaded.pdu, expected_pdu))
    reference = RoutableDeliverSm(expected_pdu, Connector(cid))
    if loaded.pdu != reference.pdu or loaded.connector.cid != reference.connector.cid:
        problems.append("differs from reference routable")
print(json.dumps({"ok": not problems, "problems": problems}))
`

// TestEncodeRoutableDeliverSMDifferential proves the MO-ingress pickle: the
// bridge output unpickles (under the stock loader the legacy router uses) to
// a RoutableDeliverSm equal to the legacy listener's own construction.
func TestEncodeRoutableDeliverSMDifferential(t *testing.T) {
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

	cases := []struct {
		name string
		body smppwire.SMBody
	}{
		{"plain_mo", smppwire.SMBody{
			SourceAddress: []byte("31612345678"), DestinationAddress: []byte("2222"),
			ShortMessage: []byte("hello from the SMSC"),
		}},
		{"binary_content_udh_single", smppwire.SMBody{
			SourceAddress: []byte("1111"), DestinationAddress: []byte("2222"),
			ESMClass: 0x40, DataCoding: 0x04,
			ShortMessage: append([]byte{0x05, 0x00, 0x04, 0x0B, 0x84, 0x23}, []byte("payload")...),
		}},
		{"empty_source", smppwire.SMBody{
			DestinationAddress: []byte("15551234567"), ShortMessage: []byte("anon"),
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			body := testCase.body
			pdu := smppwire.PDU{
				Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 3},
				SM:     &body,
			}
			wire, err := smppwire.Encode(pdu)
			if err != nil {
				t.Fatal(err)
			}
			pickled, err := bridge.EncodeRoutableDeliverSM(ctx, wire, "oracle-cid")
			if err != nil {
				t.Fatalf("bridge encode: %v", err)
			}
			payload, err := json.Marshal(map[string]string{
				"wire":    base64.StdEncoding.EncodeToString(wire),
				"pickled": base64.StdEncoding.EncodeToString(pickled),
				"cid":     "oracle-cid",
			})
			if err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, pythonPath, "-c", deliverRoutableOracleScript, string(payload))
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			output, err := command.Output()
			if err != nil {
				var exitError *exec.ExitError
				if ok := asExitError(err, &exitError); ok {
					t.Fatalf("oracle: %v (%s)", err, exitError.Stderr)
				}
				t.Fatalf("oracle: %v", err)
			}
			var verdict struct {
				OK       bool     `json:"ok"`
				Problems []string `json:"problems"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(output), &verdict); err != nil {
				t.Fatalf("oracle output %q: %v", output, err)
			}
			if !verdict.OK {
				t.Fatalf("legacy inequality: %v", verdict.Problems)
			}
		})
	}
}

func asExitError(err error, target *(*exec.ExitError)) bool {
	if exitError, ok := err.(*exec.ExitError); ok {
		*target = exitError
		return true
	}
	return false
}

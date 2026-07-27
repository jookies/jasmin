package picklecompat_test

import (
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

// deliverEncodeOracleScript loads the native RoutableDeliverSm pickle and checks
// it equals the legacy construction — RoutableDeliverSm(PDUEncoder().decode(wire),
// Connector(cid)) — pdu-equal, same connector cid, and a real datetime.
const deliverEncodeOracleScript = `
import sys, json, base64, pickle, io, contextlib
from io import BytesIO
from datetime import datetime
from smpp.pdu.pdu_encoding import PDUEncoder
from jasmin.routing.Routables import RoutableDeliverSm
from jasmin.routing.jasminApi import Connector
payload = json.loads(sys.argv[1])
wire = base64.b64decode(payload["wire"]); cid = payload["cid"]
loaded = pickle.loads(base64.b64decode(payload["pickled"]))
expected = PDUEncoder().decode(BytesIO(wire))
problems = []
with contextlib.redirect_stdout(io.StringIO()):
    if type(loaded).__name__ != "RoutableDeliverSm":
        problems.append("type=%s" % type(loaded).__name__)
    if loaded.connector.cid != cid:
        problems.append("cid=%r" % loaded.connector.cid)
    if not isinstance(loaded.datetime, datetime):
        problems.append("datetime type=%s" % type(loaded.datetime).__name__)
    if loaded.pdu != expected:
        problems.append("pdu mismatch: loaded=%r expected=%r" % (loaded.pdu, expected))
print(json.dumps({"ok": not problems, "problems": problems}))
`

// TestNativeEncodeRoutableDeliverSMMatchesLegacy proves the native routable pickle
// reconstructs to the same RoutableDeliverSm the legacy MO listener would build.
func TestNativeEncodeRoutableDeliverSMMatchesLegacy(t *testing.T) {
	python := os.Getenv("PYTHON_PATH")
	if python == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	native := picklecompat.NewNativeCodec()

	cases := map[string]struct {
		id   uint32
		body smppwire.SMBody
	}{
		"plain deliver_sm": {smppwire.CommandDeliverSM, smppwire.SMBody{
			SourceAddress: []byte("31612345678"), DestinationAddress: []byte("2255"), ShortMessage: []byte("hello mo"),
			SourceAddressTON: 2, SourceAddressNPI: 1, DestinationAddressTON: 1, DestinationAddressNPI: 1,
		}},
		"udhi + ucs2": {smppwire.CommandDeliverSM, smppwire.SMBody{
			SourceAddress: []byte("1111"), DestinationAddress: []byte("2222"), ESMClass: 0x40, DataCoding: 8,
			ShortMessage: append([]byte{0x05, 0x00, 0x03, 0x2A, 0x02, 0x01}, []byte("part")...),
		}},
		"empty source": {smppwire.CommandDeliverSM, smppwire.SMBody{
			DestinationAddress: []byte("15551234567"), ShortMessage: []byte("anon"),
		}},
		"data_sm payload": {smppwire.CommandDataSM, smppwire.SMBody{
			SourceAddress: []byte("2255"), DestinationAddress: []byte("31600000000"),
			Optional: smppwire.OptionalParameters{MessagePayload: []byte("via data_sm")},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body := tc.body
			wire, err := smppwire.Encode(smppwire.PDU{
				Header: smppwire.Header{CommandID: tc.id, SequenceNumber: 5}, SM: &body,
			})
			if err != nil {
				t.Fatal(err)
			}
			pickled, err := native.EncodeRoutableDeliverSM(ctx, wire, "smsc-in")
			if err != nil {
				t.Fatalf("native encode: %v", err)
			}
			payload, _ := json.Marshal(map[string]string{
				"wire": base64.StdEncoding.EncodeToString(wire), "cid": "smsc-in",
				"pickled": base64.StdEncoding.EncodeToString(pickled),
			})
			command := exec.CommandContext(ctx, python, "-c", deliverEncodeOracleScript, string(payload))
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			out, err := command.Output()
			if err != nil {
				t.Fatalf("oracle: %v (%s)", err, out)
			}
			var verdict struct {
				OK       bool     `json:"ok"`
				Problems []string `json:"problems"`
			}
			if err := json.Unmarshal(out, &verdict); err != nil {
				t.Fatalf("verdict %q: %v", out, err)
			}
			if !verdict.OK {
				t.Fatalf("%v", verdict.Problems)
			}
		})
	}
}

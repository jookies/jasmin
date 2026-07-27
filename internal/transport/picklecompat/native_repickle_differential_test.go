package picklecompat_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// fieldsEqual compares routing fields semantically (nil == empty), so a Go
// slice-nilness artifact between the two decoders is not a false mismatch.
func fieldsEqual(a, b picklecompat.RoutableFields) bool {
	return bytes.Equal(a.SourceAddr, b.SourceAddr) &&
		bytes.Equal(a.DestinationAddr, b.DestinationAddr) &&
		bytes.Equal(a.ShortMessage, b.ShortMessage) &&
		slices.Equal(a.Tags, b.Tags)
}

const repicklePDUCompareScript = `
import sys, base64, pickle, io, contextlib
def load(b): return pickle.loads(base64.b64decode(b))
native = load(sys.argv[1]); bridge = load(sys.argv[2])
with contextlib.redirect_stdout(io.StringIO()):
    equal = native == bridge
print("ok" if equal else "MISMATCH native=%r bridge=%r" % (native, bridge))
`

// TestNativeRepickleRoutablePDUMatchesBridge proves the native repickle returns
// the same routing fields as the bridge and a bare-PDU pickle that loads to the
// same PDU, on RoutableDeliverSm pickles the bridge produced.
func TestNativeRepickleRoutablePDUMatchesBridge(t *testing.T) {
	python := os.Getenv("PYTHON_PATH")
	if python == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	bridge, err := picklecompat.NewBridge(ctx, python)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	native := picklecompat.NewNativeCodec()

	cases := map[string]smppwire.SMBody{
		"plain": {SourceAddress: []byte("31612345678"), DestinationAddress: []byte("2255"), ShortMessage: []byte("hello mo")},
		"udh": {SourceAddress: []byte("1111"), DestinationAddress: []byte("2222"), ESMClass: 0x40, DataCoding: 0x04,
			ShortMessage: append([]byte{0x05, 0x00, 0x03, 0x2A, 0x02, 0x01}, []byte("part")...)},
		"empty source": {DestinationAddress: []byte("15551234567"), ShortMessage: []byte("anon")},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			body := body
			wire, err := smppwire.Encode(smppwire.PDU{
				Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 3}, SM: &body,
			})
			if err != nil {
				t.Fatal(err)
			}
			routablePickle, err := bridge.EncodeRoutableDeliverSM(ctx, wire, "smsc-in")
			if err != nil {
				t.Fatalf("encode routable: %v", err)
			}

			nativePDU, nativeFields, err := native.RepickleRoutablePDU(ctx, routablePickle)
			if err != nil {
				t.Fatalf("native repickle: %v", err)
			}
			bridgePDU, bridgeFields, err := bridge.RepickleRoutablePDU(ctx, routablePickle)
			if err != nil {
				t.Fatalf("bridge repickle: %v", err)
			}
			if !fieldsEqual(nativeFields, bridgeFields) {
				t.Fatalf("fields differ:\n  native %+v\n  bridge %+v", nativeFields, bridgeFields)
			}
			command := exec.CommandContext(ctx, python, "-c", repicklePDUCompareScript,
				base64.StdEncoding.EncodeToString(nativePDU),
				base64.StdEncoding.EncodeToString(bridgePDU))
			command.Env = append(os.Environ(), "PYTHONPATH=../../..")
			out, err := command.Output()
			if err != nil {
				t.Fatalf("compare: %v (%s)", err, out)
			}
			if got := strings.TrimSpace(string(out)); got != "ok" {
				t.Fatalf("%s", got)
			}
		})
	}
}

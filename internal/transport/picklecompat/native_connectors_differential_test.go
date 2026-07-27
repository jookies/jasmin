package picklecompat_test

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// connectorCompareScript loads the native and bridge connector-list pickles and
// prints "ok" iff every connector's full field tuple (class, cid, baseurl,
// method, system_id, _str, _repr) matches — the fields the MO thrower and any
// legacy consumer read.
const connectorCompareScript = `
import sys, base64, pickle
def load(b64):
    return pickle.loads(base64.b64decode(b64))
def fields(lst):
    out = []
    for c in lst:
        out.append((type(c).__name__, c.cid, getattr(c, "baseurl", None),
                    getattr(c, "method", None), getattr(c, "system_id", None),
                    c._str, c._repr))
    return out
native = fields(load(sys.argv[1]))
bridge = fields(load(sys.argv[2]))
if native == bridge:
    print("ok")
else:
    print("MISMATCH\nnative=%r\nbridge=%r" % (native, bridge))
`

// TestNativeConnectorListMatchesBridge proves the native EncodeConnectorList
// output reconstructs connectors field-identical to the Python bridge's output.
func TestNativeConnectorListMatchesBridge(t *testing.T) {
	python := os.Getenv("PYTHON_PATH")
	if python == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	bridge, err := picklecompat.NewBridge(ctx, python)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	native := picklecompat.NewNativeCodec()

	cases := [][]picklecompat.MOConnectorSpec{
		{{Type: "http", CID: "conn1", URL: "http://example.com/mo", Method: "GET"}},
		{{Type: "http", CID: "cid-2", URL: "http://host.example.org/inbound", Method: "POST"}},
		{{Type: "http", CID: "defmethod", URL: "http://1.2.3.4/mo"}}, // method defaults to GET
		{{Type: "smpps", SystemID: "sys1"}},
		{{Type: "http", CID: "aconn", URL: "http://a.example.com/m", Method: "GET"}, {Type: "smpps", SystemID: "bsys"}},
	}
	for i, specs := range cases {
		nativePickle, err := native.EncodeConnectorList(ctx, specs)
		if err != nil {
			t.Fatalf("case %d native: %v", i, err)
		}
		bridgePickle, err := bridge.EncodeConnectorList(ctx, specs)
		if err != nil {
			t.Fatalf("case %d bridge: %v", i, err)
		}
		command := exec.CommandContext(ctx, python, "-c", connectorCompareScript,
			base64.StdEncoding.EncodeToString(nativePickle),
			base64.StdEncoding.EncodeToString(bridgePickle))
		command.Env = append(os.Environ(), "PYTHONPATH=../../..")
		out, err := command.Output()
		if err != nil {
			t.Fatalf("case %d compare: %v (%s)", i, err, out)
		}
		if got := strings.TrimSpace(string(out)); got != "ok" {
			t.Fatalf("case %d: %s", i, got)
		}
	}
}

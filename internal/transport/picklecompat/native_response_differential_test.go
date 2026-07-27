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

const responseCompareScript = `
import sys, base64, pickle
def load(b): return pickle.loads(base64.b64decode(b))
def fields(p): return (p.id.name, p.seqNum, p.status.name, p.params.get("message_id"))
native = fields(load(sys.argv[1]))
bridge = fields(load(sys.argv[2]))
print("ok" if native == bridge else "MISMATCH\nnative=%r\nbridge=%r" % (native, bridge))
`

// TestNativeSubmitSMResponseMatchesBridge proves the native SubmitSMResp
// reconstructs to the same id/seqNum/status/message_id as the bridge, on
// ESME_ROK (message_id present) and error statuses (message_id None).
func TestNativeSubmitSMResponseMatchesBridge(t *testing.T) {
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

	cases := []struct {
		name      string
		status    uint32
		sequence  uint32
		messageID []byte
	}{
		{"rok with id", 0x0, 1, []byte("MSG-123")},
		{"rok empty id", 0x0, 42, []byte("")},
		{"rok high seq", 0x0, 0x7fffffff, []byte("abcDEF987")},
		{"error invsrcadr", 0xa, 7, []byte("ignored-on-error")},
		{"error syserr", 0x8, 100, nil},
		{"error unknown", 0xff, 5, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nativePickle, err := native.EncodeSubmitSMResponse(ctx, tc.status, tc.sequence, tc.messageID)
			if err != nil {
				t.Fatalf("native: %v", err)
			}
			bridgePickle, err := bridge.EncodeSubmitSMResponse(ctx, tc.status, tc.sequence, tc.messageID)
			if err != nil {
				t.Fatalf("bridge: %v", err)
			}
			command := exec.CommandContext(ctx, python, "-c", responseCompareScript,
				base64.StdEncoding.EncodeToString(nativePickle),
				base64.StdEncoding.EncodeToString(bridgePickle))
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

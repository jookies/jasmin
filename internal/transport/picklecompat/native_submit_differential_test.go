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

const submitCompareScript = `
import sys, base64, pickle, io, contextlib
def load(b): return pickle.loads(base64.b64decode(b))
native = load(sys.argv[1]); bridge = load(sys.argv[2])
def fields(p): return (p.id, p.seqNum, p.status, p.custom_tlvs, p.params)
with contextlib.redirect_stdout(io.StringIO()):
    equal = fields(native) == fields(bridge)
if equal:
    print("ok")
else:
    print("MISMATCH\nnative.params=%r\nbridge.params=%r\nnative.hdr=%r\nbridge.hdr=%r" % (
        native.params, bridge.params,
        (native.id, native.seqNum, native.status, native.custom_tlvs),
        (bridge.id, bridge.seqNum, bridge.status, bridge.custom_tlvs)))
`

// TestNativeSubmitSMMatchesBridge proves the native EncodeSubmitSM body
// reconstructs to a SubmitSM equal to the bridge's (id/seqNum/status/custom_tlvs
// and every params key incl. the DataCoding/EsmClass/RegisteredDelivery/AddrTon
// objects) across a matrix of connector defaults and message shapes.
func TestNativeSubmitSMMatchesBridge(t *testing.T) {
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

	base := func() picklecompat.SubmitSMEncodeRequest {
		return picklecompat.SubmitSMEncodeRequest{
			Sequence: 1, SourceAddr: picklecompat.Bytes("1111"),
			DestinationAddr: picklecompat.Bytes("2222"), ShortMessage: picklecompat.Bytes("hi"),
			DataCoding: 0, Priority: 0,
			SourceAddrTON: 2, SourceAddrNPI: 1, DestAddrTON: 1, DestAddrNPI: 1,
			IncludeBill: false,
		}
	}
	cases := map[string]func(*picklecompat.SubmitSMEncodeRequest){
		"default 2/1/1/1":     func(r *picklecompat.SubmitSMEncodeRequest) {},
		"service_type":        func(r *picklecompat.SubmitSMEncodeRequest) { r.ServiceType = "CMT" },
		"non-default ton/npi": func(r *picklecompat.SubmitSMEncodeRequest) { r.SourceAddrTON = 5; r.DestAddrTON = 1; r.DestAddrNPI = 9 },
		"protocol + smdefault": func(r *picklecompat.SubmitSMEncodeRequest) {
			r.ProtocolID = 0x34
			r.SmDefaultMsgID = 5
		},
		"registered delivery": func(r *picklecompat.SubmitSMEncodeRequest) { r.RegisteredDelivery = true },
		"udh":                 func(r *picklecompat.SubmitSMEncodeRequest) { r.UDH = true },
		"ucs2":                func(r *picklecompat.SubmitSMEncodeRequest) { r.DataCoding = 8 },
		"priority":            func(r *picklecompat.SubmitSMEncodeRequest) { r.Priority = 2 },
		"replace":             func(r *picklecompat.SubmitSMEncodeRequest) { r.ReplaceIfPresentFlag = 1 },
		"sar segment": func(r *picklecompat.SubmitSMEncodeRequest) {
			r.UDH = true
			r.SAR = &picklecompat.SubmitSMSAR{Reference: 258, Total: 3, Sequence: 2}
		},
		"empty source": func(r *picklecompat.SubmitSMEncodeRequest) { r.SourceAddr = picklecompat.Bytes("") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := base()
			mutate(&request)
			nativeResult, err := native.EncodeSubmitSM(ctx, request)
			if err != nil {
				t.Fatalf("native: %v", err)
			}
			bridgeResult, err := bridge.EncodeSubmitSM(ctx, request)
			if err != nil {
				t.Fatalf("bridge: %v", err)
			}
			command := exec.CommandContext(ctx, python, "-c", submitCompareScript,
				base64.StdEncoding.EncodeToString(nativeResult.Body),
				base64.StdEncoding.EncodeToString(bridgeResult.Body))
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

package picklecompat_test

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// bodyTLV bundles a decoded submit body with its custom TLVs for comparison.
type bodyTLV struct {
	Body smppwire.SubmitSMBody
	TLVs []tlv.TLV
}

func decodeWith(fn func(context.Context, []byte) (smppwire.SubmitSMBody, []tlv.TLV, error)) func(context.Context, []byte) (bodyTLV, error) {
	return func(ctx context.Context, data []byte) (bodyTLV, error) {
		body, tlvs, err := fn(ctx, data)
		return bodyTLV{Body: body, TLVs: tlvs}, err
	}
}

// TestNativeDecodeSubmitSMMatchesBridge proves the native decode projects a
// pickled SubmitSM into the same wire body + TLVs as the bridge — on both
// bridge-produced and native-produced pickles, across the submit matrix. This
// closes the submit_sm round-trip end to end natively.
func TestNativeDecodeSubmitSMMatchesBridge(t *testing.T) {
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
			DestinationAddr: picklecompat.Bytes("2222"), ShortMessage: picklecompat.Bytes("hello"),
			SourceAddrTON: 2, SourceAddrNPI: 1, DestAddrTON: 1, DestAddrNPI: 1,
		}
	}
	cases := map[string]func(*picklecompat.SubmitSMEncodeRequest){
		"default":              func(r *picklecompat.SubmitSMEncodeRequest) {},
		"service_type":         func(r *picklecompat.SubmitSMEncodeRequest) { r.ServiceType = "CMT" },
		"non-default ton/npi":  func(r *picklecompat.SubmitSMEncodeRequest) { r.SourceAddrTON = 5; r.DestAddrNPI = 9 },
		"protocol + smdefault": func(r *picklecompat.SubmitSMEncodeRequest) { r.ProtocolID = 0x34; r.SmDefaultMsgID = 5 },
		"registered":           func(r *picklecompat.SubmitSMEncodeRequest) { r.RegisteredDelivery = true },
		"udh":                  func(r *picklecompat.SubmitSMEncodeRequest) { r.UDH = true },
		"ucs2":                 func(r *picklecompat.SubmitSMEncodeRequest) { r.DataCoding = 8 },
		"priority":             func(r *picklecompat.SubmitSMEncodeRequest) { r.Priority = 2 },
		"replace":              func(r *picklecompat.SubmitSMEncodeRequest) { r.ReplaceIfPresentFlag = 1 },
		"sar": func(r *picklecompat.SubmitSMEncodeRequest) {
			r.UDH = true
			r.SAR = &picklecompat.SubmitSMSAR{Reference: 258, Total: 3, Sequence: 2}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			request := base()
			mutate(&request)

			bridgePickle, err := bridge.EncodeSubmitSM(ctx, request)
			if err != nil {
				t.Fatalf("bridge encode: %v", err)
			}
			nativePickle, err := native.EncodeSubmitSM(ctx, request)
			if err != nil {
				t.Fatalf("native encode: %v", err)
			}

			// Decode both pickles through both decoders; all four wire bodies must
			// be identical.
			results := map[string]struct {
				pickle  []byte
				decoder func(context.Context, []byte) (bodyTLV, error)
			}{
				"native<-bridge": {bridgePickle.Body, decodeWith(native.DecodeSubmitSM)},
				"bridge<-bridge": {bridgePickle.Body, decodeWith(bridge.DecodeSubmitSM)},
				"native<-native": {nativePickle.Body, decodeWith(native.DecodeSubmitSM)},
				"bridge<-native": {nativePickle.Body, decodeWith(bridge.DecodeSubmitSM)},
			}
			var reference *bodyTLV
			var refName string
			for label, spec := range results {
				got, err := spec.decoder(ctx, spec.pickle)
				if err != nil {
					t.Fatalf("%s decode: %v", label, err)
				}
				if reference == nil {
					got := got
					reference = &got
					refName = label
					continue
				}
				if !reflect.DeepEqual(got, *reference) {
					t.Fatalf("%s wire body != %s:\n  %+v\n  %+v", label, refName, got, *reference)
				}
			}
		})
	}
}

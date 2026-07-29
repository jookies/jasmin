package picklecompat_test

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// bodyWire re-encodes a decoded body to wire so two bodies compare semantically
// (nil == empty slice), avoiding a false DeepEqual mismatch on slice-nilness.
func bodyWire(t *testing.T, body smppwire.SMBody) []byte {
	t.Helper()
	wire, err := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 1}, SM: &body,
	})
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return wire
}

// TestNativeDecodeRoutedDeliverSMMatchesBridge proves the native routed-content
// projection (connectors + deliver body + optionals) equals the bridge's, on
// content produced by the native encode+repickle path.
func TestNativeDecodeRoutedDeliverSMMatchesBridge(t *testing.T) {
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

	connectorSpecs := []picklecompat.MOConnectorSpec{
		{Type: "http", CID: "mo-http", URL: "http://example.com/mo", Method: "GET"},
		{Type: "smpps", SystemID: "app-sys"},
	}
	connectors, err := native.EncodeConnectorList(ctx, connectorSpecs)
	if err != nil {
		t.Fatal(err)
	}
	userMessageReference := uint16(513)

	cases := map[string]struct {
		id   uint32
		body smppwire.SMBody
	}{
		"plain deliver": {smppwire.CommandDeliverSM, smppwire.SMBody{
			SourceAddress: []byte("31612345678"), DestinationAddress: []byte("2255"), ShortMessage: []byte("hello mo"),
			SourceAddressTON: 2, SourceAddressNPI: 1, DestinationAddressTON: 1, DestinationAddressNPI: 1,
		}},
		"data_sm payload": {smppwire.CommandDataSM, smppwire.SMBody{
			SourceAddress: []byte("2255"), DestinationAddress: []byte("31600000000"),
			Optional: smppwire.OptionalParameters{MessagePayload: []byte("via data_sm")},
		}},
		"standard and vendor TLVs": {smppwire.CommandDeliverSM, smppwire.SMBody{
			SourceAddress: []byte("1111"), DestinationAddress: []byte("2222"), ShortMessage: []byte("hello"),
			Optional: smppwire.OptionalParameters{UserMessageReference: &userMessageReference},
			CapturedVendorTLVs: []smppwire.CapturedVendorTLV{
				{Tag: 0x1401, Value: []byte{0x01, 0x02}},
				{Tag: 0x1401, Value: []byte{0xff}},
			},
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body := tc.body
			wire, err := smppwire.Encode(smppwire.PDU{
				Header: smppwire.Header{CommandID: tc.id, SequenceNumber: 4}, SM: &body,
			})
			if err != nil {
				t.Fatal(err)
			}
			routable, err := native.EncodeRoutableDeliverSM(ctx, wire, "smsc-in")
			if err != nil {
				t.Fatalf("encode routable: %v", err)
			}
			barePDU, _, err := native.RepickleRoutablePDU(ctx, routable)
			if err != nil {
				t.Fatalf("repickle: %v", err)
			}

			nativeResult, err := native.DecodeRoutedDeliverSM(ctx, connectors, barePDU)
			if err != nil {
				t.Fatalf("native decode: %v", err)
			}
			bridgeResult, err := bridge.DecodeRoutedDeliverSM(ctx, connectors, barePDU)
			if err != nil {
				t.Fatalf("bridge decode: %v", err)
			}
			if !reflect.DeepEqual(nativeResult.Connectors, bridgeResult.Connectors) {
				t.Fatalf("connectors differ:\n  native %+v\n  bridge %+v", nativeResult.Connectors, bridgeResult.Connectors)
			}
			if !bytes.Equal(bodyWire(t, nativeResult.Body), bodyWire(t, bridgeResult.Body)) {
				t.Fatalf("body differs:\n  native %+v\n  bridge %+v", nativeResult.Body, bridgeResult.Body)
			}
			if nativeResult.ValidityText != bridgeResult.ValidityText {
				t.Fatalf("validity differs: native %q bridge %q", nativeResult.ValidityText, bridgeResult.ValidityText)
			}
			if !reflect.DeepEqual(nativeResult.CustomTLVs, bridgeResult.CustomTLVs) {
				t.Fatalf("custom TLVs differ:\n  native %+v\n  bridge %+v", nativeResult.CustomTLVs, bridgeResult.CustomTLVs)
			}
		})
	}
}

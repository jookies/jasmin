package picklecompat_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// dcExpectedWire is the wire data_coding smpp.pdu round-trips a byte to:
// identity for DEFAULT/RAW and GSM 0xf0..0xf7, lossy (clear bit 3) for 0xf8..0xff.
func dcExpectedWire(b uint8) uint8 {
	if b >= 0xf8 {
		return b & 0xf7
	}
	return b
}

func dcRequest(b uint8) picklecompat.SubmitSMEncodeRequest {
	return picklecompat.SubmitSMEncodeRequest{
		Sequence: 1, SourceAddr: picklecompat.Bytes("1"), DestinationAddr: picklecompat.Bytes("2"),
		ShortMessage: picklecompat.Bytes("x"), SourceAddrTON: 2, SourceAddrNPI: 1, DestAddrTON: 1, DestAddrNPI: 1,
		DataCoding: b,
	}
}

// TestNativeDataCodingRoundTripAllBytes proves every data_coding byte (0-255) is
// encodable natively and native-decodes to the correct (lossy-matching) wire
// byte — no byte poisons, so flipping the default is safe for SMPPs-inbound
// submits that carry unconstrained data_coding.
func TestNativeDataCodingRoundTripAllBytes(t *testing.T) {
	ctx := context.Background()
	native := picklecompat.NewNativeCodec()
	for b := 0; b < 256; b++ {
		enc, err := native.EncodeSubmitSM(ctx, dcRequest(uint8(b)))
		if err != nil {
			t.Fatalf("byte %#02x native encode: %v", b, err)
		}
		body, _, err := native.DecodeSubmitSM(ctx, enc.Body)
		if err != nil {
			t.Fatalf("byte %#02x native decode: %v", b, err)
		}
		if got := body.DataCoding; got != dcExpectedWire(uint8(b)) {
			t.Fatalf("byte %#02x round-trip wire %#02x, want %#02x", b, got, dcExpectedWire(uint8(b)))
		}
	}
}

// TestNativeDataCodingMatchesBridge cross-checks native against the live bridge on
// one representative byte per DataCoding shape plus the lossy GSM edges: the
// native pickle is bridge-loadable (bridge decode == expected wire) and native
// decodes the bridge's own pickle to the same wire.
func TestNativeDataCodingMatchesBridge(t *testing.T) {
	python := os.Getenv("PYTHON_PATH")
	if python == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 110*time.Second)
	defer cancel()
	bridge, err := picklecompat.NewBridge(ctx, python)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	native := picklecompat.NewNativeCodec()

	for _, b := range []uint8{0x00, 0x08, 0x0b, 0x50, 0xc0, 0xf0, 0xf5, 0xf8, 0xff} {
		want := dcExpectedWire(b)
		// native pickle -> bridge decode (proves loadable + smpp.pdu re-encodes right)
		nativeEnc, err := native.EncodeSubmitSM(ctx, dcRequest(b))
		if err != nil {
			t.Fatalf("byte %#02x native encode: %v", b, err)
		}
		bridgeBody, _, err := bridge.DecodeSubmitSM(ctx, nativeEnc.Body)
		if err != nil {
			t.Fatalf("byte %#02x bridge decode of native pickle: %v", b, err)
		}
		if bridgeBody.DataCoding != want {
			t.Fatalf("byte %#02x bridge decode wire %#02x, want %#02x", b, bridgeBody.DataCoding, want)
		}
		// bridge pickle -> native decode (proves native reads the bridge's object shape)
		bridgeEnc, err := bridge.EncodeSubmitSM(ctx, dcRequest(b))
		if err != nil {
			t.Fatalf("byte %#02x bridge encode: %v", b, err)
		}
		nativeBody, _, err := native.DecodeSubmitSM(ctx, bridgeEnc.Body)
		if err != nil {
			t.Fatalf("byte %#02x native decode of bridge pickle: %v", b, err)
		}
		if nativeBody.DataCoding != want {
			t.Fatalf("byte %#02x native decode wire %#02x, want %#02x", b, nativeBody.DataCoding, want)
		}
	}
}

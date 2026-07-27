package picklecompat_test

import (
	"context"
	"math/big"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// TestNativeCustomTLVsMatchBridge proves the native encode+decode of
// pdu.custom_tlvs agrees with the bridge across the scalar value shapes and the
// optional length/type fields: a native submit pickle carrying the TLVs decodes
// (native and bridge) to identical []tlv.TLV.
func TestNativeCustomTLVsMatchBridge(t *testing.T) {
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

	length7 := 7
	tlvs := []picklecompat.SubmitSMCustomTLV{
		{Tag: big.NewInt(0x1400), Length: nil, Type: "COctetString", Value: "vendor-str"},
		{Tag: big.NewInt(0x1401), Length: &length7, Type: "", Value: int64(42)},
		{Tag: big.NewInt(0x1402), Length: nil, Type: "OctetString", Value: []byte{0x01, 0x02, 0xff}},
		{Tag: big.NewInt(0x1403), Length: nil, Type: "", Value: true},
		{Tag: big.NewInt(0x1404), Length: nil, Type: "Integer", Value: nil},
	}
	request := picklecompat.SubmitSMEncodeRequest{
		Sequence: 1, SourceAddr: picklecompat.Bytes("1111"), DestinationAddr: picklecompat.Bytes("2222"),
		ShortMessage: picklecompat.Bytes("hi"), SourceAddrTON: 2, SourceAddrNPI: 1, DestAddrTON: 1, DestAddrNPI: 1,
		CustomTLVs: tlvs,
	}
	pickle, err := native.EncodeSubmitSM(ctx, request)
	if err != nil {
		t.Fatalf("native encode: %v", err)
	}
	_, nativeTLVs, err := native.DecodeSubmitSM(ctx, pickle.Body)
	if err != nil {
		t.Fatalf("native decode: %v", err)
	}
	_, bridgeTLVs, err := bridge.DecodeSubmitSM(ctx, pickle.Body)
	if err != nil {
		t.Fatalf("bridge decode: %v", err)
	}
	if len(nativeTLVs) != len(tlvs) {
		t.Fatalf("native decoded %d TLVs, want %d", len(nativeTLVs), len(tlvs))
	}
	if !reflect.DeepEqual(nativeTLVs, bridgeTLVs) {
		t.Fatalf("custom TLVs differ:\n native %#v\n bridge %#v", nativeTLVs, bridgeTLVs)
	}
}

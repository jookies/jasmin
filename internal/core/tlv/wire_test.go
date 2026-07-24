package tlv

import (
	"encoding/hex"
	"math/big"
	"testing"
)

// Expected hex in this file was produced by running the fork's patched wire
// encoder (smpp.pdu3 encodeRawParams + install_pdu_encoder_patch) on the same
// tuples; error cases correspond to legacy crashes that reject the message,
// except the corrupt-frame cases documented as conscious rejections (Q-018).

func wireHex(t *testing.T, tlvs []TLV) string {
	t.Helper()
	out, err := WireEncodeCustomTLVs(tlvs)
	if err != nil {
		t.Fatalf("WireEncodeCustomTLVs(%+v) error: %v", tlvs, err)
	}
	return hex.EncodeToString(out)
}

func wantWireError(t *testing.T, tlvs []TLV) {
	t.Helper()
	if _, err := WireEncodeCustomTLVs(tlvs); err == nil {
		t.Errorf("WireEncodeCustomTLVs(%+v): want error", tlvs)
	}
}

func TestWireEncode_Int8Patch(t *testing.T) {
	// Oracle: 1400000817b1138750e6c845 for int and decimal-string values.
	for _, value := range []any{int64(1707167205648943173), "1707167205648943173", uint64(1707167205648943173)} {
		got := wireHex(t, []TLV{{Tag: big.NewInt(0x1400), Type: TypeInt8, Value: value}})
		if got != "1400000817b1138750e6c845" {
			t.Errorf("value %#v wire = %s", value, got)
		}
	}
	// Oracle: floats truncate through the patch's int(value).
	if got := wireHex(t, []TLV{{Tag: big.NewInt(0x1400), Type: TypeInt8, Value: 3.7}}); got != "140000080000000000000003" {
		t.Errorf("float wire = %s", got)
	}
	// Oracle raises: negative, non-decimal string.
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1400), Type: TypeInt8, Value: int64(-5)}})
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1400), Type: TypeInt8, Value: "0x10"}})
	// Conscious rejection: the patch would emit a corrupt frame (length 3, 8 bytes).
	hint := 3
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1400), Length: &hint, Type: TypeInt8, Value: int64(7)}})
}

func TestWireEncode_UpstreamInts(t *testing.T) {
	if got := wireHex(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeInt2, Value: int64(123)}}); got != "14010002007b" {
		t.Errorf("int2 wire = %s", got)
	}
	// Oracle: bool is an int subclass and emits.
	if got := wireHex(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeInt2, Value: true}}); got != "140100020001" {
		t.Errorf("bool wire = %s", got)
	}
	// Oracle raises: strings (TypeError), floats (struct.error), range, negative.
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeInt2, Value: "123"}})
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeInt2, Value: 3.7}})
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeInt2, Value: int64(70000)}})
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeInt1, Value: int64(-1)}})
}

func TestWireEncode_Octets(t *testing.T) {
	if got := wireHex(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeOctetString, Value: "1401778070000018542"}}); got != "1401001331343031373738303730303030303138353432" {
		t.Errorf("octet wire = %s", got)
	}
	// Oracle: UTF-8 for OctetString ("héllo"), bytes verbatim.
	if got := wireHex(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeOctetString, Value: "héllo"}}); got != "1401000668c3a96c6c6f" {
		t.Errorf("utf8 wire = %s", got)
	}
	if got := wireHex(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeOctetString, Value: []byte{0x01, 0x02}}}); got != "140100020102" {
		t.Errorf("bytes wire = %s", got)
	}
	// Oracle: COctetString is NUL-terminated, ASCII-only for strings.
	if got := wireHex(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeCOctetString, Value: "abc"}}); got != "1401000461626300" {
		t.Errorf("coctet wire = %s", got)
	}
	if got := wireHex(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeCOctetString, Value: []byte("abc")}}); got != "1401000461626300" {
		t.Errorf("coctet bytes wire = %s", got)
	}
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeCOctetString, Value: "héllo"}})
	// Oracle raises len() TypeError: integers, floats, nil.
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeOctetString, Value: int64(123)}})
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeOctetString, Value: 3.7}})
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeOctetString, Value: nil}})
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Type: TypeCOctetString, Value: int64(7)}})
}

func TestWireEncode_UnknownTypesSkipSilently(t *testing.T) {
	// Oracle: exact, untrimmed type-name matching — everything else is skipped
	// even though validation would have measured it (Q-018).
	for _, typeName := range []string{"", "Foo", " Int8 ", "octet_string"} {
		if got := wireHex(t, []TLV{{Tag: big.NewInt(0x1401), Type: typeName, Value: "x"}}); got != "" {
			t.Errorf("type %q wire = %s, want empty", typeName, got)
		}
	}
}

func TestWireEncode_LengthHints(t *testing.T) {
	two := 2
	if got := wireHex(t, []TLV{{Tag: big.NewInt(0x1401), Length: &two, Type: TypeInt2, Value: int64(123)}}); got != "14010002007b" {
		t.Errorf("hint-eq wire = %s", got)
	}
	// Legacy emits a corrupt frame (declared 1, two bytes) or pads-and-crashes
	// (declared 4); both are rejections here.
	one, four := 1, 4
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Length: &one, Type: TypeInt2, Value: int64(123)}})
	wantWireError(t, []TLV{{Tag: big.NewInt(0x1401), Length: &four, Type: TypeInt2, Value: int64(123)}})
}

func TestWireEncode_Tags(t *testing.T) {
	// Oracle raises: no masking on the wire path, unlike EncodeCustomTLVs.
	wantWireError(t, []TLV{{Tag: new(big.Int).SetUint64(0xFFFF1401), Type: TypeInt2, Value: int64(1)}})
	wantWireError(t, []TLV{{Tag: big.NewInt(-1), Type: TypeInt2, Value: int64(1)}})
	wantWireError(t, []TLV{{Tag: nil, Type: TypeInt2, Value: int64(1)}})
}

func TestWireEncode_OrderAndMixedBatch(t *testing.T) {
	// Oracle: 15000001621401000101140000080000000000000007 — tuple order, and a
	// user-supplied SAR-range tag is emitted verbatim (no allowlisting).
	got := wireHex(t, []TLV{
		{Tag: big.NewInt(0x1500), Type: TypeOctetString, Value: "b"},
		{Tag: big.NewInt(0x1400), Type: TypeInt8, Value: int64(7)},
		{Tag: big.NewInt(0x1401), Type: TypeInt1, Value: int64(1)},
	})
	if got != "15000001621401000101140000080000000000000007" {
		t.Errorf("mixed wire = %s", got)
	}
	if got := wireHex(t, []TLV{{Tag: big.NewInt(0x020c), Type: TypeInt2, Value: int64(99)}}); got != "020c00020063" {
		t.Errorf("sar-range wire = %s", got)
	}
}

func TestWireEncode_EmptyAndSkippedOnly(t *testing.T) {
	if out, err := WireEncodeCustomTLVs(nil); err != nil || out != nil {
		t.Errorf("nil input = %x, %v", out, err)
	}
}

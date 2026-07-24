package tlv

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"
)

func intp(n int) *int { return &n }

func TestParseTagKey(t *testing.T) {
	cases := []struct {
		in       string
		wantTag  string
		wantType string
		wantErr  bool
	}{
		{"0x1401", "5121", "", false},
		{"0x1401:OctetString", "5121", "OctetString", false},
		{"0x1401:Int8", "5121", "Int8", false},
		{"5121", "5121", "", false},
		{"5121:Int4", "5121", "Int4", false},
		{"  0x1400  ", "5120", "", false},
		{"0X1401:Int2", "5121", "Int2", false},
		{"-1", "-1", "", false},
		{"18446744073709551616", "18446744073709551616", "", false},
		{"0x1401:Bogus", "", "", true},
		{"notanumber", "", "", true},
	}
	for _, c := range cases {
		gotTag, gotType, err := ParseTagKey(c.in)
		gotTagText := ""
		if gotTag != nil {
			gotTagText = gotTag.String()
		}
		if gotTagText != c.wantTag || gotType != c.wantType || (err != nil) != c.wantErr {
			t.Errorf("ParseTagKey(%q) = (%s,%q,%v), want (%s,%q,err=%v)",
				c.in, gotTagText, gotType, err, c.wantTag, c.wantType, c.wantErr)
		}
	}
}

func TestEncodeValue_Integers(t *testing.T) {
	// Big-endian packing, hand vectors.
	if got, err := EncodeValue(5121, TypeInt2); err != nil || !bytes.Equal(got, []byte{0x14, 0x01}) {
		t.Errorf("Int2(5121) = %x, %v; want 1401", got, err)
	}
	if got, err := EncodeValue(255, TypeInt1); err != nil || !bytes.Equal(got, []byte{0xff}) {
		t.Errorf("Int1(255) = %x, %v; want ff", got, err)
	}
	if got, err := EncodeValue(uint32(0x01020304), TypeInt4); err != nil || !bytes.Equal(got, []byte{1, 2, 3, 4}) {
		t.Errorf("Int4 = %x, %v; want 01020304", got, err)
	}
	// Int8 with a 19-digit vendor id (within uint64); compare to a direct big-endian pack.
	var id uint64 = 1707167205648943173
	want := make([]byte, 8)
	binary.BigEndian.PutUint64(want, id)
	if got, err := EncodeValue(id, TypeInt8); err != nil || !bytes.Equal(got, want) {
		t.Errorf("Int8(%d) = %x, %v; want %x", id, got, err, want)
	}
	// Numeric string is coerced (Python int(value)).
	if got, err := EncodeValue("5121", TypeInt2); err != nil || !bytes.Equal(got, []byte{0x14, 0x01}) {
		t.Errorf("Int2(\"5121\") = %x, %v; want 1401", got, err)
	}
}

func TestEncodeValue_IntegerOverflowAndNegative(t *testing.T) {
	if _, err := EncodeValue(256, TypeInt1); err == nil {
		t.Error("Int1(256) should overflow")
	}
	if _, err := EncodeValue(0x10000, TypeInt2); err == nil {
		t.Error("Int2(65536) should overflow")
	}
	if _, err := EncodeValue(-1, TypeInt1); err == nil {
		t.Error("Int1(-1) negative should error")
	}
	huge, ok := new(big.Int).SetString("18446744073709551616", 10)
	if !ok {
		t.Fatal("parse huge integer")
	}
	if _, err := EncodeValue(huge, TypeInt8); err == nil {
		t.Error("Int8(2^64) should overflow at the wire boundary")
	}
}

func TestEncodeValue_Strings(t *testing.T) {
	// OctetString: UTF-8 of the string, no terminator.
	if got, err := EncodeValue("1401778070000018542", TypeOctetString); err != nil ||
		!bytes.Equal(got, []byte("1401778070000018542")) {
		t.Errorf("OctetString = %q, %v", got, err)
	}
	// COctetString: UTF-8 plus a trailing NUL.
	if got, err := EncodeValue("abc", TypeCOctetString); err != nil ||
		!bytes.Equal(got, []byte{'a', 'b', 'c', 0x00}) {
		t.Errorf("COctetString = %x, %v; want 61626300", got, err)
	}
	// Unknown/empty type falls back to UTF-8 str(value).
	if got, err := EncodeValue(1234, ""); err != nil || !bytes.Equal(got, []byte("1234")) {
		t.Errorf("empty-type int = %q, %v; want \"1234\"", got, err)
	}
}

func TestEncodeValue_BytesVerbatim(t *testing.T) {
	// A []byte value is returned verbatim regardless of the declared type (bytes short-circuit).
	raw := []byte{0xde, 0xad, 0xbe, 0xef}
	if got, err := EncodeValue(raw, TypeInt2); err != nil || !bytes.Equal(got, raw) {
		t.Errorf("bytes verbatim = %x, %v", got, err)
	}
	// The returned slice must be a copy.
	got, _ := EncodeValue(raw, TypeOctetString)
	got[0] = 0
	if raw[0] != 0xde {
		t.Error("EncodeValue aliased the input slice")
	}
}

func TestEncodeValue_Float64Rejected(t *testing.T) {
	// float64 is rejected on both the int and the string path to prevent 19-digit precision loss.
	if _, err := EncodeValue(float64(5121), TypeInt2); err == nil {
		t.Error("float64 int TLV should be rejected")
	}
	if _, err := EncodeValue(float64(5121), TypeOctetString); err == nil {
		t.Error("float64 string TLV should be rejected")
	}
}

func TestEncodeCustomTLVs_ByteExact(t *testing.T) {
	// Tag 0x1400, OctetString "17": header 0014 0002, body '1''7'.
	got, err := EncodeCustomTLVs([]TLV{{Tag: big.NewInt(0x1400), Type: TypeOctetString, Value: "17"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x14, 0x00, 0x00, 0x02, '1', '7'} // tag 0x1400 BE, len 0x0002, "17"
	if !bytes.Equal(got, want) {
		t.Errorf("EncodeCustomTLVs = %x, want %x", got, want)
	}

	// Two TLVs concatenate in order; Int2 body length is 2.
	got, err = EncodeCustomTLVs([]TLV{
		{Tag: big.NewInt(0x1401), Type: TypeInt2, Value: 5121},
		{Tag: big.NewInt(0x1400), Type: TypeCOctetString, Value: "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want = []byte{
		0x14, 0x01, 0x00, 0x02, 0x14, 0x01, // 0x1401 len2 body 1401
		0x14, 0x00, 0x00, 0x02, 'x', 0x00, // 0x1400 len2 body "x\0"
	}
	if !bytes.Equal(got, want) {
		t.Errorf("two-TLV encode = %x, want %x", got, want)
	}
}

func TestEncodeCustomTLVs_Empty(t *testing.T) {
	if got, err := EncodeCustomTLVs(nil); err != nil || got != nil {
		t.Errorf("empty = %x, %v; want nil,nil", got, err)
	}
}

func TestResolveTLVTypes(t *testing.T) {
	rules := []ConnectorRule{
		{Tag: 0x1401, Type: TypeInt8},
		{Tag: 0x1400, Type: TypeOctetString},
	}
	in := []TLV{
		{Tag: big.NewInt(0x1401), Value: "1707167205648943173"}, // untyped -> Int8, string coerced to uint64
		{Tag: big.NewInt(0x1400), Value: "hello"},               // untyped -> OctetString
		{Tag: big.NewInt(0x9999), Value: "x"},                   // not in rules -> OctetString default
		{Tag: big.NewInt(0x1401), Type: TypeInt2, Value: 5},     // already typed -> unchanged
	}
	out, err := ResolveTLVTypes(in, rules)
	if err != nil {
		t.Fatal(err)
	}

	if out[0].Type != TypeInt8 {
		t.Errorf("tag 0x1401 type = %q, want Int8", out[0].Type)
	}
	if v, ok := out[0].Value.(uint64); !ok || v != 1707167205648943173 {
		t.Errorf("tag 0x1401 value = %v (%T), want uint64 1707167205648943173", out[0].Value, out[0].Value)
	}
	if out[1].Type != TypeOctetString {
		t.Errorf("tag 0x1400 type = %q, want OctetString", out[1].Type)
	}
	if out[2].Type != TypeOctetString {
		t.Errorf("unknown tag type = %q, want OctetString default", out[2].Type)
	}
	if out[3].Type != TypeInt2 || out[3].Value.(int) != 5 {
		t.Errorf("already-typed tuple changed: %+v", out[3])
	}
}

func TestResolveTLVTypes_HexStringCoercion(t *testing.T) {
	rules := []ConnectorRule{{Tag: 0x1401, Type: TypeInt2}}
	out, err := ResolveTLVTypes([]TLV{{Tag: big.NewInt(0x1401), Value: "0x1401"}}, rules)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := out[0].Value.(uint64); !ok || v != 0x1401 {
		t.Errorf("hex string value = %v (%T), want uint64 5121", out[0].Value, out[0].Value)
	}
}

func TestValidateCustomTLVs(t *testing.T) {
	rules := []ConnectorRule{
		{Tag: 0x1401, Type: TypeOctetString, Length: intp(5), Required: true},
		{Tag: 0x1400, Type: TypeOctetString, Length: nil}, // unbounded, optional
	}

	// Missing required tag.
	err := ValidateCustomTLVs(nil, rules)
	var re *RuleError
	if !errors.As(err, &re) || !re.Missing || re.Tag != 0x1401 {
		t.Errorf("missing required: got %v", err)
	}

	// Present, within max length.
	if err := ValidateCustomTLVs([]TLV{{Tag: big.NewInt(0x1401), Type: TypeOctetString, Value: "12345"}}, rules); err != nil {
		t.Errorf("5-byte value within max 5 rejected: %v", err)
	}

	// Present, exceeds max length.
	err = ValidateCustomTLVs([]TLV{{Tag: big.NewInt(0x1401), Type: TypeOctetString, Value: "123456"}}, rules)
	if !errors.As(err, &re) || re.Missing || re.Tag != 0x1401 || re.ActualLen != 6 || re.MaxLen != 5 {
		t.Errorf("length exceeded: got %v", err)
	}

	// Unbounded optional tag with a long value passes; required tag also present.
	long := []TLV{
		{Tag: big.NewInt(0x1401), Type: TypeOctetString, Value: "12345"},
		{Tag: big.NewInt(0x1400), Type: TypeOctetString, Value: "a-very-long-vendor-value"},
	}
	if err := ValidateCustomTLVs(long, rules); err != nil {
		t.Errorf("unbounded tag rejected: %v", err)
	}
}

func TestValidateCustomTLVs_NonRuleTagPasses(t *testing.T) {
	// A TLV whose tag is not in the rules is allowed through untouched.
	rules := []ConnectorRule{{Tag: 0x1401, Type: TypeOctetString, Length: intp(2)}}
	tlvs := []TLV{{Tag: big.NewInt(0x7777), Type: TypeOctetString, Value: "this-is-long-but-not-ruled"}}
	if err := ValidateCustomTLVs(tlvs, rules); err != nil {
		t.Errorf("non-rule tag should pass: %v", err)
	}
}

func TestValidateCustomTLVs_NoRules(t *testing.T) {
	if err := ValidateCustomTLVs([]TLV{{Tag: big.NewInt(1), Value: "x"}}, nil); err != nil {
		t.Errorf("no rules should allow everything: %v", err)
	}
}

func TestValidateCustomTLVs_FallsBackToRuleType(t *testing.T) {
	// An untyped (Type "") present TLV uses the rule's type for its length computation:
	// Int2 encodes to 2 bytes, within a max of 2.
	rules := []ConnectorRule{{Tag: 0x1401, Type: TypeInt2, Length: intp(2)}}
	if err := ValidateCustomTLVs([]TLV{{Tag: big.NewInt(0x1401), Value: 5121}}, rules); err != nil {
		t.Errorf("Int2 (2 bytes) within max 2 rejected: %v", err)
	}
}

func TestEncodedValueLength(t *testing.T) {
	if n, err := EncodedValueLength("abc", TypeCOctetString); err != nil || n != 4 {
		t.Errorf("COctetString len = %d, %v; want 4", n, err)
	}
	if n, err := EncodedValueLength(5121, TypeInt8); err != nil || n != 8 {
		t.Errorf("Int8 len = %d, %v; want 8", n, err)
	}
}

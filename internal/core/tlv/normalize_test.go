package tlv

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"testing"
)

func TestNormalize_EmptyAndFalsy(t *testing.T) {
	for _, in := range []any{nil, "", "   ", map[string]any{}, []any{}, []byte(nil), 42, true} {
		if got := Normalize(in); got != nil {
			t.Errorf("Normalize(%#v) = %v, want nil", in, got)
		}
	}
}

func TestNormalize_Dict(t *testing.T) {
	in := map[string]any{
		"0x1401:OctetString": "1401778070000018542",
		"0x1400":             "1707167205648943173",
	}
	got := Normalize(in)
	// Tag-sorted: 0x1400 before 0x1401.
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Tag != 0x1400 || got[0].Type != "" || got[0].Value != "1707167205648943173" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].Tag != 0x1401 || got[1].Type != TypeOctetString || got[1].Value != "1401778070000018542" {
		t.Errorf("entry 1 = %+v", got[1])
	}
}

func TestNormalize_LegacyFourTuple(t *testing.T) {
	// Values decoded with UseNumber survive as json.Number and become digit strings.
	raw := `[[5121, null, "Int8", 1707167205648943173]]`
	got := Normalize(raw)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	tlv := got[0]
	if tlv.Tag != 5121 || tlv.Type != TypeInt8 || tlv.Value != "1707167205648943173" {
		t.Errorf("tuple = %+v", tlv)
	}
	if tlv.Length != nil {
		t.Errorf("length = %v, want nil (json null)", tlv.Length)
	}
	// And it encodes without precision loss.
	body, err := EncodeValue(tlv.Value, tlv.Type)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, 8)
	binary.BigEndian.PutUint64(want, 1707167205648943173)
	if !bytes.Equal(body, want) {
		t.Errorf("Int8 encode = %x, want %x", body, want)
	}
}

func TestNormalize_ListOfDicts(t *testing.T) {
	in := []any{
		map[string]any{"tag": "0x1401", "value": "abc"},
		map[string]any{"tag": float64(5120), "value": "def"}, // numeric tag
	}
	got := Normalize(in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Tag != 0x1401 || got[0].Value != "abc" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].Tag != 5120 || got[1].Value != "def" {
		t.Errorf("entry 1 = %+v", got[1])
	}
}

func TestNormalize_TwoTuple(t *testing.T) {
	// [tag, value] -> (tag, None, None, value). List order preserved.
	got := Normalize([]any{[]any{float64(0x1401), "v"}})
	if len(got) != 1 || got[0].Tag != 0x1401 || got[0].Type != "" || got[0].Value != "v" {
		t.Errorf("two-tuple = %+v", got)
	}
}

func TestNormalize_JSONStringDict(t *testing.T) {
	got := Normalize(`{"0x1401:Int2": "5121"}`)
	if len(got) != 1 || got[0].Tag != 0x1401 || got[0].Type != TypeInt2 || got[0].Value != "5121" {
		t.Errorf("json-string dict = %+v", got)
	}
}

func TestNormalize_InvalidJSONString(t *testing.T) {
	if got := Normalize(`{not json`); got != nil {
		t.Errorf("invalid JSON = %v, want nil", got)
	}
}

func TestNormalize_SkipsMalformed(t *testing.T) {
	in := []any{
		"scalar",             // skipped
		[]any{float64(0x10)}, // len 1 -> skipped
		[]any{float64(0x1401), "ok"},
	}
	got := Normalize(in)
	if len(got) != 1 || got[0].Tag != 0x1401 || got[0].Value != "ok" {
		t.Errorf("skip-malformed = %+v", got)
	}
}

func TestNormalize_EndToEndDict(t *testing.T) {
	// Full pipeline: JSON string -> Normalize -> Resolve (fill types) -> Encode, byte-exact.
	raw := `{"0x1401": "5121", "0x1400": "hi"}`
	rules := []ConnectorRule{
		{Tag: 0x1401, Type: TypeInt2},
		{Tag: 0x1400, Type: TypeOctetString},
	}
	resolved := ResolveTLVTypes(Normalize(raw), rules)
	got, err := EncodeCustomTLVs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	// Tag-sorted: 0x1400 (OctetString "hi") then 0x1401 (Int2 5121 -> 0x1401).
	want := []byte{
		0x14, 0x00, 0x00, 0x02, 'h', 'i',
		0x14, 0x01, 0x00, 0x02, 0x14, 0x01,
	}
	if !bytes.Equal(got, want) {
		t.Errorf("end-to-end = %x, want %x", got, want)
	}
}

func TestNormalize_UseNumberFromCaller(t *testing.T) {
	// A caller decoding JSON with UseNumber and passing the map directly also preserves
	// large integers (json.Number -> digit string).
	var parsed any
	dec := json.NewDecoder(bytes.NewReader([]byte(`{"0x1400": 1707167205648943173}`)))
	dec.UseNumber()
	if err := dec.Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	got := Normalize(parsed)
	if len(got) != 1 || got[0].Value != "1707167205648943173" {
		t.Errorf("UseNumber value = %+v", got)
	}
}

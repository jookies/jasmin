package tlv

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
)

// Expected values and wire bytes in this file were produced by running the frozen
// oracle (jasmin/tools/tlv_encoder.py, baseline 0aac58e466d583d0f0436df7b8afa3dc96191263)
// on the same inputs; deliberate divergences assert the behavior documented on Normalize.

func wantTag(t *testing.T, got *big.Int, want int64) {
	t.Helper()
	if got == nil || got.Cmp(big.NewInt(want)) != 0 {
		t.Errorf("tag = %v, want %d", got, want)
	}
}

func mustNormalize(t *testing.T, raw any) []TLV {
	t.Helper()
	got, err := Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize(%#v) error: %v", raw, err)
	}
	return got
}

func wantValueError(t *testing.T, raw any) {
	t.Helper()
	_, err := Normalize(raw)
	var ve *ValueError
	if !errors.As(err, &ve) {
		t.Errorf("Normalize(%#v) error = %v, want *ValueError", raw, err)
	}
}

func TestNormalize_EmptyFalsyUnknown(t *testing.T) {
	for _, in := range []any{
		nil, "", "   ", map[string]any{}, []any{}, []byte(nil), []byte("  "),
		42, true, 3.14, "{}", "[]", "null", "42", "true", "not json",
	} {
		got, err := Normalize(in)
		if err != nil {
			t.Errorf("Normalize(%#v) error: %v", in, err)
		}
		if got != nil {
			t.Errorf("Normalize(%#v) = %v, want nil", in, got)
		}
	}
}

func TestNormalize_MapInput(t *testing.T) {
	in := map[string]any{
		"0x1401:OctetString": "1401778070000018542",
		"0x1400":             "1707167205648943173",
	}
	got := mustNormalize(t, in)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	// Map input is processed in sorted-key order: "0x1400" < "0x1401:OctetString".
	wantTag(t, got[0].Tag, 0x1400)
	if got[0].Type != "" || got[0].Value != "1707167205648943173" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	wantTag(t, got[1].Tag, 0x1401)
	if got[1].Type != TypeOctetString || got[1].Value != "1401778070000018542" {
		t.Errorf("entry 1 = %+v", got[1])
	}
}

func TestNormalize_MapInput_BadKeysRaise(t *testing.T) {
	// The oracle raises ValueError out of _parse_tag_key for all three.
	for _, key := range []string{"banana", "0x1401:", "0x1401:Int9"} {
		wantValueError(t, map[string]any{key: 1})
	}
}

func TestNormalize_JSONDictDocumentOrder(t *testing.T) {
	// Oracle: [(5376, None, None, 'b'), (5120, None, None, 'a')] — document order,
	// not numeric or lexicographic order.
	got := mustNormalize(t, `{"0x1500": "b", "0x1400": "a"}`)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	wantTag(t, got[0].Tag, 0x1500)
	wantTag(t, got[1].Tag, 0x1400)
	if got[0].Value != "b" || got[1].Value != "a" {
		t.Errorf("values = %v, %v", got[0].Value, got[1].Value)
	}
}

func TestNormalize_JSONDictDuplicateKeys(t *testing.T) {
	// Python dict semantics: duplicate key "0x1401" keeps its first position with the
	// last value; distinct key "5121" (same tag) stays a separate entry.
	// Oracle: [(5121, None, None, 3), (5121, None, None, 2)].
	got := mustNormalize(t, `{"0x1401": 1, "5121": 2, "0x1401": 3}`)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	wantTag(t, got[0].Tag, 5121)
	wantTag(t, got[1].Tag, 5121)
	if got[0].Value != "3" || got[1].Value != "2" {
		t.Errorf("values = %v, %v, want 3, 2", got[0].Value, got[1].Value)
	}
}

func TestNormalize_LegacyFourTuple(t *testing.T) {
	got := mustNormalize(t, `[[5121, null, "Int8", 1707167205648943173]]`)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	e := got[0]
	wantTag(t, e.Tag, 5121)
	if e.Type != TypeInt8 || e.Length != nil || e.Value != "1707167205648943173" {
		t.Errorf("tuple = %+v", e)
	}
	// Oracle wire bytes: 19-digit value survives with no precision loss.
	wire, err := EncodeCustomTLVs(got)
	if err != nil {
		t.Fatal(err)
	}
	if h := hex.EncodeToString(wire); h != "1401000817b1138750e6c845" {
		t.Errorf("wire = %s", h)
	}
}

func TestNormalize_FourTupleFields(t *testing.T) {
	t.Run("length hints", func(t *testing.T) {
		got := mustNormalize(t, `[[5121, 8, "Int8", 1], [5121, "x", "Int8", 1], [5121, 3.0, "Int8", 1]]`)
		if got[0].Length == nil || *got[0].Length != 8 {
			t.Errorf("length 0 = %v, want 8", got[0].Length)
		}
		if got[1].Length != nil {
			t.Errorf("length 1 = %v, want nil (non-numeric hint drops)", got[1].Length)
		}
		if got[2].Length == nil || *got[2].Length != 3 {
			t.Errorf("length 2 = %v, want 3", got[2].Length)
		}
	})

	t.Run("type field verbatim and falsy", func(t *testing.T) {
		got := mustNormalize(t, `[[5121, null, " Int8 ", 7], [5121, null, null, 7], [5121, null, "", 7], [5121, null, 0, 7], [5121, null, false, 7]]`)
		if got[0].Type != " Int8 " {
			t.Errorf("type 0 = %q, want verbatim \" Int8 \"", got[0].Type)
		}
		// The frozen encoder trims exactly where the oracle's .strip() does.
		if b, err := EncodeValue(got[0].Value, got[0].Type); err != nil || len(b) != 8 {
			t.Errorf("verbatim-type encode = %x, %v", b, err)
		}
		// Falsy non-null types map to unresolved "" (KNOWN_QUIRKS Q-017 decision).
		for i := 1; i < 5; i++ {
			if got[i].Type != "" {
				t.Errorf("type %d = %q, want \"\"", i, got[i].Type)
			}
		}
	})

	t.Run("truthy non-string type errors", func(t *testing.T) {
		if _, err := Normalize(`[[5121, null, 42, "x"]]`); err == nil {
			t.Error("want error (oracle: AttributeError at encode)")
		}
	})

	t.Run("tag conversions", func(t *testing.T) {
		got := mustNormalize(t, `[[5121.9, null, "Int1", 1], ["  5121  ", null, "Int1", 7]]`)
		wantTag(t, got[0].Tag, 5121) // int(5121.9) truncates toward zero
		wantTag(t, got[1].Tag, 5121) // int(" 5121 ") strips whitespace
	})

	t.Run("hex string tag errors decimal-only", func(t *testing.T) {
		// Oracle succeeds at normalize and raises ValueError at encode
		// (int() without base 16); the typed shape hoists that error here.
		wantValueError(t, `[["0x1401", null, "Int1", 1]]`)
	})

	t.Run("Python decimal lexical forms", func(t *testing.T) {
		got := mustNormalize(t, `[["1_000", null, "Int1", 1], ["１２", null, "Int1", 2]]`)
		wantTag(t, got[0].Tag, 1000)
		wantTag(t, got[1].Tag, 12)
	})

	t.Run("null tag errors", func(t *testing.T) {
		_, err := Normalize(`[[null, null, "Int1", 1]]`)
		if err == nil {
			t.Error("want error (oracle: TypeError)")
		}
		var ve *ValueError
		if errors.As(err, &ve) {
			t.Error("null tag is the oracle's TypeError, not ValueError")
		}
	})
}

func TestNormalize_ShortTuples(t *testing.T) {
	// Oracle: [(5121, None, None, 'x'), (5122, None, None, 'v2'), (5123, None, None, 9)]
	// with wire 14010001781402000276321403000139; single-element and scalar entries skip.
	got := mustNormalize(t, `[[5121, "x"], [5122, "mid", "v2"], {"tag": "0x1403", "value": 9}, [5124], "scalar", 7]`)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
	wantTag(t, got[0].Tag, 5121)
	wantTag(t, got[1].Tag, 5122)
	wantTag(t, got[2].Tag, 5123)
	if got[0].Value != "x" || got[1].Value != "v2" || got[2].Value != "9" {
		t.Errorf("values = %v", []any{got[0].Value, got[1].Value, got[2].Value})
	}
	resolved, err := ResolveTLVTypes(got, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := EncodeCustomTLVs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if h := hex.EncodeToString(wire); h != "14010001781402000276321403000139" {
		t.Errorf("wire = %s", h)
	}
	// 2/3-tuple string tags are decimal-only (bare int()), unlike dict tags.
	wantValueError(t, `[["0x14", 1]]`)
}

func TestNormalize_ByteTagPythonDecimalSyntax(t *testing.T) {
	got := mustNormalize(t, []any{[]any{[]byte(" 	1_000\r\n"), "x"}})
	wantTag(t, got[0].Tag, 1000)
	wantValueError(t, []any{[]any{[]byte("１２"), "x"}})
	wantValueError(t, []any{[]any{[]byte("\xc2\xa01"), "x"}})
}

func TestNormalize_ListDictEntries(t *testing.T) {
	got := mustNormalize(t, `[{"value": 9}, {"0": "5121", "value": 9}, {"tag": true, "value": 9}, {"tag": 5121.9, "value": 9}, {"tag": "0x1401", "value": 9}, {"tag": 5121}]`)
	if len(got) != 6 {
		t.Fatalf("len = %d, want 6", len(got))
	}
	wantTag(t, got[0].Tag, 0)    // neither "tag" nor "0": oracle defaults to 0
	wantTag(t, got[1].Tag, 5121) // fallback key "0"
	wantTag(t, got[2].Tag, 1)    // int(True)
	wantTag(t, got[3].Tag, 5121) // int(5121.9) truncates
	wantTag(t, got[4].Tag, 5121) // dict tags are 0x-aware
	if got[5].Value != nil {
		t.Errorf("missing value = %v, want nil (Python None)", got[5].Value)
	}

	_, err := Normalize(`[{"tag": null, "value": 9}]`)
	if err == nil {
		t.Error("null tag: want error (oracle: TypeError)")
	}
}

func TestNormalize_JSONTextParseFailuresAreSilent(t *testing.T) {
	// The oracle json.loads-parses fully, then normalizes: syntax failures (including
	// trailing data) yield [] silently even when a bad tag precedes the syntax error.
	for _, in := range []string{
		"{bad",
		`{"banana": 1, `,
		`{"0x1401": 1} extra`,
		`{"0x1401": 1}{}`,
		`[1, 2`,
	} {
		got, err := Normalize(in)
		if err != nil || got != nil {
			t.Errorf("Normalize(%q) = %v, %v, want nil, nil", in, got, err)
		}
	}
	// But a well-formed document with a bad tag raises — the two-phase contrast.
	wantValueError(t, `{"banana": 1}`)
}

func TestNormalize_DoubleEncodedJSONString(t *testing.T) {
	inner := `[[5121, null, "Int8", 7]]`
	quoted, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	got := mustNormalize(t, string(quoted))
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	wantTag(t, got[0].Tag, 5121)
	if got[0].Type != TypeInt8 || got[0].Value != "7" {
		t.Errorf("entry = %+v", got[0])
	}
}

func TestNormalize_NumberCarriage(t *testing.T) {
	t.Run("negative zero canonicalizes", func(t *testing.T) {
		// Oracle: json -0 → int 0 → str() "0" on the octet wire (hex 30).
		got := mustNormalize(t, `[[5121, null, "OctetString", -0]]`)
		if got[0].Value != "0" {
			t.Errorf("value = %q, want \"0\"", got[0].Value)
		}
	})

	t.Run("non-integral numbers are rejected loudly downstream", func(t *testing.T) {
		// Deliberate divergence: the oracle truncates 3.7 to 3 for Int types and
		// renders "3.7" for octet types; the frozen encoder rejects float64.
		got := mustNormalize(t, `[[5121, null, "Int2", 3.7]]`)
		f, ok := got[0].Value.(float64)
		if !ok || f != 3.7 {
			t.Fatalf("value = %#v, want float64 3.7", got[0].Value)
		}
		if _, err := EncodeValue(got[0].Value, got[0].Type); err == nil {
			t.Error("want encode rejection for float64")
		}
	})

	t.Run("exponent form is non-integral", func(t *testing.T) {
		got := mustNormalize(t, `[[5121, null, "OctetString", 1e3]]`)
		if f, ok := got[0].Value.(float64); !ok || f != 1000 {
			t.Errorf("value = %#v, want float64 1000", got[0].Value)
		}
	})
}

func TestNormalize_HugeTagDeferredMasking(t *testing.T) {
	// Oracle: tag carried at full precision (4294906881), masked to 0x1401 on the wire.
	got := mustNormalize(t, `{"0xFFFF1401": "z"}`)
	wantTag(t, got[0].Tag, 0xFFFF1401)
	resolved, err := ResolveTLVTypes(got, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := EncodeCustomTLVs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if h := hex.EncodeToString(wire); h != "140100017a" {
		t.Errorf("wire = %s", h)
	}
}

func TestNormalize_EndToEndGolden(t *testing.T) {
	// Full front-door flow against oracle-produced bytes: dict payload with a type
	// hint on one tag, connector rules typing and requiring the other.
	payload := `{"0x1401:OctetString": "1401778070000018542", "0x1400": "1707167205648943173"}`
	length32 := 32
	rules := []ConnectorRule{
		{Tag: 0x1400, Type: TypeInt8, Required: true},
		{Tag: 0x1401, Type: TypeCOctetString, Length: &length32},
	}
	tlvs := mustNormalize(t, payload)
	resolved, err := ResolveTLVTypes(tlvs, rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCustomTLVs(resolved, rules); err != nil {
		t.Fatal(err)
	}
	wire, err := EncodeCustomTLVs(resolved)
	if err != nil {
		t.Fatal(err)
	}
	want := "14010013313430313737383037303030303031383534321400000817b1138750e6c845"
	if h := hex.EncodeToString(wire); h != want {
		t.Errorf("wire = %s\n want = %s", h, want)
	}
}

func TestNormalize_RawInputVariants(t *testing.T) {
	text := `[[5121, null, "Int8", 7]]`
	for name, in := range map[string]any{
		"string":     text,
		"bytes":      []byte(text),
		"RawMessage": json.RawMessage(text),
		"whitespace": "  " + text + "  ",
	} {
		got := mustNormalize(t, in)
		if len(got) != 1 || got[0].Type != TypeInt8 {
			t.Errorf("%s: got %+v", name, got)
		}
	}
}

func TestNormalize_PredecodedListPreservesUseNumber(t *testing.T) {
	// A caller that pre-decodes with UseNumber gets identical treatment to JSON text.
	dec := json.NewDecoder(strings.NewReader(`[[5121, null, "Int8", 1707167205648943173]]`))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		t.Fatal(err)
	}
	got := mustNormalize(t, parsed)
	if got[0].Value != "1707167205648943173" {
		t.Errorf("value = %#v, want digit string", got[0].Value)
	}
}

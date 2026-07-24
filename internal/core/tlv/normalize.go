package tlv

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strings"
)

// Normalize accepts the per-message custom_tlvs input shapes seen on the REST/HTTP
// boundary and normalizes them to []TLV — the (tag, length, type, value) shape the typed
// pipeline consumes — porting normalize_custom_tlvs. It is the front door: its output
// feeds ResolveTLVTypes → ValidateCustomTLVs → EncodeCustomTLVs.
//
// Accepted shapes (raw is a decoded JSON value, or JSON text as string/[]byte/json.RawMessage):
//
//   - Preferred dict, optional type hint in the key:
//     {"0x1401:OctetString": "1401778070000018542", "0x1400": "1707167205648943173"}
//   - Legacy list of 4-tuples: [[5121, null, "Int8", 1707167205648943173]]
//   - List of dicts: [{"tag": "0x1401", "value": 123}]; the tag falls back to key "0",
//     then to literal 0 when both are absent (the oracle's entry.get chain).
//   - 2/3-element tuples: [tag, value] / [tag, _, value] — the last element is the value.
//     String tags here are decimal-only (bare Python int()), unlike dict keys and dict
//     "tag" fields, which accept 0x-hex.
//   - JSON text encoding any of the above; JSON that decodes to a string recurses, so
//     double-encoded query params unwrap exactly like the oracle.
//
// Falsy/empty input and unrecognized top-level shapes yield (nil, nil); malformed JSON
// text also yields (nil, nil), matching the oracle's silent parse-failure → []. A tag or
// type field the oracle would raise on returns an error instead: *ValueError where Python
// raises ValueError, a plain error where Python raises TypeError/AttributeError. Values
// pass through verbatim (json.Number aside — see below); shapes the oracle would repr()
// onto the wire (dicts, lists, None, booleans) are rejected later by the frozen encoder.
//
// Ordering: JSON text input preserves document order for a top-level object, including
// Python-dict duplicate-key semantics (last value wins, first position kept), so wire
// order and duplicate-tag resolution match the oracle byte-for-byte. A pre-decoded
// map[string]any has no order left to preserve; its keys are processed in sorted order
// for determinism. Pass the raw JSON text (e.g. json.RawMessage) when order must be exact.
//
// Deliberate divergences from the oracle, each loud rather than silent:
//
//   - Integral JSON numbers are carried as Python-int equivalents (int64, then uint64,
//     then a canonical decimal string beyond uint64), so 19-digit vendor IDs survive
//     without float64 precision loss and the wire encoder sees an integer exactly where
//     Python would. Non-integral numbers (3.7, 1e3) are carried as float64, which the
//     frozen encoder rejects; Python would silently truncate them for Int types and
//     render repr() text for octet types.
//   - A 4-tuple's tag converts here with Python int() semantics rather than at encode
//     time where the oracle converts it; the same inputs succeed or fail across the
//     front-door flow, one stage earlier.
//   - A 4-tuple type field that is falsy but non-null ("", 0, false) maps to unresolved
//     "", so the connector rule's type applies consistently; the oracle bypasses
//     resolution yet validates with the rule's type and then encodes as an octet fallback
//     (KNOWN_QUIRKS Q-017). A truthy non-string type field errors here; the oracle
//     crashes at encode with AttributeError.
func Normalize(raw any) ([]TLV, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return normalizeMap(v)
	case []any:
		return normalizeList(v)
	case string:
		return normalizeJSONText(v)
	case []byte:
		return normalizeJSONText(string(v))
	case json.RawMessage:
		return normalizeJSONText(string(v))
	default:
		return nil, nil // the oracle: falsy or unhandled top-level shape → []
	}
}

// normalizeMap handles a pre-decoded top-level dict. Sorted keys are the only
// deterministic total order available once Go's map has erased document order.
func normalizeMap(m map[string]any) ([]TLV, error) {
	if len(m) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]TLV, 0, len(m))
	for _, key := range keys {
		t, err := tlvFromDictKey(key, m[key])
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// tlvFromDictKey builds one TLV from a preferred-dict entry: the key carries the tag and
// an optional ":Type" hint. An unparseable tag propagates ParseTagKey's *ValueError —
// the oracle raises out of _parse_tag_key rather than skipping the entry.
func tlvFromDictKey(key string, value any) (TLV, error) {
	tag, tlvType, err := ParseTagKey(key)
	if err != nil {
		return TLV{}, err
	}
	return TLV{Tag: tag, Type: tlvType, Value: normalizeValue(value)}, nil
}

// normalizeList handles the legacy list form: dict entries, 4-tuples, and 2/3-tuples.
// List order is wire order. Sub-2-element tuples and scalar entries are skipped — the
// oracle's only tolerated malformations; bad tag and type fields error like the oracle.
func normalizeList(list []any) ([]TLV, error) {
	if len(list) == 0 {
		return nil, nil
	}
	var out []TLV
	for _, entry := range list {
		switch e := entry.(type) {
		case map[string]any:
			t, err := tlvFromListDict(e)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		case []any:
			switch {
			case len(e) >= 4:
				t, err := tlvFromFourTuple(e)
				if err != nil {
					return nil, err
				}
				out = append(out, t)
			case len(e) >= 2:
				tag, err := tupleTag(e[0])
				if err != nil {
					return nil, err
				}
				out = append(out, TLV{Tag: tag, Value: normalizeValue(e[len(e)-1])})
			}
		default:
			// scalar entry: skipped, per the oracle
		}
	}
	return out, nil
}

// tlvFromListDict builds one TLV from a {"tag": ..., "value": ...} list entry. The tag
// falls back to key "0", then to literal 0; an explicitly null tag is an error (the
// oracle's int(None) TypeError), and a missing value rides through as nil (Python None).
func tlvFromListDict(e map[string]any) (TLV, error) {
	tagRaw, ok := e["tag"]
	if !ok {
		if tagRaw, ok = e["0"]; !ok {
			return TLV{Tag: big.NewInt(0), Value: normalizeValue(e["value"])}, nil
		}
	}
	tag, err := dictTag(tagRaw)
	if err != nil {
		return TLV{}, err
	}
	return TLV{Tag: tag, Value: normalizeValue(e["value"])}, nil
}

// tlvFromFourTuple carries a legacy (tag, length, type, value) entry into the typed
// shape. The oracle stores the tuple verbatim and int()s the tag only at encode time;
// the typed TLV shape forces that conversion here. A string type field travels verbatim —
// the frozen encoder trims it exactly where the oracle's .strip() does.
func tlvFromFourTuple(e []any) (TLV, error) {
	tag, err := tupleTag(e[0])
	if err != nil {
		return TLV{}, err
	}
	tlvType, err := tupleTypeField(e[2])
	if err != nil {
		return TLV{}, err
	}
	return TLV{Tag: tag, Length: lengthHint(e[1]), Type: tlvType, Value: normalizeValue(e[3])}, nil
}

// tupleTypeField ports the oracle's `(tlv_type or ”).strip()` treatment of a 4-tuple
// type field, hoisted from encode time: null and falsy scalars map to unresolved ""
// (KNOWN_QUIRKS Q-017 records the falsy-non-null decision), strings pass verbatim, and
// a truthy non-string errors — the oracle's AttributeError at encode.
func tupleTypeField(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		if !t {
			return "", nil
		}
	case json.Number:
		if f, err := t.Float64(); err == nil && f == 0 {
			return "", nil
		}
	case float64:
		if t == 0 {
			return "", nil
		}
	case int:
		if t == 0 {
			return "", nil
		}
	}
	return "", fmt.Errorf("unsupported TLV type field %v (%T)", v, v)
}

// lengthHint carries an integral numeric length as *int and drops everything else. The
// oracle carries the field verbatim, but no stage — resolve, validate, or encode — ever
// reads it, so lenient carriage is behavior-preserving across the whole pipeline.
func lengthHint(v any) *int {
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil && int64(int(i)) == i {
			l := int(i)
			return &l
		}
		if f, err := n.Float64(); err == nil {
			return lengthHint(f)
		}
	case float64:
		if n == math.Trunc(n) && n >= math.MinInt && n <= math.MaxInt {
			l := int(n)
			return &l
		}
	case int:
		l := n
		return &l
	case int64:
		if int64(int(n)) == n {
			l := int(n)
			return &l
		}
	}
	return nil
}

// normalizeValue prepares a decoded JSON value for the typed pipeline. An integral
// json.Number becomes the Python-int equivalent (int64, then uint64, then a canonical
// decimal string beyond uint64), so int-vs-string distinctions survive to the wire
// encoder exactly as they do in Python — the legacy wire path accepts integers where
// it crashes on strings, and only the resolve step coerces strings. A non-integral
// number becomes float64. Everything else passes through verbatim.
func normalizeValue(v any) any {
	n, ok := v.(json.Number)
	if !ok {
		return v
	}
	if i, ok := new(big.Int).SetString(string(n), 10); ok {
		if i.IsInt64() {
			return i.Int64()
		}
		if i.IsUint64() {
			return i.Uint64()
		}
		return i.String()
	}
	if f, err := n.Float64(); err == nil {
		return f
	}
	return string(n)
}

// dictTag ports Python int(x) for a tag drawn from a dict "tag"/"0" field, where string
// tags are 0x-aware (int(s, 16) when 0x-prefixed, else int(s)).
func dictTag(v any) (*big.Int, error) {
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if n, ok := parseTagInt(s); ok {
			return n, nil
		}
		return nil, &ValueError{Message: fmt.Sprintf("invalid integer tag %q", s)}
	}
	return nonStringTag(v)
}

// tupleTag ports Python int(x) for a tag drawn from a tuple position, where string tags
// are decimal-only — the oracle's bare int() raises on "0x..." here.
func tupleTag(v any) (*big.Int, error) {
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if n, ok := parsePythonDecimalInt(s, true); ok {
			return n, nil
		}
		return nil, &ValueError{Message: fmt.Sprintf("invalid integer tag %q", s)}
	}
	return nonStringTag(v)
}

// nonStringTag ports Python int(x) for non-string tag fields. The tag stays
// arbitrary-precision — masking is deferred to rule lookup and wire encoding, matching
// the frozen pipeline. Python raises TypeError on None and non-numeric shapes; those
// return plain errors, while unparseable numeric text is the oracle's ValueError.
func nonStringTag(v any) (*big.Int, error) {
	switch t := v.(type) {
	case json.Number:
		if i, ok := new(big.Int).SetString(string(t), 10); ok {
			return i, nil
		}
		if f, err := t.Float64(); err == nil {
			return truncTag(f)
		}
		return nil, &ValueError{Message: fmt.Sprintf("invalid integer tag %q", string(t))}
	case float64:
		return truncTag(t)
	case bool:
		if t {
			return big.NewInt(1), nil
		}
		return big.NewInt(0), nil
	case int:
		return big.NewInt(int64(t)), nil
	case int8:
		return big.NewInt(int64(t)), nil
	case int16:
		return big.NewInt(int64(t)), nil
	case int32:
		return big.NewInt(int64(t)), nil
	case int64:
		return big.NewInt(t), nil
	case uint:
		return new(big.Int).SetUint64(uint64(t)), nil
	case uint8:
		return big.NewInt(int64(t)), nil
	case uint16:
		return big.NewInt(int64(t)), nil
	case uint32:
		return big.NewInt(int64(t)), nil
	case uint64:
		return new(big.Int).SetUint64(t), nil
	case *big.Int:
		if t == nil {
			return nil, fmt.Errorf("nil big integer tag")
		}
		return new(big.Int).Set(t), nil
	case []byte:
		s := strings.Trim(string(t), " 	\n\r\v\f")
		if n, ok := parsePythonDecimalInt(s, false); ok { // Python int(bytes) is ASCII decimal-only
			return n, nil
		}
		return nil, &ValueError{Message: fmt.Sprintf("invalid integer tag %q", s)}
	case nil:
		return nil, fmt.Errorf("null TLV tag")
	default:
		return nil, fmt.Errorf("unsupported TLV tag type %T", v)
	}
}

// truncTag is Python int(float): truncation toward zero at full float precision.
func truncTag(f float64) (*big.Int, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, &ValueError{Message: fmt.Sprintf("cannot convert %v to an integer tag", f)}
	}
	i, _ := new(big.Float).SetFloat64(f).Int(nil)
	return i, nil
}

// orderedPair is one top-level JSON object member in document order.
type orderedPair struct {
	key   string
	value any
}

// normalizeJSONText ports the oracle's string branch: parse the JSON fully (rejecting
// trailing data, like json.loads), then normalize the parsed value. Parse failure is
// silent — (nil, nil) — matching the oracle's except → []; tag/type errors after a
// successful parse propagate. Parsing completes before any tag is examined, so a syntax
// error late in the text silences a bad tag earlier in it, exactly like the oracle.
func normalizeJSONText(s string) ([]TLV, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if s[0] == '{' {
		pairs, ok := decodeOrderedObject(dec)
		if !ok || !consumedAll(dec) {
			return nil, nil
		}
		if len(pairs) == 0 {
			return nil, nil
		}
		out := make([]TLV, 0, len(pairs))
		for _, p := range pairs {
			t, err := tlvFromDictKey(p.key, p.value)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
		return out, nil
	}
	var parsed any
	if err := dec.Decode(&parsed); err != nil || !consumedAll(dec) {
		return nil, nil
	}
	switch v := parsed.(type) {
	case []any:
		return normalizeList(v)
	case string:
		return Normalize(v) // the oracle recurses into a JSON-encoded string
	default:
		return nil, nil // bare scalar: not a dict/list/string shape → []
	}
}

// decodeOrderedObject consumes one complete JSON object from dec, preserving document
// key order with Python-dict duplicate-key semantics (last value wins, first position
// kept). false means the object was malformed.
func decodeOrderedObject(dec *json.Decoder) ([]orderedPair, bool) {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	var pairs []orderedPair
	index := make(map[string]int)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, false
		}
		var value any
		if err := dec.Decode(&value); err != nil {
			return nil, false
		}
		if i, dup := index[key]; dup {
			pairs[i].value = value
		} else {
			index[key] = len(pairs)
			pairs = append(pairs, orderedPair{key: key, value: value})
		}
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, false
	}
	return pairs, true
}

// consumedAll reports whether dec has no trailing JSON data — json.loads rejects
// "{} garbage", and the oracle turns that rejection into [].
func consumedAll(dec *json.Decoder) bool {
	_, err := dec.Token()
	return err == io.EOF
}

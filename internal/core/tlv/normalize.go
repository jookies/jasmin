package tlv

import (
	"encoding/json"
	"sort"
	"strings"
)

// Normalize accepts the various per-message custom_tlvs input shapes seen on the REST/HTTP
// boundary and normalizes them to a []TLV (the internal (tag, length, type, value) shape),
// porting normalize_custom_tlvs. It is the front door to the pipeline: the result feeds
// ResolveTLVTypes → ValidateCustomTLVs → EncodeCustomTLVs. Falsy/empty input yields nil.
//
// Accepted shapes (raw is a decoded JSON value or a JSON string):
//   - Preferred dict, type hint in the key: {"0x1401:OctetString": "...", "0x1400": "..."}
//   - Legacy list of 4-tuples:              [[5121, null, "Int8", 1707167205648943173]]
//   - List of dicts:                        [{"tag": "0x1401", "value": 123}, ...]
//   - A JSON string encoding any of the above (from URL query-param encoding).
//
// Gotcha: integer TLV values overflow float64 (19-digit vendor IDs). Decode JSON with
// json.Decoder.UseNumber() before calling Normalize, or pass a JSON string and let Normalize
// decode it (it uses UseNumber internally) — json.Number values are carried through as
// strings so EncodeValue can pack them without precision loss.
//
// Divergences from the Python oracle, both benign and documented: (1) the dict form's output
// is ordered by tag rather than by insertion order (Go maps have no insertion order; TLV
// order is not semantically significant — validation is by-tag and SMSCs treat the section
// as a set); (2) a malformed tag is skipped rather than raising (matching the module's
// tolerant handling of malformed list entries).
func Normalize(raw any) []TLV {
	switch v := raw.(type) {
	case nil:
		return nil
	case map[string]any:
		return normalizeDict(v)
	case []any:
		return normalizeList(v)
	case string:
		return normalizeJSONString(v)
	case []byte:
		return normalizeJSONString(string(v))
	default:
		return nil
	}
}

// normalizeDict handles the preferred dict shape: each key may carry a ":Type" hint. Output
// is tag-sorted for determinism (see Normalize's divergence note).
func normalizeDict(m map[string]any) []TLV {
	if len(m) == 0 {
		return nil
	}
	out := make([]TLV, 0, len(m))
	for key, value := range m {
		tag, tlvType, ok := ParseTagKey(key)
		if !ok {
			continue
		}
		out = append(out, TLV{Tag: tag, Type: tlvType, Value: normalizeValue(value)})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out
}

// normalizeList handles the legacy list form: entries are 4-tuples, 2+-tuples, or dicts.
// List order is preserved (it is faithful and semantically meaningful for the wire).
func normalizeList(list []any) []TLV {
	if len(list) == 0 {
		return nil
	}
	var out []TLV
	for _, entry := range list {
		switch e := entry.(type) {
		case map[string]any:
			// {"tag": "0x1401", "value": 123}: prefer "tag", fall back to "0".
			tagRaw, ok := e["tag"]
			if !ok {
				tagRaw = e["0"]
			}
			tag, ok := tagFromAny(tagRaw)
			if !ok {
				continue
			}
			out = append(out, TLV{Tag: tag, Value: normalizeValue(e["value"])})
		case []any:
			if len(e) >= 4 {
				tag, ok := tagFromAny(e[0])
				if !ok {
					continue
				}
				tlvType, _ := e[2].(string)
				out = append(out, TLV{
					Tag:    tag,
					Length: lengthFromAny(e[1]),
					Type:   tlvType,
					Value:  normalizeValue(e[3]),
				})
			} else if len(e) >= 2 {
				tag, ok := tagFromAny(e[0])
				if !ok {
					continue
				}
				out = append(out, TLV{Tag: tag, Value: normalizeValue(e[len(e)-1])})
			}
			// len < 2: skip malformed
		default:
			// scalar: skip
		}
	}
	return out
}

// normalizeJSONString parses a JSON string (with UseNumber to preserve large integers) and
// re-normalizes the result. An empty string or a parse error yields nil.
func normalizeJSONString(s string) []TLV {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var parsed any
	if err := dec.Decode(&parsed); err != nil {
		return nil
	}
	return Normalize(parsed)
}

// normalizeValue carries a value through unchanged, except a json.Number is rendered to its
// digit string so the downstream encoder packs it without float64 precision loss.
func normalizeValue(value any) any {
	if n, ok := value.(json.Number); ok {
		return n.String()
	}
	return value
}

// tagFromAny converts a tag drawn from decoded JSON (string, json.Number, float64, or a Go
// integer) into a 2-byte tag. Strings are hex when 0x-prefixed, else decimal.
func tagFromAny(v any) (uint16, bool) {
	switch t := v.(type) {
	case string:
		return parseTagInt(t)
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return uint16(uint64(n) & 0xFFFF), true
	case float64:
		return uint16(uint64(int64(t)) & 0xFFFF), true
	case int:
		return uint16(uint64(t) & 0xFFFF), true
	case int64:
		return uint16(uint64(t) & 0xFFFF), true
	case uint16:
		return t, true
	case uint64:
		return uint16(t & 0xFFFF), true
	default:
		return 0, false
	}
}

// lengthFromAny converts a tuple length field (JSON null or a number) into an optional int.
func lengthFromAny(v any) *int {
	switch n := v.(type) {
	case nil:
		return nil
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			return nil
		}
		l := int(i)
		return &l
	case float64:
		l := int(n)
		return &l
	case int:
		return &n
	case int64:
		l := int(n)
		return &l
	default:
		return nil
	}
}

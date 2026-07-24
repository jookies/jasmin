// Package tlv ports Jasmin's fork-local vendor-range custom-TLV pipeline
// (jasmin/tools/tlv_encoder.py): parse a per-message TLV tag key, resolve untyped TLVs
// against a connector's declared types, typed-encode each value onto the SMPP wire, and
// validate required/max-length rules. This is the MT/submit-side typed codec — distinct
// from internal/core/mo/tlv.go, which only renders MO-egress TLVs as a JSON array.
//
// The Python module's monkey-patch installers (encoder/decoder/wire-logger) are runtime
// patches of the vendored smpp.pdu library and Twisted transport; they are out of scope
// here — the Go smppwire codec owns wire-level TLV framing. Only the pure pipeline is ported.
//
// Gotcha: 19-digit vendor identifiers overflow float64 (2^53 ≈ 9e15). Never pass integer
// TLV values as float64 (e.g. straight from encoding/json's default number decoding) —
// EncodeValue rejects float64 to prevent silent precision loss. Pass int64/uint64 or a
// decimal/hex string instead.
package tlv

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
)

// SMPP TLV value-type names (_VALID_TLV_TYPES in the oracle).
const (
	TypeInt1         = "Int1"
	TypeInt2         = "Int2"
	TypeInt4         = "Int4"
	TypeInt8         = "Int8"
	TypeOctetString  = "OctetString"
	TypeCOctetString = "COctetString"
)

// intWidths maps the integer type names to their big-endian byte width (_TYPE_TO_INT_FMT:
// '>B' / '>H' / '>I' / '>Q').
var intWidths = map[string]int{
	TypeInt1: 1,
	TypeInt2: 2,
	TypeInt4: 4,
	TypeInt8: 8,
}

var validTypes = map[string]bool{
	TypeInt1: true, TypeInt2: true, TypeInt4: true, TypeInt8: true,
	TypeOctetString: true, TypeCOctetString: true,
}

// TLV is one custom (vendor-range) TLV in the Python (tag, length, type, value) shape.
// Type "" means unresolved (Python None); ResolveTLVTypes fills it in. Value may be a
// string, an integer (any int/uint width), or []byte (returned verbatim by EncodeValue).
type TLV struct {
	Tag    uint16
	Length *int // optional hint; encoding ignores it and uses the encoded body length
	Type   string
	Value  any
}

// ConnectorRule is one smppcc custom_tlvs config rule: a declared type for a tag, an
// optional max encoded length (nil = unbounded), and whether the tag is required.
type ConnectorRule struct {
	Tag      uint16
	Type     string
	Length   *int
	Required bool
}

// ParseTagKey parses a tag key that may carry an optional ":Type" hint — "0x1401",
// "0x1401:Int8", "5121", "5121:Int4". A hint applies only when it names a valid type;
// otherwise the whole key is treated as the tag string. The tag is hex when 0x-prefixed,
// else decimal. Returns ok=false when the tag is unparseable (Python raises ValueError).
func ParseTagKey(tagKey string) (tag uint16, tlvType string, ok bool) {
	tagKey = strings.TrimSpace(tagKey)
	tagStr := tagKey
	if i := strings.Index(tagKey, ":"); i >= 0 {
		candidateType := strings.TrimSpace(tagKey[i+1:])
		if validTypes[candidateType] {
			tlvType = candidateType
			tagStr = strings.TrimSpace(tagKey[:i])
		} else {
			tagStr = tagKey // colon is part of the tag string (defensive, per the oracle)
		}
	}
	t, ok := parseTagInt(tagStr)
	if !ok {
		return 0, "", false
	}
	return t, tlvType, true
}

// parseTagInt parses a tag as hex (0x-prefixed) or decimal, masking to the 2-byte tag space.
func parseTagInt(s string) (uint16, bool) {
	s = strings.TrimSpace(s)
	var n uint64
	var err error
	if len(s) >= 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		n, err = strconv.ParseUint(s[2:], 16, 64)
	} else {
		n, err = strconv.ParseUint(s, 10, 64)
	}
	if err != nil {
		return 0, false
	}
	return uint16(n & 0xFFFF), true
}

// ResolveTLVTypes fills in the type of every TLV whose Type is "" (Python None): a tag found
// in the connector rules takes that rule's type, a tag not in the rules defaults to
// OctetString. Already-typed TLVs pass through unchanged. When a resolved type is integer
// and the value is still a string, it is coerced to its integer (hex when 0x-prefixed, else
// decimal), matching resolve_tlv_types so the encoder receives a number.
func ResolveTLVTypes(tlvs []TLV, rules []ConnectorRule) []TLV {
	if len(tlvs) == 0 {
		return tlvs
	}
	ruleByTag := make(map[uint16]ConnectorRule, len(rules))
	for _, r := range rules {
		ruleByTag[r.Tag] = r
	}
	out := make([]TLV, 0, len(tlvs))
	for _, t := range tlvs {
		if t.Type != "" {
			out = append(out, t)
			continue
		}
		resolved := t
		if r, ok := ruleByTag[t.Tag]; ok {
			resolved.Type = r.Type
		} else {
			resolved.Type = TypeOctetString
		}
		if _, isInt := intWidths[resolved.Type]; isInt {
			if s, ok := resolved.Value.(string); ok {
				if n, ok := coerceIntString(s); ok {
					resolved.Value = n
				}
			}
		}
		out = append(out, resolved)
	}
	return out
}

// EncodeValue encodes a TLV value per its declared type (encode_tlv_value):
//   - []byte: returned verbatim (caller may pre-encode).
//   - Int1/2/4/8: big-endian packing; a negative or too-wide value is an error (struct.error).
//   - COctetString: UTF-8 of the value's string form plus a terminating NUL.
//   - OctetString / unknown / "": UTF-8 of the value's string form.
func EncodeValue(value any, tlvType string) ([]byte, error) {
	if b, ok := value.([]byte); ok {
		return append([]byte(nil), b...), nil
	}
	t := strings.TrimSpace(tlvType)
	if width, isInt := intWidths[t]; isInt {
		n, err := toUint(value)
		if err != nil {
			return nil, fmt.Errorf("tlv: %s: %w", t, err)
		}
		return packBigEndian(n, width, t)
	}
	s, err := strValue(value)
	if err != nil {
		return nil, err
	}
	if t == TypeCOctetString {
		return append([]byte(s), 0x00), nil
	}
	return []byte(s), nil
}

// EncodedValueLength returns the wire byte length a value would take for a type — the basis
// of connector max-length validation (encoded_value_length).
func EncodedValueLength(value any, tlvType string) (int, error) {
	b, err := EncodeValue(value, tlvType)
	if err != nil {
		return 0, err
	}
	return len(b), nil
}

// EncodeCustomTLVs serializes TLVs into concatenated SMPP TLV headers+bodies: for each,
// a 2-byte big-endian tag, a 2-byte big-endian body length, then the body (encode_custom_tlvs).
// Empty input yields nil. A value that fails to encode, or a body exceeding 65535 bytes
// (the 2-byte length field), is an error — Python would raise.
func EncodeCustomTLVs(tlvs []TLV) ([]byte, error) {
	if len(tlvs) == 0 {
		return nil, nil
	}
	var out []byte
	for _, t := range tlvs {
		body, err := EncodeValue(t.Value, t.Type)
		if err != nil {
			return nil, fmt.Errorf("tlv 0x%04X: %w", t.Tag, err)
		}
		if len(body) > 0xFFFF {
			return nil, fmt.Errorf("tlv 0x%04X: body length %d exceeds 65535", t.Tag, len(body))
		}
		header := make([]byte, 4)
		binary.BigEndian.PutUint16(header[0:2], t.Tag)
		binary.BigEndian.PutUint16(header[2:4], uint16(len(body)))
		out = append(out, header...)
		out = append(out, body...)
	}
	return out, nil
}

// RuleError is returned by ValidateCustomTLVs when a connector rule is violated. Its message
// matches the oracle's human strings.
type RuleError struct {
	Tag       uint16
	Missing   bool // a required tag was absent
	ActualLen int  // encoded length (length violations only)
	MaxLen    int  // configured max (length violations only)
}

func (e *RuleError) Error() string {
	if e.Missing {
		return fmt.Sprintf("missing required TLV 0x%04X", e.Tag)
	}
	return fmt.Sprintf("TLV 0x%04X value length %d exceeds configured max %d", e.Tag, e.ActualLen, e.MaxLen)
}

// ValidateCustomTLVs checks per-message TLVs against connector rules (validate_custom_tlvs):
// a required tag must be present, and an encoded value must not exceed the rule's max length
// (nil = unbounded). TLVs whose tag is not in the rules pass through untouched. Returns the
// first violation as a *RuleError, or nil when valid. With no rules, everything is allowed.
func ValidateCustomTLVs(tlvs []TLV, rules []ConnectorRule) error {
	if len(rules) == 0 {
		return nil
	}
	byTag := make(map[uint16]TLV, len(tlvs))
	for _, t := range tlvs {
		byTag[t.Tag] = t // last wins on duplicate tags, matching the Python dict
	}
	for _, rule := range rules {
		present, ok := byTag[rule.Tag]
		if !ok {
			if rule.Required {
				return &RuleError{Tag: rule.Tag, Missing: true}
			}
			continue
		}
		if rule.Length == nil {
			continue // unbounded
		}
		typeStr := present.Type
		if typeStr == "" {
			typeStr = rule.Type
		}
		actual, err := EncodedValueLength(present.Value, typeStr)
		if err != nil {
			return err
		}
		if actual > *rule.Length {
			return &RuleError{Tag: rule.Tag, ActualLen: actual, MaxLen: *rule.Length}
		}
	}
	return nil
}

// packBigEndian packs an unsigned integer into width big-endian bytes, erroring when it does
// not fit (mirroring struct.pack's overflow on the unsigned '>B'/'>H'/'>I'/'>Q' formats).
func packBigEndian(n uint64, width int, typeName string) ([]byte, error) {
	if width < 8 && n >= uint64(1)<<(8*uint(width)) {
		return nil, fmt.Errorf("value %d does not fit in %s (%d bytes)", n, typeName, width)
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, n)
	return buf[8-width:], nil
}

// toUint coerces a value to an unsigned integer for integer TLV types. Accepts signed and
// unsigned Go integers and numeric strings (Python int(value)); negative values and
// float64 are rejected — float64 because 64-bit vendor IDs lose precision as floats.
func toUint(value any) (uint64, error) {
	switch v := value.(type) {
	case string:
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid integer value %q", v)
		}
		return n, nil
	case int:
		return nonNegative(int64(v))
	case int8:
		return nonNegative(int64(v))
	case int16:
		return nonNegative(int64(v))
	case int32:
		return nonNegative(int64(v))
	case int64:
		return nonNegative(v)
	case uint:
		return uint64(v), nil
	case uint8:
		return uint64(v), nil
	case uint16:
		return uint64(v), nil
	case uint32:
		return uint64(v), nil
	case uint64:
		return v, nil
	case float64:
		return 0, fmt.Errorf("float64 value %v is unsafe for an integer TLV; pass int64/uint64 or a string", v)
	default:
		return 0, fmt.Errorf("unsupported integer TLV value type %T", value)
	}
}

func nonNegative(n int64) (uint64, error) {
	if n < 0 {
		return 0, fmt.Errorf("negative value %d", n)
	}
	return uint64(n), nil
}

// strValue mirrors Python str(value) for the OctetString/COctetString path. Strings pass
// through; integers format as decimal. []byte is handled earlier by EncodeValue.
func strValue(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case int:
		return strconv.Itoa(v), nil
	case int8:
		return strconv.FormatInt(int64(v), 10), nil
	case int16:
		return strconv.FormatInt(int64(v), 10), nil
	case int32:
		return strconv.FormatInt(int64(v), 10), nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case uint:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(v), 10), nil
	case uint64:
		return strconv.FormatUint(v, 10), nil
	case float64:
		return "", fmt.Errorf("float64 value %v is unsafe for a string TLV; pass a string or integer", v)
	default:
		return "", fmt.Errorf("unsupported string TLV value type %T", value)
	}
}

// coerceIntString parses an integer string for type resolution: hex when 0x-prefixed, else
// decimal (resolve_tlv_types' `int(value, 16 or 10)`). ok=false leaves the value unchanged.
func coerceIntString(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		n, err := strconv.ParseUint(s[2:], 16, 64)
		return n, err == nil
	}
	n, err := strconv.ParseUint(s, 10, 64)
	return n, err == nil
}

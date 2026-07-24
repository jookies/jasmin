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
	"math/big"
	"strconv"
	"strings"
	"unicode"
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
// string, an integer (Go int/uint widths or *big.Int), or []byte (returned verbatim by EncodeValue).
type TLV struct {
	Tag    *big.Int // preserves Python's arbitrary-precision intermediate tag domain
	Length *int     // optional hint; encoding ignores it and uses the encoded body length
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
func ParseTagKey(tagKey string) (tag *big.Int, tlvType string, err error) {
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
		return nil, "", &ValueError{Message: fmt.Sprintf("invalid integer tag %q", tagStr)}
	}
	return t, tlvType, nil
}

// ValueError represents the Python ValueError boundary used by parsing and type resolution.
type ValueError struct{ Message string }

func (e *ValueError) Error() string { return e.Message }

// WireError represents Python struct.error at an unsigned integer wire-width boundary.
type WireError struct{ Message string }

func (e *WireError) Error() string { return e.Message }

// parseTagInt preserves Python's arbitrary-precision signed integer domain. Masking is
// intentionally deferred to connector-rule lookup, validation, and final wire encoding.
func parseTagInt(s string) (*big.Int, bool) {
	s = strings.TrimSpace(s)
	base, digits := 10, s
	if len(s) >= 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		base, digits = 16, s[2:]
		if strings.HasPrefix(digits, "+") || strings.HasPrefix(digits, "-") {
			return nil, false
		}
	}
	digits, ok := normalizePythonIntDigits(digits, base, base == 16, true)
	if !ok {
		return nil, false
	}
	n, ok := new(big.Int).SetString(digits, base)
	return n, ok
}

// ResolveTLVTypes fills in the type of every TLV whose Type is "" (Python None): a tag found
// in the connector rules takes that rule's type, a tag not in the rules defaults to
// OctetString. Already-typed TLVs pass through unchanged. When a resolved type is integer
// and the value is still a string, it is coerced to its integer (hex when 0x-prefixed, else
// decimal), matching resolve_tlv_types so the encoder receives a number.
func ResolveTLVTypes(tlvs []TLV, rules []ConnectorRule) ([]TLV, error) {
	if len(tlvs) == 0 {
		return tlvs, nil
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
		if r, ok := ruleByTag[maskedTag(t.Tag)]; ok {
			resolved.Type = r.Type
		} else {
			resolved.Type = TypeOctetString
		}
		if _, isInt := intWidths[resolved.Type]; isInt {
			if s, ok := resolved.Value.(string); ok {
				n, err := coerceIntString(s)
				if err != nil {
					return nil, fmt.Errorf("tlv 0x%04X: resolve %s value %q: %w", maskedTag(resolved.Tag), resolved.Type, s, err)
				}
				resolved.Value = n
			}
		}
		out = append(out, resolved)
	}
	return out, nil
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
			return nil, fmt.Errorf("tlv 0x%04X: %w", maskedTag(t.Tag), err)
		}
		if len(body) > 0xFFFF {
			return nil, fmt.Errorf("tlv 0x%04X: body length %d exceeds 65535", maskedTag(t.Tag), len(body))
		}
		header := make([]byte, 4)
		binary.BigEndian.PutUint16(header[0:2], maskedTag(t.Tag))
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
		byTag[maskedTag(t.Tag)] = t // last wins after Python's & 0xFFFF normalization
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
func packBigEndian(n uint64, width int, _ string) ([]byte, error) {
	if width < 8 && n >= uint64(1)<<(8*uint(width)) {
		return nil, &WireError{Message: "int too large to convert"}
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
	case *big.Int:
		if v == nil {
			return 0, fmt.Errorf("nil big integer")
		}
		if v.Sign() < 0 {
			return 0, &WireError{Message: "int too large to convert"}
		}
		if !v.IsUint64() {
			return 0, &WireError{Message: "int too large to convert"}
		}
		return v.Uint64(), nil
	case float64:
		return 0, fmt.Errorf("float64 value %v is unsafe for an integer TLV; pass int64/uint64 or a string", v)
	default:
		return 0, fmt.Errorf("unsupported integer TLV value type %T", value)
	}
}

func nonNegative(n int64) (uint64, error) {
	if n < 0 {
		return 0, &WireError{Message: "int too large to convert"}
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
	case *big.Int:
		if v == nil {
			return "", fmt.Errorf("nil big integer")
		}
		return v.String(), nil
	case float64:
		return "", fmt.Errorf("float64 value %v is unsafe for a string TLV; pass a string or integer", v)
	default:
		return "", fmt.Errorf("unsupported string TLV value type %T", value)
	}
}

// coerceIntString parses an integer string for type resolution: hex when 0x-prefixed, else
// decimal (resolve_tlv_types' `int(value, 16 or 10)`). Invalid input is returned as an error
// so callers cannot pass unresolved text into the wire encoder.
func coerceIntString(s string) (any, error) {
	s = strings.TrimSpace(s)
	base := 10
	digits := s
	if len(s) >= 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		base = 16
		digits = s[2:]
		if strings.HasPrefix(digits, "+") || strings.HasPrefix(digits, "-") {
			return nil, &ValueError{Message: fmt.Sprintf("invalid integer value %q", s)}
		}
	}
	digits, ok := normalizePythonIntDigits(digits, base, base == 16, true)
	if !ok {
		return nil, &ValueError{Message: fmt.Sprintf("invalid integer value %q", s)}
	}
	n, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return nil, &ValueError{Message: fmt.Sprintf("invalid integer value %q", s)}
	}
	if n.IsUint64() {
		return n.Uint64(), nil
	}
	if n.IsInt64() {
		return n.Int64(), nil
	}
	return n, nil
}

// normalizePythonIntDigits mirrors the lexical subset accepted by Python int(text, base):
// Unicode decimal digits and single underscores between digits. Python also permits one
// underscore immediately after a recognized 0x prefix, which the caller has already removed.
func normalizePythonIntDigits(s string, base int, allowLeadingUnderscore, allowUnicode bool) (string, bool) {
	if s == "" {
		return "", false
	}
	var out strings.Builder
	previousDigit := false
	seenDigit := false
	leadingUnderscoreUsed := false
	for i, r := range s {
		if r == '+' || r == '-' {
			if i != 0 {
				return "", false
			}
			out.WriteRune(r)
			continue
		}
		if r == '_' {
			if i == len(s)-1 {
				return "", false
			}
			if !previousDigit {
				if !allowLeadingUnderscore || seenDigit || leadingUnderscoreUsed {
					return "", false
				}
				leadingUnderscoreUsed = true
			}
			previousDigit = false
			continue
		}
		digit, ok := pythonDigitValue(r, base, allowUnicode)
		if !ok {
			return "", false
		}
		out.WriteByte("0123456789abcdef"[digit])
		previousDigit = true
		seenDigit = true
	}
	return out.String(), seenDigit && previousDigit
}

func pythonDigitValue(r rune, base int, allowUnicode bool) (int, bool) {
	if r >= '0' && r <= '9' {
		v := int(r - '0')
		return v, v < base
	}
	if base == 16 {
		if r >= 'a' && r <= 'f' {
			return int(r-'a') + 10, true
		}
		if r >= 'A' && r <= 'F' {
			return int(r-'A') + 10, true
		}
	}
	if !allowUnicode || !unicode.Is(unicode.Nd, r) {
		return 0, false
	}
	for _, table := range unicode.Nd.R16 {
		r16 := uint16(r)
		if r <= unicode.MaxLatin1 || r <= 0xffff {
			if r16 >= table.Lo && r16 <= table.Hi && (r16-table.Lo)%table.Stride == 0 {
				v := int((r16-table.Lo)/table.Stride) % 10
				return v, v < base
			}
		}
	}
	for _, table := range unicode.Nd.R32 {
		r32 := uint32(r)
		if r32 >= table.Lo && r32 <= table.Hi && (r32-table.Lo)%table.Stride == 0 {
			v := int((r32-table.Lo)/table.Stride) % 10
			return v, v < base
		}
	}
	return 0, false
}

func parsePythonDecimalInt(s string, allowUnicode bool) (*big.Int, bool) {
	if allowUnicode {
		s = strings.TrimSpace(s)
	} else {
		s = strings.Trim(s, " 	\n\r\v\f")
	}
	digits, ok := normalizePythonIntDigits(s, 10, false, allowUnicode)
	if !ok {
		return nil, false
	}
	return new(big.Int).SetString(digits, 10)
}

func maskedTag(tag *big.Int) uint16 {
	if tag == nil {
		return 0
	}
	return uint16(new(big.Int).And(new(big.Int).Set(tag), big.NewInt(0xFFFF)).Uint64())
}

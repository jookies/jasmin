package tlv

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode"
)

// WireEncodeCustomTLVs ports the fork's actual wire emission for
// pdu.custom_tlvs: smpp.pdu3's PDUEncoder.encodeRawParams plus the fork's
// Int8 patch (jasmin/tools/tlv_encoder.py install_pdu_encoder_patch). This is
// a different oracle from EncodeCustomTLVs, which mirrors encode_tlv_value —
// the module used for connector length validation. The two disagree on
// purpose; parity means matching the wire function here and the validation
// function there (KNOWN_QUIRKS Q-018):
//
//   - Type names match exactly, untrimmed: "", "Foo", " Int8 ", and non-null
//     falsy types are silently skipped on the wire even though validation
//     measured them via the trim-and-fallback rules.
//   - Tags are not masked: anything outside 0..65535 is an error (the legacy
//     Int2Encoder raises and the listener rejects the message).
//   - Integer values must already be integers (bool included). The legacy
//     encoder crashes on strings and floats for Int1/2/4 — except Int8, whose
//     patch applies Python int(): decimal strings parse and floats truncate.
//   - Octet values must be strings or bytes; integers, floats, and nil crash
//     the legacy encoder (len() on a non-sized value), so they error here.
//     COctetString is ASCII-only (legacy .encode('ascii')) and NUL-terminated;
//     OctetString is UTF-8.
//   - A length hint different from the encoded length is an error: the legacy
//     path pads with a str and crashes when the hint is larger, and emits a
//     corrupt frame (length field lies) when smaller — the corrupt-frame case
//     is deliberately rejected instead of reproduced.
//
// Every error maps to the legacy outcome — the listener's rejectMessage — so
// callers should reject the message without requeue.
//
// Emission order mirrors the patch's split: non-Int8 entries first in tuple
// order (the upstream pass), then Int8 entries in tuple order (the patch's
// appended pass) — a mixed batch is reordered on the wire exactly like legacy.
func WireEncodeCustomTLVs(tlvs []TLV) ([]byte, error) {
	if len(tlvs) == 0 {
		return nil, nil
	}
	var out []byte
	for _, pass := range []string{"upstream", "int8"} {
		for _, t := range tlvs {
			if (t.Type == TypeInt8) != (pass == "int8") {
				continue
			}
			body, emit, err := wireEncodeValue(t)
			if err != nil {
				return nil, fmt.Errorf("tlv %s: %w", tagText(t.Tag), err)
			}
			if !emit {
				continue // unknown type name: the legacy encoder's `else: continue`
			}
			tag, err := wireTag(t.Tag)
			if err != nil {
				return nil, err
			}
			if t.Length != nil && *t.Length != len(body) {
				return nil, fmt.Errorf("tlv %s: length hint %d does not match encoded length %d (legacy pads-and-crashes or emits a corrupt frame)",
					tagText(t.Tag), *t.Length, len(body))
			}
			if len(body) > 0xFFFF {
				return nil, fmt.Errorf("tlv %s: body length %d exceeds 65535", tagText(t.Tag), len(body))
			}
			header := make([]byte, 4)
			binary.BigEndian.PutUint16(header[0:2], tag)
			binary.BigEndian.PutUint16(header[2:4], uint16(len(body)))
			out = append(out, header...)
			out = append(out, body...)
		}
	}
	return out, nil
}

// wireTag enforces the legacy Int2Encoder domain: an unmasked 0..65535 tag.
func wireTag(tag *big.Int) (uint16, error) {
	if tag == nil || tag.Sign() < 0 || !tag.IsUint64() || tag.Uint64() > 0xFFFF {
		return 0, fmt.Errorf("tlv %s: tag is outside 0..65535 (legacy Int2Encoder raises)", tagText(tag))
	}
	return uint16(tag.Uint64()), nil
}

// wireEncodeValue returns the encoded body for one TLV, or emit=false when the
// legacy encoder would skip the entry (unmatched type name).
func wireEncodeValue(t TLV) (body []byte, emit bool, err error) {
	switch t.Type {
	case TypeInt1:
		body, err = wireInt(t.Value, 1)
	case TypeInt2:
		body, err = wireInt(t.Value, 2)
	case TypeInt4:
		body, err = wireInt(t.Value, 4)
	case TypeInt8:
		body, err = wireInt8(t.Value)
	case TypeOctetString:
		body, err = wireOctets(t.Value, false)
	case TypeCOctetString:
		body, err = wireOctets(t.Value, true)
	default:
		return nil, false, nil
	}
	return body, err == nil, err
}

// wireInt ports IntegerBaseEncoder._encode for Int1/2/4: an integer (bool
// included) within 0..max; strings and floats are the legacy TypeError and
// struct.error crashes.
func wireInt(value any, width int) ([]byte, error) {
	n, ok, err := pythonIntValue(value)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%T value is not an integer (legacy raises)", value)
	}
	maxValue := new(big.Int).Lsh(big.NewInt(1), uint(8*width))
	maxValue.Sub(maxValue, big.NewInt(1))
	if n.Sign() < 0 || n.Cmp(maxValue) > 0 {
		return nil, fmt.Errorf("value %s is outside 0..%s", n.String(), maxValue.String())
	}
	return bigEndianBytes(n, width), nil
}

// wireInt8 ports the fork's Int8 patch: struct.pack('>Q', int(value)) — so
// decimal strings parse and floats truncate toward zero, unlike Int1/2/4.
func wireInt8(value any) ([]byte, error) {
	n, ok, err := pythonIntValue(value)
	if err != nil {
		return nil, err
	}
	if !ok {
		switch v := value.(type) {
		case string:
			parsed, parsedOK := parsePythonDecimalInt(strings.TrimSpace(v), true)
			if !parsedOK {
				return nil, fmt.Errorf("invalid integer value %q", v)
			}
			n = parsed
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil, fmt.Errorf("cannot convert %v to an integer", v)
			}
			n, _ = new(big.Float).SetFloat64(v).Int(nil)
		default:
			return nil, fmt.Errorf("%T value is not an integer (legacy int() raises)", value)
		}
	}
	maxValue := new(big.Int).Lsh(big.NewInt(1), 64)
	maxValue.Sub(maxValue, big.NewInt(1))
	if n.Sign() < 0 || n.Cmp(maxValue) > 0 {
		return nil, fmt.Errorf("value %s does not fit in Int8 (legacy struct raises)", n.String())
	}
	return bigEndianBytes(n, 8), nil
}

// wireOctets ports OctetStringEncoder/COctetStringEncoder._encode: strings and
// bytes only — the legacy len(value) crashes on everything else. COctetString
// is ASCII-only and NUL-terminated.
func wireOctets(value any, cOctet bool) ([]byte, error) {
	var body []byte
	switch v := value.(type) {
	case string:
		if cOctet {
			for _, r := range v {
				if r > unicode.MaxASCII {
					return nil, fmt.Errorf("COctetString value %q is not ASCII (legacy raises UnicodeEncodeError)", v)
				}
			}
		}
		body = []byte(v)
	case []byte:
		body = append([]byte(nil), v...)
	default:
		return nil, fmt.Errorf("%T value is not a string or bytes (legacy raises)", value)
	}
	if cOctet {
		body = append(body, 0x00)
	}
	return body, nil
}

// pythonIntValue reports whether value is a Python-int equivalent (bool
// included) and returns it at arbitrary precision. ok=false means the value
// is some other type; err is reserved for nil *big.Int corruption.
func pythonIntValue(value any) (*big.Int, bool, error) {
	switch v := value.(type) {
	case bool:
		if v {
			return big.NewInt(1), true, nil
		}
		return big.NewInt(0), true, nil
	case int:
		return big.NewInt(int64(v)), true, nil
	case int8:
		return big.NewInt(int64(v)), true, nil
	case int16:
		return big.NewInt(int64(v)), true, nil
	case int32:
		return big.NewInt(int64(v)), true, nil
	case int64:
		return big.NewInt(v), true, nil
	case uint:
		return new(big.Int).SetUint64(uint64(v)), true, nil
	case uint8:
		return big.NewInt(int64(v)), true, nil
	case uint16:
		return big.NewInt(int64(v)), true, nil
	case uint32:
		return big.NewInt(int64(v)), true, nil
	case uint64:
		return new(big.Int).SetUint64(v), true, nil
	case *big.Int:
		if v == nil {
			return nil, false, fmt.Errorf("nil big integer value")
		}
		return new(big.Int).Set(v), true, nil
	default:
		return nil, false, nil
	}
}

// bigEndianBytes packs a non-negative integer that fits width bytes.
func bigEndianBytes(n *big.Int, width int) []byte {
	out := make([]byte, width)
	n.FillBytes(out)
	return out
}

// tagText renders a tag for error messages without assuming it fits uint16.
func tagText(tag *big.Int) string {
	if tag == nil {
		return "<nil>"
	}
	if tag.Sign() >= 0 && tag.IsUint64() && tag.Uint64() <= 0xFFFF {
		return fmt.Sprintf("0x%04X", tag.Uint64())
	}
	return tag.String()
}

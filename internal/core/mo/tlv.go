package mo

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// StandardOptionalParams is the fixed order in which jasmin's deliverSmThrower emits the
// standard optional TLVs into tlv_params (routing/throwers.py). The caller supplies present
// params in this order so the tlv_params JSON is byte-identical.
var StandardOptionalParams = []string{
	"user_message_reference", "source_port", "destination_port",
	"sar_msg_ref_num", "sar_total_segments", "sar_segment_seqnum",
	"payload_type", "privacy_indicator", "callback_num",
	"language_indicator", "its_session_info", "network_error_code",
	"message_state", "receipted_message_id",
}

// TLVParam is one standard optional TLV to forward, already formatted the Jasmin way
// (hex for bytes, enum .name, or str()). Ordered slices preserve json.dumps key order.
type TLVParam struct {
	Name  string
	Value string
}

// CustomTLV is one vendor TLV to forward. Value is already hex-encoded when it was bytes,
// matching jasmin's custom_tlvs_data entries.
type CustomTLV struct {
	Tag    int
	Length int
	Type   string
	Value  string
}

// encodeTLVParams renders the ordered params as a JSON object byte-identical to Python's
// json.dumps default (", " / ": " separators, insertion order, no HTML escaping).
func encodeTLVParams(params []TLVParam) string {
	var b strings.Builder
	b.WriteByte('{')
	for i, p := range params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(jsonString(p.Name))
		b.WriteString(": ")
		b.WriteString(jsonString(p.Value))
	}
	b.WriteByte('}')
	return b.String()
}

// encodeCustomTLVs renders the vendor TLVs as a JSON array of {tag, length, type, value}
// objects, byte-identical to json.dumps (tag/length bare integers, type/value strings).
func encodeCustomTLVs(tlvs []CustomTLV) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, t := range tlvs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(`{"tag": `)
		b.WriteString(strconv.Itoa(t.Tag))
		b.WriteString(`, "length": `)
		b.WriteString(strconv.Itoa(t.Length))
		b.WriteString(`, "type": `)
		b.WriteString(jsonString(t.Type))
		b.WriteString(`, "value": `)
		b.WriteString(jsonString(t.Value))
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return b.String()
}

// jsonString JSON-encodes a string with HTML escaping disabled, matching json.dumps for
// ASCII values (the only values Jasmin emits here: hex, enum names, decimals).
func jsonString(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimRight(buf.String(), "\n")
}

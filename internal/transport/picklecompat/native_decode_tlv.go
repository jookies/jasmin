package picklecompat

import (
	"encoding/binary"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/pumpitspace/jasmin/internal/transport/gopickle"
)

// projectOptionalTLVs projects the SAR / message_payload params into the
// bridge's optional_tlvs shape. more_messages_to_send is not yet ported.
func projectOptionalTLVs(params gopickle.Dict) ([]submitSMOptionalTLV, error) {
	var tlvs []submitSMOptionalTLV
	for _, integer := range []struct {
		key  string
		tag  uint16
		size int
	}{
		{"user_message_reference", 0x0204, 2},
		{"source_port", 0x020a, 2},
		{"destination_port", 0x020b, 2},
	} {
		if value, ok := paramUint(params, integer.key); ok {
			if value >= uint64(1)<<(integer.size*8) {
				return nil, poisonSubmitError("%s outside its wire width", integer.key)
			}
			buffer := make([]byte, integer.size)
			binary.BigEndian.PutUint16(buffer, uint16(value))
			tlvs = append(tlvs, submitSMOptionalTLV{Tag: integer.tag, Value: Bytes(buffer)})
		}
	}
	for _, integer := range []struct {
		key string
		tag uint16
		max uint64
	}{
		{"user_response_code", 0x0205, 0xff},
		{"number_of_messages", 0x0304, 99},
	} {
		if value, ok := paramUint(params, integer.key); ok {
			if value > integer.max {
				return nil, poisonSubmitError("%s outside its wire range", integer.key)
			}
			tlvs = append(tlvs, submitSMOptionalTLV{Tag: integer.tag, Value: Bytes{byte(value)}})
		}
	}
	for _, sar := range []struct {
		key  string
		tag  uint16
		size int
	}{
		{"sar_msg_ref_num", 0x020c, 2},
		{"sar_total_segments", 0x020e, 1},
		{"sar_segment_seqnum", 0x020f, 1},
	} {
		value := paramValue(params, sar.key)
		if value == nil {
			continue
		}
		if _, isNone := value.(gopickle.None); isNone {
			continue
		}
		integer, ok := value.(gopickle.Int)
		if !ok || integer < 0 || int64(integer) >= int64(1)<<(sar.size*8) {
			return nil, poisonSubmitError("%s outside its wire width", sar.key)
		}
		buffer := make([]byte, sar.size)
		if sar.size == 2 {
			binary.BigEndian.PutUint16(buffer, uint16(integer))
		} else {
			buffer[0] = byte(integer)
		}
		tlvs = append(tlvs, submitSMOptionalTLV{Tag: sar.tag, Value: Bytes(buffer)})
	}
	if payload, ok := paramValue(params, "message_payload").(gopickle.Bytes); ok {
		tlvs = append(tlvs, submitSMOptionalTLV{Tag: 0x0424, Value: Bytes(payload)})
	}
	for _, enum := range []struct {
		key  string
		name string
		tag  uint16
		max  uint8
	}{
		{"payload_type", "PayloadType", 0x0019, 1},
		{"privacy_indicator", "PrivacyIndicator", 0x0201, 3},
		{"language_indicator", "LanguageIndicator", 0x020d, 5},
		{"more_messages_to_send", "MoreMessagesToSend", 0x0426, 1},
		{"source_addr_subunit", "AddrSubunit", 0x000d, 4},
		{"dest_addr_subunit", "AddrSubunit", 0x0005, 4},
		{"display_time", "DisplayTime", 0x1201, 2},
	} {
		value := paramValue(params, enum.key)
		if value == nil {
			continue
		}
		if _, isNone := value.(gopickle.None); isNone {
			continue
		}
		ordinal, name, ok := enumReduceOrdinal(value)
		wire := ordinal - 1
		if !ok || name != enum.name || wire < 0 || wire > int(enum.max) {
			return nil, poisonSubmitError("%s is not a valid %s", enum.key, enum.name)
		}
		tlvs = append(tlvs, submitSMOptionalTLV{Tag: enum.tag, Value: Bytes{byte(wire)}})
	}
	if value := paramValue(params, "callback_num"); value != nil {
		if _, isNone := value.(gopickle.None); !isNone {
			wire, err := callbackNumberWire(value)
			if err != nil {
				return nil, err
			}
			tlvs = append(tlvs, submitSMOptionalTLV{Tag: 0x0381, Value: Bytes(wire)})
		}
	}
	for _, subaddress := range []struct {
		key string
		tag uint16
	}{
		{"source_subaddress", 0x0202},
		{"dest_subaddress", 0x0203},
	} {
		value := paramValue(params, subaddress.key)
		if value == nil {
			continue
		}
		if _, isNone := value.(gopickle.None); isNone {
			continue
		}
		wire, err := subaddressWire(value)
		if err != nil {
			return nil, err
		}
		tlvs = append(tlvs, submitSMOptionalTLV{Tag: subaddress.tag, Value: Bytes(wire)})
	}
	if signal, ok := paramValue(params, "sms_signal").(gopickle.Bytes); ok {
		tlvs = append(tlvs, submitSMOptionalTLV{Tag: 0x1203, Value: Bytes(signal)})
	}
	return tlvs, nil
}

var subaddressTypeTagOrdinalToWire = map[int]byte{
	1: 0x80,
	2: 0x88,
	3: 0xa0,
	4: 0x00,
}

func subaddressWire(value gopickle.Value) ([]byte, error) {
	object, ok := value.(gopickle.Object)
	if !ok || object.Class.Module != "smpp.pdu.pdu_types" || object.Class.Name != "Subaddress" {
		return nil, poisonSubmitError("subaddress is not a Subaddress")
	}
	args, ok := object.Args.(gopickle.Tuple)
	if !ok || len(args) != 2 {
		return nil, poisonSubmitError("subaddress args malformed")
	}
	ordinal, name, ok := enumReduceOrdinal(args[0])
	typeTag, known := subaddressTypeTagOrdinalToWire[ordinal]
	if !ok || name != "SubaddressTypeTag" || !known {
		return nil, poisonSubmitError("subaddress type tag malformed")
	}
	data, ok := args[1].(gopickle.Bytes)
	if !ok {
		return nil, poisonSubmitError("subaddress value malformed")
	}
	return append([]byte{typeTag}, data...), nil
}

func callbackNumberWire(value gopickle.Value) ([]byte, error) {
	object, ok := value.(gopickle.Object)
	if !ok || object.Class.Module != "smpp.pdu.pdu_types" || object.Class.Name != "CallbackNum" {
		return nil, poisonSubmitError("callback_num is not a CallbackNum")
	}
	args, ok := object.Args.(gopickle.Tuple)
	if !ok || len(args) != 4 {
		return nil, poisonSubmitError("callback_num args malformed")
	}
	digitOrdinal, digitName, ok := enumReduceOrdinal(args[0])
	if !ok || digitName != "CallbackNumDigitModeIndicator" || digitOrdinal < 1 || digitOrdinal > 2 {
		return nil, poisonSubmitError("callback_num digit mode malformed")
	}
	tonOrdinal, tonName, ok := enumReduceOrdinal(args[1])
	ton, tonKnown := addrTONOrdinalToWire[tonOrdinal]
	if !ok || tonName != "AddrTon" || !tonKnown {
		return nil, poisonSubmitError("callback_num TON malformed")
	}
	npiOrdinal, npiName, ok := enumReduceOrdinal(args[2])
	npi, npiKnown := addrNPIOrdinalToWire[npiOrdinal]
	if !ok || npiName != "AddrNpi" || !npiKnown {
		return nil, poisonSubmitError("callback_num NPI malformed")
	}
	digits, ok := args[3].(gopickle.Bytes)
	if !ok {
		return nil, poisonSubmitError("callback_num digits malformed")
	}
	return append([]byte{byte(digitOrdinal - 1), ton, npi}, digits...), nil
}

// projectCustomTLVTuples projects pdu.custom_tlvs into the bridge's
// [tag, length, type, value] JSON entries (which buildSubmitPart then feeds to
// decodeWireCustomTLVs). Empty / absent yields nil.
func projectCustomTLVTuples(state gopickle.Dict) ([]json.RawMessage, error) {
	list, ok := paramValue(state, "custom_tlvs").(gopickle.List)
	if !ok || len(list) == 0 {
		return nil, nil
	}
	return customTLVTuplesJSON(list)
}

func sortInts(values []int) { sort.Ints(values) }

// joinOrdinals renders sorted ordinals as the comma-joined component key
// (matching the code-generated component tables).
func joinOrdinals(values []int) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

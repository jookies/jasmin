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
	if value := paramValue(params, "more_messages_to_send"); value != nil {
		if _, isNone := value.(gopickle.None); !isNone {
			return nil, poisonSubmitError("more_messages_to_send decoding not yet ported")
		}
	}
	return tlvs, nil
}

// projectCustomTLVTuples projects pdu.custom_tlvs. Empty is the common case;
// non-empty custom TLV decoding is not yet ported (symmetric with encode).
func projectCustomTLVTuples(state gopickle.Dict) ([]json.RawMessage, error) {
	list, ok := paramValue(state, "custom_tlvs").(gopickle.List)
	if !ok || len(list) == 0 {
		return nil, nil
	}
	return nil, poisonSubmitError("custom TLV decoding not yet ported")
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

package smppwire

import (
	"encoding/binary"
	"testing"
)

// deliverSMFrame builds a minimal deliver_sm with the given optional-parameter
// bytes appended, so a test can inject exactly one off-spec TLV combination.
func deliverSMFrame(shortMessage []byte, optional []byte) []byte {
	body := []byte{}
	body = append(body, 0)                     // service_type
	body = append(body, 0, 0, '1', '0', '0', 0) // source: ton, npi, addr
	body = append(body, 0, 0, '2', '0', '0', 0) // destination
	body = append(body, 0)                     // esm_class
	body = append(body, 0)                     // protocol_id
	body = append(body, 0)                     // priority_flag
	body = append(body, 0)                     // schedule_delivery_time
	body = append(body, 0)                     // validity_period
	body = append(body, 0)                     // registered_delivery
	body = append(body, 0)                     // replace_if_present
	body = append(body, 0)                     // data_coding
	body = append(body, 0)                     // sm_default_msg_id
	body = append(body, byte(len(shortMessage)))
	body = append(body, shortMessage...)
	body = append(body, optional...)

	frame := make([]byte, 16+len(body))
	binary.BigEndian.PutUint32(frame[0:], uint32(16+len(body)))
	binary.BigEndian.PutUint32(frame[4:], uint32(CommandDeliverSM))
	binary.BigEndian.PutUint32(frame[8:], 0)
	binary.BigEndian.PutUint32(frame[12:], 7)
	copy(frame[16:], body)
	return frame
}

func tlv(tag uint16, value ...byte) []byte {
	out := make([]byte, 4+len(value))
	binary.BigEndian.PutUint16(out[0:], tag)
	binary.BigEndian.PutUint16(out[2:], uint16(len(value)))
	copy(out[4:], value)
	return out
}

// TestDeliverSMToleratesCrossTLVViolations is the regression for a total MO
// outage.
//
// decodeTLVs deliberately tolerates a bad optional parameter on an inbound
// deliver_sm, because the client read loop treats a decode error as fatal and
// the SMSC redelivers the identical bytes after every rebind — so one off-spec
// TLV from a carrier becomes a permanent connect/decode/disconnect loop. Two
// cross-TLV rules ran outside that tolerance and defeated it: an incomplete SAR
// trio, and message_payload alongside a non-empty short_message. Real carriers
// send both.
func TestDeliverSMToleratesCrossTLVViolations(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		short    []byte
		optional []byte
	}{
		{
			name:     "sar_msg_ref_num without its siblings",
			short:    []byte("hello"),
			optional: tlv(tagSARMessageRef, 0x00, 0x2a),
		},
		{
			name:  "sar_segment_seqnum beyond sar_total_segments",
			short: []byte("hello"),
			optional: concat(
				tlv(tagSARMessageRef, 0x00, 0x2a),
				tlv(tagSARTotalSegments, 0x02),
				tlv(tagSARSegmentSequence, 0x05),
			),
		},
		{
			name:     "message_payload alongside short_message",
			short:    []byte("hello"),
			optional: tlv(tagMessagePayload, 'w', 'o', 'r', 'l', 'd'),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			pdu, err := Decode(deliverSMFrame(testCase.short, testCase.optional))
			if err != nil {
				t.Fatalf("deliver_sm rejected: %v — one off-spec carrier PDU takes MO down for the connector", err)
			}
			if pdu.SM == nil {
				t.Fatal("no deliver_sm body decoded")
			}
		})
	}
}

// The same violations must still be refused on submit_sm, where the sender is a
// customer who gets an actionable error status and can fix their PDU.
func TestSubmitSMStillRefusesCrossTLVViolations(t *testing.T) {
	frame := deliverSMFrame([]byte("hello"), tlv(tagSARMessageRef, 0x00, 0x2a))
	binary.BigEndian.PutUint32(frame[4:], uint32(CommandSubmitSM))
	if _, err := Decode(frame); err == nil {
		t.Fatal("submit_sm accepted an incomplete SAR group")
	}
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

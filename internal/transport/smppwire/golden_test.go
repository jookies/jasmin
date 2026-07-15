package smppwire_test

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type goldenDocument struct {
	Cases []goldenCase `json:"cases"`
}

type goldenCase struct {
	ID               string        `json:"id"`
	Direction        string        `json:"direction"`
	WireHex          string        `json:"wire_hex"`
	Decoded          goldenDecoded `json:"decoded"`
	RoundTripWireHex *string       `json:"roundtrip_wire_hex"`
	RoundTripError   *string       `json:"roundtrip_error"`
}

type goldenDecoded struct {
	CommandID      string                     `json:"command_id"`
	CommandStatus  string                     `json:"command_status"`
	SequenceNumber uint32                     `json:"sequence_number"`
	Parameters     map[string]json.RawMessage `json:"parameters"`
}

type goldenBytes struct {
	Hex string `json:"hex"`
}

func TestGoldenDecodeAndRoundTrip(t *testing.T) {
	document := loadSMPPGolden(t)
	if len(document.Cases) != 7 {
		t.Fatalf("fixture cases = %d, want 7", len(document.Cases))
	}

	for _, tc := range document.Cases {
		t.Run(tc.ID, func(t *testing.T) {
			wire := decodeHex(t, tc.WireHex)
			pdu, err := smppwire.Decode(wire)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			assertHeader(t, pdu, tc)
			assertSemanticProjection(t, pdu, tc)

			roundTrip, err := smppwire.Encode(pdu)
			if tc.RoundTripError != nil {
				if !errors.Is(err, smppwire.ErrLegacyMessagePayloadRoundTrip) {
					t.Fatalf("round-trip error = %v, want ErrLegacyMessagePayloadRoundTrip (%s)", err, *tc.RoundTripError)
				}
				if roundTrip != nil {
					t.Fatalf("round-trip bytes = %x, want nil on compatibility error", roundTrip)
				}
				return
			}
			if err != nil {
				t.Fatalf("Encode(decoded): %v", err)
			}
			if tc.RoundTripWireHex == nil {
				t.Fatal("fixture has neither round-trip bytes nor error")
			}
			want := decodeHex(t, *tc.RoundTripWireHex)
			if !bytes.Equal(roundTrip, want) {
				t.Errorf("round-trip bytes\n got: %x\nwant: %x", roundTrip, want)
			}
		})
	}
}

func TestGoldenTypedEncoding(t *testing.T) {
	document := loadSMPPGolden(t)
	encoded := 0
	for _, tc := range document.Cases {
		if tc.Direction != "encode" {
			continue
		}
		encoded++
		t.Run(tc.ID, func(t *testing.T) {
			pdu := typedPDUFromGolden(t, tc)
			wire, err := smppwire.Encode(pdu)
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			want := decodeHex(t, tc.WireHex)
			if !bytes.Equal(wire, want) {
				t.Errorf("wire bytes\n got: %x\nwant: %x", wire, want)
			}
		})
	}
	if encoded != 5 {
		t.Fatalf("encode-direction cases = %d, want 5", encoded)
	}
}

func typedPDUFromGolden(t *testing.T, tc goldenCase) smppwire.PDU {
	t.Helper()
	pdu := smppwire.PDU{Header: smppwire.Header{
		CommandID:      commandID(t, tc.Decoded.CommandID),
		CommandStatus:  0,
		SequenceNumber: tc.Decoded.SequenceNumber,
	}}

	switch tc.Decoded.CommandID {
	case "CommandId.bind_transceiver":
		pdu.Bind = &smppwire.BindBody{
			SystemID:         parameterBytes(t, tc, "system_id"),
			Password:         parameterBytes(t, tc, "password"),
			SystemType:       parameterBytes(t, tc, "system_type"),
			InterfaceVersion: byte(parameterInt(t, tc, "interface_version")),
			AddressRange:     parameterBytes(t, tc, "address_range"),
		}
	case "CommandId.submit_sm", "CommandId.deliver_sm":
		body := &smppwire.SMBody{
			ServiceType:        parameterBytes(t, tc, "service_type"),
			SourceAddress:      parameterBytes(t, tc, "source_addr"),
			DestinationAddress: parameterBytes(t, tc, "destination_addr"),
			ProtocolID:         byte(parameterInt(t, tc, "protocol_id")),
			ShortMessage:       parameterBytes(t, tc, "short_message"),
			SMDefaultMessageID: byte(parameterInt(t, tc, "sm_default_msg_id")),
		}
		if value, ok := optionalInt(t, tc, "sar_msg_ref_num"); ok {
			v := uint16(value)
			body.Optional.SARMessageReference = &v
		}
		if value, ok := optionalInt(t, tc, "sar_total_segments"); ok {
			v := byte(value)
			body.Optional.SARTotalSegments = &v
		}
		if value, ok := optionalInt(t, tc, "sar_segment_seqnum"); ok {
			v := byte(value)
			body.Optional.SARSegmentSequence = &v
		}
		pdu.SM = body
	case "CommandId.submit_sm_resp":
		pdu.SubmitResponse = &smppwire.SubmitResponseBody{
			MessageID: parameterBytes(t, tc, "message_id"),
		}
	default:
		t.Fatalf("unsupported fixture command: %s", tc.Decoded.CommandID)
	}
	return pdu
}

func assertHeader(t *testing.T, pdu smppwire.PDU, tc goldenCase) {
	t.Helper()
	if pdu.Header.CommandID != commandID(t, tc.Decoded.CommandID) {
		t.Errorf("command ID = %#x, want %#x", pdu.Header.CommandID, commandID(t, tc.Decoded.CommandID))
	}
	if pdu.Header.CommandStatus != 0 {
		t.Errorf("command status = %#x, want 0", pdu.Header.CommandStatus)
	}
	if pdu.Header.SequenceNumber != tc.Decoded.SequenceNumber {
		t.Errorf("sequence = %d, want %d", pdu.Header.SequenceNumber, tc.Decoded.SequenceNumber)
	}
}

func assertSemanticProjection(t *testing.T, pdu smppwire.PDU, tc goldenCase) {
	t.Helper()
	switch tc.Decoded.CommandID {
	case "CommandId.bind_transceiver":
		if pdu.Bind == nil {
			t.Fatal("Bind body is nil")
		}
		assertBytes(t, "system_id", pdu.Bind.SystemID, parameterBytes(t, tc, "system_id"))
		assertBytes(t, "password", pdu.Bind.Password, parameterBytes(t, tc, "password"))
	case "CommandId.submit_sm", "CommandId.deliver_sm":
		if pdu.SM == nil {
			t.Fatal("SM body is nil")
		}
		assertBytes(t, "source_addr", pdu.SM.SourceAddress, parameterBytes(t, tc, "source_addr"))
		assertBytes(t, "destination_addr", pdu.SM.DestinationAddress, parameterBytes(t, tc, "destination_addr"))
		assertBytes(t, "short_message", pdu.SM.ShortMessage, parameterBytes(t, tc, "short_message"))
		assertRawSMControls(t, pdu.SM, tc.ID)
		if expected, ok := optionalInt(t, tc, "sar_msg_ref_num"); ok {
			if pdu.SM.Optional.SARMessageReference == nil || int(*pdu.SM.Optional.SARMessageReference) != expected {
				t.Errorf("SAR reference = %v, want %d", pdu.SM.Optional.SARMessageReference, expected)
			}
		}
		if raw, ok := tc.Decoded.Parameters["message_payload"]; ok {
			assertBytes(t, "message_payload", pdu.SM.Optional.MessagePayload, decodeGoldenBytes(t, raw))
		}
		if raw, ok := tc.Decoded.Parameters["receipted_message_id"]; ok {
			assertBytes(t, "receipted_message_id", pdu.SM.Optional.ReceiptedMessageID, decodeGoldenBytes(t, raw))
		}
		if expected, ok := optionalEnumByte(t, tc, "message_state", map[string]byte{"MessageState.DELIVERED": 2}); ok {
			if pdu.SM.Optional.MessageState == nil || *pdu.SM.Optional.MessageState != expected {
				t.Errorf("message state = %v, want %d", pdu.SM.Optional.MessageState, expected)
			}
		}
	case "CommandId.submit_sm_resp":
		if pdu.SubmitResponse == nil {
			t.Fatal("submit response body is nil")
		}
		assertBytes(t, "message_id", pdu.SubmitResponse.MessageID, parameterBytes(t, tc, "message_id"))
	}
}

func assertRawSMControls(t *testing.T, body *smppwire.SMBody, caseID string) {
	t.Helper()
	type controls struct {
		sourceTON, sourceNPI           byte
		destinationTON, destinationNPI byte
		esmClass, registered, dcs      byte
		validity                       []byte
	}
	want := controls{}
	switch caseID {
	case "deliver_sm_dlr_message_payload":
		want.esmClass = 0x04
		want.registered = 0x01
	case "submit_sm_unknown_vendor_tlv":
		want.sourceTON = 0x05
		want.destinationTON = 0x01
		want.destinationNPI = 0x01
		want.esmClass = 0x03
		want.registered = 0x01
		want.dcs = 0xf1
		want.validity = []byte("151110145607000+")
	}
	got := controls{
		sourceTON: body.SourceAddressTON, sourceNPI: body.SourceAddressNPI,
		destinationTON: body.DestinationAddressTON, destinationNPI: body.DestinationAddressNPI,
		esmClass: body.ESMClass, registered: body.RegisteredDelivery, dcs: body.DataCoding,
		validity: body.ValidityPeriod,
	}
	if got.sourceTON != want.sourceTON || got.sourceNPI != want.sourceNPI ||
		got.destinationTON != want.destinationTON || got.destinationNPI != want.destinationNPI ||
		got.esmClass != want.esmClass || got.registered != want.registered || got.dcs != want.dcs ||
		!bytes.Equal(got.validity, want.validity) {
		t.Errorf("raw SM controls = %#v, want %#v", got, want)
	}
	if body.ProtocolID != 0 || body.PriorityFlag != 0 || body.ReplaceIfPresentFlag != 0 || body.SMDefaultMessageID != 0 {
		t.Errorf("unexpected non-zero default controls: protocol=%d priority=%d replace=%d default_msg=%d",
			body.ProtocolID, body.PriorityFlag, body.ReplaceIfPresentFlag, body.SMDefaultMessageID)
	}
}

func commandID(t *testing.T, value string) uint32 {
	t.Helper()
	ids := map[string]uint32{
		"CommandId.bind_transceiver": smppwire.CommandBindTransceiver,
		"CommandId.submit_sm":        smppwire.CommandSubmitSM,
		"CommandId.deliver_sm":       smppwire.CommandDeliverSM,
		"CommandId.submit_sm_resp":   smppwire.CommandSubmitSMResp,
	}
	id, ok := ids[value]
	if !ok {
		t.Fatalf("unknown fixture command ID %q", value)
	}
	return id
}

func parameterBytes(t *testing.T, tc goldenCase, name string) []byte {
	t.Helper()
	raw, ok := tc.Decoded.Parameters[name]
	if !ok {
		t.Fatalf("fixture parameter %q is absent", name)
	}
	return decodeGoldenBytes(t, raw)
}

func decodeGoldenBytes(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var value goldenBytes
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode golden bytes: %v", err)
	}
	return decodeHex(t, value.Hex)
}

func parameterInt(t *testing.T, tc goldenCase, name string) int {
	t.Helper()
	value, ok := optionalInt(t, tc, name)
	if !ok {
		t.Fatalf("fixture integer parameter %q is absent", name)
	}
	return value
}

func optionalInt(t *testing.T, tc goldenCase, name string) (int, bool) {
	t.Helper()
	raw, ok := tc.Decoded.Parameters[name]
	if !ok {
		return 0, false
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode fixture integer %q: %v", name, err)
	}
	return value, true
}

func optionalEnumByte(t *testing.T, tc goldenCase, name string, values map[string]byte) (byte, bool) {
	t.Helper()
	raw, ok := tc.Decoded.Parameters[name]
	if !ok {
		return 0, false
	}
	var value struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("decode fixture enum %q: %v", name, err)
	}
	result, ok := values[value.Value]
	if !ok {
		t.Fatalf("unknown fixture enum %q value %q", name, value.Value)
	}
	return result, true
}

func assertBytes(t *testing.T, name string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("%s = %x, want %x", name, got, want)
	}
}

func decodeHex(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	return decoded
}

func loadSMPPGolden(t *testing.T) goldenDocument {
	t.Helper()
	path := filepath.Join("..", "..", "..", "compat", "fixtures", "smpp", "baseline.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document goldenDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

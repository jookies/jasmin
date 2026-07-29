package smppwire_test

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func TestReadHandlesPartialAndCoalescedFrames(t *testing.T) {
	first := fixtureWire(t, "bind_transceiver")
	second := fixtureWire(t, "submit_sm_resp_ok")
	reader := &chunkReader{reader: bytes.NewReader(append(append([]byte(nil), first...), second...)), maximum: 1}

	gotFirst, err := smppwire.Read(reader, 1024)
	if err != nil {
		t.Fatalf("Read first: %v", err)
	}
	gotSecond, err := smppwire.Read(reader, 1024)
	if err != nil {
		t.Fatalf("Read second: %v", err)
	}
	if gotFirst.Header.CommandID != smppwire.CommandBindTransceiver || gotFirst.Header.SequenceNumber != 1 {
		t.Fatalf("first header = %#v", gotFirst.Header)
	}
	if gotSecond.Header.CommandID != smppwire.CommandSubmitSMResp || gotSecond.Header.SequenceNumber != 7 {
		t.Fatalf("second header = %#v", gotSecond.Header)
	}
	if reader.reader.Len() != 0 {
		t.Fatalf("unread bytes = %d, want 0", reader.reader.Len())
	}
}

func TestDecodeRejectsInvalidFrameBoundaries(t *testing.T) {
	tests := []struct {
		name string
		wire []byte
		want error
	}{
		{name: "short header", wire: make([]byte, 15), want: smppwire.ErrTruncatedFrame},
		{name: "declared below header", wire: headerOnly(15, smppwire.CommandBindTransceiver), want: smppwire.ErrInvalidCommandLength},
		{name: "declared oversized", wire: headerOnly(smppwire.DefaultMaxSize+1, smppwire.CommandBindTransceiver), want: smppwire.ErrFrameTooLarge},
		{name: "truncated body", wire: headerOnly(17, smppwire.CommandBindTransceiver), want: smppwire.ErrTruncatedFrame},
		{name: "unterminated bind cstring", wire: append(headerOnly(17, smppwire.CommandBindTransceiver), 'x'), want: smppwire.ErrMalformedCString},
		{name: "unsupported command", wire: headerOnly(16, 0x7fffffff), want: smppwire.ErrUnsupportedCommand},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := smppwire.Decode(tc.wire)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestFailedBindResponseMayHaveEmptyBody(t *testing.T) {
	wire := headerOnly(16, smppwire.CommandBindTransceiverResp)
	binary.BigEndian.PutUint32(wire[8:12], 0x0000000e)
	binary.BigEndian.PutUint32(wire[12:16], 1)
	pdu, err := smppwire.Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	if pdu.BindResponse != nil {
		t.Fatalf("bind response = %#v, want absent body", pdu.BindResponse)
	}
	roundTrip, err := smppwire.Encode(pdu)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(roundTrip, wire) {
		t.Fatalf("round-trip = %x, want %x", roundTrip, wire)
	}
}

func TestReadRejectsOversizeBeforeBodyAllocation(t *testing.T) {
	_, err := smppwire.Read(bytes.NewReader(headerOnly(1025, smppwire.CommandSubmitSM)), 1024)
	if !errors.Is(err, smppwire.ErrFrameTooLarge) {
		t.Fatalf("error = %v, want ErrFrameTooLarge", err)
	}
}

func TestDecodeRejectsMalformedTLVs(t *testing.T) {
	base := fixtureWire(t, "submit_sm_ascii")
	tests := []struct {
		name string
		tlv  []byte
	}{
		{name: "truncated value", tlv: []byte{0x02, 0x0c, 0x00, 0x02, 0x12}},
		{name: "wrong fixed width", tlv: []byte{0x02, 0x0e, 0x00, 0x02, 0x01, 0x02}},
		{name: "receipt without terminator", tlv: []byte{0x00, 0x1e, 0x00, 0x01, 'x'}},
		{name: "truncated header", tlv: []byte{0x02, 0x0c, 0x00}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			wire := appendTLV(base, tc.tlv)
			_, err := smppwire.Decode(wire)
			if !errors.Is(err, smppwire.ErrMalformedTLV) {
				t.Fatalf("error = %v, want ErrMalformedTLV", err)
			}
		})
	}
}

func TestDecodeClassifiesOptionalParameterErrors(t *testing.T) {
	base := fixtureWire(t, "submit_sm_ascii")
	tests := []struct {
		name   string
		tlv    []byte
		want   error
		status uint32
	}{
		{
			name:   "stream",
			tlv:    []byte{0x02},
			want:   smppwire.ErrInvalidOptionalStream,
			status: smppwire.StatusInvalidOptionalParameterStream,
		},
		{
			name:   "not allowed",
			tlv:    []byte{0x04, 0x27, 0x00, 0x01, 0x02},
			want:   smppwire.ErrOptionalParameterNotAllowed,
			status: smppwire.StatusOptionalParameterNotAllowed,
		},
		{
			name:   "length",
			tlv:    []byte{0x02, 0x0e, 0x00, 0x02, 0x01, 0x02},
			want:   smppwire.ErrInvalidOptionalParameterLength,
			status: smppwire.StatusInvalidParameterLength,
		},
		{
			name:   "value",
			tlv:    []byte{0x03, 0x04, 0x00, 0x01, 0xff},
			want:   smppwire.ErrInvalidOptionalParameterValue,
			status: smppwire.StatusInvalidOptionalParameterValue,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := smppwire.Decode(appendTLV(base, tc.tlv))
			if !errors.Is(err, smppwire.ErrMalformedTLV) || !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want ErrMalformedTLV and %v", err, tc.want)
			}
			var parseErr *smppwire.ParseError
			if !errors.As(err, &parseErr) || parseErr.CommandStatus != tc.status {
				t.Fatalf("parse error = %+v, want status %#x", parseErr, tc.status)
			}
		})
	}
}

func TestDecodeRejectsSpecInvalidOptionalLengthsAndValues(t *testing.T) {
	tests := []struct {
		name    string
		command uint32
		tlv     []byte
		want    error
		status  uint32
		// tolerated: an inbound carrier PDU must keep the message and skip the
		// offending optional parameter rather than fail the whole decode.
		tolerated bool
	}{
		{
			name: "more_messages_to_send value",
			tlv:  []byte{0x04, 0x26, 0x00, 0x01, 0x02},
			want: smppwire.ErrInvalidOptionalParameterValue, status: smppwire.StatusInvalidOptionalParameterValue,
		},
		{
			name: "sar_total_segments zero",
			tlv:  []byte{0x02, 0x0e, 0x00, 0x01, 0x00},
			want: smppwire.ErrInvalidOptionalParameterValue, status: smppwire.StatusInvalidOptionalParameterValue,
		},
		{
			name: "sar_segment_seqnum zero",
			tlv:  []byte{0x02, 0x0f, 0x00, 0x01, 0x00},
			want: smppwire.ErrInvalidOptionalParameterValue, status: smppwire.StatusInvalidOptionalParameterValue,
		},
		{
			name: "sms_signal length",
			tlv:  []byte{0x12, 0x03, 0x00, 0x01, 0x01},
			want: smppwire.ErrInvalidOptionalParameterLength, status: smppwire.StatusInvalidParameterLength,
		},
		{
			name: "callback_num too short",
			tlv:  []byte{0x03, 0x81, 0x00, 0x03, 0x01, 0x01, 0x01},
			want: smppwire.ErrInvalidOptionalParameterLength, status: smppwire.StatusInvalidParameterLength,
		},
		{
			name: "callback_num too long",
			tlv: append([]byte{0x03, 0x81, 0x00, 0x14, 0x01, 0x01, 0x01},
				bytes.Repeat([]byte{'1'}, 17)...),
			want: smppwire.ErrInvalidOptionalParameterLength, status: smppwire.StatusInvalidParameterLength,
		},
		{
			name: "source_subaddress too long",
			tlv: append([]byte{0x02, 0x02, 0x00, 0x18, 0x80},
				bytes.Repeat([]byte{'1'}, 23)...),
			want: smppwire.ErrInvalidOptionalParameterLength, status: smppwire.StatusInvalidParameterLength,
		},
		{
			// Strictness applies to submit_sm: it arrives from a customer, so an
			// error status is actionable for them. user_message_reference is valid
			// on submit_sm and fixed at two octets, so a one-octet value is a
			// length violation rather than a not-allowed parameter.
			name: "user_message_reference length on submit_sm",
			tlv:  []byte{0x02, 0x04, 0x00, 0x01, 0x01},
			want: smppwire.ErrInvalidOptionalParameterLength, status: smppwire.StatusInvalidParameterLength,
		},
		{
			// The same parameter on a carrier-originated PDU must NOT cost the
			// message. Failing the decode surfaces in the client read loop, so one
			// off-spec optional parameter would become a reconnect loop and an MO
			// outage. Skip the parameter, keep the message.
			name:      "network_error_code length tolerated on data_sm",
			command:   smppwire.CommandDataSM,
			tlv:       []byte{0x04, 0x23, 0x00, 0x02, 0x01, 0x02},
			tolerated: true,
		},
		{
			name: "incomplete sar group",
			tlv:  []byte{0x02, 0x0c, 0x00, 0x02, 0x00, 0x01},
			want: smppwire.ErrMissingOptionalParameter, status: smppwire.StatusMissingOptionalParameter,
		},
		{
			name: "sar sequence exceeds total",
			tlv: []byte{
				0x02, 0x0c, 0x00, 0x02, 0x00, 0x01,
				0x02, 0x0e, 0x00, 0x01, 0x01,
				0x02, 0x0f, 0x00, 0x01, 0x02,
			},
			want: smppwire.ErrInvalidOptionalParameterValue, status: smppwire.StatusInvalidOptionalParameterValue,
		},
		{
			name: "message_payload with short_message",
			tlv:  []byte{0x04, 0x24, 0x00, 0x01, 'x'},
			want: smppwire.ErrInvalidOptionalParameterValue, status: smppwire.StatusInvalidOptionalParameterValue,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			command := tc.command
			if command == 0 {
				command = smppwire.CommandSubmitSM
			}
			frame, err := smppwire.Encode(smppwire.PDU{
				Header: smppwire.Header{CommandID: command, SequenceNumber: 1},
				SM: &smppwire.SMBody{
					SourceAddress:      []byte("111"),
					DestinationAddress: []byte("222"),
					ShortMessage:       []byte("message"),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := smppwire.Decode(appendTLV(frame, tc.tlv))
			if tc.tolerated {
				if err != nil {
					t.Fatalf("inbound decode rejected an off-spec optional parameter: %v", err)
				}
				if decoded.SM == nil || string(decoded.SM.SourceAddress) != "111" {
					t.Fatalf("mandatory fields lost while skipping the parameter: %+v", decoded.SM)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			var parseErr *smppwire.ParseError
			if !errors.As(err, &parseErr) || parseErr.CommandStatus != tc.status {
				t.Fatalf("parse error = %+v, want status %#x", parseErr, tc.status)
			}
		})
	}
}

func TestDecodeRequiresMandatoryParameters(t *testing.T) {
	for _, command := range []uint32{
		smppwire.CommandBindReceiver,
		smppwire.CommandBindTransmitter,
		smppwire.CommandBindTransceiver,
		smppwire.CommandSubmitSM,
		smppwire.CommandDeliverSM,
		smppwire.CommandDataSM,
		smppwire.CommandBindReceiverResp,
		smppwire.CommandBindTransmitterResp,
		smppwire.CommandBindTransceiverResp,
		smppwire.CommandSubmitSMResp,
		smppwire.CommandDeliverSMResp,
		smppwire.CommandDataSMResp,
	} {
		_, err := smppwire.Decode(headerOnly(16, command))
		if !errors.Is(err, smppwire.ErrMissingMandatoryParameter) {
			t.Errorf("command %#x error = %v, want ErrMissingMandatoryParameter", command, err)
		}
	}
}

func TestDecodeRejectsBodiesForbiddenByCommandLength(t *testing.T) {
	control := append(headerOnly(17, smppwire.CommandEnquireLink), 0)
	if _, err := smppwire.Decode(control); !errors.Is(err, smppwire.ErrInvalidCommandLength) {
		t.Fatalf("control PDU error = %v, want ErrInvalidCommandLength", err)
	}

	errorResponse := append(headerOnly(17, smppwire.CommandSubmitSMResp), 0)
	binary.BigEndian.PutUint32(errorResponse[8:12], 0x00000008)
	if _, err := smppwire.Decode(errorResponse); !errors.Is(err, smppwire.ErrInvalidCommandLength) {
		t.Fatalf("error response error = %v, want ErrInvalidCommandLength", err)
	}
}

func TestEncodeRejectsUnrepresentableValues(t *testing.T) {
	t.Run("NUL in C-octet string", func(t *testing.T) {
		_, err := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{CommandID: smppwire.CommandBindTransceiver},
			Bind:   &smppwire.BindBody{SystemID: []byte{'a', 0, 'b'}},
		})
		if !errors.Is(err, smppwire.ErrMalformedCString) {
			t.Fatalf("error = %v, want ErrMalformedCString", err)
		}
	})

	t.Run("short message above uint8 length", func(t *testing.T) {
		_, err := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM},
			SM:     &smppwire.SMBody{ShortMessage: make([]byte, 256)},
		})
		if err == nil {
			t.Fatal("expected short_message length error")
		}
	})

	t.Run("TLV above uint16 length", func(t *testing.T) {
		_, err := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM},
			SM: &smppwire.SMBody{Optional: smppwire.OptionalParameters{
				MessagePayload: make([]byte, 1<<16),
			}},
		})
		if !errors.Is(err, smppwire.ErrMalformedTLV) {
			t.Fatalf("error = %v, want ErrMalformedTLV", err)
		}
	})

	t.Run("invalid optional value", func(t *testing.T) {
		value := byte(2)
		_, err := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM},
			SM: &smppwire.SMBody{Optional: smppwire.OptionalParameters{
				MoreMessagesToSend: &value,
			}},
		})
		if !errors.Is(err, smppwire.ErrInvalidOptionalParameterValue) {
			t.Fatalf("error = %v, want ErrInvalidOptionalParameterValue", err)
		}
	})

	t.Run("invalid optional length", func(t *testing.T) {
		_, err := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{CommandID: smppwire.CommandDataSM},
			SM: &smppwire.SMBody{Optional: smppwire.OptionalParameters{
				NetworkErrorCode: []byte{1, 2},
			}},
		})
		if !errors.Is(err, smppwire.ErrInvalidOptionalParameterLength) {
			t.Fatalf("error = %v, want ErrInvalidOptionalParameterLength", err)
		}
	})

	t.Run("encoded body above configured frame maximum", func(t *testing.T) {
		_, err := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{CommandID: smppwire.CommandBindTransceiver},
			Bind:   &smppwire.BindBody{SystemID: make([]byte, smppwire.DefaultMaxSize)},
		})
		if !errors.Is(err, smppwire.ErrFrameTooLarge) {
			t.Fatalf("error = %v, want ErrFrameTooLarge", err)
		}
	})
}

// An optional parameter that belongs to a different PDU is rejected on the PDU
// we receive from customers and tolerated on the one we receive from carriers.
// Same wire error, opposite correct response: a customer can act on an error
// status, whereas failing a carrier's deliver_sm decode surfaces in the client
// read loop and turns one off-spec parameter into a reconnect loop and an MO
// outage.
func TestKnownOptionalFromAnotherPDUIsStrictInboundFromCustomersOnly(t *testing.T) {
	// sms_signal is valid for submit_sm/data_sm but not deliver_sm.
	smsSignal := []byte{0x12, 0x03, 0x00, 0x02, 0x00, 0x01}
	// receipted_message_id is valid for deliver_sm but not submit_sm.
	receiptedID := []byte{0x00, 0x1e, 0x00, 0x05, 'a', 'b', 'c', '1', 0x00}

	build := func(t *testing.T, command uint32, tlv []byte) []byte {
		t.Helper()
		frame, err := smppwire.Encode(smppwire.PDU{
			Header: smppwire.Header{CommandID: command, SequenceNumber: 1},
			SM: &smppwire.SMBody{
				SourceAddress:      []byte("111"),
				DestinationAddress: []byte("222"),
				ShortMessage:       []byte("hi"),
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		frame = append(frame, tlv...)
		binary.BigEndian.PutUint32(frame[:4], uint32(len(frame)))
		return frame
	}

	t.Run("submit_sm rejects", func(t *testing.T) {
		frame := build(t, smppwire.CommandSubmitSM, receiptedID)
		if _, err := smppwire.Decode(frame); !errors.Is(err, smppwire.ErrOptionalParameterNotAllowed) {
			t.Fatalf("error=%v want ErrOptionalParameterNotAllowed", err)
		}
	})

	t.Run("deliver_sm tolerates and keeps the message", func(t *testing.T) {
		frame := build(t, smppwire.CommandDeliverSM, smsSignal)
		decoded, err := smppwire.Decode(frame)
		if err != nil {
			t.Fatalf("deliver_sm decode rejected an off-spec optional parameter: %v", err)
		}
		if decoded.SM == nil || string(decoded.SM.ShortMessage) != "hi" {
			t.Fatalf("message lost while skipping the parameter: %+v", decoded.SM)
		}
	})
}

func TestSubmitSMRejectsKnownOptionalFromAnotherPDU(t *testing.T) {
	frame, err := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM, SequenceNumber: 1},
		SM: &smppwire.SMBody{
			SourceAddress:      []byte("111"),
			DestinationAddress: []byte("222"),
			ShortMessage:       []byte("hi"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// message_state is a known, supported deliver_sm/data_sm optional, but it
	// is not in SubmitSM.optionalParams. The frozen decoder rejects it rather
	// than accepting and silently dropping it.
	frame = append(frame, 0x04, 0x27, 0x00, 0x01, 0x02)
	binary.BigEndian.PutUint32(frame[:4], uint32(len(frame)))
	if _, err := smppwire.Decode(frame); !errors.Is(err, smppwire.ErrMalformedTLV) {
		t.Fatalf("error=%v want ErrMalformedTLV", err)
	}
}

func TestDataSMToleratesKnownOptionalFromAnotherPDU(t *testing.T) {
	frame, err := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDataSM, SequenceNumber: 1},
		SM: &smppwire.SMBody{
			SourceAddress:      []byte("111"),
			DestinationAddress: []byte("222"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// additional_status_info_text is valid only on data_sm_resp, so it has no
	// business on a data_sm. It is still only an OPTIONAL parameter: data_sm
	// carries the same command_id in both directions, our SMPPs server refuses
	// customer data_sm outright, so what reaches this decode is carrier-
	// originated MO. Failing the decode there surfaces in the client read loop
	// and turns one off-spec parameter into a reconnect loop and an MO outage.
	// Skip the parameter, keep the message.
	frame = append(frame, 0x00, 0x1d, 0x00, 0x02, 'x', 0)
	binary.BigEndian.PutUint32(frame[:4], uint32(len(frame)))
	pdu, err := smppwire.Decode(frame)
	if err != nil {
		t.Fatalf("data_sm decode failed on an off-spec optional parameter: %v", err)
	}
	if pdu.SM == nil || string(pdu.SM.SourceAddress) != "111" {
		t.Fatalf("mandatory fields lost while skipping the parameter: %+v", pdu.SM)
	}
}

func TestEncodeHonorsExactFrameMaximum(t *testing.T) {
	maximumMessageID := int(smppwire.DefaultMaxSize-smppwire.HeaderSize) - 1
	pdu := smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandSubmitSMResp},
		SubmitResponse: &smppwire.SubmitResponseBody{
			MessageID: bytes.Repeat([]byte{'a'}, maximumMessageID),
		},
	}
	wire, err := smppwire.Encode(pdu)
	if err != nil {
		t.Fatalf("Encode exact maximum: %v", err)
	}
	if len(wire) != int(smppwire.DefaultMaxSize) {
		t.Fatalf("wire length = %d, want %d", len(wire), smppwire.DefaultMaxSize)
	}

	pdu.SubmitResponse.MessageID = append(pdu.SubmitResponse.MessageID, 'a')
	_, err = smppwire.Encode(pdu)
	if !errors.Is(err, smppwire.ErrFrameTooLarge) {
		t.Fatalf("maximum + 1 error = %v, want ErrFrameTooLarge", err)
	}
}

func FuzzDecodeNeverPanics(f *testing.F) {
	f.Add([]byte{})
	f.Add(headerOnly(16, smppwire.CommandSubmitSM))
	bind, _ := hex.DecodeString("00000023000000090000000000000001636c69656e7400736563726574000000000000")
	f.Add(bind)
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = smppwire.Decode(data)
	})
}

type chunkReader struct {
	reader  *bytes.Reader
	maximum int
}

func (r *chunkReader) Read(buffer []byte) (int, error) {
	if len(buffer) > r.maximum {
		buffer = buffer[:r.maximum]
	}
	return r.reader.Read(buffer)
}

func fixtureWire(t *testing.T, id string) []byte {
	t.Helper()
	for _, tc := range loadSMPPGolden(t).Cases {
		if tc.ID == id {
			return decodeHex(t, tc.WireHex)
		}
	}
	t.Fatalf("fixture %q not found", id)
	return nil
}

func headerOnly(length uint32, commandID uint32) []byte {
	wire := make([]byte, 16)
	binary.BigEndian.PutUint32(wire[0:4], length)
	binary.BigEndian.PutUint32(wire[4:8], commandID)
	return wire
}

func appendTLV(base, tlv []byte) []byte {
	wire := append(append([]byte(nil), base...), tlv...)
	binary.BigEndian.PutUint32(wire[0:4], uint32(len(wire)))
	return wire
}

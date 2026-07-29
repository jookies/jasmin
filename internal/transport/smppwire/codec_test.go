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

func TestDataSMRejectsKnownOptionalFromAnotherPDU(t *testing.T) {
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
	// additional_status_info_text has a working frozen encoder, but is valid
	// only on data_sm_resp. DataSM must reject it rather than accept/drop it.
	frame = append(frame, 0x00, 0x1d, 0x00, 0x02, 'x', 0)
	binary.BigEndian.PutUint32(frame[:4], uint32(len(frame)))
	if _, err := smppwire.Decode(frame); !errors.Is(err, smppwire.ErrMalformedTLV) {
		t.Fatalf("error=%v want ErrMalformedTLV", err)
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

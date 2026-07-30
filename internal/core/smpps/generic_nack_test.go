package smpps

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

func TestUnknownCommandAnswersGenericNACK(t *testing.T) {
	_, addr := startServer(t, mapResolver{}, ServerConfig{})
	conn := dial(t, addr)

	writeRawHeader(t, conn, 16, 0x0000ffff, 0, 77)
	header := readRawHeader(t, conn)
	if header.commandID != 0x80000000 ||
		header.commandStatus != 0x00000003 ||
		header.sequenceNumber != 77 {
		t.Fatalf("generic_nack = command %#x status %#x sequence %d",
			header.commandID, header.commandStatus, header.sequenceNumber)
	}

	writePDU(t, conn, smppwire.PDU{Header: smppwire.Header{
		CommandID: smppwire.CommandEnquireLink, SequenceNumber: 78,
	}})
	if response := readPDU(t, conn); response.Header.CommandID != smppwire.CommandEnquireLinkResp {
		t.Fatalf("session closed after unknown command; got %#x", response.Header.CommandID)
	}
}

func TestInvalidCommandLengthAnswersGenericNACK(t *testing.T) {
	_, addr := startServer(t, mapResolver{}, ServerConfig{})
	conn := dial(t, addr)

	writeRawHeader(t, conn, 15, CommandEnquireLink, 0, 91)
	header := readRawHeader(t, conn)
	if header.commandID != 0x80000000 ||
		header.commandStatus != 0x00000002 ||
		header.sequenceNumber != 91 {
		t.Fatalf("generic_nack = command %#x status %#x sequence %d",
			header.commandID, header.commandStatus, header.sequenceNumber)
	}
}

func TestMalformedOptionalParameterStatuses(t *testing.T) {
	tests := []struct {
		name   string
		tlv    []byte
		status uint32
	}{
		{
			name:   "truncated stream",
			tlv:    []byte{0x02},
			status: 0x000000c0,
		},
		{
			name:   "parameter not allowed",
			tlv:    []byte{0x04, 0x27, 0x00, 0x01, 0x02},
			status: 0x000000c1,
		},
		{
			name:   "invalid parameter length",
			tlv:    []byte{0x02, 0x0e, 0x00, 0x02, 0x01, 0x02},
			status: 0x000000c2,
		},
		{
			name:   "invalid parameter value",
			tlv:    []byte{0x03, 0x04, 0x00, 0x01, 0xff},
			status: 0x000000c4,
		},
		{
			name:   "missing related parameter",
			tlv:    []byte{0x02, 0x0c, 0x00, 0x02, 0x00, 0x01},
			status: 0x000000c3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, addr := startServer(t, mapResolver{}, ServerConfig{})
			conn := dial(t, addr)
			frame, err := smppwire.Encode(smppwire.PDU{
				Header: smppwire.Header{
					CommandID:      smppwire.CommandSubmitSM,
					SequenceNumber: 33,
				},
				SM: &smppwire.SMBody{
					SourceAddress:      []byte("111"),
					DestinationAddress: []byte("222"),
					ShortMessage:       []byte("message"),
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			frame = append(frame, tc.tlv...)
			binary.BigEndian.PutUint32(frame[0:4], uint32(len(frame)))
			if _, err := conn.Write(frame); err != nil {
				t.Fatal(err)
			}

			header := readRawHeader(t, conn)
			if header.commandID != smppwire.CommandGenericNACK ||
				header.commandStatus != tc.status ||
				header.sequenceNumber != 33 {
				t.Fatalf("generic_nack = command %#x status %#x sequence %d, want status %#x",
					header.commandID, header.commandStatus, header.sequenceNumber, tc.status)
			}
		})
	}
}

func TestMissingMandatoryParametersAnswersInvalidCommandLength(t *testing.T) {
	_, addr := startServer(t, mapResolver{}, ServerConfig{})
	conn := dial(t, addr)

	writeRawHeader(t, conn, 16, CommandBindTransceiver, 0, 41)
	header := readRawHeader(t, conn)
	if header.commandID != smppwire.CommandGenericNACK ||
		header.commandStatus != smppwire.StatusInvalidCommandLength ||
		header.sequenceNumber != 41 {
		t.Fatalf("generic_nack = command %#x status %#x sequence %d",
			header.commandID, header.commandStatus, header.sequenceNumber)
	}
}

func TestResponseCommandForUnknownIsGenericNACK(t *testing.T) {
	if got := responseCommandFor(0x0000ffff); got != smppwire.CommandGenericNACK {
		t.Fatalf("response command = %#x, want generic_nack", got)
	}
}

type rawHeader struct {
	commandID      uint32
	commandStatus  uint32
	sequenceNumber uint32
}

func writeRawHeader(t *testing.T, conn net.Conn, length, commandID, status, sequence uint32) {
	t.Helper()
	frame := make([]byte, 16)
	binary.BigEndian.PutUint32(frame[0:4], length)
	binary.BigEndian.PutUint32(frame[4:8], commandID)
	binary.BigEndian.PutUint32(frame[8:12], status)
	binary.BigEndian.PutUint32(frame[12:16], sequence)
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}
}

func readRawHeader(t *testing.T, conn net.Conn) rawHeader {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	frame := make([]byte, 16)
	if _, err := io.ReadFull(conn, frame); err != nil {
		t.Fatalf("read response header: %v", err)
	}
	if length := binary.BigEndian.Uint32(frame[0:4]); length != 16 {
		t.Fatalf("response command_length = %d, want 16", length)
	}
	return rawHeader{
		commandID:      binary.BigEndian.Uint32(frame[4:8]),
		commandStatus:  binary.BigEndian.Uint32(frame[8:12]),
		sequenceNumber: binary.BigEndian.Uint32(frame[12:16]),
	}
}

package smppwire

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// deliverWithTLVSection builds a minimal deliver_sm frame with the given raw
// TLV section appended, mirroring the probe harness used against the legacy
// patched decoder.
func deliverWithTLVSection(t *testing.T, tlvHex string) []byte {
	t.Helper()
	body := &SMBody{SourceAddress: []byte("1111"), DestinationAddress: []byte("2222"), ShortMessage: []byte("hello")}
	frame, err := Encode(PDU{Header: Header{CommandID: CommandDeliverSM, SequenceNumber: 5}, SM: body})
	if err != nil {
		t.Fatal(err)
	}
	section, err := hex.DecodeString(tlvHex)
	if err != nil {
		t.Fatal(err)
	}
	frame = append(frame, section...)
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(frame)))
	return frame
}

func TestDecodeCapturesVendorTLVs(t *testing.T) {
	// Legacy capture: [(5376, 1, 'OctetString', b'b'), (5121, 4, ..., b'test'),
	// (5121, 2, ..., b'hi')] — wire order, duplicates kept.
	pdu, err := Decode(deliverWithTLVSection(t, "15000001621401000474657374140100026869"))
	if err != nil {
		t.Fatal(err)
	}
	captured := pdu.SM.CapturedVendorTLVs
	if len(captured) != 3 ||
		captured[0].Tag != 0x1500 || string(captured[0].Value) != "b" ||
		captured[1].Tag != 0x1401 || string(captured[1].Value) != "test" ||
		captured[2].Tag != 0x1401 || string(captured[2].Value) != "hi" {
		t.Fatalf("captured = %+v", captured)
	}
}

func TestDecodeCaptureBoundaryMatchesLegacyKnownTags(t *testing.T) {
	// source_port (0x020A) is known to the legacy library — decoded into params
	// there, ignored-not-captured here; the vendor tag is captured.
	pdu, err := Decode(deliverWithTLVSection(t, "020a00020fa01401000161"))
	if err != nil {
		t.Fatal(err)
	}
	captured := pdu.SM.CapturedVendorTLVs
	if len(captured) != 1 || captured[0].Tag != 0x1401 || string(captured[0].Value) != "a" {
		t.Fatalf("captured = %+v", captured)
	}
}

func TestDecodeCaptureKeepsRawOctets(t *testing.T) {
	// NUL bytes and zero-length values are preserved verbatim, like the legacy
	// bucket's bytes(option.value).
	pdu, err := Decode(deliverWithTLVSection(t, "140100046162630014020000"))
	if err != nil {
		t.Fatal(err)
	}
	captured := pdu.SM.CapturedVendorTLVs
	if len(captured) != 2 || !bytes.Equal(captured[0].Value, []byte("abc\x00")) ||
		captured[1].Tag != 0x1402 || len(captured[1].Value) != 0 {
		t.Fatalf("captured = %+v", captured)
	}
}

func TestDecodeCaptureIsNotReencoded(t *testing.T) {
	// Q-016: re-encoding still omits unknown TLVs — capture is decode-only.
	frame := deliverWithTLVSection(t, "1401000474657374")
	pdu, err := Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := Encode(pdu)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(reencoded, []byte("test")) {
		t.Fatalf("re-encode retained the vendor TLV: %x", reencoded)
	}
}

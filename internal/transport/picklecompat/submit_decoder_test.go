package picklecompat_test

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func TestDecodeSubmitSMProjectsCanonicalWireBody(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()

	encoded, err := bridge.EncodeSubmitSM(ctx, picklecompat.SubmitSMEncodeRequest{
		Sequence:   7,
		SourceAddr: picklecompat.Bytes("1111"), DestinationAddr: picklecompat.Bytes("2222"),
		ShortMessage: picklecompat.Bytes{0x00, 0xff, 0x41}, DataCoding: 8, Priority: 2,
		ScheduleAt: "2026-07-21T10:11:12Z", ValidityUntil: "2026-07-22T10:11:12Z",
		RegisteredDelivery: true, UDH: true,
		SAR:         &picklecompat.SubmitSMSAR{Reference: 0x1234, Total: 3, Sequence: 2},
		CustomTLVs:  []picklecompat.SubmitSMCustomTLV{{Tag: 0x1403, Value: picklecompat.Bytes("vendor")}},
		IncludeBill: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := bridge.DecodeSubmitSM(ctx, encoded.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body.SourceAddress) != "1111" || string(body.DestinationAddress) != "2222" {
		t.Fatalf("addresses = %q -> %q", body.SourceAddress, body.DestinationAddress)
	}
	if body.DataCoding != 8 || body.PriorityFlag != 2 || body.RegisteredDelivery == 0 || body.ESMClass&0x40 == 0 {
		t.Fatalf("encoded flags not preserved: %+v", body)
	}
	if string(body.ShortMessage) != string([]byte{0x00, 0xff, 0x41}) {
		t.Fatalf("binary short_message = %x", body.ShortMessage)
	}
	if body.Optional.SARMessageReference == nil || *body.Optional.SARMessageReference != 0x1234 ||
		body.Optional.SARTotalSegments == nil || *body.Optional.SARTotalSegments != 3 ||
		body.Optional.SARSegmentSequence == nil || *body.Optional.SARSegmentSequence != 2 {
		t.Fatalf("SAR options = %+v", body.Optional)
	}
	pdu := smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandSubmitSM, SequenceNumber: 9}, SM: &body}
	wire, err := smppwire.Encode(pdu)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := smppwire.Decode(wire)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded.SM.ShortMessage) != string(body.ShortMessage) || decoded.SM.Optional.SARMessageReference == nil {
		t.Fatalf("socket projection mismatch: %+v", decoded.SM)
	}
}

func TestDecodeSubmitSMRejectsUnallowlistedRoot(t *testing.T) {
	pythonPath := os.Getenv("PYTHON_PATH")
	if pythonPath == "" {
		t.Skip("PYTHON_PATH is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	bridge, err := picklecompat.NewBridge(ctx, pythonPath)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	data, err := base64.StdEncoding.DecodeString("gAJ9cQBYAQAAAGFxAUsBcy4=") // protocol-2 {"a": 1}
	if err != nil {
		t.Fatal(err)
	}
	_, err = bridge.DecodeSubmitSM(ctx, data)
	if !errors.Is(err, picklecompat.ErrInvalidSubmitSM) {
		t.Fatalf("error = %v, want ErrInvalidSubmitSM", err)
	}
}

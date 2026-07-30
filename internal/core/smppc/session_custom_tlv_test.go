package smppc_test

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

type tupleSubmitDecoder struct {
	body   smppwire.SubmitSMBody
	tuples []tlv.TLV
}

func (decoder tupleSubmitDecoder) DecodeSubmitSM(context.Context, []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	return decoder.body, decoder.tuples, nil
}

func readRawFrame(conn net.Conn) ([]byte, error) {
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	length := binary.BigEndian.Uint32(header)
	rest := make([]byte, length-4)
	if _, err := io.ReadFull(conn, rest); err != nil {
		return nil, err
	}
	return append(header, rest...), nil
}

func TestSessionSubmitEmitsVendorTLVSection(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// An untyped 19-digit tuple typed Int8 by the connector rules — the legacy
	// listener's resolve + patched-encoder emission, byte-for-byte.
	decoder := tupleSubmitDecoder{
		body:   smppwire.SubmitSMBody{DestinationAddress: []byte("15551230000"), ShortMessage: []byte("hi")},
		tuples: []tlv.TLV{{Tag: big.NewInt(0x1400), Value: "1707167205648943173"}},
	}
	cfg := smppc.Config{CID: "tlv", CustomTLVs: []smppc.CustomTLVRule{
		{Tag: 0x1400, Type: tlv.TypeInt8, Required: true},
	}}
	session := smppc.NewSessionWithDecoder(client, cfg, nil, nil, decoder, nil)

	type frameResult struct {
		frame []byte
		err   error
	}
	frames := make(chan frameResult, 1)
	go func() {
		frame, err := readRawFrame(server)
		frames <- frameResult{frame: frame, err: err}
	}()

	properties, _ := amqpcompat.NewProperties("tlv-1", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.tlv", properties, []byte("pickled"))
	if err := session.Submit(context.Background(), injectDelivery(envelope, func(bool) {})); err != nil {
		t.Fatal(err)
	}
	result := <-frames
	if result.err != nil {
		t.Fatalf("read frame: %v", result.err)
	}
	frame := result.frame
	want := "1400000817b1138750e6c845"
	if got := hex.EncodeToString(frame); !strings.HasSuffix(got, want) {
		t.Fatalf("frame = %s, want vendor section suffix %s", got, want)
	}
	// The frame stays a well-formed PDU with the section counted in its length.
	if _, err := smppwire.Decode(frame); err != nil {
		t.Fatalf("frame does not round-trip the codec: %v", err)
	}
}

func TestSessionSubmitRejectsTLVRuleViolation(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	decoder := tupleSubmitDecoder{
		body: smppwire.SubmitSMBody{DestinationAddress: []byte("15551230000"), ShortMessage: []byte("hi")},
		// Missing the required 0x1400 tag entirely.
		tuples: []tlv.TLV{{Tag: big.NewInt(0x1401), Value: "x"}},
	}
	cfg := smppc.Config{CID: "tlv", CustomTLVs: []smppc.CustomTLVRule{
		{Tag: 0x1400, Type: tlv.TypeInt8, Required: true},
	}}
	session := smppc.NewSessionWithDecoder(client, cfg, nil, nil, decoder, nil)

	settled := make(chan bool, 1)
	properties, _ := amqpcompat.NewProperties("tlv-2", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.tlv", properties, []byte("pickled"))
	err := session.Submit(context.Background(), injectDelivery(envelope, func(requeue bool) { settled <- requeue }))
	if !errors.Is(err, smppc.ErrCustomTLVRejected) {
		t.Fatalf("Submit error = %v, want ErrCustomTLVRejected", err)
	}
	select {
	case requeue := <-settled:
		if requeue {
			t.Fatal("rule violation must reject without requeue (legacy rejectMessage)")
		}
	case <-time.After(time.Second):
		t.Fatal("rule violation left the delivery unsettled")
	}
}

func TestSessionSubmitRejectsWireCrashEquivalents(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	// A tag outside uint16 crashes the legacy Int2Encoder at wire time; the
	// session rejects instead of masking (unlike EncodeCustomTLVs).
	decoder := tupleSubmitDecoder{
		body:   smppwire.SubmitSMBody{DestinationAddress: []byte("15551230000"), ShortMessage: []byte("hi")},
		tuples: []tlv.TLV{{Tag: new(big.Int).SetUint64(0xFFFF1401), Type: tlv.TypeOctetString, Value: "z"}},
	}
	session := smppc.NewSessionWithDecoder(client, smppc.Config{CID: "tlv"}, nil, nil, decoder, nil)

	settled := make(chan bool, 1)
	properties, _ := amqpcompat.NewProperties("tlv-3", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.tlv", properties, []byte("pickled"))
	err := session.Submit(context.Background(), injectDelivery(envelope, func(requeue bool) { settled <- requeue }))
	if !errors.Is(err, smppc.ErrCustomTLVRejected) {
		t.Fatalf("Submit error = %v, want ErrCustomTLVRejected", err)
	}
	if requeue := <-settled; requeue {
		t.Fatal("wire-crash equivalent must reject without requeue")
	}
}

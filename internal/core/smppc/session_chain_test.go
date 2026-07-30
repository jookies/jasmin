package smppc_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/picklecompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// twoPartChainDecoder projects any submit body into a 2-part SAR chain, so the
// session must send two PDUs and settle the message once.
type twoPartChainDecoder struct{}

func (twoPartChainDecoder) DecodeSubmitSM(context.Context, []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	return smppwire.SubmitSMBody{}, nil, errors.New("single-part path unused in this test")
}

func (twoPartChainDecoder) DecodeSubmitSMChain(context.Context, []byte) ([]picklecompat.SubmitSMChainPart, error) {
	ref := uint16(1)
	total := byte(2)
	mk := func(seq byte, message string) smppwire.SubmitSMBody {
		r, tot, sq := ref, total, seq
		return smppwire.SubmitSMBody{
			SourceAddress: []byte("111"), DestinationAddress: []byte("222"), ShortMessage: []byte(message),
			Optional: smppwire.OptionalParameters{SARMessageReference: &r, SARTotalSegments: &tot, SARSegmentSequence: &sq},
		}
	}
	return []picklecompat.SubmitSMChainPart{{Body: mk(1, "part-one")}, {Body: mk(2, "part-two")}}, nil
}

func writeSubmitResp(t *testing.T, conn net.Conn, seq uint32, smscID string) {
	t.Helper()
	resp, err := smppwire.Encode(smppwire.PDU{
		Header:         smppwire.Header{CommandID: smppwire.CommandSubmitSMResp, SequenceNumber: seq},
		SubmitResponse: &smppwire.SubmitResponseBody{MessageID: []byte(smscID)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(resp); err != nil {
		t.Fatal(err)
	}
}

func TestSessionMultipartChainSendsAllPartsAndSettlesOnce(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSessionWithDecoder(client, smppc.Config{ResTimeout: 5, WindowSize: 2}, retry, readiness, twoPartChainDecoder{}, nil)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- session.Run(ctx) }()
	defer func() {
		stop()
		_ = server.Close()
		<-done
	}()

	properties, _ := amqpcompat.NewProperties("multipart-msg", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.chain", properties, []byte("chained-pickle"))
	settled := make(chan bool, 4)
	delivery := injectDelivery(envelope, func(requeue bool) { settled <- requeue })
	go func() { _ = session.Submit(context.Background(), delivery) }()

	// Both parts hit the wire with distinct sequence numbers.
	seqs := make([]uint32, 0, 2)
	for i := 0; i < 2; i++ {
		pdu, err := smppwire.Read(server, smppwire.DefaultMaxSize)
		if err != nil {
			t.Fatalf("read part %d: %v", i+1, err)
		}
		if pdu.Header.CommandID != smppwire.CommandSubmitSM {
			t.Fatalf("part %d command = %#x, want submit_sm", i+1, pdu.Header.CommandID)
		}
		seqs = append(seqs, pdu.Header.SequenceNumber)
	}
	if seqs[0] == seqs[1] {
		t.Fatal("multipart parts reused a sequence number")
	}

	// The first part's response must not settle the shared delivery.
	writeSubmitResp(t, server, seqs[0], "smsc-1")
	select {
	case <-settled:
		t.Fatal("multipart settled after only the first part's response")
	case <-time.After(150 * time.Millisecond):
	}

	// The last part's response settles the message exactly once.
	writeSubmitResp(t, server, seqs[1], "smsc-2")
	select {
	case <-settled:
	case <-time.After(time.Second):
		t.Fatal("multipart never settled after the last part's response")
	}
	select {
	case <-settled:
		t.Fatal("multipart settled more than once")
	case <-time.After(150 * time.Millisecond):
	}
}

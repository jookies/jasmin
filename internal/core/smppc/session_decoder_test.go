package smppc_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type failingSubmitDecoder struct{ err error }

func (decoder failingSubmitDecoder) DecodeSubmitSM(context.Context, []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	return smppwire.SubmitSMBody{}, nil, decoder.err
}

func TestSessionDecodeFailureRequeuesWithoutSMPPWrite(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	decodeErr := errors.New("unallowlisted pickle")
	session := smppc.NewSessionWithDecoder(client, smppc.Config{}, nil, nil, failingSubmitDecoder{err: decodeErr}, nil)
	properties, _ := amqpcompat.NewProperties("decode-failure", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.decode", properties, []byte("not-a-submit-pickle"))
	settled := make(chan bool, 1)
	delivery := injectDelivery(envelope, func(requeue bool) { settled <- requeue })

	err := session.Submit(context.Background(), delivery)
	if !errors.Is(err, decodeErr) {
		t.Fatalf("Submit error = %v, want decoder error", err)
	}
	select {
	case requeue := <-settled:
		if !requeue {
			t.Fatal("decode failure was discarded instead of requeued")
		}
	case <-time.After(time.Second):
		t.Fatal("decode failure was not settled")
	}
	if err := server.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := server.Read(buffer); err == nil {
		t.Fatal("decode failure wrote bytes to SMPP socket")
	}
}

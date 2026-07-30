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
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

type blockingSubmitDecoder struct{}

func (blockingSubmitDecoder) DecodeSubmitSM(ctx context.Context, _ []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	<-ctx.Done()
	return smppwire.SubmitSMBody{}, nil, ctx.Err()
}

func newWindowDelivery(t *testing.T, messageID string, settled chan<- bool) *amqpcompat.Delivery {
	t.Helper()
	properties, err := amqpcompat.NewProperties(messageID, nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("submit.sm.window", properties, []byte("chained-pickle"))
	if err != nil {
		t.Fatal(err)
	}
	return injectDelivery(envelope, func(requeue bool) { settled <- requeue })
}

func startWindowSession(t *testing.T, cfg smppc.Config) (*smppc.Session, net.Conn, context.CancelFunc, <-chan error) {
	t.Helper()
	client, server := net.Pipe()
	session := smppc.NewSessionWithDecoder(client, cfg, nil, nil, twoPartChainDecoder{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- session.Run(ctx) }()
	return session, server, cancel, done
}

func stopWindowSession(t *testing.T, server net.Conn, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	cancel()
	_ = server.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session did not stop")
	}
}

func TestSessionWindowBackpressuresMultipartPDUs(t *testing.T) {
	session, server, cancel, done := startWindowSession(t, smppc.Config{
		CID: "window", WindowSize: 1, ResTimeout: 5,
	})
	defer stopWindowSession(t, server, cancel, done)

	settled := make(chan bool, 2)
	submitDone := make(chan error, 1)
	go func() {
		submitDone <- session.Submit(context.Background(), newWindowDelivery(t, "window-1", settled))
	}()

	first, err := smppwire.Read(server, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SetReadDeadline(time.Now().Add(75 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if second, readErr := smppwire.Read(server, smppwire.DefaultMaxSize); readErr == nil {
		t.Fatalf("second submit_sm sequence %d passed a full window", second.Header.SequenceNumber)
	} else {
		var timeout net.Error
		if !errors.As(readErr, &timeout) || !timeout.Timeout() {
			t.Fatalf("read while window full = %v, want timeout", readErr)
		}
	}
	if err := server.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}

	writeSubmitResp(t, server, first.Header.SequenceNumber, "smsc-1")
	second, err := smppwire.Read(server, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatal(err)
	}
	if second.Header.CommandID != smppwire.CommandSubmitSM {
		t.Fatalf("second command = %#x, want submit_sm", second.Header.CommandID)
	}
	select {
	case err := <-submitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit did not finish after the window reopened")
	}
	writeSubmitResp(t, server, second.Header.SequenceNumber, "smsc-2")
	select {
	case requeue := <-settled:
		if requeue {
			t.Fatal("successful multipart submit was requeued")
		}
	case <-time.After(time.Second):
		t.Fatal("multipart submit was not settled")
	}
}

func TestSessionStartsResponseTimeoutWhenEachPartIsWritten(t *testing.T) {
	session, server, cancel, done := startWindowSession(t, smppc.Config{
		CID: "window-timeout", WindowSize: 1, ResTimeout: 0.05,
	})
	defer stopWindowSession(t, server, cancel, done)

	settled := make(chan bool, 2)
	go func() {
		_ = session.Submit(context.Background(), newWindowDelivery(t, "window-timeout-1", settled))
	}()
	if _, err := smppwire.Read(server, smppwire.DefaultMaxSize); err != nil {
		t.Fatal(err)
	}
	// Do not read a second frame and do not answer the first. Its response
	// timer must already be live even though the multipart submit has more work.
	select {
	case requeue := <-settled:
		if !requeue {
			t.Fatal("timed-out multipart submit was not requeued")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first part had no response timeout while a later part was backpressured")
	}
}

func TestSessionWriteTimeoutBoundsSilentPeer(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	session := smppc.NewSession(client, smppc.Config{
		CID: "write-timeout", PDUTimeout: 0.05, ResTimeout: 5,
	}, nil, nil, nil)
	settled := make(chan bool, 1)
	submitDone := make(chan error, 1)
	go func() {
		submitDone <- session.Submit(context.Background(), newWindowDelivery(t, "write-timeout-1", settled))
	}()
	select {
	case err := <-submitDone:
		if err == nil {
			t.Fatal("blocked write returned no error")
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("blocked SMPP write had no timeout")
	}
	select {
	case requeue := <-settled:
		if !requeue {
			t.Fatal("write timeout did not requeue the delivery")
		}
	case <-time.After(time.Second):
		t.Fatal("write timeout left the delivery unsettled")
	}
}

func TestSessionBoundsSubmitDecoderCall(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	session := smppc.NewSessionWithDecoder(client, smppc.Config{
		CID: "decoder-timeout", PDUTimeout: 0.05,
	}, nil, nil, blockingSubmitDecoder{}, nil)
	settled := make(chan bool, 1)
	submitDone := make(chan error, 1)
	go func() {
		submitDone <- session.Submit(context.Background(), newWindowDelivery(t, "decoder-timeout-1", settled))
	}()
	select {
	case err := <-submitDone:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("decoder error = %v, want deadline exceeded", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("submit decoder call had no timeout")
	}
	select {
	case requeue := <-settled:
		if !requeue {
			t.Fatal("decoder timeout did not requeue the delivery")
		}
	case <-time.After(time.Second):
		t.Fatal("decoder timeout left the delivery unsettled")
	}
}

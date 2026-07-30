package smppc_test

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/smppc"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

type chunkedWriteConn struct {
	net.Conn
	maximum int
}

func (c *chunkedWriteConn) Write(data []byte) (int, error) {
	if len(data) > c.maximum {
		data = data[:c.maximum]
	}
	return c.Conn.Write(data)
}

func TestSessionCorrelation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	cfg := smppc.Config{
		CID:        "test-session",
		ResTimeout: 1, // 1 second
	}

	connChan := make(chan net.Conn, 1)
	go func() {
		conn, _ := ln.Accept()
		if conn != nil {
			connChan <- conn
		}
	}()

	clientConn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())

	session := smppc.NewSession(clientConn, cfg, retry, readiness, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.Run(ctx)

	// Accept on server side
	serverConn := <-connChan
	defer serverConn.Close()

	// Submit a message
	headers := make(map[string]amqpcompat.Field)
	props, _ := amqpcompat.NewProperties("msg-1", headers)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.test", props, []byte("hello"))

	ackChan := make(chan bool, 1)
	delivery := injectDelivery(envelope, func(requeue bool) { ackChan <- requeue })

	err = session.Submit(ctx, delivery)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	// Server: Read SUBMIT_SM
	pdu, err := smppwire.Read(serverConn, 1024)
	if err != nil {
		t.Fatalf("Server Read failed: %v", err)
	}
	if pdu.Header.CommandID != smppwire.CommandSubmitSM {
		t.Errorf("expected SUBMIT_SM, got %#x", pdu.Header.CommandID)
	}

	// Server: Send SUBMIT_SM_RESP
	resp := smppwire.PDU{
		Header: smppwire.Header{
			CommandID:      smppwire.CommandSubmitSMResp,
			SequenceNumber: pdu.Header.SequenceNumber,
			CommandStatus:  0,
		},
		SubmitResponse: &smppwire.SubmitResponseBody{
			MessageID: []byte("smpp-123"),
		},
	}
	wire, _ := smppwire.Encode(resp)
	serverConn.Write(wire)

	// Verify ACK
	select {
	case requeue := <-ackChan:
		if requeue {
			t.Errorf("expected ACK (requeue=false), got requeue=true")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ACK")
	}
}

func TestSessionTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	addr := ln.Addr().(*net.TCPAddr)
	cfg := smppc.Config{
		CID:        "test-timeout",
		ResTimeout: 0.2, // 200ms
	}

	clientConn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer clientConn.Close()

	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())

	session := smppc.NewSession(clientConn, cfg, retry, readiness, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.Run(ctx)

	// Submit a message
	headers := make(map[string]amqpcompat.Field)
	props, _ := amqpcompat.NewProperties("msg-2", headers)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.test", props, []byte("hello"))

	ackChan := make(chan bool, 1)
	delivery := injectDelivery(envelope, func(requeue bool) { ackChan <- requeue })

	_ = session.Submit(ctx, delivery)

	// Wait for timeout (Server does nothing)
	select {
	case requeue := <-ackChan:
		if !requeue {
			t.Errorf("expected Requeue (requeue=true) on timeout")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for timeout settlement")
	}
}

func TestSessionCancellationRequeuesPendingExactlyOnce(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(client, smppc.Config{CID: "cancel", ResTimeout: 5}, retry, readiness, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- session.Run(ctx) }()
	read := make(chan struct{})
	go func() {
		_, _ = smppwire.Read(server, 1024)
		close(read)
	}()

	props, _ := amqpcompat.NewProperties("cancel-1", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.cancel", props, []byte("hello"))
	settled := make(chan bool, 2)
	if err := session.Submit(ctx, injectDelivery(envelope, func(requeue bool) { settled <- requeue })); err != nil {
		t.Fatal(err)
	}
	<-read
	cancel()
	select {
	case requeue := <-settled:
		if !requeue {
			t.Fatal("cancellation did not requeue pending delivery")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation left pending delivery unsettled")
	}
	select {
	case duplicate := <-settled:
		t.Fatalf("duplicate settlement after cancellation: requeue=%v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session Run did not stop after cancellation")
	}
}

func TestSessionWritesValidEnquireLink(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(client, smppc.Config{CID: "enquire", EnquireLinkInterval: 0.02}, retry, readiness, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.Run(ctx)

	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	pdu, err := smppwire.Read(server, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if pdu.Header.CommandID != smppwire.CommandEnquireLink {
		t.Fatalf("command = %#x, want enquire_link", pdu.Header.CommandID)
	}
}

func TestSessionMissingEnquireLinkResponseTerminates(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(client, smppc.Config{
		CID: "enquire-timeout", EnquireLinkInterval: 0.02, ResTimeout: 0.05,
	}, retry, readiness, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- session.Run(ctx) }()

	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	first, err := smppwire.Read(server, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if first.Header.CommandID != smppwire.CommandEnquireLink {
		t.Fatalf("command = %#x, want enquire_link", first.Header.CommandID)
	}
	mismatched, err := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{
		CommandID: smppwire.CommandEnquireLinkResp, SequenceNumber: first.Header.SequenceNumber + 100,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write(mismatched); err != nil {
		t.Fatal(err)
	}
	// Keep draining requests without sending a matching response so successful writes continually
	// refresh the coarse inactivity timer. The correlated response timer must
	// still terminate the session.
	go func() {
		for {
			if _, readErr := smppwire.Read(server, 1024); readErr != nil {
				return
			}
		}
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("missing enquire_link_resp terminated without an error")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("session stayed alive without enquire_link_resp")
	}
}

func TestSessionRespondsToEnquireLinkWithMatchingSequence(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(client, smppc.Config{CID: "peer-enquire"}, retry, readiness, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go session.Run(ctx)

	request, err := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{
		CommandID: smppwire.CommandEnquireLink, SequenceNumber: 77,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write(request); err != nil {
		t.Fatal(err)
	}
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	response, err := smppwire.Read(server, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header.CommandID != smppwire.CommandEnquireLinkResp || response.Header.SequenceNumber != 77 {
		t.Fatalf("response = %+v", response.Header)
	}
}

func TestSessionConnectionLossRequeuesPendingExactlyOnce(t *testing.T) {
	client, server := net.Pipe()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(client, smppc.Config{CID: "loss", ResTimeout: 5, WindowSize: 2}, retry, readiness, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- session.Run(ctx) }()

	settled := make(chan bool, 4)
	readDone := make(chan error, 1)
	go func() {
		for index := 0; index < 2; index++ {
			if _, readErr := smppwire.Read(server, 1024); readErr != nil {
				readDone <- readErr
				return
			}
		}
		readDone <- nil
	}()
	for index := 0; index < 2; index++ {
		properties, _ := amqpcompat.NewProperties(fmt.Sprintf("loss-message-%d", index), nil)
		envelope, _ := amqpcompat.NewEnvelope("submit.sm.loss", properties, []byte("hello"))
		if err := session.Submit(ctx, injectDelivery(envelope, func(requeue bool) { settled <- requeue })); err != nil {
			t.Fatal(err)
		}
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	_ = server.Close()
	for index := 0; index < 2; index++ {
		select {
		case requeue := <-settled:
			if !requeue {
				t.Fatal("connection loss did not requeue pending delivery")
			}
		case <-time.After(time.Second):
			t.Fatal("connection loss left delivery pending")
		}
	}
	select {
	case duplicate := <-settled:
		t.Fatalf("duplicate settlement after connection loss: %v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session did not terminate after connection loss")
	}
}

func TestSessionWriteFailureSettlesExactlyOnce(t *testing.T) {
	client, server := net.Pipe()
	_ = server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(client, smppc.Config{CID: "write-fail", ResTimeout: 0.02}, retry, readiness, nil)
	props, _ := amqpcompat.NewProperties("write-fail-1", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.write-fail", props, []byte("hello"))
	settled := make(chan bool, 2)
	err := session.Submit(context.Background(), injectDelivery(envelope, func(requeue bool) { settled <- requeue }))
	if err == nil {
		t.Fatal("expected socket write error")
	}
	select {
	case requeue := <-settled:
		if !requeue {
			t.Fatal("write failure did not requeue")
		}
	case <-time.After(time.Second):
		t.Fatal("write failure left delivery unsettled")
	}
	select {
	case duplicate := <-settled:
		t.Fatalf("duplicate settlement after write failure: requeue=%v", duplicate)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSessionSerializesCompleteConcurrentWrites(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(&chunkedWriteConn{Conn: client, maximum: 3}, smppc.Config{CID: "serialized", WindowSize: 12}, retry, readiness, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- session.Run(ctx) }()

	const count = 12
	errs := make(chan error, count)
	var submits sync.WaitGroup
	for i := 0; i < count; i++ {
		submits.Add(1)
		go func(index int) {
			defer submits.Done()
			props, _ := amqpcompat.NewProperties("serialized", nil)
			envelope, _ := amqpcompat.NewEnvelope("submit.sm.serialized", props, []byte{byte(index)})
			errs <- session.Submit(ctx, injectDelivery(envelope, func(bool) {}))
		}(i)
	}
	seen := make(map[byte]bool, count)
	for i := 0; i < count; i++ {
		pdu, err := smppwire.Read(server, 1024)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		if pdu.Header.CommandID != smppwire.CommandSubmitSM || len(pdu.SM.ShortMessage) != 1 {
			t.Fatalf("frame %d was interleaved or incomplete: %#v", i, pdu)
		}
		seen[pdu.SM.ShortMessage[0]] = true
	}
	submits.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Submit: %v", err)
		}
	}
	if len(seen) != count {
		t.Fatalf("decoded unique payloads = %d, want %d", len(seen), count)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session did not stop")
	}
}

func TestSessionResponseTimeoutStartsAfterCompleteWrite(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(client, smppc.Config{CID: "post-write-timeout", ResTimeout: 0.2, WindowSize: 2}, retry, readiness, nil)
	settled := make(chan bool, 4)
	submitDone := make(chan error, 2)
	for i := 0; i < 2; i++ {
		props, _ := amqpcompat.NewProperties("queued", nil)
		envelope, _ := amqpcompat.NewEnvelope("submit.sm.queued", props, []byte{byte(i)})
		go func(delivery *amqpcompat.Delivery) {
			submitDone <- session.Submit(context.Background(), delivery)
		}(injectDelivery(envelope, func(requeue bool) { settled <- requeue }))
	}
	// Both submissions are blocked before a complete socket write. Their SMPP
	// response deadline must not include serialized-writer queueing time.
	time.Sleep(250 * time.Millisecond)
	select {
	case requeue := <-settled:
		t.Fatalf("delivery settled before its frame reached the wire: requeue=%v", requeue)
	default:
	}
	for i := 0; i < 2; i++ {
		if _, err := smppwire.Read(server, 1024); err != nil {
			t.Fatalf("read queued frame %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := <-submitDone; err != nil {
			t.Fatalf("Submit %d: %v", i, err)
		}
	}
}

func TestSessionRunWaitsForResponseSettlementHandler(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(client, smppc.Config{CID: "joined-reader", ResTimeout: 5}, retry, readiness, nil)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- session.Run(ctx) }()

	ackEntered := make(chan struct{})
	releaseAck := make(chan struct{})
	props, _ := amqpcompat.NewProperties("joined-reader", nil)
	envelope, _ := amqpcompat.NewEnvelope("submit.sm.joined-reader", props, []byte("hello"))
	submitDone := make(chan error, 1)
	go func() {
		submitDone <- session.Submit(ctx, injectDelivery(envelope, func(bool) {
			close(ackEntered)
			<-releaseAck
		}))
	}()
	submit, err := smppwire.Read(server, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-submitDone; err != nil {
		t.Fatal(err)
	}
	response, err := smppwire.Encode(smppwire.PDU{
		Header:         smppwire.Header{CommandID: smppwire.CommandSubmitSMResp, SequenceNumber: submit.Header.SequenceNumber},
		SubmitResponse: &smppwire.SubmitResponseBody{MessageID: []byte("smsc")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write(response); err != nil {
		t.Fatal(err)
	}
	<-ackEntered
	cancel()
	select {
	case err := <-runDone:
		t.Fatalf("Run returned before settlement handler completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseAck)
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after settlement handler completed")
	}
}

package smppc_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

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

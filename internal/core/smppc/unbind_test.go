package smppc_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/smppc"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func TestSessionGracefulUnbindRoundTrip(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	retry, _ := smppc.NewErrorRetryPolicy(smppc.DefaultErrorRetryRules())
	readiness, _ := smppc.NewReadinessPolicy(smppc.DefaultReadinessConfig())
	session := smppc.NewSession(client, smppc.Config{TrxTimeout: 1}, retry, readiness, nil)
	runDone := make(chan error, 1)
	go func() { runDone <- session.Run(context.Background()) }()

	unbindDone := make(chan error, 1)
	go func() { unbindDone <- session.Unbind(context.Background()) }()
	request, err := smppwire.Read(server, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatal(err)
	}
	if request.Header.CommandID != smppwire.CommandUnbind {
		t.Fatalf("command=%#x", request.Header.CommandID)
	}
	response, err := smppwire.Encode(smppwire.PDU{Header: smppwire.Header{
		CommandID: smppwire.CommandUnbindResp, SequenceNumber: request.Header.SequenceNumber,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write(response); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-unbindDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unbind did not complete")
	}
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("session did not stop after unbind response")
	}
}

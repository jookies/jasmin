package smppsdelivery_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/app/smppsdelivery"
	"github.com/pumpitspace/jasmin/internal/app/smppsserver"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/core/smpps"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type nopSubmitter struct{}

func (nopSubmitter) Submit(context.Context, core.SubmitRequest) (string, error) { return "x", nil }

// The composed path end to end: a bound SMPPS receiver receives a deliver_sm
// receipt pushed through the ReceiptSink over a running smppsserver — the exact
// wiring the gateway installs into the DLR thrower.
func TestReceiptSinkDeliversToBoundSession(t *testing.T) {
	service, err := smppsserver.NewService(smppsserver.Config{
		BindAddr: "127.0.0.1:0",
		Users:    []smppsserver.UserConfig{{SystemID: "alice", Password: "secret"}},
	}, nopSubmitter{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = service.Run(ctx) }()
	t.Cleanup(func() { cancel(); _ = service.Close() })

	conn, err := net.Dial("tcp", service.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Bind as a transceiver so the session is a delivery target.
	bindFrame, err := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{CommandID: smpps.CommandBindTransceiver, SequenceNumber: 1},
		Bind:   &smppwire.BindBody{SystemID: []byte("alice"), Password: []byte("secret"), SystemType: []byte(""), InterfaceVersion: 0x34},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(bindFrame); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if resp, err := smppwire.Read(conn, smppwire.DefaultMaxSize); err != nil || resp.Header.CommandStatus != smpps.StatusROK {
		t.Fatalf("bind resp = %+v err=%v", resp.Header, err)
	}

	sink, err := smppsdelivery.NewReceiptSink(service.Server())
	if err != nil {
		t.Fatal(err)
	}
	params := dlr.SMPPSReceiptParams{
		MsgID: "m-1", SystemID: "alice", MessageStatus: "DELIVRD", Err: "0",
		SubDate: "2026-01-02 03:04:05", SourceAddr: "1111", DestAddr: "2222",
	}
	// Retry until the async bind has registered the session.
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := sink.DeliverReceipt(context.Background(), params)
		if err == nil {
			break
		}
		if !errors.Is(err, smpps.ErrNoBoundSession) {
			t.Fatalf("DeliverReceipt: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("session never became a delivery target")
		}
		time.Sleep(5 * time.Millisecond)
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatalf("read deliver_sm: %v", err)
	}
	if got.Header.CommandID != smppwire.CommandDeliverSM {
		t.Fatalf("delivered command = %#x, want deliver_sm", got.Header.CommandID)
	}
	// The receipt swaps addressing and carries the receipted_message_id TLV.
	if string(got.SM.SourceAddress) != "2222" || string(got.SM.DestinationAddress) != "1111" {
		t.Fatalf("receipt addressing = %s -> %s", got.SM.SourceAddress, got.SM.DestinationAddress)
	}
	if string(got.SM.Optional.ReceiptedMessageID) != "m-1" {
		t.Fatalf("receipted_message_id = %q", got.SM.Optional.ReceiptedMessageID)
	}
}

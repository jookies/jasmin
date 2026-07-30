package smppsdelivery_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/app/smppsdelivery"
	"github.com/pumpitspace/synevyr/internal/app/smppsserver"
	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/dlr"
	"github.com/pumpitspace/synevyr/internal/core/smpps"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
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
	result := make(chan error, 1)
	go func() {
		result <- sink.DeliverReceipt(context.Background(), params)
	}()

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
	writeDeliverSMResp(t, conn, got.Header.SequenceNumber)
	if err := <-result; err != nil {
		t.Fatalf("DeliverReceipt: %v", err)
	}
}

// The MO path end to end: a bound SMPPS receiver receives an MO deliver_sm
// pushed through the MOSink over a running smppsserver.
func TestMOSinkDeliversToBoundSession(t *testing.T) {
	service, err := smppsserver.NewService(smppsserver.Config{
		BindAddr: "127.0.0.1:0",
		Users:    []smppsserver.UserConfig{{SystemID: "bob", Password: "pw"}},
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
	bindFrame, _ := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{CommandID: smpps.CommandBindReceiver, SequenceNumber: 1},
		Bind:   &smppwire.BindBody{SystemID: []byte("bob"), Password: []byte("pw"), SystemType: []byte(""), InterfaceVersion: 0x34},
	})
	if _, err := conn.Write(bindFrame); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if resp, err := smppwire.Read(conn, smppwire.DefaultMaxSize); err != nil || resp.Header.CommandStatus != smpps.StatusROK {
		t.Fatalf("bind resp = %+v err=%v", resp.Header, err)
	}

	sink, err := smppsdelivery.NewMOSink(service.Server())
	if err != nil {
		t.Fatal(err)
	}
	mo := &smppwire.SMBody{SourceAddress: []byte("111"), DestinationAddress: []byte("bob"), ShortMessage: []byte("hello-mo")}
	result := make(chan error, 1)
	go func() {
		result <- sink.DeliverMO(context.Background(), "bob", mo)
	}()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := smppwire.Read(conn, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatalf("read deliver_sm: %v", err)
	}
	if got.Header.CommandID != smppwire.CommandDeliverSM || string(got.SM.ShortMessage) != "hello-mo" {
		t.Fatalf("delivered = %#x %q", got.Header.CommandID, got.SM.ShortMessage)
	}
	writeDeliverSMResp(t, conn, got.Header.SequenceNumber)
	if err := <-result; err != nil {
		t.Fatalf("DeliverMO: %v", err)
	}
}

func writeDeliverSMResp(t *testing.T, conn net.Conn, sequence uint32) {
	t.Helper()
	frame, err := smppwire.Encode(smppwire.PDU{
		Header: smppwire.Header{
			CommandID:      smppwire.CommandDeliverSMResp,
			CommandStatus:  smpps.StatusROK,
			SequenceNumber: sequence,
		},
		SubmitResponse: &smppwire.SubmitResponseBody{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(frame); err != nil {
		t.Fatal(err)
	}
}

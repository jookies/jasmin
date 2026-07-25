package smppsdelivery

import (
	"context"
	"errors"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/core/smpps"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type recordingDeliverer struct {
	systemID string
	pdu      smppwire.PDU
	err      error
}

func (r *recordingDeliverer) Deliver(_ context.Context, systemID string, pdu smppwire.PDU) error {
	r.systemID = systemID
	r.pdu = pdu
	return r.err
}

func validParams() dlr.SMPPSReceiptParams {
	return dlr.SMPPSReceiptParams{
		MsgID: "m-1", SystemID: "alice", MessageStatus: "DELIVRD", Err: "0",
		SubDate: "2026-01-02 03:04:05", SourceAddr: "1111", DestAddr: "2222",
	}
}

func TestReceiptSinkBuildsAndDelivers(t *testing.T) {
	deliverer := &recordingDeliverer{}
	sink, err := NewReceiptSink(deliverer)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.DeliverReceipt(context.Background(), validParams()); err != nil {
		t.Fatal(err)
	}
	if deliverer.systemID != "alice" {
		t.Fatalf("system_id = %q", deliverer.systemID)
	}
	if deliverer.pdu.Header.CommandID != smppwire.CommandDeliverSM {
		t.Fatalf("command = %#x", deliverer.pdu.Header.CommandID)
	}
	// The receipt swaps source/destination and carries the receipt TLVs.
	if string(deliverer.pdu.SM.SourceAddress) != "2222" || string(deliverer.pdu.SM.DestinationAddress) != "1111" {
		t.Fatalf("addresses not swapped: %+v", deliverer.pdu.SM)
	}
	if string(deliverer.pdu.SM.Optional.ReceiptedMessageID) != "m-1" {
		t.Fatalf("receipted_message_id = %q", deliverer.pdu.SM.Optional.ReceiptedMessageID)
	}
}

func TestReceiptSinkNoBoundSessionPropagates(t *testing.T) {
	deliverer := &recordingDeliverer{err: smpps.ErrNoBoundSession}
	sink, _ := NewReceiptSink(deliverer)
	if err := sink.DeliverReceipt(context.Background(), validParams()); !errors.Is(err, smpps.ErrNoBoundSession) {
		t.Fatalf("error = %v, want ErrNoBoundSession (thrower retries)", err)
	}
}

func TestReceiptSinkRequiresSystemID(t *testing.T) {
	sink, _ := NewReceiptSink(&recordingDeliverer{})
	params := validParams()
	params.SystemID = ""
	if err := sink.DeliverReceipt(context.Background(), params); err == nil {
		t.Fatal("want error for empty system_id")
	}
}

// Compile-time proof the adapter satisfies the thrower seam.
var _ dlr.SMPPSReceiptSink = (*ReceiptSink)(nil)

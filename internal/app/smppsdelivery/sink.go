// Package smppsdelivery adapts the SMPPS server into the DLR/MO throwers'
// delivery seam: a receipt Forward becomes a deliver_sm pushed down the
// system_id's bound receiver/transceiver session.
package smppsdelivery

import (
	"context"
	"fmt"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/dlr"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// Deliverer is the SMPPS server's push interface (implemented by *smpps.Server).
type Deliverer interface {
	Deliver(ctx context.Context, systemID string, pdu smppwire.PDU) error
}

// ReceiptSink implements dlr.SMPPSReceiptSink over an SMPPS server: it builds
// the legacy deliver_sm receipt and delivers it to the sender's bound session.
// When no session is bound it returns the server's ErrNoBoundSession, which the
// thrower's retry policy treats like any transient delivery failure — matching
// the legacy behavior of requeuing a receipt with no bound recipient.
type ReceiptSink struct {
	server Deliverer
	now    func() time.Time
}

func NewReceiptSink(server Deliverer) (*ReceiptSink, error) {
	if server == nil {
		return nil, fmt.Errorf("smppsdelivery: nil server")
	}
	return &ReceiptSink{server: server, now: time.Now}, nil
}

func (s *ReceiptSink) DeliverReceipt(ctx context.Context, params dlr.SMPPSReceiptParams) error {
	if params.SystemID == "" {
		return fmt.Errorf("smppsdelivery: receipt has no system_id")
	}
	pdu, err := dlr.BuildDeliverSMReceipt(params, s.now())
	if err != nil {
		return err
	}
	return s.server.Deliver(ctx, params.SystemID, pdu)
}

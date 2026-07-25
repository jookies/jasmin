package smppsdelivery

import (
	"context"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// MOSink implements mo.MODeliverySink over an SMPPS server: an MO deliver_sm is
// pushed to the destination system_id's bound receiver/transceiver session.
// ErrNoBoundSession (from the server) propagates so the MO thrower retries,
// matching the legacy no-bound-recipient requeue.
type MOSink struct {
	server Deliverer
}

func NewMOSink(server Deliverer) (*MOSink, error) {
	if server == nil {
		return nil, fmt.Errorf("smppsdelivery: nil server")
	}
	return &MOSink{server: server}, nil
}

// DeliverMO delivers the MO deliver_sm to the system_id's bound session.
func (s *MOSink) DeliverMO(ctx context.Context, systemID string, sm *smppwire.SMBody) error {
	if systemID == "" {
		return fmt.Errorf("smppsdelivery: MO delivery has no system_id")
	}
	pdu := smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM},
		SM:     sm,
	}
	return s.server.Deliver(ctx, systemID, pdu)
}

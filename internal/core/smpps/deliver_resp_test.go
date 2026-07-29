package smpps

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// An ESME MUST answer every deliver_sm with a deliver_sm_resp (SMPP 3.4 §4.6).
// The session has to stay open afterwards, because every MO and every delivery
// receipt sent to an SMPP-bound customer produces exactly this ack. If the ack
// is treated as an unsupported *request* the bind is torn down on the first
// message the customer receives, which makes SMPPs delivery unusable in
// production even though TestDeliverToBoundTransceiver passes -- that test
// never acks.
func TestDeliverSMRespKeepsTheBindOpen(t *testing.T) {
	server, addr := startServer(t, mapResolver{"u": testUser("p")}, ServerConfig{})
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindTransceiver, "u", "p", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusROK {
		t.Fatal("bind should succeed")
	}

	deliver := smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 100},
		SM: &smppwire.SMBody{
			SourceAddress:      []byte("111"),
			DestinationAddress: []byte("222"),
			ShortMessage:       []byte("mo"),
		},
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := server.Deliver(context.Background(), "u", deliver); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Deliver never succeeded")
		}
		time.Sleep(5 * time.Millisecond)
	}

	got := readPDU(t, conn)
	if got.Header.CommandID != smppwire.CommandDeliverSM {
		t.Fatalf("expected deliver_sm, got %#x", got.Header.CommandID)
	}

	// The customer acks, exactly as a real ESME does.
	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{
			CommandID:      smppwire.CommandDeliverSMResp,
			SequenceNumber: got.Header.SequenceNumber,
			CommandStatus:  StatusROK,
		},
		// deliver_sm_resp carries a message_id C-string, conventionally NULL.
		SubmitResponse: &smppwire.SubmitResponseBody{},
	})

	// The bind must still be usable. enquire_link is the cheapest proof.
	writePDU(t, conn, smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandEnquireLink, SequenceNumber: 101},
	})
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	resp := readPDU(t, conn)
	if resp.Header.CommandID != smppwire.CommandEnquireLinkResp {
		t.Fatalf("after deliver_sm_resp the session answered %#x (status %#x); "+
			"the ack was treated as an unsupported request and the bind was torn down",
			resp.Header.CommandID, resp.Header.CommandStatus)
	}
}

package smppc

import (
	"testing"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// TestHandleDataSMReceiptPublishesDLRAndResponds proves an inbound data_sm
// carrying a delivery receipt is classified and published like a deliver_sm,
// and the session answers data_sm_resp (not deliver_sm_resp).
func TestHandleDataSMReceiptPublishesDLRAndResponds(t *testing.T) {
	session, server, _ := newDeliverTestSession(t)
	publisher := &capturePublisher{}
	session.SetDeliverUpstream(publisher, &fakeDeliverEncoder{})

	body := &smppwire.SMBody{
		SourceAddress: []byte("1111"), DestinationAddress: []byte("2222"),
	}
	receiptID := []byte("ABC123")
	state := byte(2) // DELIVERED
	body.Optional.ReceiptedMessageID = receiptID
	body.Optional.MessageState = &state
	pdu := smppwire.PDU{Header: smppwire.Header{CommandID: smppwire.CommandDataSM, SequenceNumber: 11}, SM: body}

	done := make(chan smppwire.PDU, 1)
	go func() { done <- readResponse(t, server) }()
	if err := session.handleDeliver(pdu); err != nil {
		t.Fatal(err)
	}
	response := <-done
	if response.Header.CommandID != smppwire.CommandDataSMResp {
		t.Fatalf("response command = %#x want data_sm_resp", response.Header.CommandID)
	}
	if response.Header.CommandStatus != 0 {
		t.Fatalf("response status = %#x want ROK", response.Header.CommandStatus)
	}
	if len(publisher.published) != 1 {
		t.Fatalf("published=%d want 1 (DLR)", len(publisher.published))
	}
	publication := publisher.published[0]
	if publication.routingKey != "dlr.deliver_sm" {
		t.Fatalf("routing key=%s want dlr.deliver_sm", publication.routingKey)
	}
	if string(publication.envelope.Body()) != "DELIVRD" {
		t.Fatalf("dlr body=%q want DELIVRD", publication.envelope.Body())
	}
}

// TestDataSMResponseCommandDerivesFromRequest guards the resp-command mapping:
// data_sm (0x103) -> data_sm_resp (0x80000103), deliver_sm -> deliver_sm_resp.
func TestDataSMResponseCommandDerivesFromRequest(t *testing.T) {
	for _, testCase := range []struct {
		request  uint32
		response uint32
	}{
		{smppwire.CommandDataSM, smppwire.CommandDataSMResp},
		{smppwire.CommandDeliverSM, smppwire.CommandDeliverSMResp},
	} {
		if got := testCase.request | 0x80000000; got != testCase.response {
			t.Fatalf("%#x|0x80000000 = %#x want %#x", testCase.request, got, testCase.response)
		}
	}
}

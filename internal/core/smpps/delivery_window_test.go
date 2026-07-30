package smpps

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

func TestDeliverWaitsForMatchingResponse(t *testing.T) {
	server, conn := boundReceiver(t, ServerConfig{})
	result := make(chan error, 1)
	go func() {
		result <- server.Deliver(context.Background(), "u", testDeliverSM())
	}()

	deliver := readPDU(t, conn)
	select {
	case err := <-result:
		t.Fatalf("Deliver returned before deliver_sm_resp: %v", err)
	default:
	}

	writePDU(t, conn, deliverSMResp(deliver.Header.SequenceNumber+1, StatusROK))
	select {
	case err := <-result:
		t.Fatalf("Deliver returned for unrelated sequence_number: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	writePDU(t, conn, deliverSMResp(deliver.Header.SequenceNumber, StatusROK))
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Deliver returned matching response error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Deliver did not return after matching deliver_sm_resp")
	}
}

func TestDeliverReturnsNegativeResponse(t *testing.T) {
	server, conn := boundReceiver(t, ServerConfig{})
	result := make(chan error, 1)
	go func() {
		result <- server.Deliver(context.Background(), "u", testDeliverSM())
	}()

	deliver := readPDU(t, conn)
	writePDU(t, conn, deliverSMResp(deliver.Header.SequenceNumber, StatusSystemError))

	select {
	case err := <-result:
		var responseErr *DeliverSMResponseError
		if !errors.As(err, &responseErr) {
			t.Fatalf("Deliver error = %v, want DeliverSMResponseError", err)
		}
		if responseErr.CommandStatus != StatusSystemError ||
			responseErr.SequenceNumber != deliver.Header.SequenceNumber {
			t.Fatalf("response error = %+v", responseErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Deliver did not return the negative deliver_sm_resp")
	}
}

func TestDeliverReturnsGenericNACK(t *testing.T) {
	server, conn := boundReceiver(t, ServerConfig{})
	result := make(chan error, 1)
	go func() {
		result <- server.Deliver(context.Background(), "u", testDeliverSM())
	}()

	deliver := readPDU(t, conn)
	writePDU(t, conn, smppwire.PDU{Header: smppwire.Header{
		CommandID:      smppwire.CommandGenericNACK,
		CommandStatus:  smppwire.StatusInvalidCommandID,
		SequenceNumber: deliver.Header.SequenceNumber,
	}})

	select {
	case err := <-result:
		var responseErr *DeliverSMResponseError
		if !errors.As(err, &responseErr) ||
			responseErr.CommandID != smppwire.CommandGenericNACK {
			t.Fatalf("Deliver error = %v, want generic_nack response error", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Deliver did not return the generic_nack")
	}
}

func TestDeliverTimesOutWaitingForResponse(t *testing.T) {
	server, conn := boundReceiver(t, ServerConfig{
		DeliverSMResponseTimeout: 40 * time.Millisecond,
	})
	result := make(chan error, 1)
	go func() {
		result <- server.Deliver(context.Background(), "u", testDeliverSM())
	}()
	_ = readPDU(t, conn)

	select {
	case err := <-result:
		if !errors.Is(err, ErrDeliverSMResponseTimeout) {
			t.Fatalf("Deliver error = %v, want ErrDeliverSMResponseTimeout", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Deliver did not enforce its response timeout")
	}
}

func TestDeliverWindowBlocksUntilOutstandingRequestSettles(t *testing.T) {
	server, conn := boundReceiver(t, ServerConfig{
		DeliverSMWindowSize:      1,
		DeliverSMResponseTimeout: 2 * time.Second,
	})
	firstResult := make(chan error, 1)
	go func() {
		firstResult <- server.Deliver(context.Background(), "u", testDeliverSM())
	}()
	first := readPDU(t, conn)

	secondResult := make(chan error, 1)
	go func() {
		secondResult <- server.Deliver(context.Background(), "u", testDeliverSM())
	}()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	if _, err := smppwire.Read(conn, smppwire.DefaultMaxSize); err == nil {
		t.Fatal("second deliver_sm exceeded the one-request window")
	}

	writePDU(t, conn, deliverSMResp(first.Header.SequenceNumber, StatusROK))
	if err := <-firstResult; err != nil {
		t.Fatalf("first Deliver: %v", err)
	}
	second := readPDU(t, conn)
	writePDU(t, conn, deliverSMResp(second.Header.SequenceNumber, StatusROK))
	if err := <-secondResult; err != nil {
		t.Fatalf("second Deliver: %v", err)
	}
}

func boundReceiver(t *testing.T, cfg ServerConfig) (*Server, net.Conn) {
	t.Helper()
	server, addr := startServer(t, mapResolver{"u": testUser("p")}, cfg)
	conn := dial(t, addr)
	writePDU(t, conn, bindPDU(CommandBindReceiver, "u", "p", 1))
	if readPDU(t, conn).Header.CommandStatus != StatusROK {
		t.Fatal("bind should succeed")
	}
	return server, conn
}

func testDeliverSM() smppwire.PDU {
	return smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM},
		SM: &smppwire.SMBody{
			SourceAddress:      []byte("111"),
			DestinationAddress: []byte("222"),
			ShortMessage:       []byte("message"),
		},
	}
}

func deliverSMResp(sequence, status uint32) smppwire.PDU {
	pdu := smppwire.PDU{
		Header: smppwire.Header{
			CommandID:      smppwire.CommandDeliverSMResp,
			CommandStatus:  status,
			SequenceNumber: sequence,
		},
	}
	if status == StatusROK {
		pdu.SubmitResponse = &smppwire.SubmitResponseBody{}
	}
	return pdu
}

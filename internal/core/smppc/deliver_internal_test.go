package smppc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

type capturedPublish struct {
	exchange   string
	routingKey string
	envelope   amqpcompat.Envelope
}

type capturePublisher struct {
	published []capturedPublish
	err       error
}

func (p *capturePublisher) Publish(_ context.Context, exchange, routingKey string, envelope amqpcompat.Envelope) error {
	p.published = append(p.published, capturedPublish{exchange: exchange, routingKey: routingKey, envelope: envelope})
	return p.err
}

type fakeDeliverEncoder struct {
	wire    []byte
	cid     string
	pickled []byte
	err     error
}

func (e *fakeDeliverEncoder) EncodeRoutableDeliverSM(_ context.Context, wire []byte, cid string) ([]byte, error) {
	e.wire = append([]byte(nil), wire...)
	e.cid = cid
	return e.pickled, e.err
}

func deliverPDU(shortMessage []byte, mutate func(*smppwire.SMBody)) smppwire.PDU {
	body := &smppwire.SMBody{
		SourceAddress:      []byte("1111"),
		DestinationAddress: []byte("2222"),
		ShortMessage:       shortMessage,
	}
	if mutate != nil {
		mutate(body)
	}
	return smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM, SequenceNumber: 7},
		SM:     body,
	}
}

func newDeliverTestSession(t *testing.T) (*Session, net.Conn, *bytes.Buffer) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	session := NewSessionWithDecoder(client, Config{CID: "cid-1"}, nil, nil, nil, nil)
	var logged bytes.Buffer
	session.SetSubmitAuditLogger(slog.New(slog.NewTextHandler(&logged, nil)), false)
	return session, server, &logged
}

func readResponse(t *testing.T, server net.Conn) smppwire.PDU {
	t.Helper()
	_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
	pdu, err := smppwire.Read(server, smppwire.DefaultMaxSize)
	if err != nil {
		t.Fatalf("read deliver_sm_resp: %v", err)
	}
	return pdu
}

func TestHandleDeliverReceiptPublishesDLR(t *testing.T) {
	session, server, _ := newDeliverTestSession(t)
	publisher := &capturePublisher{}
	session.SetDeliverUpstream(publisher, &fakeDeliverEncoder{})

	receiptText := "id:255 sub:001 dlvrd:001 submit date:2107261200 done date:2107261201 stat:DELIVRD err:000 text:ok"
	session.cfg.DLRMsgIDBases = 1 // decimal receipt id -> hex msgid
	pdu := deliverPDU([]byte(receiptText), nil)

	done := make(chan smppwire.PDU, 1)
	go func() { done <- readResponse(t, server) }()
	if err := session.handleDeliver(pdu); err != nil {
		t.Fatal(err)
	}
	response := <-done
	if response.Header.CommandID != smppwire.CommandDeliverSM|0x80000000 || response.Header.CommandStatus != 0 {
		t.Fatalf("response=%#x status=%#x", response.Header.CommandID, response.Header.CommandStatus)
	}
	if len(publisher.published) != 1 {
		t.Fatalf("published=%d want 1", len(publisher.published))
	}
	publication := publisher.published[0]
	if publication.routingKey != "dlr.deliver_sm" || publication.exchange != "messaging" {
		t.Fatalf("published to %s/%s", publication.exchange, publication.routingKey)
	}
	if got := publication.envelope.Properties().MessageID(); got != "FF" {
		t.Fatalf("coded message id=%q want FF (255 dec->hex)", got)
	}
	if got := string(publication.envelope.Body()); got != "DELIVRD" {
		t.Fatalf("body=%q want DELIVRD", got)
	}
	headers := publication.envelope.Properties().Headers()
	for key, want := range map[string]string{
		"type": "deliver_sm", "cid": "cid-1", "dlr_id": "255", "dlr_stat": "DELIVRD",
		"dlr_sub": "001", "dlr_dlvrd": "001", "dlr_sdate": "2107261200",
		"dlr_ddate": "2107261201", "dlr_err": "000", "dlr_text": "ok",
	} {
		value, _ := headers[key].String()
		if value != want {
			t.Fatalf("header %s=%q want %q", key, value, want)
		}
	}
}

func TestHandleDeliverMOPublishesRoutable(t *testing.T) {
	session, server, logged := newDeliverTestSession(t)
	publisher := &capturePublisher{}
	encoder := &fakeDeliverEncoder{pickled: []byte("pickled-routable")}
	session.SetDeliverUpstream(publisher, encoder)

	pdu := deliverPDU([]byte("hello mo"), nil)
	done := make(chan smppwire.PDU, 1)
	go func() { done <- readResponse(t, server) }()
	if err := session.handleDeliver(pdu); err != nil {
		t.Fatal(err)
	}
	if response := <-done; response.Header.CommandStatus != 0 {
		t.Fatalf("status=%#x want ROK", response.Header.CommandStatus)
	}
	if encoder.cid != "cid-1" || len(encoder.wire) == 0 {
		t.Fatalf("encoder got cid=%q wire=%d bytes", encoder.cid, len(encoder.wire))
	}
	reDecoded, err := smppwire.Read(bytes.NewReader(encoder.wire), smppwire.DefaultMaxSize)
	if err != nil || string(reDecoded.SM.ShortMessage) != "hello mo" {
		t.Fatalf("re-encoded wire invalid: %v %q", err, reDecoded.SM.ShortMessage)
	}
	if len(publisher.published) != 1 {
		t.Fatalf("published=%d want 1", len(publisher.published))
	}
	publication := publisher.published[0]
	if publication.routingKey != "deliver.sm.cid-1" {
		t.Fatalf("routing key=%s", publication.routingKey)
	}
	if !bytes.Equal(publication.envelope.Body(), []byte("pickled-routable")) {
		t.Fatal("body is not the pickled routable")
	}
	headers := publication.envelope.Properties().Headers()
	if cid, _ := headers["connector-id"].String(); cid != "cid-1" {
		t.Fatalf("connector-id=%q", cid)
	}
	if tryCount, _ := headers["try-count"].Integer(); tryCount != 0 {
		t.Fatalf("try-count=%d", tryCount)
	}
	if concatenated, ok := headers["concatenated"].Bool(); !ok || concatenated {
		t.Fatalf("concatenated=%v ok=%v want false bool", concatenated, ok)
	}
	if !strings.Contains(logged.String(), "SMS-MO [cid:cid-1] [queue-msgid:") ||
		!strings.Contains(logged.String(), "[from:b'1111'] [to:b'2222'] [content:b'hello mo']") {
		t.Fatalf("SMS-MO line missing; log: %q", logged.String())
	}
}

func TestHandleDeliverPublishFailureRespondsUnknownError(t *testing.T) {
	session, server, _ := newDeliverTestSession(t)
	session.SetDeliverUpstream(&capturePublisher{err: errors.New("broker down")}, &fakeDeliverEncoder{pickled: []byte("x")})

	pdu := deliverPDU([]byte("hello"), nil)
	done := make(chan smppwire.PDU, 1)
	go func() { done <- readResponse(t, server) }()
	if err := session.handleDeliver(pdu); err != nil {
		t.Fatal(err)
	}
	if response := <-done; response.Header.CommandStatus != smppStatusUnknownError {
		t.Fatalf("status=%#x want ESME_RUNKNOWNERR", response.Header.CommandStatus)
	}
}

func TestHandleDeliverWithoutUpstreamAcksAndDrops(t *testing.T) {
	session, server, logged := newDeliverTestSession(t)

	pdu := deliverPDU([]byte("hello"), nil)
	done := make(chan smppwire.PDU, 1)
	go func() { done <- readResponse(t, server) }()
	if err := session.handleDeliver(pdu); err != nil {
		t.Fatal(err)
	}
	if response := <-done; response.Header.CommandStatus != 0 {
		t.Fatalf("status=%#x want ROK (legacy RouterPB-not-set drop)", response.Header.CommandStatus)
	}
	if !strings.Contains(logged.String(), "no upstream publisher") {
		t.Fatalf("drop left no trace: %q", logged.String())
	}
}

func TestHandleDeliverLongPartIsDroppedWithCriticalLine(t *testing.T) {
	session, server, logged := newDeliverTestSession(t)
	publisher := &capturePublisher{}
	session.SetDeliverUpstream(publisher, &fakeDeliverEncoder{pickled: []byte("x")})

	reference := uint16(42)
	pdu := deliverPDU([]byte("part-1"), func(body *smppwire.SMBody) {
		total, sequence := byte(2), byte(1)
		body.Optional.SARMessageReference = &reference
		body.Optional.SARTotalSegments = &total
		body.Optional.SARSegmentSequence = &sequence
	})
	done := make(chan smppwire.PDU, 1)
	go func() { done <- readResponse(t, server) }()
	if err := session.handleDeliver(pdu); err != nil {
		t.Fatal(err)
	}
	if response := <-done; response.Header.CommandStatus != 0 {
		t.Fatalf("status=%#x want ROK", response.Header.CommandStatus)
	}
	if len(publisher.published) != 0 {
		t.Fatal("long part must not publish before reassembly lands")
	}
	if !strings.Contains(logged.String(), "MSG IS LOST !") {
		t.Fatalf("legacy critical line missing: %q", logged.String())
	}
}

func TestHandleDeliverUDHPartDetection(t *testing.T) {
	udhContent := append([]byte{0x05, 0x00, 0x03, 0x2A, 0x02, 0x01}, []byte("part")...)
	body := &smppwire.SMBody{ShortMessage: udhContent, ESMClass: 0x40}
	if !isLongDeliverPart(body, udhContent) {
		t.Fatal("UDHI concat part not detected")
	}
	class2 := &smppwire.SMBody{ShortMessage: udhContent, ESMClass: 0x40, DataCoding: 0xF2}
	if isLongDeliverPart(class2, udhContent) {
		t.Fatal("GSM class-2 must not be treated as concat part")
	}
	plain := &smppwire.SMBody{ShortMessage: []byte("hello"), ESMClass: 0x40}
	if isLongDeliverPart(plain, []byte("hello")) {
		t.Fatal("plain UDHI without 050003 prefix misdetected")
	}
}

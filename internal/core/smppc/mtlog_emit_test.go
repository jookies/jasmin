package smppc

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/logging"
	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func newAuditSession(t *testing.T, buf *bytes.Buffer, retry *ErrorRetryPolicy, privacy bool) *Session {
	t.Helper()
	session := &Session{cfg: Config{CID: "smppc1"}, retry: retry}
	session.SetSubmitAuditLogger(logging.Logger("jasmin-sm-listener", logging.Config{Writer: buf}), privacy)
	return session
}

func auditPending(t *testing.T, chain *submitChain) *pendingRequest {
	t.Helper()
	headers := map[string]amqpcompat.Field{"expiration": amqpcompat.StringField("2026-07-26 12:00:00")}
	properties, err := amqpcompat.NewProperties("qmsg-1", headers, amqpcompat.WithPriority(2))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("submit.sm.smppc1", properties, []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	return &pendingRequest{
		envelope: envelope, chain: chain,
		attempt:            submittransaction.SendAttempt{Number: 1},
		sourceAddr:         []byte("1111"),
		destAddr:           []byte("2222"),
		shortMessage:       []byte("hi"),
		registeredDelivery: 1,
	}
}

func respPDU(status uint32, messageID string) smppwire.PDU {
	return smppwire.PDU{
		Header:         smppwire.Header{CommandStatus: status},
		SubmitResponse: &smppwire.SubmitResponseBody{MessageID: []byte(messageID)},
	}
}

func TestLogSubmitAuditSuccess(t *testing.T) {
	var buffer bytes.Buffer
	session := newAuditSession(t, &buffer, nil, false)
	session.logSubmitAudit(auditPending(t, nil), respPDU(0, "ABC"))
	want := "SMS-MT [cid:smppc1] [queue-msgid:qmsg-1] [smpp-msgid:b'ABC'] [status:CommandStatus.ESME_ROK] " +
		"[prio:2] [dlr:RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED] [validity:2026-07-26 12:00:00] " +
		"[from:b'1111'] [to:b'2222'] [content:b'hi'] [tlvs:none]"
	if !strings.Contains(buffer.String(), want) {
		t.Errorf("success line missing:\n got %q\nwant substring %q", buffer.String(), want)
	}
	if !strings.Contains(buffer.String(), "INFO") {
		t.Errorf("not rendered at INFO through the jasmin logger: %q", buffer.String())
	}
}

func TestLogSubmitAuditErrorWillRetry(t *testing.T) {
	retry, err := NewErrorRetryPolicy([]ErrorRetryRule{{Status: "ESME_RSYSERR", Count: 2, DelaySeconds: 30}})
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	session := newAuditSession(t, &buffer, retry, false)
	session.logSubmitAudit(auditPending(t, nil), respPDU(0x08, "")) // ESME_RSYSERR, attempt 1 < count 2 -> retry
	want := "SMS-MT [cid:smppc1] [queue-msgid:qmsg-1] [status:ERROR/CommandStatus.ESME_RSYSERR] [retry:True] " +
		"[prio:2] [dlr:RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED] [validity:2026-07-26 12:00:00] " +
		"[from:b'1111'] [to:b'2222'] [content:b'hi'] [tlvs:none]"
	if !strings.Contains(buffer.String(), want) {
		t.Errorf("error line missing:\n got %q\nwant substring %q", buffer.String(), want)
	}
}

func TestLogSubmitAuditErrorNoRetry(t *testing.T) {
	var buffer bytes.Buffer
	session := newAuditSession(t, &buffer, nil, false) // no retry policy -> will_be_retried False
	session.logSubmitAudit(auditPending(t, nil), respPDU(0x08, ""))
	if !strings.Contains(buffer.String(), "[status:ERROR/CommandStatus.ESME_RSYSERR] [retry:False]") {
		t.Errorf("error line retry:False missing: %q", buffer.String())
	}
}

func TestLogSubmitAuditPrivacy(t *testing.T) {
	var buffer bytes.Buffer
	session := newAuditSession(t, &buffer, nil, true)
	session.logSubmitAudit(auditPending(t, nil), respPDU(0, "ABC"))
	if !strings.Contains(buffer.String(), "[content:** 2 byte content **]") {
		t.Errorf("privacy content not redacted: %q", buffer.String())
	}
}

func TestLogSubmitAuditNilLoggerNoop(t *testing.T) {
	session := &Session{cfg: Config{CID: "x"}} // auditLogger nil
	session.logSubmitAudit(auditPending(t, nil), respPDU(0, "ABC"))
}

func TestReassembleMultipart(t *testing.T) {
	u16 := func(v uint16) *uint16 { return &v }
	// SAR: full concat of every part.
	sarFirst := &pendingRequest{optional: smppwire.OptionalParameters{SARMessageReference: u16(1)}}
	sar := &chainAudit{first: sarFirst, partContents: [][]byte{[]byte("Hello "), []byte("World")}}
	if got := reassembleMultipart(sar); string(got) != "Hello World" {
		t.Errorf("SAR reassembly = %q, want %q", got, "Hello World")
	}
	// UDH: 6-byte concat header (05 00 03 ref total seq) stripped from each part.
	udhPart := func(seq byte, msg string) []byte {
		return append([]byte{0x05, 0x00, 0x03, 0xAB, 0x02, seq}, msg...)
	}
	udhFirst := &pendingRequest{esmClass: 0x40, shortMessage: udhPart(1, "Hello ")}
	udh := &chainAudit{first: udhFirst, partContents: [][]byte{udhPart(1, "Hello "), udhPart(2, "World")}}
	if got := reassembleMultipart(udh); string(got) != "Hello World" {
		t.Errorf("UDH reassembly = %q, want %q", got, "Hello World")
	}
}

func TestLogSubmitAuditMultipartLine(t *testing.T) {
	u16 := func(v uint16) *uint16 { return &v }
	var buffer bytes.Buffer
	session := newAuditSession(t, &buffer, nil, false)
	chain := &submitChain{remaining: 1}
	first := auditPending(t, chain)
	first.shortMessage = []byte("Part1 ")
	first.optional = smppwire.OptionalParameters{SARMessageReference: u16(7)}
	last := auditPending(t, chain)
	last.shortMessage = []byte("Part2")
	last.destAddr = []byte("9999") // last part's fields are the logged ones
	last.optional = smppwire.OptionalParameters{SARMessageReference: u16(7), SARSegmentSequence: bytePtr(2)}
	chain.audit.first = first
	chain.audit.last = last
	chain.audit.partContents = [][]byte{first.shortMessage, last.shortMessage}

	session.logSubmitAudit(last, respPDU(0, "MID"))

	line := buffer.String()
	if !strings.Contains(line, "[content:b'Part1 Part2']") {
		t.Errorf("multipart content not reassembled: %q", line)
	}
	if !strings.Contains(line, "[to:b'9999']") {
		t.Errorf("multipart line must use the last part's fields: %q", line)
	}
	if !strings.Contains(line, "sar_msg_ref_num:7,sar_segment_seqnum:2") {
		t.Errorf("multipart tlvs from last part missing: %q", line)
	}
}

func bytePtr(v byte) *byte { return &v }

func TestLogSubmitAuditIncludesTLVs(t *testing.T) {
	var buffer bytes.Buffer
	session := newAuditSession(t, &buffer, nil, false)
	pending := auditPending(t, nil)
	pending.optional = smppwire.OptionalParameters{MessagePayload: []byte("payload")}
	pending.customTLVs = []tlv.TLV{{Tag: new(big.Int).SetUint64(0x1400), Value: "v"}}
	session.logSubmitAudit(pending, respPDU(0, "ABC"))
	if !strings.Contains(buffer.String(), "[tlvs:message_payload:b'payload',0x1400:v]") {
		t.Errorf("tlvs not rendered into the line: %q", buffer.String())
	}
}

func TestConnectorSetSubmitAuditLogger(t *testing.T) {
	connector := &Connector{}
	var buffer bytes.Buffer
	logger := logging.Logger("jasmin-sm-listener", logging.Config{Writer: &buffer})
	connector.SetSubmitAuditLogger(logger, true)
	connector.mu.RLock()
	defer connector.mu.RUnlock()
	if connector.auditLogger != logger || !connector.auditPrivacy {
		t.Error("SetSubmitAuditLogger did not store logger/privacy under the lock")
	}
}

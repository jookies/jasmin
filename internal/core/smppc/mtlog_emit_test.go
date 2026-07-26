package smppc

import (
	"bytes"
	"strings"
	"testing"

	"github.com/pumpitspace/jasmin/internal/core/logging"
	"github.com/pumpitspace/jasmin/internal/core/submittransaction"
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

func TestLogSubmitAuditMultipartSkipped(t *testing.T) {
	var buffer bytes.Buffer
	session := newAuditSession(t, &buffer, nil, false)
	session.logSubmitAudit(auditPending(t, &submitChain{remaining: 2}), respPDU(0, "ABC"))
	if buffer.Len() != 0 {
		t.Errorf("multipart chain must be skipped for now, got: %q", buffer.String())
	}
}

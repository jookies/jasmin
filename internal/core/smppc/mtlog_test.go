package smppc

import (
	"strings"
	"testing"
)

func TestPythonBytesRepr(t *testing.T) {
	// Exact CPython repr()/str() outputs, captured from the interpreter.
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte("plain"), `b'plain'`},
		{[]byte("1111"), `b'1111'`},
		{[]byte("it's"), `b"it's"`},                                       // has ' not " -> double quote
		{[]byte(`say "hi"`), `b'say "hi"'`},                               // has " not ' -> single quote
		{[]byte(`both ' and "`), `b'both \' and "'`},                      // both -> single, escape '
		{[]byte("tab\there"), `b'tab\there'`},                             // \t
		{[]byte("nl\nhere"), `b'nl\nhere'`},                               // \n
		{[]byte(`back\slash`), `b'back\\slash'`},                          // backslash
		{[]byte{0x00, 0x01, 0x7f, 0x80, 0xff}, `b'\x00\x01\x7f\x80\xff'`}, // control/high -> \xNN
		{[]byte{}, `b''`},
		{[]byte("caf\xc3\xa9"), `b'caf\xc3\xa9'`}, // café UTF-8
	}
	for _, testCase := range cases {
		if got := pythonBytesRepr(testCase.in); got != testCase.want {
			t.Errorf("pythonBytesRepr(%q) = %s, want %s", testCase.in, got, testCase.want)
		}
	}
}

func TestReceiptForLog(t *testing.T) {
	cases := map[byte]string{
		0x00: "RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED",
		0x01: "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED",
		0x02: "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED_FOR_FAILURE",
		// Higher bits (intermediate-notification etc.) do not affect the receipt.
		0x11: "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED",
	}
	for octet, want := range cases {
		if got := receiptForLog(octet); got != want {
			t.Errorf("receiptForLog(%#02x) = %s, want %s", octet, got, want)
		}
	}
}

func TestStatusForLog(t *testing.T) {
	if got := statusForLog(0x00000000); got != "CommandStatus.ESME_ROK" {
		t.Errorf("statusForLog(ROK) = %s", got)
	}
	if got := statusForLog(0x0000000b); got != "CommandStatus.ESME_RINVDSTADR" {
		t.Errorf("statusForLog(0x0b) = %s", got)
	}
}

func TestContentForLog(t *testing.T) {
	if got := contentForLog(false, []byte("hi")); got != `b'hi'` {
		t.Errorf("content non-privacy = %s", got)
	}
	if got := contentForLog(true, []byte("secret")); got != "** 6 byte content **" {
		t.Errorf("content privacy = %s", got)
	}
}

func TestSubmitAuditLineSuccess(t *testing.T) {
	got := submitAuditLineSuccess(submitAuditFields{
		ConnectorID: "smppc1", QueueMsgID: "abc", SMPPMsgID: "7F", Status: 0x00000000,
		Priority: 1, RegisteredDelivery: 0x01, Validity: "none",
		SourceAddr: []byte("1111"), DestAddr: []byte("2222"), ShortMessage: []byte("hi"),
	})
	want := "SMS-MT [cid:smppc1] [queue-msgid:abc] [smpp-msgid:7F] [status:CommandStatus.ESME_ROK] " +
		"[prio:1] [dlr:RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED] [validity:none] " +
		"[from:b'1111'] [to:b'2222'] [content:b'hi'] [tlvs:none]"
	if got != want {
		t.Errorf("success line:\n got %q\nwant %q", got, want)
	}
}

func TestSubmitAuditLineError(t *testing.T) {
	got := submitAuditLineError(submitAuditFields{
		ConnectorID: "smppc1", QueueMsgID: "abc", Status: 0x0000000b, WillRetry: true,
		Priority: 2, RegisteredDelivery: 0x00, Validity: "2026-07-25 12:00:00",
		SourceAddr: []byte("1111"), DestAddr: []byte("2222"), ShortMessage: []byte("hi"),
	})
	want := "SMS-MT [cid:smppc1] [queue-msgid:abc] [status:ERROR/CommandStatus.ESME_RINVDSTADR] " +
		"[retry:True] [prio:2] [dlr:RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED] " +
		"[validity:2026-07-25 12:00:00] [from:b'1111'] [to:b'2222'] [content:b'hi'] [tlvs:none]"
	if got != want {
		t.Errorf("error line:\n got %q\nwant %q", got, want)
	}
}

func TestSubmitAuditContentPrivacy(t *testing.T) {
	got := submitAuditLineSuccess(submitAuditFields{
		ConnectorID: "c", QueueMsgID: "m", SMPPMsgID: "1", Status: 0, Priority: 0,
		Validity: "none", SourceAddr: []byte("a"), DestAddr: []byte("b"),
		ShortMessage: []byte("secret body"), Privacy: true,
	})
	if !strings.Contains(got, "[content:** 11 byte content **]") {
		t.Errorf("privacy content not redacted in line: %s", got)
	}
}

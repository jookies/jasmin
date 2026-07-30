package smppc

import (
	"math/big"
	"strings"
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
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

func TestFormatTLVsForLog(t *testing.T) {
	u16 := func(v uint16) *uint16 { return &v }
	bp := func(v byte) *byte { return &v }
	tag := func(v uint64) *big.Int { return new(big.Int).SetUint64(v) }
	cases := []struct {
		name     string
		optional smppwire.OptionalParameters
		custom   []tlv.TLV
		privacy  bool
		want     string
	}{
		{"none", smppwire.OptionalParameters{}, nil, false, "none"},
		{"payload", smppwire.OptionalParameters{MessagePayload: []byte("hello")}, nil, false, "message_payload:b'hello'"},
		{"sar", smppwire.OptionalParameters{SARMessageReference: u16(5), SARTotalSegments: bp(3), SARSegmentSequence: bp(1)}, nil, false, "sar_msg_ref_num:5,sar_total_segments:3,sar_segment_seqnum:1"},
		{"more1", smppwire.OptionalParameters{MoreMessagesToSend: bp(1)}, nil, false, "more_messages_to_send:MoreMessagesToSend.MORE_MESSAGES"},
		{"more0", smppwire.OptionalParameters{MoreMessagesToSend: bp(0)}, nil, false, "more_messages_to_send:MoreMessagesToSend.NO_MORE_MESSAGES"},
		{"custom", smppwire.OptionalParameters{}, []tlv.TLV{{Tag: tag(0x1400), Value: "hello"}, {Tag: tag(0x1401), Value: int64(42)}}, false, "0x1400:hello,0x1401:42"},
		{"custom_bytes", smppwire.OptionalParameters{}, []tlv.TLV{{Tag: tag(0x1500), Value: []byte("x")}}, false, "0x1500:b'x'"},
		{"combined", smppwire.OptionalParameters{SARMessageReference: u16(2), MessagePayload: []byte("p")}, []tlv.TLV{{Tag: tag(0x1400), Value: "v"}}, false, "sar_msg_ref_num:2,message_payload:b'p',0x1400:v"},
		{"privacy", smppwire.OptionalParameters{SARMessageReference: u16(2), MessagePayload: []byte("p")}, []tlv.TLV{{Tag: tag(0x1400), Value: "v"}}, true, "sar_msg_ref_num,message_payload,0x1400"},
	}
	for _, testCase := range cases {
		if got := formatTLVsForLog(testCase.optional, testCase.custom, testCase.privacy); got != testCase.want {
			t.Errorf("%s: got %q, want %q", testCase.name, got, testCase.want)
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
		ConnectorID: "smppc1", QueueMsgID: "abc", SMPPMsgID: []byte("7F"), Status: 0x00000000,
		Priority: 1, RegisteredDelivery: 0x01, Validity: "none",
		SourceAddr: []byte("1111"), DestAddr: []byte("2222"), ShortMessage: []byte("hi"),
	})
	want := "SMS-MT [cid:smppc1] [queue-msgid:abc] [smpp-msgid:b'7F'] [status:CommandStatus.ESME_ROK] " +
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
		ConnectorID: "c", QueueMsgID: "m", SMPPMsgID: []byte("1"), Status: 0, Priority: 0,
		Validity: "none", SourceAddr: []byte("a"), DestAddr: []byte("b"),
		ShortMessage: []byte("secret body"), Privacy: true,
	})
	if !strings.Contains(got, "[content:** 11 byte content **]") {
		t.Errorf("privacy content not redacted in line: %s", got)
	}
}

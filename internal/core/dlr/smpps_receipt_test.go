package dlr

import (
	"errors"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func TestMessageStateFor(t *testing.T) {
	cases := []struct {
		status string
		state  byte
		stat   string
	}{
		{"ESME_ROK", msgStateAccepted, "ACCEPTD"},
		{"ESME_RSYSERR", msgStateUndeliverable, "UNDELIV"},
		{"ESME_RTHROTTLED", msgStateUndeliverable, "UNDELIV"},
		{"DELIVRD", msgStateDelivered, "DELIVRD"},
		{"UNDELIV", msgStateUndeliverable, "UNDELIV"},
		{"REJECTD", msgStateRejected, "REJECTD"},
		{"EXPIRED", msgStateExpired, "EXPIRED"},
		{"DELETED", msgStateDeleted, "DELETED"},
		{"ACCEPTD", msgStateAccepted, "ACCEPTD"},
		{"ENROUTE", msgStateEnroute, "ENROUTE"},
		{"UNKNOWN", msgStateUnknown, "UNKNOWN"},
	}
	for _, c := range cases {
		state, stat, err := messageStateFor(c.status)
		if err != nil || state != c.state || stat != c.stat {
			t.Errorf("messageStateFor(%q) = (%d,%q,%v), want (%d,%q,nil)", c.status, state, stat, err, c.state, c.stat)
		}
	}
	if _, _, err := messageStateFor("BOGUS"); !errors.Is(err, ErrUnknownMessageStatus) {
		t.Errorf("unknown status: want ErrUnknownMessageStatus, got %v", err)
	}
}

func TestReceiptText(t *testing.T) {
	now := time.Date(2021, 1, 2, 13, 45, 0, 0, time.UTC)
	text, err := receiptText("6AAD5", "2021-01-01 12:00:00.123456", "DELIVRD", "000", now)
	if err != nil {
		t.Fatalf("receiptText: %v", err)
	}
	want := "id:6AAD5 submit date:2101011200 done date:2101021345 stat:DELIVRD err:000"
	if text != want {
		t.Errorf("receiptText =\n %q\nwant\n %q", text, want)
	}
	// A second sub_date form (no fractional seconds).
	if _, err := receiptText("m", "2021-01-01 12:00:00", "DELIVRD", "0", now); err != nil {
		t.Errorf("no-fraction sub_date: %v", err)
	}
	// Unparseable sub_date errors.
	if _, err := receiptText("m", "not-a-date", "DELIVRD", "0", now); err == nil {
		t.Errorf("bad sub_date: expected error")
	}
}

func TestBuildDeliverSMReceipt_FieldsAndRoundTrip(t *testing.T) {
	now := time.Date(2021, 1, 2, 13, 45, 0, 0, time.UTC)
	p := SMPPSReceiptParams{
		MsgID: "6AAD5", MessageStatus: "DELIVRD", Err: "000", SubDate: "2021-01-01 12:00:00",
		SourceAddr: "12345", DestAddr: "447700900000",
		SourceAddrTON: 5, SourceAddrNPI: 0, DestAddrTON: 1, DestAddrNPI: 1,
	}
	pdu, err := BuildDeliverSMReceipt(p, now)
	if err != nil {
		t.Fatalf("BuildDeliverSMReceipt: %v", err)
	}

	// Addresses and TON/NPI must be swapped relative to the original submit.
	sm := pdu.SM
	if string(sm.SourceAddress) != "447700900000" || sm.SourceAddressTON != 1 || sm.SourceAddressNPI != 1 {
		t.Errorf("source not swapped to original dest: %+v", sm)
	}
	if string(sm.DestinationAddress) != "12345" || sm.DestinationAddressTON != 5 || sm.DestinationAddressNPI != 0 {
		t.Errorf("dest not swapped to original source: %+v", sm)
	}
	if sm.ESMClass != esmClassSMSCDeliveryReceipt {
		t.Errorf("esm_class = %#x, want %#x (SMSC delivery receipt)", sm.ESMClass, esmClassSMSCDeliveryReceipt)
	}
	if string(sm.Optional.ReceiptedMessageID) != "6AAD5" {
		t.Errorf("receipted_message_id = %q", sm.Optional.ReceiptedMessageID)
	}
	if sm.Optional.MessageState == nil || *sm.Optional.MessageState != msgStateDelivered {
		t.Errorf("message_state = %v, want %d", sm.Optional.MessageState, msgStateDelivered)
	}
	wantText := "id:6AAD5 submit date:2101011200 done date:2101021345 stat:DELIVRD err:000"
	if string(sm.ShortMessage) != wantText {
		t.Errorf("short_message = %q, want %q", sm.ShortMessage, wantText)
	}

	// The receipt must be wire-valid: encode then decode back.
	frame, err := smppwire.Encode(pdu)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := smppwire.Decode(frame)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Header.CommandID != smppwire.CommandDeliverSM {
		t.Errorf("decoded command id = %#x, want deliver_sm", got.Header.CommandID)
	}
	if got.SM == nil || string(got.SM.ShortMessage) != wantText || got.SM.ESMClass != esmClassSMSCDeliveryReceipt {
		t.Errorf("round-trip lost receipt content: %+v", got.SM)
	}
	if got.SM.Optional.MessageState == nil || *got.SM.Optional.MessageState != msgStateDelivered {
		t.Errorf("round-trip lost message_state")
	}
	if string(got.SM.Optional.ReceiptedMessageID) != "6AAD5" {
		t.Errorf("round-trip lost receipted_message_id: %q", got.SM.Optional.ReceiptedMessageID)
	}
}

func TestBuildDeliverSMReceipt_UnknownStatus(t *testing.T) {
	if _, err := BuildDeliverSMReceipt(SMPPSReceiptParams{MessageStatus: "NOPE", SubDate: "2021-01-01 12:00:00"}, time.Now()); !errors.Is(err, ErrUnknownMessageStatus) {
		t.Errorf("want ErrUnknownMessageStatus, got %v", err)
	}
}

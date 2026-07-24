package dlr

import (
	"errors"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

func TestParseAddrTONNPI(t *testing.T) {
	tonCases := map[string]byte{
		"AddrTon.UNKNOWN": 0, "AddrTon.INTERNATIONAL": 1, "AddrTon.NATIONAL": 2,
		"AddrTon.ALPHANUMERIC": 5, "ABBREVIATED": 6, // bare form also accepted
	}
	for in, want := range tonCases {
		if got, err := ParseAddrTON(in); err != nil || got != want {
			t.Errorf("ParseAddrTON(%q) = (%d,%v), want %d", in, got, err, want)
		}
	}
	npiCases := map[string]byte{
		"AddrNpi.UNKNOWN": 0, "AddrNpi.ISDN": 1, "AddrNpi.DATA": 3, "AddrNpi.LAND_MOBILE": 6,
		"AddrNpi.ERMES": 0x0a, "AddrNpi.INTERNET": 0x0e, "AddrNpi.WAP_CLIENT_ID": 0x12,
	}
	for in, want := range npiCases {
		if got, err := ParseAddrNPI(in); err != nil || got != want {
			t.Errorf("ParseAddrNPI(%q) = (%d,%v), want %d", in, got, err, want)
		}
	}
	if _, err := ParseAddrTON("AddrTon.BOGUS"); !errors.Is(err, ErrInvalidAddrEnum) {
		t.Errorf("bad ton: want ErrInvalidAddrEnum, got %v", err)
	}
	if _, err := ParseAddrNPI("nope"); !errors.Is(err, ErrInvalidAddrEnum) {
		t.Errorf("bad npi: want ErrInvalidAddrEnum, got %v", err)
	}
}

func TestSMPPSReceiptFromForward(t *testing.T) {
	f := Forward{
		Target: ForwardSMPPS, Status: "DELIVRD", QueueMsgID: "q", Err: "000", SubDate: "2021-01-01 12:00:00",
		SystemID: "sys1", SourceAddr: "12345", DestinationAddr: "447700900000",
		SourceAddrTON: "AddrTon.ALPHANUMERIC", SourceAddrNPI: "AddrNpi.UNKNOWN",
		DestAddrTON: "AddrTon.INTERNATIONAL", DestAddrNPI: "AddrNpi.ISDN",
	}
	p, err := SMPPSReceiptFromForward(f)
	if err != nil {
		t.Fatalf("from forward: %v", err)
	}
	if p.MsgID != "q" || p.MessageStatus != "DELIVRD" || p.Err != "000" || p.SourceAddr != "12345" || p.DestAddr != "447700900000" {
		t.Errorf("fields wrong: %+v", p)
	}
	if p.SourceAddrTON != 5 || p.SourceAddrNPI != 0 || p.DestAddrTON != 1 || p.DestAddrNPI != 1 {
		t.Errorf("TON/NPI not parsed: %+v", p)
	}

	// Empty err (submit_sm_resp leg) defaults to 99.
	f2 := f
	f2.Err = ""
	p2, err := SMPPSReceiptFromForward(f2)
	if err != nil || p2.Err != "99" {
		t.Errorf("empty err should default to 99, got %q (err %v)", p2.Err, err)
	}

	// Non-smpps forward is rejected.
	if _, err := SMPPSReceiptFromForward(Forward{Target: ForwardHTTP}); err == nil {
		t.Errorf("expected error for http forward")
	}
	// Invalid TON string propagates.
	fBad := f
	fBad.SourceAddrTON = "AddrTon.NONSENSE"
	if _, err := SMPPSReceiptFromForward(fBad); !errors.Is(err, ErrInvalidAddrEnum) {
		t.Errorf("bad TON: want ErrInvalidAddrEnum, got %v", err)
	}
}

// TestForwardToReceiptEndToEnd chains the smpps path: a correlation-engine ForwardSMPPS →
// receipt params → deliver_sm PDU → wire round-trip, with address swap and the err default.
func TestForwardToReceiptEndToEnd(t *testing.T) {
	now := time.Date(2021, 1, 2, 13, 45, 0, 0, time.UTC)
	f := Forward{
		Target: ForwardSMPPS, Status: "DELIVRD", QueueMsgID: "6AAD5", SubDate: "2021-01-01 12:00:00",
		SystemID: "sys1", SourceAddr: "12345", DestinationAddr: "447700900000",
		SourceAddrTON: "AddrTon.ALPHANUMERIC", SourceAddrNPI: "AddrNpi.UNKNOWN",
		DestAddrTON: "AddrTon.INTERNATIONAL", DestAddrNPI: "AddrNpi.ISDN",
		// Err empty -> receipt should show err:99
	}
	p, err := SMPPSReceiptFromForward(f)
	if err != nil {
		t.Fatalf("bridge: %v", err)
	}
	pdu, err := BuildDeliverSMReceipt(p, now)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// Source of the receipt is the original destination (swapped).
	if string(pdu.SM.SourceAddress) != "447700900000" || pdu.SM.SourceAddressTON != 1 {
		t.Errorf("swap wrong: %+v", pdu.SM)
	}
	wantText := "id:6AAD5 submit date:2101011200 done date:2101021345 stat:DELIVRD err:99"
	if string(pdu.SM.ShortMessage) != wantText {
		t.Errorf("short_message = %q, want %q", pdu.SM.ShortMessage, wantText)
	}
	// Still wire-valid.
	frame, err := smppwire.Encode(pdu)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, err := smppwire.Decode(frame); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

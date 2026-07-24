package dlr

import "testing"

func bptr(b byte) *byte { return &b }

func TestParseReceipt_TextForm(t *testing.T) {
	sm := []byte("id:a3ed98a664 sub:1 dlvrd:1 submit date:1410160036 done date:1410160038 stat:DELIVRD err:0 text:hello")
	r, isDLR := ParseReceipt(nil, nil, sm)
	if !isDLR {
		t.Fatal("expected a DLR")
	}
	if r.ID != "a3ed98a664" || r.Stat != "DELIVRD" {
		t.Errorf("id/stat wrong: %+v", r)
	}
	// sub/dlvrd/err zero-padded to width 3.
	if r.Sub != "001" || r.Dlvrd != "001" || r.Err != "000" {
		t.Errorf("padding wrong: sub=%q dlvrd=%q err=%q", r.Sub, r.Dlvrd, r.Err)
	}
	if r.SDate != "1410160036" || r.DDate != "1410160038" || r.Text != "hello" {
		t.Errorf("date/text wrong: %+v", r)
	}
}

func TestParseReceipt_TLVForm(t *testing.T) {
	r, isDLR := ParseReceipt([]byte("6AAD5"), bptr(msgStateDelivered), nil)
	if !isDLR || r.ID != "6AAD5" || r.Stat != "DELIVRD" {
		t.Errorf("TLV DLR wrong: %+v (isDLR=%v)", r, isDLR)
	}
	// Unset text fields keep their ND defaults.
	if r.Sub != "ND" || r.Dlvrd != "ND" || r.Err != "ND" {
		t.Errorf("expected ND defaults, got %+v", r)
	}
}

func TestParseReceipt_TLVNotOverwrittenByText(t *testing.T) {
	// TLV says DELIVRD/6AAD5; the text disagrees — TLV wins for id and stat.
	sm := []byte("id:OTHER sub:2 dlvrd:2 submit date:1 done date:2 stat:UNDELIV err:9 text:x")
	r, isDLR := ParseReceipt([]byte("6AAD5"), bptr(msgStateDelivered), sm)
	if !isDLR {
		t.Fatal("expected a DLR")
	}
	if r.ID != "6AAD5" || r.Stat != "DELIVRD" {
		t.Errorf("TLV id/stat must win: got id=%q stat=%q", r.ID, r.Stat)
	}
	// Other fields still come from the text.
	if r.Sub != "002" || r.Err != "009" || r.Text != "x" {
		t.Errorf("text fields wrong: %+v", r)
	}
}

func TestParseReceipt_UnmappedStateIsUnknown(t *testing.T) {
	// ENROUTE (byte 1) is not in the map -> UNKNOWN.
	r, isDLR := ParseReceipt([]byte("id1"), bptr(msgStateEnroute), nil)
	if !isDLR || r.Stat != "UNKNOWN" {
		t.Errorf("unmapped state: got stat=%q isDLR=%v", r.Stat, isDLR)
	}
}

func TestParseReceipt_MO(t *testing.T) {
	// Plain MO content: no id/stat -> not a DLR.
	if _, isDLR := ParseReceipt(nil, nil, []byte("Hello, this is a normal message")); isDLR {
		t.Error("plain MO must not be a DLR")
	}
	// id but no stat -> not a DLR.
	if _, isDLR := ParseReceipt(nil, nil, []byte("id:abc but no stat here")); isDLR {
		t.Error("id without stat must not be a DLR")
	}
	// A 5-char stat does not match the exactly-7 stat pattern -> no stat -> MO.
	if _, isDLR := ParseReceipt(nil, nil, []byte("id:abc stat:DELIV")); isDLR {
		t.Error("stat must be exactly 7 word chars")
	}
}

func TestParseReceipt_AlphanumericErr(t *testing.T) {
	// Vendor err codes are alphanumeric (\w), not just digits, and pad to 3.
	sm := []byte("id:x stat:UNDELIV err:A2B text:")
	r, isDLR := ParseReceipt(nil, nil, sm)
	if !isDLR || r.Err != "A2B" {
		t.Errorf("alphanumeric err: got %q isDLR=%v", r.Err, isDLR)
	}
}

func TestParseReceipt_CapitalText(t *testing.T) {
	r, _ := ParseReceipt(nil, nil, []byte("id:x stat:DELIVRD Text:CapitalTee"))
	if r.Text != "CapitalTee" {
		t.Errorf("Text: (capital) not matched: %q", r.Text)
	}
}

func TestDeliverReceiptEventFromReceipt(t *testing.T) {
	r := Receipt{ID: "6AAD5", Stat: "DELIVRD", Sub: "001", Dlvrd: "001", SDate: "s", DDate: "d", Err: "000", Text: "t"}
	ev := DeliverReceiptEventFromReceipt(r, MsgIDBaseReceiptDec, "conn-1")
	if ev.RawDLRID != "6AAD5" || ev.Base != MsgIDBaseReceiptDec || ev.ConnectorID != "conn-1" ||
		ev.Status != "DELIVRD" || ev.SubmitDate != "s" || ev.DoneDate != "d" || ev.Err != "000" {
		t.Errorf("event projection wrong: %+v", ev)
	}
}

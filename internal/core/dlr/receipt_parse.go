package dlr

import (
	"regexp"
	"strings"
)

// Receipt holds the delivery-receipt fields extracted from a deliver_sm/data_sm. Unset
// text-derived fields default to "ND" (empty for text), matching jasmin isDeliveryReceipt.
type Receipt struct {
	ID    string
	Stat  string
	Sub   string
	Dlvrd string
	SDate string
	DDate string
	Err   string
	Text  string
}

// stateNameByValue maps a message_state wire byte to the receipt stat token — the reverse
// of the getReceipt mapping (operations.py message_state_map). ENROUTE and any value not
// in the map resolve to "UNKNOWN".
var stateNameByValue = map[byte]string{
	msgStateDelivered:     "DELIVRD",
	msgStateExpired:       "EXPIRED",
	msgStateDeleted:       "DELETED",
	msgStateUndeliverable: "UNDELIV",
	msgStateAccepted:      "ACCEPTD",
	msgStateUnknown:       "UNKNOWN",
	msgStateRejected:      "REJECTD",
}

// Delivery-receipt text patterns (operations.py isDeliveryReceipt step 2). RE2-equivalent
// to the Python patterns; the id class is {digit, A-Z, a-z, '-', '_'}.
var (
	reReceiptID    = regexp.MustCompile(`id:([\dA-Za-z_-]+)`)
	reReceiptSub   = regexp.MustCompile(`sub:(\d{1,3})`)
	reReceiptDlvrd = regexp.MustCompile(`dlvrd:(\d{1,3})`)
	reReceiptSDate = regexp.MustCompile(`submit date:(\d+)`)
	reReceiptDDate = regexp.MustCompile(`done date:(\d+)`)
	reReceiptStat  = regexp.MustCompile(`stat:(\w{7})`)
	reReceiptErr   = regexp.MustCompile(`err:(\w{1,3})`)
	reReceiptText  = regexp.MustCompile(`[tT]ext:(.*)`)
)

// ParseReceipt classifies a deliver_sm/data_sm as a delivery receipt, matching jasmin
// protocols/smpp/operations.py isDeliveryReceipt. It returns the parsed receipt and true
// when the PDU is a DLR — it has both an id and a stat, from the receipted_message_id +
// message_state TLVs and/or the receipt text — or a zero Receipt and false for an MO.
//
// TLV-derived id/stat are authoritative: the text parse fills the remaining fields but
// never overwrites an id or stat already set by the TLVs.
func ParseReceipt(receiptedMessageID []byte, messageState *byte, shortMessage []byte) (Receipt, bool) {
	r := Receipt{Sub: "ND", Dlvrd: "ND", SDate: "ND", DDate: "ND", Err: "ND"}
	idSet, statSet := false, false

	// Step 1: optional parameters.
	if receiptedMessageID != nil && messageState != nil {
		r.ID = string(receiptedMessageID)
		idSet = true
		if name, ok := stateNameByValue[*messageState]; ok {
			r.Stat = name
		} else {
			r.Stat = "UNKNOWN"
		}
		statSet = true
	}

	// Step 2: message content parsing.
	if len(shortMessage) > 0 {
		text := strings.ToValidUTF8(string(shortMessage), "")
		if !idSet {
			if m := reReceiptID.FindStringSubmatch(text); m != nil {
				r.ID = m[1]
				idSet = true
			}
		}
		if !statSet {
			if m := reReceiptStat.FindStringSubmatch(text); m != nil {
				r.Stat = m[1]
				statSet = true
			}
		}
		if m := reReceiptSub.FindStringSubmatch(text); m != nil {
			r.Sub = m[1]
		}
		if m := reReceiptDlvrd.FindStringSubmatch(text); m != nil {
			r.Dlvrd = m[1]
		}
		if m := reReceiptSDate.FindStringSubmatch(text); m != nil {
			r.SDate = m[1]
		}
		if m := reReceiptDDate.FindStringSubmatch(text); m != nil {
			r.DDate = m[1]
		}
		if m := reReceiptErr.FindStringSubmatch(text); m != nil {
			r.Err = m[1]
		}
		if m := reReceiptText.FindStringSubmatch(text); m != nil {
			r.Text = m[1]
		}
	}

	// Zero-pad sub/dlvrd/err to width 3 when present (operations.py).
	if r.Sub != "ND" {
		r.Sub = zeroPad3(r.Sub)
	}
	if r.Dlvrd != "ND" {
		r.Dlvrd = zeroPad3(r.Dlvrd)
	}
	if r.Err != "ND" {
		r.Err = zeroPad3(r.Err)
	}

	return r, idSet && statSet
}

// DeliverReceiptEventFromReceipt projects a parsed receipt into the correlation-engine
// event for OnDeliverReceipt, given the connector's base and id. The receipt's raw id is
// coded into the correlation key by OnDeliverReceipt via the base.
func DeliverReceiptEventFromReceipt(r Receipt, base MsgIDBase, connectorID string) DeliverReceiptEvent {
	return DeliverReceiptEvent{
		RawDLRID: r.ID, Base: base, ConnectorID: connectorID, Status: r.Stat,
		Sub: r.Sub, Dlvrd: r.Dlvrd, SubmitDate: r.SDate, DoneDate: r.DDate, Err: r.Err, Text: r.Text,
	}
}

func zeroPad3(s string) string {
	if len(s) >= 3 {
		return s
	}
	return strings.Repeat("0", 3-len(s)) + s
}

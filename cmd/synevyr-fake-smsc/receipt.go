package main

import (
	"fmt"
	"strings"
	"time"
)

// receiptFields is the delivered count and error code that belong with one
// delivery state. It is a port of the Python fake SMSC's DLR_RECEIPT_FIELDS
// table (dlr-smpp-python/fake_smsc.py:46-53), which is the only one of the three
// emulators in this estate that got this right.
//
// The rule it encodes: `dlvrd` counts messages actually delivered, so it is 000
// for every state that is not a delivery, and `err` is 000 only on success. A
// receipt reading `stat:UNDELIV dlvrd:001 err:000` says three contradictory
// things at once — nothing was delivered, one thing was delivered, and nothing
// went wrong — and a partner parsing any one of the three fields draws a
// different conclusion about the same message.
type receiptFields struct {
	Delivered int
	Err       string
}

// receiptFieldsByStat maps the delivery states this emulator can be asked to
// produce. It matches the Python table for the two states that table covers and
// extends it to the rest of the SMPP v3.4 §5.2.28 set, all of which are
// non-delivery states and therefore take the fallback shape.
var receiptFieldsByStat = map[string]receiptFields{
	"DELIVRD": {Delivered: 1, Err: "000"},
	"REJECTD": {Delivered: 0, Err: "008"},
	"UNDELIV": {Delivered: 0, Err: "008"},
	"EXPIRED": {Delivered: 0, Err: "008"},
	"DELETED": {Delivered: 0, Err: "008"},
	"ACCEPTD": {Delivered: 0, Err: "008"},
	"UNKNOWN": {Delivered: 0, Err: "008"},
	"ENROUTE": {Delivered: 0, Err: "008"},
}

// receiptFallback matches the Python _DLR_RECEIPT_FALLBACK: an unrecognised
// state is treated as a non-delivery, never as a delivery. Failing towards
// "nothing was delivered" is the safe direction — a partner that under-counts a
// delivery retries, one that over-counts loses the message silently.
var receiptFallback = receiptFields{Delivered: 0, Err: "008"}

// fieldsForStat resolves the counters for a delivery state. The lookup is
// case-insensitive on input but the receipt always carries the state as the
// caller spelled it, matching the Python emulator, which interpolates `stat`
// verbatim.
func fieldsForStat(stat string) receiptFields {
	if fields, ok := receiptFieldsByStat[strings.ToUpper(strings.TrimSpace(stat))]; ok {
		return fields
	}
	return receiptFallback
}

// buildReceiptText renders a delivery receipt body.
//
// The field order and spacing reproduce the Python emulator's f-string
// (fake_smsc.py:290-296), which is in turn the shape Jasmin's receipt parser
// expects. dlvrd and err are derived from stat here and nowhere else, so no call
// site can spell an impossible combination.
func buildReceiptText(messageID, stat, submitDate, doneDate string) string {
	fields := fieldsForStat(stat)
	return fmt.Sprintf(
		"id:%s sub:001 dlvrd:%03d submit date:%s done date:%s stat:%s err:%s text:",
		messageID, fields.Delivered, submitDate, doneDate, stat, fields.Err)
}

// receiptDate renders a receipt timestamp in the SMPP receipt format
// (YYMMDDhhmm), the same `%y%m%d%H%M` the Python emulator uses.
func receiptDate(at time.Time) string {
	return at.Format("0601021504")
}

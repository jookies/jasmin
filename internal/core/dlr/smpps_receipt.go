package dlr

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

// SMPP message_state values (smpp.pdu MessageState).
const (
	msgStateEnroute       byte = 1
	msgStateDelivered     byte = 2
	msgStateExpired       byte = 3
	msgStateDeleted       byte = 4
	msgStateUndeliverable byte = 5
	msgStateAccepted      byte = 6
	msgStateUnknown       byte = 7
	msgStateRejected      byte = 8
)

// esmClassSMSCDeliveryReceipt is EsmClass(DEFAULT mode, SMSC_DELIVERY_RECEIPT type) = 0x04.
const esmClassSMSCDeliveryReceipt byte = 0x04

// receiptDateLayout is Go's rendering of Python strftime "%y%m%d%H%M".
const receiptDateLayout = "0601021504"

// ErrUnknownMessageStatus reports a message_status getReceipt cannot map (Jasmin raises).
var ErrUnknownMessageStatus = errors.New("dlr: unknown message_status")

// SMPPSReceiptParams holds the original submit's addressing plus the receipt status, used
// to build a deliver_sm delivery receipt back to the smpps sender. The addresses/TON/NPI
// are the ORIGINAL submit values; the receipt swaps source and destination.
type SMPPSReceiptParams struct {
	MsgID         string
	MessageStatus string // ESME_* (from submit_sm_resp) or a receipt state (DELIVRD, ...)
	Err           string
	SubDate       string // stored submission datetime string
	SourceAddr    string
	DestAddr      string
	SourceAddrTON byte
	SourceAddrNPI byte
	DestAddrTON   byte
	DestAddrNPI   byte
}

// messageStateFor maps a message_status to the SMPP message_state byte and the receipt
// text 'stat' token, matching operations.py getReceipt (lines 271-297). ESME_ statuses
// normalize (ROK → ACCEPTD, any other → UNDELIV); receipt states map 1:1.
func messageStateFor(status string) (state byte, stat string, err error) {
	if strings.HasPrefix(status, "ESME_") {
		if status == "ESME_ROK" {
			return msgStateAccepted, "ACCEPTD", nil
		}
		return msgStateUndeliverable, "UNDELIV", nil
	}
	switch status {
	case "UNDELIV":
		return msgStateUndeliverable, status, nil
	case "REJECTD":
		return msgStateRejected, status, nil
	case "DELIVRD":
		return msgStateDelivered, status, nil
	case "EXPIRED":
		return msgStateExpired, status, nil
	case "DELETED":
		return msgStateDeleted, status, nil
	case "ACCEPTD":
		return msgStateAccepted, status, nil
	case "ENROUTE":
		return msgStateEnroute, status, nil
	case "UNKNOWN":
		return msgStateUnknown, status, nil
	default:
		return 0, "", fmt.Errorf("%w: %q", ErrUnknownMessageStatus, status)
	}
}

// receiptText builds the appendix-B short_message, matching getReceipt lines 301-307:
// "id:%s submit date:%y%m%d%H%M done date:%y%m%d%H%M stat:%s err:%s". The done date is the
// injected clock; the submit date is parsed from the stored sub_date.
func receiptText(msgid, subDate, stat, errCode string, now time.Time) (string, error) {
	submit, err := parseSubDate(subDate)
	if err != nil {
		return "", fmt.Errorf("dlr: parse sub_date %q: %w", subDate, err)
	}
	return fmt.Sprintf("id:%s submit date:%s done date:%s stat:%s err:%s",
		msgid, submit.Format(receiptDateLayout), now.Format(receiptDateLayout), stat, errCode), nil
}

// parseSubDate accepts the stored datetime string forms Jasmin produces via str(datetime).
func parseSubDate(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05.999999", "2006-01-02 15:04:05", time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized datetime")
}

// BuildDeliverSMReceipt builds a deliver_sm delivery-receipt PDU to send back to the smpps
// sender, matching operations.py getReceipt with dlr_pdu='deliver_sm'. Source/destination
// (and their TON/NPI) are swapped relative to the original submit, esm_class marks an SMSC
// delivery receipt, and the receipted_message_id and message_state TLVs are set. The
// sequence number is left zero for the session to assign at send time.
func BuildDeliverSMReceipt(p SMPPSReceiptParams, now time.Time) (smppwire.PDU, error) {
	state, stat, err := messageStateFor(p.MessageStatus)
	if err != nil {
		return smppwire.PDU{}, err
	}
	text, err := receiptText(p.MsgID, p.SubDate, stat, p.Err, now)
	if err != nil {
		return smppwire.PDU{}, err
	}
	messageState := state
	return smppwire.PDU{
		Header: smppwire.Header{CommandID: smppwire.CommandDeliverSM},
		SM: &smppwire.SMBody{
			SourceAddressTON:      p.DestAddrTON,        // swapped
			SourceAddressNPI:      p.DestAddrNPI,        // swapped
			SourceAddress:         []byte(p.DestAddr),   // swapped
			DestinationAddressTON: p.SourceAddrTON,      // swapped
			DestinationAddressNPI: p.SourceAddrNPI,      // swapped
			DestinationAddress:    []byte(p.SourceAddr), // swapped
			ESMClass:              esmClassSMSCDeliveryReceipt,
			ShortMessage:          []byte(text),
			Optional: smppwire.OptionalParameters{
				ReceiptedMessageID: []byte(p.MsgID),
				MessageState:       &messageState,
			},
		},
	}, nil
}

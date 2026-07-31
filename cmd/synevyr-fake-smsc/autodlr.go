package main

import (
	"fmt"
	"log"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

// registeredDeliveryWanted reports whether a submit asked for a receipt in this
// outcome, from registered_delivery bits 0-1 (SMPP v3.4 §5.2.17):
//
//	0b00 never, 0b01 on success or failure, 0b10 on failure only,
//	0b11 reserved — treated as "both", matching fake_smsc.py:378-381.
//
// Honouring it is what makes the automatic receipt realistic rather than merely
// automatic: a carrier that sent a receipt for a submit that did not ask for one
// would have the gateway correlating receipts it holds no DLR record for, which
// is a failure mode this emulator would then be manufacturing instead of
// reproducing.
func registeredDeliveryWanted(registeredDelivery byte, success bool) bool {
	switch registeredDelivery & 0x03 {
	case 1, 3:
		return true
	case 2:
		return !success
	default:
		return false
	}
}

// isSuccessStat reports whether a delivery state counts as a successful
// delivery, which is the same question fieldsForStat answers with its delivered
// count — so it is asked in exactly one place.
func isSuccessStat(stat string) bool {
	return fieldsForStat(stat).Delivered > 0
}

// scheduleAutoReceipt sends a delivery receipt for an accepted submit, after the
// configured delay.
//
// The receipt is fired from a goroutine rather than inline because the response
// must reach the client first: a receipt for a message whose submit_sm_resp has
// not arrived cannot be correlated, and would exercise the gateway's
// receipt-before-mapping race on every single message rather than occasionally.
func scheduleAutoReceipt(target *session, submit *smppwire.SMBody, messageID string, opts options) {
	if target == nil || submit == nil {
		return
	}
	if !registeredDeliveryWanted(submit.RegisteredDelivery, isSuccessStat(opts.dlrStat)) {
		return
	}
	// The addresses are copied: the decoded PDU is not retained past this call,
	// and the goroutine outlives it by the receipt delay.
	source := append([]byte(nil), submit.SourceAddress...)
	destination := append([]byte(nil), submit.DestinationAddress...)
	submittedAt := time.Now()
	delay := opts.dlrDelay
	if opts.dlrJitter > 0 {
		delay += time.Duration(rand.Int64N(int64(opts.dlrJitter)))
	}

	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		// The receipt travels back the way a carrier's does: from the recipient
		// to the sender, so source and destination swap.
		text := buildReceiptText(messageID, opts.dlrStat, receiptDate(submittedAt), receiptDate(time.Now()))
		pdu := smppwire.PDU{
			Header: smppwire.Header{
				CommandID:      smppwire.CommandDeliverSM,
				SequenceNumber: target.sequence.Add(1),
			},
			SM: &smppwire.SMBody{
				SourceAddress:      destination,
				DestinationAddress: source,
				ESMClass:           0x04, // MC delivery receipt
				ShortMessage:       []byte(text),
			},
		}
		if err := target.writePDU(pdu); err != nil {
			// The client may have unbound during the delay. That is normal, not
			// an error worth failing on; the Python emulator logs and moves on.
			log.Printf("auto receipt for %s dropped: %v", messageID, err)
			return
		}
		log.Printf("auto receipt sent msg_id=%s stat=%s", messageID, opts.dlrStat)
	}()
}

// parseCommandStatus reads a submit_sm_resp command status from the flag, as
// decimal or 0x-prefixed hex. Names are deliberately not accepted: the SMPP
// status vocabulary is large and partly vendor-specific, and an emulator that
// silently mapped an unrecognised name to zero would answer ESME_ROK to an
// operator who asked for a failure.
func parseCommandStatus(value string) (uint32, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, nil
	}
	base := 10
	if lowered := strings.ToLower(trimmed); strings.HasPrefix(lowered, "0x") {
		trimmed, base = lowered[2:], 16
	}
	parsed, err := strconv.ParseUint(trimmed, base, 32)
	if err != nil {
		return 0, fmt.Errorf("-submit-status %q: want a decimal or 0x-prefixed hex value", value)
	}
	return uint32(parsed), nil
}

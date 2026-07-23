// Package dlr implements Jasmin's delivery-receipt (DLR) correlation domain. This first
// unit is the SMSC message-id canonicalization that keys Redis correlation (quirk Q-006);
// the two-leg correlation engine over the rediscompat client follows in a later stage.
package dlr

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// MsgIDBase is the connector's dlr_msg_id_bases setting. It declares whether the SMSC
// returns the submit_sm_resp message id and the deliver_sm receipt id in the same base,
// or in different bases that must be reconciled so the two DLR legs correlate to one key.
// (jasmin: SMPPClientConfig.dlr_msg_id_bases; managers/listeners.py code_dlr_msgid.)
type MsgIDBase uint8

const (
	// MsgIDBaseSame: both directions use the same base; canonicalize both by upper+lstrip.
	MsgIDBaseSame MsgIDBase = 0
	// MsgIDBaseReceiptDec: submit_sm_resp id is hex, deliver_sm receipt id is decimal, so
	// the receipt id is converted decimal->uppercase-hex to match the resp key.
	MsgIDBaseReceiptDec MsgIDBase = 1
	// MsgIDBaseReceiptHex: submit_sm_resp id is decimal, deliver_sm receipt id is hex, so
	// the receipt id is converted hex->decimal to match the resp key.
	MsgIDBaseReceiptHex MsgIDBase = 2
)

// ErrInvalidMsgID reports a message id that cannot be interpreted in the required base.
// Jasmin raises (and drops the DLR) in the same situations; callers decide drop/log.
var ErrInvalidMsgID = errors.New("dlr: invalid SMSC message id")

// CanonicalizeSubmitRespID canonicalizes a submit_sm_resp message id into the form used
// for the Redis correlation key: uppercased, leading zeros stripped. This is
// base-independent (jasmin managers/content.py: `smpp_msgid.decode().upper().lstrip('0')`).
//
// Edge preserved from Jasmin: an all-zero id canonicalizes to the empty string (Python's
// lstrip('0')), so the legacy key degenerates to `queue-msgid:` — reproduced, not "fixed".
func CanonicalizeSubmitRespID(id string) string {
	return strings.TrimLeft(strings.ToUpper(id), "0")
}

// CodeReceiptID transforms a deliver_sm receipt's SMSC id into the same key space as the
// canonicalized submit_sm_resp id, per the connector's base setting (jasmin
// managers/listeners.py `code_dlr_msgid`). Arbitrary-precision integers (math/big) match
// Python's int semantics for the up-to-65-byte ids, where int64 would overflow.
//
// The base-2 path intentionally does NOT upper/lstrip its result — Jasmin's `int(id, 16)`
// returns a plain decimal, an asymmetry with bases 0/1 that is preserved for parity.
func CodeReceiptID(id string, base MsgIDBase) (string, error) {
	switch base {
	case MsgIDBaseSame:
		// str(id).upper().lstrip('0')
		return strings.TrimLeft(strings.ToUpper(id), "0"), nil
	case MsgIDBaseReceiptDec:
		// ('%x' % int(id)).upper().lstrip('0') : decimal receipt -> uppercase hex
		n, ok := new(big.Int).SetString(id, 10)
		if !ok {
			return "", fmt.Errorf("%w: %q is not decimal (base 1)", ErrInvalidMsgID, id)
		}
		return strings.TrimLeft(strings.ToUpper(n.Text(16)), "0"), nil
	case MsgIDBaseReceiptHex:
		// int(id, 16) : hex receipt -> decimal string
		n, ok := new(big.Int).SetString(id, 16)
		if !ok {
			return "", fmt.Errorf("%w: %q is not hex (base 2)", ErrInvalidMsgID, id)
		}
		return n.Text(10), nil
	default:
		return "", fmt.Errorf("%w: unknown dlr_msg_id_bases %d", ErrInvalidMsgID, base)
	}
}

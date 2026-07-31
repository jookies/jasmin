package termination

import (
	"errors"
	"strings"
)

// ErrInvalidDestination reports a destination that cannot be a subscriber
// number, so no activation window can exist for it.
var ErrInvalidDestination = errors.New("termination: destination is not a number")

// maxAddressDigits bounds a destination at the SMPP address field width
// (21 octets including the NUL terminator).
const maxAddressDigits = 20

// NormalizeDestination produces the activation-window key form of a destination:
// whitespace trimmed, a single leading '+' removed, digits only.
//
// The key form is byte-identical to the legacy normalize_msisdn
// (fake_smsc.py:78-82) for every valid number, so the same Redis keys are read.
// It differs deliberately for invalid input. The legacy helper's docstring claims
// "digits only" but the code only strips '+', so a destination of the literal
// string "undefined" became a lookup of dlr:block:undefined — a key that never
// exists, making every such message REJECTD with no trace of why. Here that
// input is an error, and the caller rejects it explicitly and counts it instead
// of consulting the gate with garbage.
//
// The partner-visible outcome is unchanged: those messages were rejected before
// and are rejected now. What changes is that the reason is recorded.
func NormalizeDestination(addr string) (string, error) {
	trimmed := strings.TrimSpace(addr)
	trimmed = strings.TrimPrefix(trimmed, "+")
	if trimmed == "" {
		return "", ErrInvalidDestination
	}
	if len(trimmed) > maxAddressDigits {
		return "", ErrInvalidDestination
	}
	for _, r := range trimmed {
		if r < '0' || r > '9' {
			return "", ErrInvalidDestination
		}
	}
	return trimmed, nil
}

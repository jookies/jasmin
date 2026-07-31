package cdr

import "testing"

// A terminating connector talks to no SMSC, so its CDR never reaches
// SMSC_ACCEPTED and the final-DLR guard refused the receipt it synthesized --
// forever, at hundreds of rejections a minute on live partner traffic. The
// partner got no receipt and the operator no delivery outcome.
func TestTerminatedLocallyIsTerminal(t *testing.T) {
	if !StateTerminatedLocally.Terminal() {
		t.Error("TERMINATED_LOCALLY must be terminal: it is this gateway's own acceptance")
	}
	// The states the guard must still refuse. ADMITTED in particular: relaxing
	// the guard to admit it would let a final DLR land on a message that
	// nothing, anywhere, ever accepted.
	for _, state := range []State{StateAdmitted, StateRetryPending, StateUnknownAfterSend} {
		if state.Terminal() {
			t.Errorf("%s must not be terminal", state)
		}
	}
}

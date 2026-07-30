package cdr_test

import (
	"testing"

	"github.com/pumpitspace/synevyr/internal/core/cdr"
)

func TestModeForAmounts(t *testing.T) {
	tests := []struct {
		early float64
		late  float64
		want  cdr.BillingMode
	}{
		{want: cdr.BillingFree},
		{early: 1, want: cdr.BillingPrepaid},
		{late: 1, want: cdr.BillingPostpaid},
		{early: 1, late: 1, want: cdr.BillingSplit},
	}
	for _, test := range tests {
		if got := cdr.ModeForAmounts(test.early, test.late); got != test.want {
			t.Errorf("ModeForAmounts(%g,%g)=%q want %q", test.early, test.late, got, test.want)
		}
	}
}

func TestTerminalStates(t *testing.T) {
	for _, state := range []cdr.State{cdr.StateSMSCAccepted, cdr.StateSMSCRejected, cdr.StateTerminalTimeout} {
		if !state.Terminal() {
			t.Errorf("%q must be terminal", state)
		}
	}
	for _, state := range []cdr.State{cdr.StateAdmitted, cdr.StateRetryPending, cdr.StateUnknownAfterSend} {
		if state.Terminal() {
			t.Errorf("%q must not be terminal", state)
		}
	}
}

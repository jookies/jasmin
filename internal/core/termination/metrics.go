package termination

import (
	"github.com/pumpitspace/synevyr/internal/core/stats"
)

// recordVerdict counts one receipt decision, and separately counts it as a gate
// bypass when the verdict source could not be reached.
//
// The two are separate series rather than a label on one, because they answer
// different questions with different urgencies. The verdict counter is a
// business signal — how much of this partner's traffic has an open activation
// window — and it is expected to be non-zero on both outcomes. The bypass
// counter is an infrastructure alarm: it must read zero, and burying it as one
// label value among several is how a number that must be zero ends up on the
// same panel as numbers that are never zero.
//
// A bypassed decision is counted in BOTH: the partner really was told something,
// and what they were told is what the verdict counter reports.
func recordVerdict(connectorID, sourceName string, verdict Verdict) {
	registry := stats.DefaultPrometheus()
	registry.RecordTerminationVerdict(connectorID, verdict.Stat)
	if verdict.GateBypassed {
		registry.RecordTerminationGateBypass(connectorID, sourceName)
	}
}

// recordDelivery counts one downstream delivery lifecycle event.
func recordDelivery(connectorID, outcome string) {
	stats.DefaultPrometheus().RecordTerminationDelivery(connectorID, outcome)
}

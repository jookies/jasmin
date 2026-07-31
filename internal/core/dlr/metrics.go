package dlr

import (
	"strconv"

	"github.com/pumpitspace/synevyr/internal/core/stats"
)

// levelUnknown labels a receipt whose requested DLR level could not be read.
//
// It is the honest label for a correlation failure: the level lives in the
// dlr:<msgid> record, and a correlation failure is precisely the case where that
// record could not be found or parsed. Guessing a level would make the alert
// group by a number nobody measured.
const levelUnknown = 0

// recordForward counts one attempt to hand a receipt onward.
//
// The outcome dimension reports the FORWARDING outcome, not the message's
// delivery state — the state is already the final_state label. This matches what
// docs/runbooks/dlrs-not-arriving.md tells an operator to read: delivered means
// the receipt was published towards the customer, failed means it was not, and
// correlation_failure means it never mapped to a submit in the first place. A
// receipt that says UNDELIV and reaches the customer is a delivered forward of a
// failed message, and both facts are visible.
func recordForward(level int, finalState string, publishErr error) {
	outcome := stats.DLROutcomeDelivered
	if publishErr != nil {
		outcome = stats.DLROutcomeFailed
	}
	stats.DefaultPrometheus().RecordDLR(level, finalState, outcome)
}

// recordCorrelationFailure counts a receipt that could not be matched to a
// submit. finalState is whatever the receipt claimed, which is still useful:
// a burst of correlation failures on one state usually means one expiry window
// is too short, not that correlation is broken generally.
func recordCorrelationFailure(finalState string) {
	stats.DefaultPrometheus().RecordDLR(levelUnknown, finalState, stats.DLROutcomeCorrelationFailure)
}

// dlrLevel reads the requested level from a DLR record, reporting levelUnknown
// for a record that does not carry a usable one.
func dlrLevel(fields map[string]string) int {
	level, err := strconv.Atoi(fields["level"])
	if err != nil {
		return levelUnknown
	}
	return level
}

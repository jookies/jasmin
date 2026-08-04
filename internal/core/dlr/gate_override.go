package dlr

import "log/slog"

// Record field names carrying a DLR registry gate's decision. They are written
// at submit time by the submit path and are absent for every ungated submit.
const (
	gateStatusField = "gate_stat"
	gateErrorField  = "gate_err"
)

// applyGateOverride replaces the receipt status a terminal leg will report with
// the one the registry gate decided when the message was submitted.
//
// It returns ev unchanged for a record with no gate decision, which is every
// record unless an operator switched the gate on for the submitting user.
//
// What this does and does not change is the whole point of the feature, so it is
// worth stating plainly: the partner is told the gate's status, while ev's
// original status has already been handed to the CDR by the caller. An override
// is therefore always visible in two places that disagree on purpose -- the
// commercial record says what the upstream reported, the receipt says what the
// registry decided -- and the log line below is what ties them together.
func applyGateOverride(ev DeliverReceiptEvent, record map[string]string, submitQueueID string, logger *slog.Logger) DeliverReceiptEvent {
	status := record[gateStatusField]
	if status == "" {
		return ev
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("DLR registry gate overrode a terminal receipt",
		slog.String("msgid", submitQueueID),
		slog.String("upstream_status", ev.Status),
		slog.String("upstream_error", ev.Err),
		slog.String("reported_status", status),
		slog.String("reported_error", record[gateErrorField]),
	)
	ev.Status = status
	if gateErr := record[gateErrorField]; gateErr != "" {
		ev.Err = gateErr
	}
	// dlvrd is overridden too, not left as the upstream reported it. The level-2
	// HTTP callback passes stat and dlvrd as separate parameters, so an override
	// that changed only the status would hand a partner "message_status=REJECTD"
	// beside "dlvrd=001" -- the self-contradictory receipt termination/verdict.go
	// derives its own counts to avoid. sub is untouched: it counts the parts that
	// were submitted, which the gate has no opinion about.
	if ev.Dlvrd != "" {
		if isSuccessState(ev.Status) {
			ev.Dlvrd = "001"
		} else {
			ev.Dlvrd = "000"
		}
	}
	return ev
}

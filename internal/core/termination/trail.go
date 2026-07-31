package termination

import (
	"context"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// The decision trail replaces the legacy `dlr:audit` Redis stream
// (fake_smsc.py:234-274), and it is a QUERY over the spool rather than a second
// table.
//
// The spool row is already the durable record of a decision: it carries the
// verdict, the reason, the gate-bypass flag, the partner, the destination, when
// the message arrived, when the receipt was due and when it was actually sent.
// Every field the legacy stream carried is either on that row or derivable from
// it, so a second table would be a copy of data that already exists, written in
// the same transaction, that could disagree with the row it copies — and one
// more place holding per-message evidence about OTP traffic.
//
// The two legacy fields the row does not carry, and why neither justifies a
// table:
//
//   - registered_delivery. The legacy stream recorded it because the fake SMSC
//     used it to decide whether to send a receipt at all. It belongs to the
//     submit PDU, and the audit line for that PDU already records it.
//   - the pre-normalization destination. The row stores the normalized digits
//     the gate was actually keyed on, which is the value a dispute is about; the
//     submitted form is in the MT audit line for the same message id.
//
// One real difference from the legacy stream, worth stating rather than
// discovering: the stream was capped by MAXLEN and the trail is bounded by the
// spool's retention window (24 h by default). Both are bounded, but they are
// bounded by different things — a quiet week keeps a MAXLEN stream far longer
// than 24 h. If the trail is ever wanted for longer than the content is, that is
// a retention decision (redact content on prune, keep the row), not an argument
// for a second table.

// DecisionEntry is one verdict as the decision trail reports it: the legacy
// audit field set, rebuilt from the spool row.
//
// It deliberately has no text or raw field. A decision trail answers "what were
// they told, and why" — nothing in that question needs the message body, and
// giving this projection one would put OTP content on a surface whose whole
// purpose is to be queried freely.
type DecisionEntry struct {
	MessageID string `json:"message_id"`
	// Connector and Partner have no legacy equivalent: the fake SMSC was
	// single-tenant, and `system_id` was the only attribution it had.
	Connector  string `json:"connector"`
	Partner    string `json:"partner"`
	SourceAddr string `json:"source_addr"`
	// DestDigits is the normalized destination — the value the activation key
	// was built from, and therefore the value the decision was actually about.
	DestDigits string `json:"dest_digits"`
	// Outcome reproduces the legacy vocabulary exactly: delivrd, rejectd, and
	// delivrd_failopen for an accept taken with the gate unreachable
	// (fake_smsc.py:386). The third value is the point of the field: a fail-open
	// accept and a real accept are the same receipt and must not be the same
	// trail entry.
	Outcome string `json:"outcome"`
	// Stat, Err and Dlvrd are the receipt fields as sent. Dlvrd is derived from
	// the verdict, never stored, so a trail entry cannot claim a delivered count
	// that contradicts its own status.
	Stat  string `json:"stat"`
	Err   string `json:"err"`
	Dlvrd int    `json:"dlvrd"`
	// Reason is the human-readable explanation. The legacy stream had no such
	// field: it recorded that a decision happened, not why.
	Reason string `json:"reason"`
	// GateBypassed is the legacy redis_ok flag, inverted to name the exceptional
	// case rather than the normal one.
	GateBypassed bool `json:"gate_bypassed"`
	// SubmittedAt is the legacy submit_ts.
	SubmittedAt time.Time `json:"submitted_at"`
	// ReceiptDueAt and ReceiptSentAt replace the legacy dlr_delay and
	// dlr_sent_ts. Two timestamps rather than a configured delay and an emission
	// time, because what an operator needs is whether a receipt is late, and a
	// configured delay cannot answer that.
	ReceiptDueAt  *time.Time `json:"receipt_due_at,omitempty"`
	ReceiptSentAt *time.Time `json:"receipt_sent_at,omitempty"`
	// ReceiptSent is the legacy `delivered` flag: was the receipt actually
	// emitted. It is not whether the partner's session took it.
	ReceiptSent bool `json:"receipt_sent"`
	// DeliveryState and DeliveryAttempts describe the downstream push, which the
	// legacy fake SMSC knew nothing about — it did not deliver content.
	DeliveryState    string `json:"delivery_state"`
	DeliveryAttempts int    `json:"delivery_attempts"`
}

// Legacy outcome vocabulary (fake_smsc.py:386, :407).
const (
	OutcomeDelivered         = "delivrd"
	OutcomeDeliveredFailOpen = "delivrd_failopen"
	OutcomeRejected          = "rejectd"
)

// DecisionEntryFromRecord projects one spool row into a trail entry.
func DecisionEntryFromRecord(record msgspool.Record) DecisionEntry {
	verdict := verdictFromRecord(record)
	return DecisionEntry{
		MessageID:        record.MessageID,
		Connector:        record.ConnectorID,
		Partner:          record.UserID,
		SourceAddr:       record.SourceAddr,
		DestDigits:       record.DestAddr,
		Outcome:          trailOutcome(verdict),
		Stat:             verdict.Stat,
		Err:              verdict.Err,
		Dlvrd:            verdict.Delivered(),
		Reason:           verdict.Reason,
		GateBypassed:     verdict.GateBypassed,
		SubmittedAt:      record.ReceivedAt,
		ReceiptDueAt:     record.ReceiptDueAt,
		ReceiptSentAt:    record.ReceiptSentAt,
		ReceiptSent:      record.ReceiptSentAt != nil,
		DeliveryState:    string(record.DeliveryState),
		DeliveryAttempts: record.DeliveryAttempts,
	}
}

func trailOutcome(verdict Verdict) string {
	switch {
	case verdict.Accept && verdict.GateBypassed:
		return OutcomeDeliveredFailOpen
	case verdict.Accept:
		return OutcomeDelivered
	default:
		return OutcomeRejected
	}
}

// TrailRequest pages the decision trail.
type TrailRequest struct {
	// Cursor is the opaque paging position from the previous page.
	Cursor string
	// Partner restricts the trail to one submitting user. It is the per-partner
	// axis the legacy single-keyspace stream could not offer at all.
	Partner   string
	Connector string
	DestAddr  string
	// Stat filters on the receipt status the partner was told.
	Stat string
	// GateBypassedOnly returns only decisions taken with the gate unreachable —
	// the blast-radius query after a Redis outage.
	GateBypassedOnly bool
	// IncludeSegmentReceipts admits the per-segment rows of a concatenated
	// message.
	//
	// Each segment of a long message is its own submit_sm under SMPP 3.4 and gets
	// its own receipt, so a partner asking "why did I get three receipts for one
	// message?" is asking about rows that carry no content and are excluded from
	// every other read. Without this the trail answers that question with one
	// decision and no explanation for the other two.
	IncludeSegmentReceipts bool
	From                   *time.Time
	To                     *time.Time
	Limit                  int
}

// TrailPage is one page of decisions.
type TrailPage struct {
	Entries    []DecisionEntry
	NextCursor string
}

// TrailReader is the spool surface the trail needs. Declared here, and
// satisfied by *msgspool.Service, so the trail cannot reach the reveal path:
// the only method it can call is the one that masks content.
type TrailReader interface {
	Search(ctx context.Context, principal msgspool.Principal, request msgspool.SearchRequest) (msgspool.SearchPage, error)
}

// DecisionTrail reads one page of the decision trail.
//
// IncludeContent is hardcoded false rather than exposed as an option. A caller
// that wants message text has msgspool.Service.Reveal, which requires the
// revealer role and writes an audit row naming the actor; letting the trail
// carry text would route content reads through a surface whose authorization is
// "may look at decisions".
func DecisionTrail(
	ctx context.Context,
	reader TrailReader,
	principal msgspool.Principal,
	request TrailRequest,
) (TrailPage, error) {
	if reader == nil {
		return TrailPage{}, msgspool.ErrInvalidInput
	}
	page, err := reader.Search(ctx, principal, msgspool.SearchRequest{
		Cursor:           request.Cursor,
		ConnectorID:      request.Connector,
		UserID:           request.Partner,
		DestAddr:         request.DestAddr,
		VerdictStat:      request.Stat,
		GateBypassedOnly: request.GateBypassedOnly,
		ReceivedFrom:     request.From,
		ReceivedTo:       request.To,
		IncludeContent:   false,
		// Never content, always optional segments: the trail is the record of
		// what was decided and told, and a segment receipt is one of those.
		IncludeReceiptOnly: request.IncludeSegmentReceipts,
		Limit:              request.Limit,
	})
	if err != nil {
		return TrailPage{}, err
	}
	entries := make([]DecisionEntry, 0, len(page.Records))
	for _, record := range page.Records {
		entries = append(entries, DecisionEntryFromRecord(record))
	}
	return TrailPage{Entries: entries, NextCursor: page.NextCursor}, nil
}

package termination

import (
	"context"
	"fmt"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

// SpoolStore is the part of msgspool.Repository the termination connector uses.
// Declared at the consumer so the worker and both runners can be tested without
// a database, and so this package cannot quietly grow a dependency on the rest
// of the spool's surface (search, reveal, audit — those belong to the console
// and the pull API, not to the message path).
type SpoolStore interface {
	Put(ctx context.Context, message msgspool.Message, createdAt time.Time) (msgspool.Record, error)
	DueForDelivery(ctx context.Context, now time.Time, limit int) ([]msgspool.Record, error)
	MarkDelivered(ctx context.Context, messageID string, at time.Time) error
	MarkAttemptFailed(ctx context.Context, messageID string, nextAttemptAt time.Time) error
	MarkDeadLettered(ctx context.Context, messageID string) error
	ClaimDueReceipts(ctx context.Context, owner string, now time.Time, lease time.Duration, limit int) ([]msgspool.Record, error)
	MarkReceiptSent(ctx context.Context, messageID, owner string, at time.Time) error
}

// Spool adapts SpoolStore to the worker's SpoolWriter.
type Spool struct {
	store SpoolStore
	now   func() time.Time
	// pushes reports whether a connector has a downstream endpoint. A row is
	// scheduled for delivery only when one does; see Record.
	pushes func(connectorID string) bool
}

// NewSpool wires the adapter.
//
// pushes answers "does this connector push to an application?" — a connector
// with no endpoint is a pull-only deployment, where the application fetches from
// the spool instead. Nil means every connector pushes, which is the right
// default for a single-connector gateway and wrong for a mixed one.
func NewSpool(store SpoolStore, now func() time.Time, pushes func(connectorID string) bool) (*Spool, error) {
	if store == nil {
		return nil, fmt.Errorf("termination: nil spool store")
	}
	if now == nil {
		now = time.Now
	}
	if pushes == nil {
		pushes = func(string) bool { return true }
	}
	return &Spool{store: store, now: now, pushes: pushes}, nil
}

// Record persists the assembled message, its verdict and when the receipt is
// owed.
//
// The first delivery attempt is scheduled for now: the delivery runner picks the
// row up rather than the worker pushing inline. That keeps the queue consumer
// free of a downstream application's latency, and it is what makes a delivery
// survive this process dying between the spool write and the push.
//
// A connector with no endpoint is scheduled for nothing. Scheduling a push that
// no sink will ever make would walk the row through its whole retry budget and
// dead-letter it — reporting a delivery failure for a message nobody intended to
// deliver, on a deployment where the application fetches from the spool instead.
func (s *Spool) Record(ctx context.Context, msg Message, verdict Verdict, receiptDueAt time.Time) error {
	now := s.now()
	var nextAttempt *time.Time
	if s.pushes(msg.Connector) {
		attemptAt := now
		nextAttempt = &attemptAt
	}
	stored := msgspool.Message{
		MessageID:   msg.MessageID,
		ConnectorID: msg.Connector,
		UserID:      msg.Partner,
		SourceAddr:  msg.From,
		DestAddr:    msg.To,
		Text:        msg.Text,
		Raw:         msg.Raw,
		DataCoding:  msg.DataCoding,
		Encoding:    msg.Encoding,
		Parts:       msg.Parts,
		Verdict: msgspool.Verdict{
			Accept:       verdict.Accept,
			Stat:         verdict.Stat,
			Err:          verdict.Err,
			Reason:       verdict.Reason,
			GateBypassed: verdict.GateBypassed,
		},
		ReceivedAt:    msg.ReceivedAt,
		NextAttemptAt: nextAttempt,
		ReceiptDueAt:  &receiptDueAt,
	}
	if _, err := s.store.Put(ctx, stored, now); err != nil {
		return fmt.Errorf("termination: spool put: %w", err)
	}
	return nil
}

// RecordReceiptOnly persists the receipt one segment of a concatenated submit is
// owed, and nothing else.
//
// Under SMPP 3.4 that segment was its own submit_sm: it was answered with its
// own message id and it carried its own registered_delivery flag, so it is owed
// its own receipt whether or not its siblings ever arrive. The legacy fake SMSC
// receipts every submit_sm unconditionally, so a partner reconciling receipts
// against submits counts N for a message sent in N segments; producing one would
// leave them permanently short by N-1 per long message, with nothing in either
// system to explain the gap.
//
// The content is deliberately not stored. This adapter clears Text and Raw
// itself rather than trusting the caller, because the caller has them in hand —
// the worker decodes every segment before it knows the message is split — and
// "the caller remembers not to pass them" is not a property anything can check.
// The segment's own text is half a message anyway, split mid-rune for any
// multi-byte encoding, and the assembled row holds the whole of it: a second
// copy would be a second breach surface for an OTP body and would answer no
// question the first cannot.
//
// No delivery is scheduled: there is nothing to deliver, and a scheduled push
// would walk the row through its retry budget and dead-letter it, reporting a
// delivery failure for a fragment nobody meant to deliver.
func (s *Spool) RecordReceiptOnly(ctx context.Context, msg Message, verdict Verdict, receiptDueAt time.Time) error {
	now := s.now()
	stored := msgspool.Message{
		MessageID:   msg.MessageID,
		ConnectorID: msg.Connector,
		UserID:      msg.Partner,
		SourceAddr:  msg.From,
		DestAddr:    msg.To,
		DataCoding:  msg.DataCoding,
		// Encoding names the codec that produced Text, and there is no Text on
		// this row. Reporting the per-segment decode would name a codec that was
		// applied to half a character sequence and got a different answer from the
		// one the assembled message will record.
		Encoding: "",
		// One segment, not the message's part count: this row accounts for the one
		// submit_sm that produced it. The assembled row is where the group's size
		// is recorded.
		Parts: 1,
		Verdict: msgspool.Verdict{
			Accept:       verdict.Accept,
			Stat:         verdict.Stat,
			Err:          verdict.Err,
			Reason:       verdict.Reason,
			GateBypassed: verdict.GateBypassed,
		},
		ReceivedAt:    msg.ReceivedAt,
		NextAttemptAt: nil,
		ReceiptDueAt:  &receiptDueAt,
		ReceiptOnly:   true,
	}
	if _, err := s.store.Put(ctx, stored, now); err != nil {
		return fmt.Errorf("termination: spool put receipt: %w", err)
	}
	return nil
}

// messageFromRecord maps a stored row back to the message a delivery carries.
//
// A record read with content redacted must never be delivered: the downstream
// application would receive an empty message body and treat it as the message.
// Callers check ContentRedacted before using this.
func messageFromRecord(record msgspool.Record) Message {
	return Message{
		MessageID:  record.MessageID,
		Connector:  record.ConnectorID,
		Partner:    record.UserID,
		From:       record.SourceAddr,
		To:         record.DestAddr,
		Text:       record.Text,
		Raw:        record.Raw,
		DataCoding: record.DataCoding,
		Encoding:   record.Encoding,
		Parts:      record.Parts,
		ReceivedAt: record.ReceivedAt,
	}
}

// verdictFromRecord maps the stored verdict back.
func verdictFromRecord(record msgspool.Record) Verdict {
	return Verdict{
		Accept:       record.Verdict.Accept,
		Stat:         record.Verdict.Stat,
		Err:          record.Verdict.Err,
		Reason:       record.Verdict.Reason,
		GateBypassed: record.Verdict.GateBypassed,
	}
}

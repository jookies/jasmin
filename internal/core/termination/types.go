// Package termination implements the MT termination connector: a connector type
// that terminates traffic on this platform instead of handing it to an upstream
// SMSC. It decodes and reassembles the message, decides the receipt from a
// verdict source, delivers the content to a downstream application and
// synthesizes the DLR the submitting partner receives.
//
// It replaces two Python services documented in docs/plans/021-mt-termination-connector.md:
// a fake SMSC that answered receipts from a Redis activation window, and a
// broker tap that decoded message content into PostgreSQL.
package termination

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// Message is one fully assembled inbound message, after decoding and multipart
// reassembly. It is the unit a verdict is decided on, a spool row is written
// from, and a downstream delivery carries. Parts are already joined: nothing
// downstream of the connector worker sees a fragment.
type Message struct {
	// MessageID is the gateway message id. It is the same value the partner
	// receives in the receipt and the idempotency key downstream deliveries
	// carry, so a retry can be recognised as the same message.
	MessageID string
	// Connector is the termination connector id that handled this message.
	Connector string
	// Partner is the submitting user (the ESME account that sent it), used for
	// per-partner attribution that the legacy single-tenant path could not do.
	Partner string
	// From is the source address as submitted; it is frequently alphanumeric
	// (a brand name), so it is never normalized as a number.
	From string
	// To is the destination, normalized to digits (see NormalizeDestination).
	To string
	// Text is the decoded message, all parts joined.
	Text string
	// Raw is the pre-decode payload, all parts concatenated. Kept because a
	// disagreement about what a message said is only settleable with the bytes.
	Raw []byte
	// DataCoding is the submitted data_coding value.
	DataCoding byte
	// Encoding names the codec that actually produced Text, which is not always
	// what DataCoding claimed.
	Encoding string
	// Parts is how many segments were reassembled into this message (1 when the
	// message arrived whole).
	Parts int
	// ReceivedAt is when the last segment arrived.
	ReceivedAt time.Time
	// SAR carries concatenation coordinates declared in the submit's optional
	// parameters rather than in a User Data Header. Both forms are in use — a
	// UDH is inline in the body, SAR is out of band — and only the UDH form is
	// recoverable from Raw alone, so this field is how the other one reaches the
	// assembler. Nil means "no SAR concatenation declared", which is the common
	// case and is not the same as "not concatenated".
	SAR *Segment
}

// Verdict is the decision that becomes the partner's receipt.
//
// It reports whether an activation window is open for the destination — it is a
// property of the number, not a judgement about this particular message — so two
// messages arriving on the same number inside one window must produce the same
// verdict.
type Verdict struct {
	// Accept is true when the message is acknowledged as delivered.
	Accept bool
	// Stat is the SMPP receipt status: DELIVRD, REJECTD, UNDELIV and so on.
	Stat string
	// Err is the receipt's error field, three digits. It must agree with Stat:
	// a DELIVRD receipt carrying a non-zero err, or a REJECTD carrying
	// dlvrd:001, is the self-contradictory receipt this plan exists to stop
	// emitting.
	Err string
	// Reason is the human-readable explanation recorded in the decision trail
	// ("activation window open", "no window", "gate unreachable").
	Reason string
	// GateBypassed records that the verdict source could not be reached and a
	// fallback was applied. The legacy fake SMSC fails open on a Redis error
	// rather than rejecting legitimate traffic on an infrastructure blip; that
	// behaviour is preserved, and this flag is what makes it visible instead of
	// silent.
	GateBypassed bool
}

// Delivered is the count reported in the receipt's dlvrd field. It is derived
// from the verdict rather than stored, because every hardcoded dlvrd in this
// codebase has been wrong: cmd/synevyr-fake-smsc and scripts/interop/smsc_probe.py
// both emit dlvrd:001 err:000 alongside stat:UNDELIV.
func (v Verdict) Delivered() int {
	if v.Accept {
		return 1
	}
	return 0
}

// VerdictSource decides the receipt for a message.
//
// Implementations must be safe for concurrent use and must not block longer than
// their configured timeout: this runs on the path between a partner's submit and
// its receipt.
type VerdictSource interface {
	Decide(ctx context.Context, msg Message) (Verdict, error)
	// Name identifies the source in logs, metrics and the decision trail.
	Name() string
}

// DeliveryResult reports what a downstream delivery attempt produced.
type DeliveryResult struct {
	// Verdict is set only in http-inline mode, where the delivery response
	// carries the accept/reject decision. HasVerdict distinguishes "the app
	// said accept" from "the app said nothing", which a zero Verdict cannot.
	Verdict    Verdict
	HasVerdict bool
	// Attempt is the 1-based attempt number this result came from.
	Attempt int
	// StatusCode is the HTTP status observed, 0 when the request never
	// completed.
	StatusCode int
}

// DeliverySink hands the decoded message to the downstream application.
//
// A sink reports failure so the caller can retry and, after the final attempt,
// dead-letter the message. Nothing may be dropped: the legacy throwers purge
// after three retries and lose the message entirely, which is the behaviour this
// connector must not inherit.
type DeliverySink interface {
	Deliver(ctx context.Context, msg Message, attempt int) (DeliveryResult, error)
	Name() string
}

// String redacts. Message text and raw bytes are one-time passcodes: they must
// never reach a log line, an error string, or a %v of a struct that happens to
// contain a message.
//
// msgspool.Message does the same for the stored form. This is the in-flight
// form, and it travels through more code — the worker, the sink, both runners —
// so it needs the same guard rather than relying on every future call site
// remembering.
//
// Note the one hole Go leaves open: %#v ignores Stringer and prints every field.
// Nothing may format a Message with %#v.
func (m Message) String() string {
	return fmt.Sprintf(
		"termination.Message{id:%s connector:%s from:%s to:%s parts:%d encoding:%s text_len:%d raw_len:%d}",
		m.MessageID, m.Connector, m.From, m.To, m.Parts, m.Encoding, len(m.Text), len(m.Raw),
	)
}

// LogValue keeps the same redaction on the structured-logging path, which does
// not go through String.
func (m Message) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("message_id", m.MessageID),
		slog.String("connector", m.Connector),
		slog.String("from", m.From),
		slog.String("to", m.To),
		slog.Int("parts", m.Parts),
		slog.String("encoding", m.Encoding),
		slog.Int("text_len", len(m.Text)),
		slog.Int("raw_len", len(m.Raw)),
	)
}

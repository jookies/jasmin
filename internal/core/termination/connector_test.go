package termination

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/dlr"
	"github.com/pumpitspace/synevyr/internal/core/tlv"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
	"github.com/pumpitspace/synevyr/internal/transport/smppwire"
)

type workerStubPublisher struct {
	published []amqpcompat.Envelope
	err       error
}

func (p *workerStubPublisher) Publish(_ context.Context, _ string, _ string, msg amqpcompat.Envelope) error {
	if p.err != nil {
		return p.err
	}
	p.published = append(p.published, msg)
	return nil
}

type workerStubSubmits struct {
	body smppwire.SubmitSMBody
	err  error
}

func (d workerStubSubmits) DecodeSubmitSM(context.Context, []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	return d.body, nil, d.err
}

type workerStubContent struct {
	text     string
	encoding string
	err      error
}

func (c workerStubContent) Decode([]byte, byte) (string, string, error) {
	return c.text, c.encoding, c.err
}

type workerStubAssembler struct {
	complete bool
}

func (a workerStubAssembler) Add(_ context.Context, msg Message) (Message, bool, error) {
	return msg, a.complete, nil
}

type workerStubVerdicts struct {
	verdict Verdict
	err     error
	calls   int
}

func (v *workerStubVerdicts) Decide(context.Context, Message) (Verdict, error) {
	v.calls++
	return v.verdict, v.err
}

func (v *workerStubVerdicts) Name() string { return "stub" }

type workerStubSpool struct {
	recorded []Message
	// receiptOnly holds the per-segment receipt rows, kept apart from recorded
	// so a test asserting "one message, three receipts" cannot pass by counting
	// the wrong thing.
	receiptOnly  []Message
	receiptDueAt time.Time
	err          error
}

func (s *workerStubSpool) Record(_ context.Context, msg Message, _ Verdict, receiptDueAt time.Time) error {
	if s.err != nil {
		return s.err
	}
	s.recorded = append(s.recorded, msg)
	s.receiptDueAt = receiptDueAt
	return nil
}

func (s *workerStubSpool) RecordReceiptOnly(_ context.Context, msg Message, _ Verdict, receiptDueAt time.Time) error {
	if s.err != nil {
		return s.err
	}
	s.receiptOnly = append(s.receiptOnly, msg)
	s.receiptDueAt = receiptDueAt
	return nil
}

func (s *workerStubSpool) MarkDelivered(context.Context, string, time.Time) error { return nil }

func (s *workerStubSpool) MarkAttemptFailed(context.Context, string, int, time.Time, string) error {
	return nil
}

func (s *workerStubSpool) MarkDeadLettered(context.Context, string, int, string) error { return nil }

func newTestWorker(t *testing.T, spool *workerStubSpool, verdicts *workerStubVerdicts, publisher *workerStubPublisher, complete bool) *Worker {
	t.Helper()
	fixed := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	leg, err := NewSMSCLeg(publisher, "partner-a-term", func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("new smsc leg: %v", err)
	}
	worker, err := NewWorker(
		WorkerConfig{CID: "partner-a-term", ReceiptDelay: 5 * time.Second, ReceiptJitter: 2 * time.Second},
		workerStubSubmits{body: smppwire.SubmitSMBody{
			SourceAddress:      []byte("NETFLIX"),
			DestinationAddress: []byte("+380671234567"),
			ShortMessage:       []byte("code 63125"),
			DataCoding:         8,
		}},
		workerStubContent{text: "code 63125", encoding: "ucs2"},
		workerStubAssembler{complete: complete},
		verdicts,
		spool,
		leg,
		func() time.Time { return fixed },
		func() float64 { return 0.5 },
	)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}
	return worker
}

func TestWorkerProcessRecordsAndPublishesAcceptLeg(t *testing.T) {
	spool := &workerStubSpool{}
	verdicts := &workerStubVerdicts{verdict: Verdict{Accept: true, Stat: "DELIVRD", Err: "000"}}
	publisher := &workerStubPublisher{}
	worker := newTestWorker(t, spool, verdicts, publisher, true)

	if err := worker.Process(context.Background(), SubmitMetadata{QueueMessageID: "msg-1", Partner: "partner-a"}, []byte("body")); err != nil {
		t.Fatalf("process: %v", err)
	}

	if len(publisher.published) != 1 {
		t.Fatalf("want one accept leg published, got %d", len(publisher.published))
	}
	if len(spool.recorded) != 1 {
		t.Fatalf("want one spool row, got %d", len(spool.recorded))
	}
	// The destination is normalized to the activation-window key form; the
	// source is left alone because it is routinely alphanumeric.
	if got := spool.recorded[0].To; got != "380671234567" {
		t.Errorf("destination = %q, want normalized digits", got)
	}
	if got := spool.recorded[0].From; got != "NETFLIX" {
		t.Errorf("source = %q, want it untouched", got)
	}
	// 5s delay + half of the 2s jitter window.
	wantDue := time.Date(2026, 7, 30, 12, 0, 6, 0, time.UTC)
	if !spool.receiptDueAt.Equal(wantDue) {
		t.Errorf("receipt due at %s, want %s", spool.receiptDueAt, wantDue)
	}
}

// A whole message the plain-split stitch is holding declared no concatenation:
// it is a guess that this message and a later one are halves of one, and it is
// announced, spooled and receipted when its group is released, under the
// released message's id. Doing any of that here would announce the same id
// twice.
func TestWorkerProcessStitchHeldMessageProducesNothing(t *testing.T) {
	spool := &workerStubSpool{}
	verdicts := &workerStubVerdicts{verdict: Verdict{Accept: true, Stat: "DELIVRD", Err: "000"}}
	publisher := &workerStubPublisher{}
	worker := newTestWorker(t, spool, verdicts, publisher, false)

	if err := worker.Process(context.Background(), SubmitMetadata{QueueMessageID: "msg-1", Partner: "partner-a"}, []byte("body")); err != nil {
		t.Fatalf("process: %v", err)
	}

	if verdicts.calls != 0 {
		t.Errorf("verdict consulted %d times for a stitch-held message, want 0", verdicts.calls)
	}
	if len(publisher.published) != 0 || len(spool.recorded) != 0 || len(spool.receiptOnly) != 0 {
		t.Errorf("published %d legs, recorded %d rows and %d receipt rows, want none",
			len(publisher.published), len(spool.recorded), len(spool.receiptOnly))
	}
}

// The defect this correction exists for, at the smallest scale that shows it.
//
// A segment of a declared concatenated message is its own submit_sm: it was
// answered with its own message id and carries its own registered_delivery flag,
// so it is owed its own receipt even though its siblings have not arrived. What
// it must NOT get is a content row — the fragment's bytes are half a message and
// the assembled row holds the whole of it.
func TestWorkerProcessIncompleteSegmentStillAcceptsAndReceipts(t *testing.T) {
	spool := &workerStubSpool{}
	verdicts := &workerStubVerdicts{verdict: Verdict{Accept: true, Stat: "DELIVRD", Err: "000"}}
	publisher := &workerStubPublisher{}
	fixed := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	leg, err := NewSMSCLeg(publisher, "partner-a-term", func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("new smsc leg: %v", err)
	}
	// UDHL=5, IEI=0x00 (8-bit concat), IEDL=3, ref=0x2A, total=3, seq=1.
	segment := append([]byte{0x05, 0x00, 0x03, 0x2A, 0x03, 0x01}, []byte("Netflix: Usa ")...)
	worker, err := NewWorker(
		WorkerConfig{CID: "partner-a-term", ReceiptDelay: 5 * time.Second, ReceiptJitter: 2 * time.Second},
		workerStubSubmits{body: smppwire.SubmitSMBody{
			SourceAddress:      []byte("NETFLIX"),
			DestinationAddress: []byte("+380671234567"),
			ShortMessage:       segment,
			DataCoding:         0,
		}},
		workerStubContent{text: "Netflix: Usa ", encoding: "gsm7"},
		workerStubAssembler{complete: false},
		verdicts,
		spool,
		leg,
		func() time.Time { return fixed },
		func() float64 { return 0.5 },
	)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}

	if err := worker.Process(context.Background(), SubmitMetadata{QueueMessageID: "msg-1", Partner: "partner-a"}, []byte("body")); err != nil {
		t.Fatalf("process: %v", err)
	}

	if verdicts.calls != 1 {
		t.Errorf("verdict consulted %d times, want once: this segment is owed a receipt", verdicts.calls)
	}
	if len(publisher.published) != 1 {
		t.Fatalf("published %d accept legs, want 1 for this segment's own submit_sm", len(publisher.published))
	}
	if len(spool.recorded) != 0 {
		t.Errorf("spooled %d content rows for a fragment, want 0", len(spool.recorded))
	}
	if len(spool.receiptOnly) != 1 {
		t.Fatalf("spooled %d receipt rows, want 1", len(spool.receiptOnly))
	}
	row := spool.receiptOnly[0]
	if row.MessageID != "msg-1" {
		t.Errorf("receipt row id = %q, want this segment's own queue message id", row.MessageID)
	}
	if row.To != "380671234567" {
		t.Errorf("destination = %q, want it normalized", row.To)
	}
	// 5s delay + half of the 2s jitter window, the same schedule a whole message
	// gets: the partner cannot tell a segment's receipt from any other.
	wantDue := time.Date(2026, 7, 30, 12, 0, 6, 0, time.UTC)
	if !spool.receiptDueAt.Equal(wantDue) {
		t.Errorf("receipt due at %s, want %s", spool.receiptDueAt, wantDue)
	}
}

// A broken concatenation header is not a fragment. AddSegment treats "part 4 of
// 3" as a whole message, so DeclaredSegment must agree: disagreeing would make
// the worker receipt a message the assembler is about to complete, producing two
// receipts and two accept legs for one submit.
func TestDeclaredSegmentAgreesWithTheAssembler(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want bool
	}{
		{"plain message", Message{Raw: []byte("code 63125")}, false},
		{
			"udh concat",
			Message{Raw: append([]byte{0x05, 0x00, 0x03, 0x2A, 0x02, 0x01}, []byte("half")...)},
			true,
		},
		{
			"udh claiming part 0 of 0",
			Message{Raw: append([]byte{0x05, 0x00, 0x03, 0x2A, 0x00, 0x00}, []byte("half")...)},
			false,
		},
		{
			"sar",
			Message{SAR: &Segment{Reference: 0x2A, Total: 3, Sequence: 2, Body: []byte("half")}},
			true,
		},
		{
			"sar claiming part 4 of 3",
			Message{SAR: &Segment{Reference: 0x2A, Total: 3, Sequence: 4, Body: []byte("half")}},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, got := DeclaredSegment(tc.msg); got != tc.want {
				t.Errorf("DeclaredSegment = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkerProcessFailsWhenSpoolFails(t *testing.T) {
	spool := &workerStubSpool{err: errors.New("db down")}
	verdicts := &workerStubVerdicts{verdict: Verdict{Accept: true, Stat: "DELIVRD", Err: "000"}}
	worker := newTestWorker(t, spool, verdicts, &workerStubPublisher{}, true)

	// The caller must see the failure so the queue delivery is redelivered
	// rather than acknowledged: an ack here loses a message that was already
	// charged and already promised a receipt.
	if err := worker.Process(context.Background(), SubmitMetadata{QueueMessageID: "msg-1", Partner: "partner-a"}, []byte("body")); err == nil {
		t.Fatal("want an error when the spool write fails, got nil")
	}
}

func TestSMSCMessageIDIsDeterministic(t *testing.T) {
	// Redelivery after a crash must reproduce the same id, or the receipt that
	// was already published correlates to an id nothing references again.
	first := SMSCMessageID("msg-1")
	if second := SMSCMessageID("msg-1"); first != second {
		t.Fatalf("id not stable: %q then %q", first, second)
	}
	if other := SMSCMessageID("msg-2"); other == first {
		t.Fatal("different queue ids produced the same smsc id")
	}
	if first == "" {
		t.Fatal("empty smsc id")
	}
}

func TestReceiptDerivesCountersFromVerdict(t *testing.T) {
	leg, err := NewSMSCLeg(&workerStubPublisher{}, "partner-a-term", time.Now)
	if err != nil {
		t.Fatalf("new smsc leg: %v", err)
	}
	submitted := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	done := submitted.Add(6 * time.Second)

	cases := []struct {
		name      string
		verdict   Verdict
		wantDlvrd string
	}{
		{"accepted", Verdict{Accept: true, Stat: "DELIVRD", Err: "000"}, "001"},
		{"rejected", Verdict{Accept: false, Stat: "REJECTD", Err: "008"}, "000"},
		{"undeliverable", Verdict{Accept: false, Stat: "UNDELIV", Err: "001"}, "000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			receipt := leg.Receipt("ABC123", tc.verdict, submitted, done)
			if receipt.Dlvrd != tc.wantDlvrd {
				t.Errorf("dlvrd = %q, want %q", receipt.Dlvrd, tc.wantDlvrd)
			}
			if receipt.Stat != tc.verdict.Stat || receipt.Err != tc.verdict.Err {
				t.Errorf("stat/err = %q/%q, want %q/%q", receipt.Stat, receipt.Err, tc.verdict.Stat, tc.verdict.Err)
			}
			// The bug this whole exercise found: a receipt that says nothing
			// was delivered must never also claim one was.
			if receipt.Stat != "DELIVRD" && receipt.Dlvrd != "000" {
				t.Errorf("non-delivered receipt claims dlvrd=%q", receipt.Dlvrd)
			}
			if receipt.Text != "" {
				t.Errorf("receipt text = %q, want empty (OTP content must not travel in receipts)", receipt.Text)
			}
			var _ dlr.Receipt = receipt
		})
	}
}

func TestReceiptDelayMath(t *testing.T) {
	cases := []struct {
		name           string
		delay, jitter  time.Duration
		jitterFraction float64
		want           time.Duration
	}{
		{"no jitter", 5 * time.Second, 0, 0.9, 5 * time.Second},
		{"half jitter", 5 * time.Second, 2 * time.Second, 0.5, 6 * time.Second},
		{"clamped fraction", 5 * time.Second, 2 * time.Second, 4, 7 * time.Second},
		{"negative delay", -time.Second, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ReceiptDelay(tc.delay, tc.jitter, tc.jitterFraction); got != tc.want {
				t.Errorf("delay = %s, want %s", got, tc.want)
			}
		})
	}
}

// The two DLR legs canonicalize the SMSC id differently: the accept leg's
// publisher strips leading zeros before the correlator writes the mapping, and
// the deliver leg publishes the coded id as given. An id that is not already a
// fixed point of that normalization is looked up under a key that was never
// written — DLRMapNotFound, and the partner silently never receives the terminal
// receipt. A sha256 hex id begins with '0' roughly one time in sixteen.
func TestSMSCMessageIDIsCanonicalForCorrelation(t *testing.T) {
	for i := 0; i < 2000; i++ {
		id := SMSCMessageID(fmt.Sprintf("queue-msg-%d", i))
		if id == "" {
			t.Fatalf("empty smsc id for queue-msg-%d", i)
		}
		if strings.HasPrefix(id, "0") {
			t.Fatalf("smsc id %q for queue-msg-%d starts with a zero the accept leg would strip", id, i)
		}
		if id != strings.ToUpper(id) {
			t.Fatalf("smsc id %q is not upper case", id)
		}
		// The exact normalization the accept leg applies must be a no-op.
		if normalized := strings.TrimLeft(strings.ToUpper(id), "0"); normalized != id {
			t.Fatalf("smsc id %q normalizes to %q; the two legs would disagree", id, normalized)
		}
	}
}

// Each segment of a concatenated submit is enqueued as its own queue message, so
// a pass-through assembler would spool, announce, receipt and deliver every
// fragment independently: the application gets half a message as if it were
// whole, and the partner gets several receipts for one submit. Until the stitch
// lands the segment must be refused, and refused terminally so the queue does
// not spin on it.
func TestPassThroughAssemblerRefusesConcatenatedSegments(t *testing.T) {
	// UDHL=5, IEI=0x00 (8-bit concat), IEDL=3, ref=0x2A, total=2, seq=1.
	segment := Message{Raw: append([]byte{0x05, 0x00, 0x03, 0x2A, 0x02, 0x01}, []byte("first half")...)}

	_, complete, err := PassThroughAssembler{}.Add(context.Background(), segment)
	if err == nil {
		t.Fatal("a concatenated segment was accepted as a whole message")
	}
	if complete {
		t.Error("refused segment reported as complete")
	}
	if !errors.Is(err, ErrMultipartUnsupported) {
		t.Errorf("error = %v, want ErrMultipartUnsupported", err)
	}
	if !IsTerminal(err) {
		t.Error("refusal is not terminal; the consumer would requeue it forever")
	}

	whole := Message{Raw: []byte("code 63125")}
	if _, complete, err := (PassThroughAssembler{}).Add(context.Background(), whole); err != nil || !complete {
		t.Errorf("whole message: complete=%v err=%v", complete, err)
	}
}

// The submitting user reaches the spool row and the downstream payload only
// through the envelope: the encoded PDU has no field for it. An empty partner
// means per-partner attribution — the reason for absorbing a single-tenant
// service — silently does not work.
func TestWorkerProcessCarriesThePartnerFromTheEnvelope(t *testing.T) {
	spool := &workerStubSpool{}
	worker := newTestWorker(t, spool, &workerStubVerdicts{verdict: Verdict{Accept: true, Stat: "DELIVRD", Err: "000"}}, &workerStubPublisher{}, true)

	if err := worker.Process(context.Background(), SubmitMetadata{QueueMessageID: "msg-1", Partner: "partner-a"}, []byte("body")); err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(spool.recorded) != 1 {
		t.Fatalf("want one spool row, got %d", len(spool.recorded))
	}
	if got := spool.recorded[0].Partner; got != "partner-a" {
		t.Errorf("partner = %q, want it taken from the envelope", got)
	}
}

func TestSubmitMetadataFromEnvelope(t *testing.T) {
	properties, err := amqpcompat.NewProperties("msg-1", map[string]amqpcompat.Field{
		"user-id": amqpcompat.StringField("partner-a"),
	})
	if err != nil {
		t.Fatalf("properties: %v", err)
	}
	meta := SubmitMetadataFromEnvelope(properties)
	if meta.QueueMessageID != "msg-1" || meta.Partner != "partner-a" {
		t.Errorf("meta = %+v, want message id msg-1 and partner partner-a", meta)
	}

	// A publisher that sets no user-id yields an empty partner rather than a
	// guess: recording the wrong customer against a message is worse than
	// recording none.
	bare, err := amqpcompat.NewProperties("msg-2", nil)
	if err != nil {
		t.Fatalf("properties: %v", err)
	}
	if meta := SubmitMetadataFromEnvelope(bare); meta.Partner != "" {
		t.Errorf("partner = %q, want empty", meta.Partner)
	}
}

type sarStubAssembler struct {
	sawSAR   bool
	segment  Segment
	complete bool
}

func (a *sarStubAssembler) Add(_ context.Context, msg Message) (Message, bool, error) {
	if msg.SAR != nil {
		a.sawSAR = true
		a.segment = *msg.SAR
	}
	return msg, a.complete, nil
}

// SAR declares concatenation in the optional parameters, not in the body, so the
// coordinates only exist inside Process — the assembler cannot recover them from
// Raw the way it recovers a UDH. Without carrying them across, a SAR-split submit
// is treated as one whole message per fragment and the application receives half
// a message as if it were the whole one.
func TestWorkerProcessCarriesSARCoordinatesToTheAssembler(t *testing.T) {
	reference, total, sequence := uint16(0x2A), byte(3), byte(2)
	assembler := &sarStubAssembler{complete: false}
	fixed := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	leg, err := NewSMSCLeg(&workerStubPublisher{}, "partner-a-term", func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("new smsc leg: %v", err)
	}
	worker, err := NewWorker(
		WorkerConfig{CID: "partner-a-term"},
		workerStubSubmits{body: smppwire.SubmitSMBody{
			SourceAddress:      []byte("NETFLIX"),
			DestinationAddress: []byte("380671234567"),
			ShortMessage:       []byte("second part"),
			Optional: smppwire.OptionalParameters{
				SARMessageReference: &reference,
				SARTotalSegments:    &total,
				SARSegmentSequence:  &sequence,
			},
		}},
		workerStubContent{text: "second part", encoding: "ascii"},
		assembler,
		&workerStubVerdicts{verdict: Verdict{Accept: true, Stat: "DELIVRD", Err: "000"}},
		&workerStubSpool{},
		leg,
		func() time.Time { return fixed },
		func() float64 { return 0 },
	)
	if err != nil {
		t.Fatalf("new worker: %v", err)
	}

	if err := worker.Process(context.Background(), SubmitMetadata{QueueMessageID: "msg-1"}, []byte("body")); err != nil {
		t.Fatalf("process: %v", err)
	}
	if !assembler.sawSAR {
		t.Fatal("the assembler never saw the SAR coordinates; a SAR-split message would be delivered as fragments")
	}
	if assembler.segment.Reference != uint32(reference) || assembler.segment.Total != total || assembler.segment.Sequence != sequence {
		t.Errorf("segment = %+v, want ref %d seq %d of %d", assembler.segment, reference, sequence, total)
	}
}

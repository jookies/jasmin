package termination

import (
	"context"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
)

type runnerStubStore struct {
	due          []msgspool.Record
	claims       []msgspool.Record
	delivered    []string
	deadLettered []string
	rescheduled  map[string]time.Time
	receiptsSent []string
	claimLostFor map[string]bool
}

func newRunnerStubStore() *runnerStubStore {
	return &runnerStubStore{rescheduled: map[string]time.Time{}, claimLostFor: map[string]bool{}}
}

func (s *runnerStubStore) Put(context.Context, msgspool.Message, time.Time) (msgspool.Record, error) {
	return msgspool.Record{}, nil
}

func (s *runnerStubStore) DueForDelivery(context.Context, time.Time, int) ([]msgspool.Record, error) {
	return s.due, nil
}

func (s *runnerStubStore) MarkDelivered(_ context.Context, messageID string, _ time.Time) error {
	s.delivered = append(s.delivered, messageID)
	return nil
}

func (s *runnerStubStore) MarkAttemptFailed(_ context.Context, messageID string, nextAttemptAt time.Time) error {
	s.rescheduled[messageID] = nextAttemptAt
	return nil
}

func (s *runnerStubStore) MarkDeadLettered(_ context.Context, messageID string) error {
	s.deadLettered = append(s.deadLettered, messageID)
	return nil
}

func (s *runnerStubStore) ClaimDueReceipts(context.Context, string, time.Time, time.Duration, int) ([]msgspool.Record, error) {
	return s.claims, nil
}

func (s *runnerStubStore) MarkReceiptSent(_ context.Context, messageID, _ string, _ time.Time) error {
	if s.claimLostFor[messageID] {
		return msgspool.ErrClaimLost
	}
	s.receiptsSent = append(s.receiptsSent, messageID)
	return nil
}

type runnerStubSink struct {
	err        error
	attempts   []int
	seenVerdic []Verdict
	lookup     PayloadVerdictLookup
}

func (s *runnerStubSink) Deliver(ctx context.Context, msg Message, attempt int) (DeliveryResult, error) {
	s.attempts = append(s.attempts, attempt)
	if s.lookup != nil {
		if verdict, ok := s.lookup.VerdictFor(ctx, msg); ok {
			s.seenVerdic = append(s.seenVerdic, verdict)
		}
	}
	return DeliveryResult{Attempt: attempt}, s.err
}

func (s *runnerStubSink) Name() string { return "stub" }

func spoolRecord(id string, attempts int) msgspool.Record {
	return msgspool.Record{
		Message: msgspool.Message{
			MessageID:   id,
			ConnectorID: "partner-a-term",
			DestAddr:    "380671234567",
			Text:        "code 63125",
			Parts:       1,
			Verdict:     msgspool.Verdict{Accept: true, Stat: "DELIVRD", Err: "000"},
			ReceivedAt:  time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC),
		},
		DeliveryAttempts: attempts,
	}
}

func TestDeliveryRunnerMarksDelivered(t *testing.T) {
	store := newRunnerStubStore()
	store.due = []msgspool.Record{spoolRecord("msg-1", 0)}
	sink := &runnerStubSink{}
	runner, err := NewDeliveryRunner(store, sink, DeliveryRunnerConfig{}, time.Now)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	sink.lookup = runner

	settled, err := runner.RunOnce(context.Background())
	if err != nil || settled != 1 {
		t.Fatalf("run once: settled=%d err=%v", settled, err)
	}
	if len(store.delivered) != 1 || store.delivered[0] != "msg-1" {
		t.Errorf("delivered = %v, want [msg-1]", store.delivered)
	}
	// The payload must report what the partner was told, which the sink can
	// only learn through the runner's lookup.
	if len(sink.seenVerdic) != 1 || sink.seenVerdic[0].Stat != "DELIVRD" {
		t.Errorf("sink saw verdicts %v, want one DELIVRD", sink.seenVerdic)
	}
}

func TestDeliveryRunnerReschedulesRetryableFailure(t *testing.T) {
	store := newRunnerStubStore()
	store.due = []msgspool.Record{spoolRecord("msg-1", 0)}
	fixed := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	sink := &runnerStubSink{err: &DeliveryError{Attempt: 1, StatusCode: 503, Retryable: true}}
	runner, err := NewDeliveryRunner(store, sink, DeliveryRunnerConfig{Backoff: 30 * time.Second}, func() time.Time { return fixed })
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	next, ok := store.rescheduled["msg-1"]
	if !ok {
		t.Fatal("message was not rescheduled")
	}
	if want := fixed.Add(30 * time.Second); !next.Equal(want) {
		t.Errorf("next attempt at %s, want %s", next, want)
	}
	if len(store.deadLettered) != 0 {
		t.Errorf("dead-lettered %v on a retryable failure", store.deadLettered)
	}
}

func TestDeliveryRunnerDeadLettersInsteadOfDropping(t *testing.T) {
	cases := []struct {
		name     string
		attempts int
		err      error
	}{
		{"terminal rejection", 0, &DeliveryError{Attempt: 1, StatusCode: 400, Retryable: false}},
		{"budget exhausted", 4, &DeliveryError{Attempt: 5, StatusCode: 503, Retryable: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newRunnerStubStore()
			store.due = []msgspool.Record{spoolRecord("msg-1", tc.attempts)}
			runner, err := NewDeliveryRunner(store, &runnerStubSink{err: tc.err}, DeliveryRunnerConfig{MaxAttempts: 5}, time.Now)
			if err != nil {
				t.Fatalf("new runner: %v", err)
			}
			if _, err := runner.RunOnce(context.Background()); err != nil {
				t.Fatalf("run once: %v", err)
			}
			// The legacy thrower purges here and the message is gone. Nothing
			// this connector handles may be lost that way.
			if len(store.deadLettered) != 1 || store.deadLettered[0] != "msg-1" {
				t.Errorf("dead-lettered = %v, want [msg-1]", store.deadLettered)
			}
		})
	}
}

func TestDeliveryRunnerSkipsRedactedContent(t *testing.T) {
	store := newRunnerStubStore()
	record := spoolRecord("msg-1", 0)
	record.ContentRedacted = true
	record.Text = ""
	store.due = []msgspool.Record{record}
	sink := &runnerStubSink{}
	runner, err := NewDeliveryRunner(store, sink, DeliveryRunnerConfig{}, time.Now)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}

	if _, err := runner.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}
	// Delivering a redacted row would hand the application an empty body it
	// would store as the message text.
	if len(sink.attempts) != 0 {
		t.Errorf("attempted delivery of redacted content: %v", sink.attempts)
	}
	if len(store.delivered) != 0 || len(store.deadLettered) != 0 {
		t.Error("redacted row was settled instead of left for an operator")
	}
}

func TestDeliveryBackoffDoublesAndCaps(t *testing.T) {
	base, maxWait := 30*time.Second, 2*time.Minute
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 2 * time.Minute}
	for i, expected := range want {
		if got := deliveryBackoff(base, maxWait, i+1); got != expected {
			t.Errorf("attempt %d backoff = %s, want %s", i+1, got, expected)
		}
	}
}

func TestReceiptRunnerEmitsAndRecords(t *testing.T) {
	store := newRunnerStubStore()
	store.claims = []msgspool.Record{spoolRecord("msg-1", 0)}
	publisher := &workerStubPublisher{}
	runner, err := NewReceiptRunner(store, publisher, ReceiptRunnerConfig{Owner: "gateway-1"}, time.Now)
	if err != nil {
		t.Fatalf("new receipt runner: %v", err)
	}

	sent, err := runner.RunOnce(context.Background())
	if err != nil || sent != 1 {
		t.Fatalf("run once: sent=%d err=%v", sent, err)
	}
	if len(publisher.published) != 1 {
		t.Errorf("published %d receipts, want 1", len(publisher.published))
	}
	if len(store.receiptsSent) != 1 {
		t.Errorf("recorded %v, want one receipt sent", store.receiptsSent)
	}
}

func TestReceiptRunnerToleratesLostClaim(t *testing.T) {
	store := newRunnerStubStore()
	store.claims = []msgspool.Record{spoolRecord("msg-1", 0)}
	store.claimLostFor["msg-1"] = true
	runner, err := NewReceiptRunner(store, &workerStubPublisher{}, ReceiptRunnerConfig{Owner: "gateway-1"}, time.Now)
	if err != nil {
		t.Fatalf("new receipt runner: %v", err)
	}

	// Another process took the lease. That is not an error to propagate:
	// retrying is exactly the duplicate receipt the claim exists to prevent.
	sent, err := runner.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if sent != 0 {
		t.Errorf("sent = %d, want 0 when the claim was lost", sent)
	}
}

func TestReceiptRunnerRequiresOwner(t *testing.T) {
	// Without a distinct owner two processes are indistinguishable in the claim
	// and the partner receives one receipt per process.
	if _, err := NewReceiptRunner(newRunnerStubStore(), &workerStubPublisher{}, ReceiptRunnerConfig{}, time.Now); err == nil {
		t.Fatal("want an error for an empty owner")
	}
}

// A gateway terminating for several partners publishes each receipt under the
// connector that actually handled the message. A single shared leg would label
// every partner's receipts with one partner's connector id — wrong on exactly
// the multi-tenant deployment this connector exists for.
func TestReceiptRunnerLabelsEachReceiptWithItsOwnConnector(t *testing.T) {
	store := newRunnerStubStore()
	first := spoolRecord("msg-1", 0)
	second := spoolRecord("msg-2", 0)
	second.ConnectorID = "partner-b-term"
	store.claims = []msgspool.Record{first, second}

	publisher := &workerStubPublisher{}
	runner, err := NewReceiptRunner(store, publisher, ReceiptRunnerConfig{Owner: "gateway-1"}, time.Now)
	if err != nil {
		t.Fatalf("new receipt runner: %v", err)
	}
	if sent, err := runner.RunOnce(context.Background()); err != nil || sent != 2 {
		t.Fatalf("run once: sent=%d err=%v", sent, err)
	}
	if len(publisher.published) != 2 {
		t.Fatalf("published %d receipts, want 2", len(publisher.published))
	}
	seen := map[string]bool{}
	for _, envelope := range publisher.published {
		if field, ok := envelope.Properties().Headers()["cid"]; ok {
			if value, ok := field.String(); ok {
				seen[value] = true
			}
		}
	}
	for _, want := range []string{"partner-a-term", "partner-b-term"} {
		if !seen[want] {
			t.Errorf("no receipt published under connector %q; got %v", want, seen)
		}
	}
}

type spoolPutRecorder struct {
	runnerStubStore
	puts []msgspool.Message
}

func (s *spoolPutRecorder) Put(_ context.Context, message msgspool.Message, _ time.Time) (msgspool.Record, error) {
	s.puts = append(s.puts, message)
	return msgspool.Record{Message: message}, nil
}

// A connector with no endpoint does not push: its application reads the spool.
// Scheduling a delivery anyway walks the row through the whole retry budget and
// dead-letters it, reporting a delivery failure for a message nobody intended to
// deliver.
func TestSpoolSchedulesDeliveryOnlyWhenTheConnectorPushes(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	msg := Message{MessageID: "msg-1", Connector: "partner-a-term", To: "380671234567", Parts: 1, ReceivedAt: now}
	verdict := Verdict{Accept: true, Stat: "DELIVRD", Err: "000"}

	for _, tc := range []struct {
		name     string
		pushes   bool
		wantDued bool
	}{
		{"push connector", true, true},
		{"pull-only connector", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &spoolPutRecorder{runnerStubStore: *newRunnerStubStore()}
			pushes := tc.pushes
			spool, err := NewSpool(store, func() time.Time { return now }, func(string) bool { return pushes })
			if err != nil {
				t.Fatalf("new spool: %v", err)
			}
			if err := spool.Record(context.Background(), msg, verdict, now.Add(6*time.Second)); err != nil {
				t.Fatalf("record: %v", err)
			}
			if len(store.puts) != 1 {
				t.Fatalf("puts = %d, want 1", len(store.puts))
			}
			if got := store.puts[0].NextAttemptAt != nil; got != tc.wantDued {
				t.Errorf("delivery scheduled = %v, want %v", got, tc.wantDued)
			}
			// The receipt is owed either way: it is the partner's, not the
			// application's.
			if store.puts[0].ReceiptDueAt == nil {
				t.Error("receipt not scheduled")
			}
		})
	}
}

// TestSpoolRecordsAcceptanceForEverySegment is the regression for the
// concatenated-submit CDR gap: a segment that has not completed its group is
// spooled through RecordReceiptOnly, which used to skip the CDR acceptance hook
// entirely. Admission wrote that segment its own cdr_records part, so leaving it
// in ADMITTED meant the final-DLR guard refused its receipt forever.
func TestSpoolRecordsAcceptanceForEverySegment(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	verdict := Verdict{Accept: true, Stat: "DELIVRD", Err: "000"}

	for _, tc := range []struct {
		name   string
		record func(*Spool, Message) error
	}{
		{"assembled message", func(s *Spool, m Message) error {
			return s.Record(context.Background(), m, verdict, now.Add(6*time.Second))
		}},
		{"segment receipt only", func(s *Spool, m Message) error {
			return s.RecordReceiptOnly(context.Background(), m, verdict, now.Add(6*time.Second))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &spoolPutRecorder{runnerStubStore: *newRunnerStubStore()}
			spool, err := NewSpool(store, func() time.Time { return now }, func(string) bool { return true })
			if err != nil {
				t.Fatalf("new spool: %v", err)
			}
			var accepted []string
			spool = spool.WithAcceptance(func(_ context.Context, msg Message) error {
				accepted = append(accepted, msg.MessageID)
				return nil
			})
			msg := Message{
				MessageID: "aggregate/000002", Connector: "partner-a-term",
				To: "380671234567", Parts: 1, ReceivedAt: now,
			}
			if err := tc.record(spool, msg); err != nil {
				t.Fatalf("record: %v", err)
			}
			if len(accepted) != 1 || accepted[0] != "aggregate/000002" {
				t.Fatalf("acceptance recorded for %v, want [aggregate/000002]", accepted)
			}
		})
	}
}

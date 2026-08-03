package outbound

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

type billingLedgerStub struct {
	mu        sync.Mutex
	applied   map[string]bool
	rejected  map[string]bool
	markError error
}

func (ledger *billingLedgerStub) BillingApplied(_ context.Context, key string) (bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	return ledger.applied[key], nil
}
func (ledger *billingLedgerStub) MarkBillingApplied(_ context.Context, key string, _ time.Time) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.markError != nil {
		return ledger.markError
	}
	ledger.applied[key] = true
	return nil
}
func (ledger *billingLedgerStub) MarkBillingRejected(_ context.Context, key string, _ time.Time) error {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	if ledger.markError != nil {
		return ledger.markError
	}
	ledger.applied[key] = false
	if ledger.rejected != nil {
		ledger.rejected[key] = true
	}
	return nil
}

type countingBillingProcessor struct{ calls int }

func (processor *countingBillingProcessor) Process(amqpcompat.Envelope) (core.LateBillingAction, error) {
	processor.calls++
	return core.LateBillingAck, nil
}

type rejectingBillingProcessor struct{ calls int }

func (processor *rejectingBillingProcessor) Process(amqpcompat.Envelope) (core.LateBillingAction, error) {
	processor.calls++
	return core.LateBillingReject, nil
}

func billingEnvelopeForTest(t *testing.T, messageID string) amqpcompat.Envelope {
	t.Helper()
	properties, err := amqpcompat.NewProperties(messageID, map[string]amqpcompat.Field{
		"user-id": amqpcompat.StringField("user-1"), "amount": amqpcompat.StringField("0.5"),
		"event-key": amqpcompat.StringField("part/000001:20-late-billing"),
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := amqpcompat.NewEnvelope("bill_request.submit_sm_resp.user-1", properties, nil)
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func TestDurableLateBillingDeduplicatesAppliedDelivery(t *testing.T) {
	ledger := &billingLedgerStub{applied: make(map[string]bool)}
	next := &countingBillingProcessor{}
	processor, err := newDurableLateBillingProcessor(next, ledger)
	if err != nil {
		t.Fatal(err)
	}
	envelope := billingEnvelopeForTest(t, "bill-1")
	for range 2 {
		action, err := processor.Process(envelope)
		if err != nil || action != core.LateBillingAck {
			t.Fatalf("process=(%s,%v)", action, err)
		}
	}
	if next.calls != 1 || !ledger.applied["part/000001:20-late-billing"] {
		t.Fatalf("calls=%d applied=%v", next.calls, ledger.applied)
	}
}

func TestDurableLateBillingMarkFailureDoesNotRepeatMutation(t *testing.T) {
	markFailure := errors.New("postgres unavailable")
	ledger := &billingLedgerStub{applied: make(map[string]bool), markError: markFailure}
	next := &countingBillingProcessor{}
	processor, err := newDurableLateBillingProcessor(next, ledger)
	if err != nil {
		t.Fatal(err)
	}
	envelope := billingEnvelopeForTest(t, "bill-2")
	if _, err := processor.Process(envelope); !errors.Is(err, markFailure) {
		t.Fatalf("first error=%v", err)
	}
	ledger.markError = nil
	action, err := processor.Process(envelope)
	if err != nil || action != core.LateBillingAck {
		t.Fatalf("redelivery=(%s,%v)", action, err)
	}
	if next.calls != 1 || !ledger.applied["part/000001:20-late-billing"] {
		t.Fatalf("calls=%d applied=%v", next.calls, ledger.applied)
	}
}

func TestDurableLateBillingRecordsRejectedApplicationOutcomeBeforeRejectingDelivery(t *testing.T) {
	ledger := &billingLedgerStub{
		applied: make(map[string]bool), rejected: make(map[string]bool),
	}
	next := &rejectingBillingProcessor{}
	processor, err := newDurableLateBillingProcessor(next, ledger)
	if err != nil {
		t.Fatal(err)
	}
	action, err := processor.Process(billingEnvelopeForTest(t, "bill-rejected"))
	if err != nil || action != core.LateBillingReject {
		t.Fatalf("process=(%s,%v)", action, err)
	}
	if next.calls != 1 || !ledger.rejected["part/000001:20-late-billing"] {
		t.Fatalf("calls=%d rejected=%v", next.calls, ledger.rejected)
	}
}

// TestDurableLateBillingFlushesTheBalanceItJustCharged narrows the crash window
// between mutating a balance and persisting it.
//
// The mutation is in memory and only a periodic flusher writes it, while the
// ledger mark is durable and immediate. A crash after the mark but before the
// next tick loses the charge outright, and the tick can be seconds away. The
// flush now happens as soon as the charge is marked, so the exposure is one
// write rather than one interval.
func TestDurableLateBillingFlushesTheBalanceItJustCharged(t *testing.T) {
	ledger := &billingLedgerStub{applied: make(map[string]bool)}
	next := &countingBillingProcessor{}
	processor, err := newDurableLateBillingProcessor(next, ledger)
	if err != nil {
		t.Fatal(err)
	}
	var flushes int
	processor.SetQuotaFlusher(func(context.Context) error {
		// The mark must already be durable when the balance is written: the
		// reverse order would replay the intent and charge twice.
		if !ledger.applied["part/000001:20-late-billing"] {
			t.Error("balance flushed before the ledger mark committed")
		}
		flushes++
		return nil
	})

	action, err := processor.Process(billingEnvelopeForTest(t, "bill-1"))
	if err != nil || action != core.LateBillingAck {
		t.Fatalf("process=(%s,%v)", action, err)
	}
	if flushes != 1 {
		t.Errorf("balance flushes = %d, want 1", flushes)
	}

	// A duplicate delivery neither re-charges nor re-flushes beyond its own mark.
	if _, err := processor.Process(billingEnvelopeForTest(t, "bill-1")); err != nil {
		t.Fatalf("duplicate delivery: %v", err)
	}
	if next.calls != 1 {
		t.Errorf("balance mutated %d times for one intent", next.calls)
	}
}

// A failing flush must not fail the delivery: the charge is already in the
// ledger, so retrying the mark would be wrong, and the periodic flusher still
// catches the balance.
func TestDurableLateBillingSurvivesAFailedBalanceFlush(t *testing.T) {
	ledger := &billingLedgerStub{applied: make(map[string]bool)}
	processor, err := newDurableLateBillingProcessor(&countingBillingProcessor{}, ledger)
	if err != nil {
		t.Fatal(err)
	}
	processor.SetQuotaFlusher(func(context.Context) error {
		return errors.New("quota store unavailable")
	})
	action, err := processor.Process(billingEnvelopeForTest(t, "bill-1"))
	if err != nil || action != core.LateBillingAck {
		t.Fatalf("a failed balance flush changed the delivery outcome: (%s,%v)", action, err)
	}
}

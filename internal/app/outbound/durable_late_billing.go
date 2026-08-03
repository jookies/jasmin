package outbound

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

type billingApplicationRepository interface {
	BillingApplied(context.Context, string) (bool, error)
	MarkBillingApplied(context.Context, string, time.Time) error
	MarkBillingRejected(context.Context, string, time.Time) error
}

// durableLateBillingProcessor gives the current in-memory balance projection a
// stable-key application boundary. If marking PostgreSQL fails after mutation,
// the process-local fence prevents a duplicate mutation on redelivery. After a
// process crash the in-memory mutation is gone, so an unapplied intent is safely
// replayed against the reconstructed projection.
type durableLateBillingProcessor struct {
	next       core.LateBillingDecisionProcessor
	repository billingApplicationRepository

	mu      sync.Mutex
	mutated map[string]struct{}

	// persistQuotas durably writes the balances the charge just mutated.
	//
	// The in-memory mutation and the durable ledger mark are two separate
	// writes, and the balance is otherwise only persisted by a periodic
	// flusher. A crash between them therefore lands somewhere inconsistent: if
	// the balance was flushed but the mark was not, the intent replays and the
	// customer is charged twice; if the mark landed but the balance was not yet
	// flushed, the charge is lost on restart. Flushing here shrinks the second
	// window from the flush interval to the length of one write.
	//
	// It does NOT close the window: only a single transaction spanning
	// billing_quotas and the intent ledger can, and that has to serialise
	// against the periodic flusher to avoid writing a stale balance over a
	// newer one. Until then a crash inside these few milliseconds can still
	// lose one late charge, which is why LATE_BILLING_APPLIED_UNFLUSHED exists
	// to report it. Nil disables the flush, which is the pre-existing
	// behaviour.
	persistQuotas func(context.Context) error
}

// SetQuotaFlusher wires the durable balance flush used after a late charge is
// marked applied. It is set after construction because the persister is built
// later than this processor.
func (processor *durableLateBillingProcessor) SetQuotaFlusher(flush func(context.Context) error) {
	processor.mu.Lock()
	defer processor.mu.Unlock()
	processor.persistQuotas = flush
}

// flushAppliedBalance persists the mutated balances, best effort. A failure is
// reported and left to the periodic flusher: the charge is already recorded in
// the ledger, so retrying the mark would be wrong.
func (processor *durableLateBillingProcessor) flushAppliedBalance(eventKey string) {
	if processor.persistQuotas == nil {
		return
	}
	ctx, cancel := billingLedgerContext()
	defer cancel()
	if err := processor.persistQuotas(ctx); err != nil {
		slog.Error("late charge applied but its balance flush failed; a crash before the next flush would lose it",
			"event_key", eventKey, "error", err.Error())
	}
}

const billingLedgerTimeout = 5 * time.Second

func billingLedgerContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), billingLedgerTimeout)
}

func newDurableLateBillingProcessor(next core.LateBillingDecisionProcessor, repository billingApplicationRepository) (*durableLateBillingProcessor, error) {
	if next == nil || repository == nil {
		return nil, errors.New("durable late billing requires processor and PostgreSQL ledger")
	}
	return &durableLateBillingProcessor{next: next, repository: repository, mutated: make(map[string]struct{})}, nil
}

func (processor *durableLateBillingProcessor) Process(envelope amqpcompat.Envelope) (core.LateBillingAction, error) {
	eventField, ok := envelope.Properties().Headers()["event-key"]
	if !ok {
		return core.LateBillingReject, nil
	}
	eventKey, ok := eventField.String()
	if !ok || eventKey == "" {
		return core.LateBillingReject, nil
	}
	processor.mu.Lock()
	defer processor.mu.Unlock()

	if _, locallyMutated := processor.mutated[eventKey]; locallyMutated {
		ctx, cancel := billingLedgerContext()
		err := processor.repository.MarkBillingApplied(ctx, eventKey, time.Now())
		cancel()
		if err != nil {
			return core.LateBillingNone, err
		}
		return core.LateBillingAck, nil
	}
	ctx, cancel := billingLedgerContext()
	applied, err := processor.repository.BillingApplied(ctx, eventKey)
	cancel()
	if errors.Is(err, sql.ErrNoRows) {
		return core.LateBillingReject, nil
	}
	if err != nil {
		return core.LateBillingNone, err
	}
	if applied {
		return core.LateBillingAck, nil
	}
	action, err := processor.next.Process(envelope)
	if err != nil {
		return action, err
	}
	if action == core.LateBillingReject {
		ctx, cancel = billingLedgerContext()
		err = processor.repository.MarkBillingRejected(ctx, eventKey, time.Now())
		cancel()
		if err != nil {
			return core.LateBillingNone, err
		}
		return action, nil
	}
	if action != core.LateBillingAck {
		return action, nil
	}
	// Keep the guard before the database mark: if the mark transiently fails,
	// redelivery in this process must retry only the mark, not mutate balance
	// again. On restart both this guard and the in-memory balance reset, so an
	// unapplied durable intent is replayed exactly once against fresh state.
	processor.mutated[eventKey] = struct{}{}
	ctx, cancel = billingLedgerContext()
	err = processor.repository.MarkBillingApplied(ctx, eventKey, time.Now())
	cancel()
	if err != nil {
		return core.LateBillingNone, err
	}
	processor.flushAppliedBalance(eventKey)
	return core.LateBillingAck, nil
}

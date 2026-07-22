package outbound

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

type billingApplicationRepository interface {
	BillingApplied(context.Context, string) (bool, error)
	MarkBillingApplied(context.Context, string, time.Time) error
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
	if err != nil || action != core.LateBillingAck {
		return action, err
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
	return core.LateBillingAck, nil
}

package termination

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/msgspool"
	"github.com/pumpitspace/synevyr/internal/core/stats"
)

// Defaults for the delivery runner. Five attempts over roughly ten minutes is
// long enough to ride out a deploy of the downstream application and short
// enough that a genuinely broken endpoint reaches the dead-letter queue while an
// operator is still watching.
const (
	DefaultMaxDeliveryAttempts = 5
	DefaultDeliveryBackoff     = 30 * time.Second
	DefaultDeliveryBackoffCap  = 15 * time.Minute
	DefaultDeliveryBatch       = 100
)

// Defaults for the receipt runner.
const (
	DefaultReceiptLease = 30 * time.Second
	DefaultReceiptBatch = 100
)

// DeliveryRunnerConfig configures one pass of the delivery runner.
type DeliveryRunnerConfig struct {
	MaxAttempts int
	Backoff     time.Duration
	BackoffCap  time.Duration
	Batch       int
}

func (c DeliveryRunnerConfig) withDefaults() DeliveryRunnerConfig {
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = DefaultMaxDeliveryAttempts
	}
	if c.Backoff <= 0 {
		c.Backoff = DefaultDeliveryBackoff
	}
	if c.BackoffCap <= 0 {
		c.BackoffCap = DefaultDeliveryBackoffCap
	}
	if c.Batch <= 0 {
		c.Batch = DefaultDeliveryBatch
	}
	return c
}

// DeliveryRunner pushes spooled messages to the downstream application.
//
// It owns retry scheduling and the dead-letter decision; the sink owns exactly
// one attempt. Nothing is ever dropped: a message that exhausts its attempts is
// dead-lettered, not purged. The legacy throwers purge after three retries and
// the message is gone, which is the behaviour this connector exists to end.
type DeliveryRunner struct {
	store SpoolStore
	sink  DeliverySink
	cfg   DeliveryRunnerConfig
	now   func() time.Time

	// inFlight carries each message's stored verdict to the sink, which needs it
	// for the payload's verdict field but is handed only a Message. Keyed by
	// message id and cleared after the attempt, so concurrent runners never see
	// each other's rows.
	inFlight sync.Map
}

// NewDeliveryRunner wires the runner.
func NewDeliveryRunner(store SpoolStore, sink DeliverySink, cfg DeliveryRunnerConfig, now func() time.Time) (*DeliveryRunner, error) {
	if store == nil || sink == nil {
		return nil, errors.New("termination: delivery runner requires a spool store and a sink")
	}
	if now == nil {
		now = time.Now
	}
	return &DeliveryRunner{store: store, sink: sink, cfg: cfg.withDefaults(), now: now}, nil
}

// VerdictFor satisfies the sink's optional verdict lookup, so the delivered
// payload reports what the partner was told rather than an empty field.
func (r *DeliveryRunner) VerdictFor(_ context.Context, msg Message) (Verdict, bool) {
	stored, ok := r.inFlight.Load(msg.MessageID)
	if !ok {
		return Verdict{}, false
	}
	verdict, ok := stored.(Verdict)
	return verdict, ok
}

// RunOnce processes one batch of due deliveries and reports how many rows it
// settled. A single failing message never blocks the batch: its error is
// recorded against its own row and the loop continues.
func (r *DeliveryRunner) RunOnce(ctx context.Context) (int, error) {
	due, err := r.store.DueForDelivery(ctx, r.now(), r.cfg.Batch)
	if err != nil {
		return 0, fmt.Errorf("termination: due for delivery: %w", err)
	}
	settled := 0
	for _, record := range due {
		if ctx.Err() != nil {
			return settled, ctx.Err()
		}
		if record.ContentRedacted {
			// Delivering a redacted row would hand the application an empty
			// message body it would store as the message. Skipping is the only
			// safe answer; the row stays due and an operator can see it.
			continue
		}
		if err := r.deliverOne(ctx, record); err != nil {
			continue
		}
		settled++
	}
	return settled, nil
}

func (r *DeliveryRunner) deliverOne(ctx context.Context, record msgspool.Record) error {
	msg := messageFromRecord(record)
	attempt := record.DeliveryAttempts + 1

	r.inFlight.Store(msg.MessageID, verdictFromRecord(record))
	_, deliverErr := r.sink.Deliver(ctx, msg, attempt)
	r.inFlight.Delete(msg.MessageID)

	// A pull-only connector's rows reach this runner because the spool is
	// shared. They are not failures and must not be counted -- including the
	// attempt counter, which is why this check precedes it. An attempt that
	// ticks for a connector nobody configured a push for would dead-letter
	// every one of its rows once the budget ran out, and make the DLQ depth
	// metric meaningless. Leave the row pending: pull is what collects it.
	if errors.Is(deliverErr, ErrDeliveryNotConfigured) {
		return nil
	}
	recordDelivery(record.ConnectorID, stats.TerminationDeliveryAttempt)

	if deliverErr == nil {
		recordDelivery(record.ConnectorID, stats.TerminationDeliverySuccess)
		if err := r.store.MarkDelivered(ctx, msg.MessageID, r.now()); err != nil {
			return fmt.Errorf("termination: mark delivered: %w", err)
		}
		return nil
	}

	// A terminal rejection is not worth four more attempts, and an exhausted
	// budget has nowhere left to go: both dead-letter. The row keeps its
	// content, so the console can replay it once the endpoint is fixed.
	if !DeliveryRetryable(deliverErr) || attempt >= r.cfg.MaxAttempts {
		// Both the failure and the dead-lettering are counted. The failure
		// belongs in the failure rate with every other failed attempt; the
		// dead-letter is the separate, much rarer event that a message stopped
		// being retried, and conflating them would make a permanently broken
		// endpoint indistinguishable from a flapping one.
		recordDelivery(record.ConnectorID, stats.TerminationDeliveryFailure)
		recordDelivery(record.ConnectorID, stats.TerminationDeliveryDeadLetter)
		if err := r.store.MarkDeadLettered(ctx, msg.MessageID); err != nil {
			return fmt.Errorf("termination: dead-letter: %w", err)
		}
		return nil
	}

	recordDelivery(record.ConnectorID, stats.TerminationDeliveryFailure)
	next := r.now().Add(deliveryBackoff(r.cfg.Backoff, r.cfg.BackoffCap, attempt))
	if err := r.store.MarkAttemptFailed(ctx, msg.MessageID, next); err != nil {
		return fmt.Errorf("termination: reschedule: %w", err)
	}
	return nil
}

// deliveryBackoff doubles per attempt up to the cap. Attempt 1 waits one base
// interval, so a downstream restart is ridden out without a thundering retry the
// instant it comes back.
func deliveryBackoff(base, maxWait time.Duration, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	wait := base
	for i := 1; i < attempt; i++ {
		wait *= 2
		if wait >= maxWait {
			return maxWait
		}
	}
	if wait > maxWait {
		return maxWait
	}
	return wait
}

// ReceiptRunnerConfig configures one pass of the receipt runner.
type ReceiptRunnerConfig struct {
	// Owner identifies this gateway process in the claim. Two processes must
	// never emit the same receipt, so this must be unique per process.
	Owner string
	Lease time.Duration
	Batch int
}

func (c ReceiptRunnerConfig) withDefaults() ReceiptRunnerConfig {
	if c.Lease <= 0 {
		c.Lease = DefaultReceiptLease
	}
	if c.Batch <= 0 {
		c.Batch = DefaultReceiptBatch
	}
	return c
}

// ReceiptRunner emits the receipts that have come due.
//
// This is what makes the 5–7 s delay survivable: the receipt is owed by a
// committed database row, not by a timer in a process. A gateway that dies
// inside the window still emits it — from this process or another one — because
// the claim is exclusive and an expired lease is re-claimable.
type ReceiptRunner struct {
	store     SpoolStore
	publisher EnvelopePublisher
	cfg       ReceiptRunnerConfig
	now       func() time.Time

	// legs caches one SMSC leg per connector id. The leg carries the cid into
	// every published receipt, so a single shared leg would label every
	// connector's receipts with one connector's name on a gateway terminating
	// for more than one partner — which is precisely the multi-tenant case this
	// connector exists for.
	legsMu sync.Mutex
	legs   map[string]*SMSCLeg
}

// NewReceiptRunner wires the runner. Owner is required: an empty owner would
// make two processes indistinguishable in the claim, and the partner would
// receive one receipt per process.
//
// It takes a publisher rather than a prepared leg because the leg is per
// connector, and which connector a receipt belongs to is a property of the row
// being claimed, not of the process claiming it.
func NewReceiptRunner(store SpoolStore, publisher EnvelopePublisher, cfg ReceiptRunnerConfig, now func() time.Time) (*ReceiptRunner, error) {
	if store == nil || publisher == nil {
		return nil, errors.New("termination: receipt runner requires a spool store and a publisher")
	}
	if cfg.Owner == "" {
		return nil, errors.New("termination: receipt runner requires an owner")
	}
	if now == nil {
		now = time.Now
	}
	return &ReceiptRunner{
		store:     store,
		publisher: publisher,
		cfg:       cfg.withDefaults(),
		now:       now,
		legs:      map[string]*SMSCLeg{},
	}, nil
}

// legFor returns the SMSC leg for one connector, building it once.
func (r *ReceiptRunner) legFor(cid string) (*SMSCLeg, error) {
	r.legsMu.Lock()
	defer r.legsMu.Unlock()
	if leg, ok := r.legs[cid]; ok {
		return leg, nil
	}
	leg, err := NewSMSCLeg(r.publisher, cid, r.now)
	if err != nil {
		return nil, err
	}
	r.legs[cid] = leg
	return leg, nil
}

// RunOnce claims the receipts that are due and emits them, reporting how many
// were sent.
func (r *ReceiptRunner) RunOnce(ctx context.Context) (int, error) {
	claimed, err := r.store.ClaimDueReceipts(ctx, r.cfg.Owner, r.now(), r.cfg.Lease, r.cfg.Batch)
	if err != nil {
		return 0, fmt.Errorf("termination: claim receipts: %w", err)
	}
	sent := 0
	for _, record := range claimed {
		if ctx.Err() != nil {
			return sent, ctx.Err()
		}
		leg, err := r.legFor(record.ConnectorID)
		if err != nil {
			// A row with no usable connector id cannot be attributed to anything;
			// leaving the lease to expire keeps it visible instead of emitting a
			// receipt labelled with a guess.
			continue
		}
		receipt := leg.Receipt(
			SMSCMessageID(record.MessageID),
			verdictFromRecord(record),
			record.ReceivedAt,
			r.now(),
		)
		if err := leg.Deliver(ctx, SMSCMessageID(record.MessageID), receipt); err != nil {
			// The lease expires and another pass re-claims it. Publishing
			// failed, so nothing was promised to the partner.
			continue
		}
		if err := r.store.MarkReceiptSent(ctx, record.MessageID, r.cfg.Owner, r.now()); err != nil {
			if errors.Is(err, msgspool.ErrClaimLost) {
				// Another process took the lease and will have emitted its own
				// receipt. Nothing to repair here, and retrying would be the
				// duplicate this claim exists to prevent.
				continue
			}
			return sent, fmt.Errorf("termination: mark receipt sent: %w", err)
		}
		sent++
	}
	return sent, nil
}

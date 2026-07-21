package submittransaction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

type Service struct {
	repository Repository
	now        func() time.Time
}

func NewService(repository Repository, now func() time.Time) (*Service, error) {
	if repository == nil {
		return nil, fmt.Errorf("%w: nil repository", ErrInvalidInput)
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, now: now}, nil
}

// NewProductionService is the only production constructor. It rejects SQLite
// and test repositories so durable submit/outbox wiring cannot silently fall
// back to process-local storage.
func NewProductionService(repository Repository, now func() time.Time) (*Service, error) {
	if _, err := RequireProductionRepository(repository); err != nil {
		return nil, err
	}
	return NewService(repository, now)
}

// AdmitSubmit persists every logical part and its publication event atomically.
// Repeating the same stable keys is idempotent. The caller intentionally keeps
// the legacy no-refund rule: authorization/debit happens before this boundary
// and is never reversed when later publication fails.
func (service *Service) AdmitSubmit(ctx context.Context, envelopes []amqpcompat.Envelope) error {
	if len(envelopes) == 0 {
		return fmt.Errorf("%w: no submit envelopes", ErrInvalidInput)
	}
	now := service.now().UTC()
	parts := make([]LogicalPart, 0, len(envelopes))
	events := make([]OutboxEvent, 0, len(envelopes))
	for index, envelope := range envelopes {
		if envelope.Route().Kind() != amqpcompat.RouteSubmitSM {
			return fmt.Errorf("%w: envelope %d is not submit", ErrInvalidInput, index)
		}
		messageID := envelope.Properties().MessageID()
		key := fmt.Sprintf("%s/%06d", messageID, index+1)
		payload, err := SnapshotEnvelope(envelope)
		if err != nil {
			return err
		}
		headers := envelope.Properties().Headers()
		userID, _ := fieldString(headers, "user-id")
		billID, _ := fieldString(headers, "bill-id")
		parts = append(parts, LogicalPart{Key: key, MessageID: messageID, PartNumber: index + 1, ConnectorID: envelope.Route().Target(), UserID: userID, BillID: billID, State: PartPending, CreatedAt: now})
		events = append(events, OutboxEvent{Key: key + ":00-submit", PartKey: key, Kind: EventSubmitRequest, Exchange: "messaging", RoutingKey: envelope.RoutingKey(), Payload: payload, CreatedAt: now, AvailableAt: now})
	}
	return service.repository.Admit(ctx, parts, events)
}

func (service *Service) BeginAttempt(ctx context.Context, partKey string) (SendAttempt, bool, error) {
	if partKey == "" {
		return SendAttempt{}, false, ErrInvalidInput
	}
	return service.repository.BeginAttempt(ctx, partKey, service.now().UTC())
}

func (service *Service) MarkSent(ctx context.Context, attemptID int64) error {
	if attemptID <= 0 {
		return ErrInvalidInput
	}
	return service.repository.MarkAttemptSent(ctx, attemptID, service.now().UTC())
}

func (service *Service) Recover(ctx context.Context) (int64, error) {
	return service.repository.RecoverUnresolved(ctx, service.now().UTC())
}

// CommitResponse atomically stores the terminal result/correlation and all
// response, billing and DLR intents. A duplicate response returns committed=false.
func (service *Service) CommitResponse(ctx context.Context, result Result, events ...OutboxEvent) (bool, error) {
	if result.PartKey == "" || result.AttemptID <= 0 || result.Kind == "" {
		return false, ErrInvalidInput
	}
	now := service.now().UTC()
	result.CommittedAt = now
	seen := make(map[string]struct{}, len(events))
	for index := range events {
		if events[index].Key == "" || events[index].PartKey != result.PartKey || events[index].RoutingKey == "" || events[index].Exchange == "" {
			return false, ErrInvalidInput
		}
		if _, duplicate := seen[events[index].Key]; duplicate {
			return false, fmt.Errorf("%w: duplicate event key %q", ErrInvalidInput, events[index].Key)
		}
		seen[events[index].Key] = struct{}{}
		if events[index].CreatedAt.IsZero() {
			events[index].CreatedAt = now
		}
		if events[index].AvailableAt.IsZero() {
			events[index].AvailableAt = now
		}
	}
	return service.repository.CommitResult(ctx, ResultCommit{Result: result, Events: events})
}

func NewEnvelopeEvent(key, partKey string, kind EventKind, exchange string, envelope amqpcompat.Envelope, availableAt time.Time) (OutboxEvent, error) {
	payload, err := SnapshotEnvelope(envelope)
	if err != nil {
		return OutboxEvent{}, err
	}
	return OutboxEvent{Key: key, PartKey: partKey, Kind: kind, Exchange: exchange, RoutingKey: envelope.RoutingKey(), Payload: payload, AvailableAt: availableAt}, nil
}

func fieldString(headers map[string]amqpcompat.Field, name string) (string, bool) {
	field, ok := headers[name]
	if !ok {
		return "", false
	}
	return field.String()
}

var ErrOutboxStopped = errors.New("submit outbox dispatcher stopped")

type ConfirmingPublisher interface {
	Publish(context.Context, string, string, amqpcompat.Envelope) error
}

type Dispatcher struct {
	repository Repository
	publisher  ConfirmingPublisher
	owner      string
	batchSize  int
	lease      time.Duration
	now        func() time.Time
}

func NewDispatcher(repository Repository, publisher ConfirmingPublisher, owner string, batchSize int, lease time.Duration, now func() time.Time) (*Dispatcher, error) {
	if repository == nil || publisher == nil || owner == "" || batchSize < 1 || lease <= 0 {
		return nil, ErrInvalidInput
	}
	if now == nil {
		now = time.Now
	}
	return &Dispatcher{repository: repository, publisher: publisher, owner: owner, batchSize: batchSize, lease: lease, now: now}, nil
}

// DispatchOnce provides explicit at-least-once delivery. Publication uses a
// broker-confirming publisher; a crash after confirm but before dispatched mark
// causes replay with the same event/message key and must be deduplicated by the
// consumer. Ordering is repository order (created_at, key), one event at a time.
func (dispatcher *Dispatcher) DispatchOnce(ctx context.Context) (int, error) {
	now := dispatcher.now().UTC()
	events, err := dispatcher.repository.ClaimOutbox(ctx, dispatcher.owner, dispatcher.batchSize, now, dispatcher.lease)
	if err != nil {
		return 0, err
	}
	published := 0
	for index, event := range events {
		envelope, restoreErr := RestoreEnvelope(event.Payload)
		if restoreErr != nil {
			dispatcher.releaseClaimed(ctx, events[index:], now, restoreErr)
			return published, restoreErr
		}
		if publishErr := dispatcher.publisher.Publish(ctx, event.Exchange, event.RoutingKey, envelope); publishErr != nil {
			dispatcher.releaseClaimed(ctx, events[index:], now, publishErr)
			return published, publishErr
		}
		if markErr := dispatcher.repository.MarkOutboxDispatched(ctx, event.Key, dispatcher.owner, dispatcher.now().UTC()); markErr != nil {
			// Confirmed but unmarked is deliberately replayable (at-least-once).
			dispatcher.releaseClaimed(ctx, events[index:], now, markErr)
			return published, markErr
		}
		published++
	}
	return published, nil
}

func (dispatcher *Dispatcher) releaseClaimed(ctx context.Context, events []OutboxEvent, now time.Time, cause error) {
	for _, event := range events {
		_ = dispatcher.repository.ReleaseOutbox(ctx, event.Key, dispatcher.owner, now, cause)
	}
}

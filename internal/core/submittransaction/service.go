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

// MarkUnknownAfterSend closes one unresolved attempt after a timeout,
// connection loss, or ambiguous write failure. A subsequent broker redelivery
// receives a new monotonically numbered attempt instead of reusing the same
// durable identity for another external socket write.
func (service *Service) MarkUnknownAfterSend(ctx context.Context, attemptID int64) error {
	if attemptID <= 0 {
		return ErrInvalidInput
	}
	return service.repository.MarkAttemptUnknownAfterSend(ctx, attemptID, service.now().UTC())
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
// consumer. Events are attempted in repository order, but one unavailable or
// malformed destination must not head-of-line block unrelated claimed events.
func (dispatcher *Dispatcher) DispatchOnce(ctx context.Context) (int, error) {
	now := dispatcher.now().UTC()
	events, err := dispatcher.repository.ClaimOutbox(ctx, dispatcher.owner, dispatcher.batchSize, now, dispatcher.lease)
	if err != nil {
		return 0, err
	}
	published := 0
	var dispatchErrors []error
	failedParts := make(map[string]error)
	for _, event := range events {
		if predecessorErr := failedParts[event.PartKey]; predecessorErr != nil {
			blockedErr := fmt.Errorf("predecessor for part %q was not dispatched: %w", event.PartKey, predecessorErr)
			if releaseErr := dispatcher.releaseClaimed(event, dispatcher.now().UTC().Add(time.Second), blockedErr); releaseErr != nil {
				dispatchErrors = append(dispatchErrors, releaseErr)
			}
			continue
		}
		envelope, restoreErr := RestoreEnvelope(event.Payload)
		if restoreErr != nil {
			eventErr := fmt.Errorf("restore outbox event %q: %w", event.Key, restoreErr)
			failedParts[event.PartKey] = eventErr
			if releaseErr := dispatcher.releaseClaimed(event, dispatcher.now().UTC().Add(time.Second), eventErr); releaseErr != nil {
				dispatchErrors = append(dispatchErrors, releaseErr)
			}
			dispatchErrors = append(dispatchErrors, eventErr)
			continue
		}
		if publishErr := dispatcher.publisher.Publish(ctx, event.Exchange, event.RoutingKey, envelope); publishErr != nil {
			eventErr := fmt.Errorf("publish outbox event %q: %w", event.Key, publishErr)
			failedParts[event.PartKey] = eventErr
			if releaseErr := dispatcher.releaseClaimed(event, dispatcher.now().UTC().Add(time.Second), eventErr); releaseErr != nil {
				dispatchErrors = append(dispatchErrors, releaseErr)
			}
			dispatchErrors = append(dispatchErrors, eventErr)
			continue
		}
		if markErr := dispatcher.repository.MarkOutboxDispatched(ctx, event.Key, dispatcher.owner, dispatcher.now().UTC()); markErr != nil {
			// Confirmed but unmarked is deliberately replayable (at-least-once).
			eventErr := fmt.Errorf("mark outbox event %q dispatched: %w", event.Key, markErr)
			failedParts[event.PartKey] = eventErr
			if releaseErr := dispatcher.releaseClaimed(event, dispatcher.now().UTC().Add(time.Second), eventErr); releaseErr != nil {
				dispatchErrors = append(dispatchErrors, releaseErr)
			}
			dispatchErrors = append(dispatchErrors, eventErr)
			continue
		}
		published++
	}
	return published, errors.Join(dispatchErrors...)
}

func (dispatcher *Dispatcher) releaseClaimed(event OutboxEvent, availableAt time.Time, cause error) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dispatcher.repository.ReleaseOutbox(cleanupCtx, event.Key, dispatcher.owner, availableAt, cause); err != nil {
		return fmt.Errorf("release outbox event %q: %w", event.Key, err)
	}
	return nil
}

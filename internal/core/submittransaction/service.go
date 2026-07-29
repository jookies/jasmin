package submittransaction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/cdr"
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

func (service *Service) AggregateStatus(ctx context.Context, messageID string) (AggregateStatus, error) {
	if messageID == "" {
		return AggregateStatus{}, fmt.Errorf("%w: empty aggregate message ID", ErrInvalidInput)
	}
	return service.repository.AggregateStatus(ctx, messageID)
}

// SubmissionExists is the narrow idempotency lookup consumed by trusted
// durable ingress. A recovered task whose stable message id is already in the
// submit ledger is complete even if the process died before acknowledging the
// REST batch row.
func (service *Service) SubmissionExists(ctx context.Context, messageID string) (bool, error) {
	_, err := service.AggregateStatus(ctx, messageID)
	if errors.Is(err, ErrPartNotFound) {
		return false, nil
	}
	return err == nil, err
}

// AdmitSubmit persists every logical part and its publication event atomically.
// Repeating the same stable keys is idempotent. The caller intentionally keeps
// the legacy no-refund rule: authorization/debit happens before this boundary
// and is never reversed when later publication fails.
func (service *Service) AdmitSubmit(ctx context.Context, envelopes []amqpcompat.Envelope) error {
	return service.admitSubmit(ctx, envelopes, cdr.SubmitMetadata{})
}

// AdmitSubmitWithCDR persists the submit and its commercial context in one
// repository transaction without adding non-legacy headers to the AMQP
// envelope.
func (service *Service) AdmitSubmitWithCDR(
	ctx context.Context,
	envelopes []amqpcompat.Envelope,
	commercial cdr.SubmitMetadata,
) error {
	return service.admitSubmit(ctx, envelopes, commercial)
}

func (service *Service) admitSubmit(
	ctx context.Context,
	envelopes []amqpcompat.Envelope,
	commercial cdr.SubmitMetadata,
) error {
	if len(envelopes) == 0 {
		return fmt.Errorf("%w: no submit envelopes", ErrInvalidInput)
	}
	now := service.now().UTC()
	parts := make([]LogicalPart, 0, len(envelopes))
	events := make([]OutboxEvent, 0, len(envelopes))
	seenParts := make(map[int]struct{}, len(envelopes))
	var aggregateID string
	for index, envelope := range envelopes {
		if envelope.Route().Kind() != amqpcompat.RouteSubmitSM {
			return fmt.Errorf("%w: envelope %d is not submit", ErrInvalidInput, index)
		}
		messageID := envelope.Properties().MessageID()
		headers := envelope.Properties().Headers()
		partNumber, partCount, envelopeAggregate, metadataErr := submitPartMetadata(headers, messageID, len(envelopes))
		if metadataErr != nil {
			return fmt.Errorf("%w: envelope %d: %v", ErrInvalidInput, index, metadataErr)
		}
		if partCount != len(envelopes) {
			return fmt.Errorf("%w: envelope %d part-count=%d want=%d", ErrInvalidInput, index, partCount, len(envelopes))
		}
		if aggregateID == "" {
			aggregateID = envelopeAggregate
		} else if aggregateID != envelopeAggregate {
			return fmt.Errorf("%w: mixed aggregate message IDs", ErrInvalidInput)
		}
		if _, duplicate := seenParts[partNumber]; duplicate {
			return fmt.Errorf("%w: duplicate part-number %d", ErrInvalidInput, partNumber)
		}
		seenParts[partNumber] = struct{}{}
		key := fmt.Sprintf("%s/%06d", envelopeAggregate, partNumber)
		payload, err := SnapshotEnvelope(envelope)
		if err != nil {
			return err
		}
		userID, _ := fieldString(headers, "user-id")
		billID, _ := fieldString(headers, "bill-id")
		cdrAdmission, err := cdrAdmissionFromEnvelope(
			key, envelopeAggregate, partNumber, partCount, envelope.Route().Target(),
			userID, billID, headers, commercial, now,
		)
		if err != nil {
			return fmt.Errorf("%w: envelope %d cdr: %v", ErrInvalidInput, index, err)
		}
		parts = append(parts, LogicalPart{
			Key: key, MessageID: envelopeAggregate, PartNumber: partNumber,
			PartCount:   partCount,
			ConnectorID: envelope.Route().Target(), UserID: userID, BillID: billID,
			State: PartPending, CreatedAt: now, CDR: cdrAdmission,
		})
		events = append(events, OutboxEvent{Key: key + ":00-submit", PartKey: key, Kind: EventSubmitRequest, Exchange: "messaging", RoutingKey: envelope.RoutingKey(), Payload: payload, CreatedAt: now, AvailableAt: now})
	}
	return service.repository.Admit(ctx, parts, events)
}

func cdrAdmissionFromEnvelope(
	id, messageID string,
	partNumber, partCount int,
	connectorID, userID, billID string,
	headers map[string]amqpcompat.Field,
	commercial cdr.SubmitMetadata,
	now time.Time,
) (cdr.Admission, error) {
	routeID := commercial.RouteID
	if routeID == "" {
		// Routes do not yet expose an immutable admin identifier. The selected
		// connector is the durable route identity for this first CDR contract.
		routeID = "connector:" + connectorID
	}
	ingress := commercial.Ingress
	if ingress == "" {
		ingress, _ = fieldString(headers, "source_connector")
	}
	currency := commercial.Currency
	if currency == "" {
		currency = cdr.DefaultCurrency
	}
	admission := cdr.Admission{
		ID: id, MessageID: messageID, PartNumber: partNumber, PartCount: partCount,
		UserID: userID, GroupID: commercial.GroupID, RouteID: routeID,
		ConnectorID: connectorID, Ingress: ingress, BillID: billID,
		Rate: commercial.Rate, Currency: currency,
		EarlyAmount: commercial.EarlyAmount, LateAmount: commercial.LateAmount,
		BillingMode: cdr.ModeForAmounts(commercial.EarlyAmount, commercial.LateAmount),
		OccurredAt:  now,
	}
	if err := cdr.ValidateAdmission(admission); err != nil {
		return cdr.Admission{}, err
	}
	return admission, nil
}

func submitPartMetadata(headers map[string]amqpcompat.Field, messageID string, envelopeCount int) (int, int, string, error) {
	aggregate, aggregateOK := fieldString(headers, "aggregate-message-id")
	partNumberValue, partNumberOK := fieldInteger(headers, "part-number")
	partCountValue, partCountOK := fieldInteger(headers, "part-count")
	if !aggregateOK && !partNumberOK && !partCountOK && envelopeCount == 1 {
		return 1, 1, messageID, nil
	}
	if !aggregateOK || aggregate == "" || !partNumberOK || !partCountOK || partNumberValue < 1 || partCountValue < 1 || partNumberValue > partCountValue {
		return 0, 0, "", errors.New("missing or invalid aggregate/part metadata")
	}
	expectedMessageID := aggregate
	if partCountValue > 1 {
		expectedMessageID = fmt.Sprintf("%s/%06d", aggregate, partNumberValue)
	}
	if messageID != expectedMessageID {
		return 0, 0, "", errors.New("message-id does not match aggregate/part identity")
	}
	return int(partNumberValue), int(partCountValue), aggregate, nil
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
	switch result.Kind {
	case ResultSuccess, ResultRetry, ResultFailure, ResultTimeout:
	default:
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

func fieldInteger(headers map[string]amqpcompat.Field, name string) (int64, bool) {
	field, ok := headers[name]
	if !ok {
		return 0, false
	}
	return field.Integer()
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
			// The reply-to response (submit.sm.resp.<user>) is best-effort: the
			// legacy listener publishes it non-mandatory, and nothing in this
			// runtime consumes it (HTTP/SMPPs responses resolve through the
			// durable store, not this queue). An unroutable return means no
			// consumer exists, so drop it — marking dispatched — exactly as
			// legacy would. Retrying forever would wedge the DLR and billing
			// events behind it via the in-order predecessor gate. Every other
			// routing key must exist, so their NO_ROUTE stays a hard failure.
			if event.Kind == EventSubmitResponse && errors.Is(publishErr, amqpcompat.ErrPublishReturned) {
				if markErr := dispatcher.repository.MarkOutboxDispatched(ctx, event.Key, dispatcher.owner, dispatcher.now().UTC()); markErr != nil {
					eventErr := fmt.Errorf("mark best-effort outbox event %q dropped: %w", event.Key, markErr)
					failedParts[event.PartKey] = eventErr
					if releaseErr := dispatcher.releaseClaimed(event, dispatcher.now().UTC().Add(time.Second), eventErr); releaseErr != nil {
						dispatchErrors = append(dispatchErrors, releaseErr)
					}
					dispatchErrors = append(dispatchErrors, eventErr)
					continue
				}
				published++
				continue
			}
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

package smppc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pumpitspace/synevyr/internal/core/submittransaction"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

var ErrInvalidSubmitResponsePublication = errors.New("submit response publication is invalid")

const SubmitResponseExchange = "messaging"

type SubmitResponseAction string

const (
	SubmitResponseAck     SubmitResponseAction = "ack"
	SubmitResponseRequeue SubmitResponseAction = "requeue"
)

// NewSubmitResponsePublication projects the legacy listener's optional
// submit_sm_resp publication. A disabled publication intentionally ignores the
// remaining inputs because the legacy listener does not construct or validate
// response content in that branch. Response bodies stay opaque.
func NewSubmitResponsePublication(
	enabled bool,
	action SubmitResponseAction,
	replyTo string,
	messageID string,
	createdAt string,
	body []byte,
) (*amqpcompat.Envelope, error) {
	if !enabled {
		return nil, nil
	}
	if action != SubmitResponseAck && action != SubmitResponseRequeue {
		return nil, fmt.Errorf("%w: invalid terminal action %q", ErrInvalidSubmitResponsePublication, action)
	}
	if createdAt == "" {
		return nil, fmt.Errorf("%w: empty created-at header", ErrInvalidSubmitResponsePublication)
	}
	properties, err := amqpcompat.NewProperties(messageID, map[string]amqpcompat.Field{
		"created_at": amqpcompat.StringField(createdAt),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSubmitResponsePublication, err)
	}
	envelope, err := amqpcompat.NewEnvelope(replyTo, properties, body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidSubmitResponsePublication, err)
	}
	if envelope.Route().Kind() != amqpcompat.RouteSubmitSMResponse {
		return nil, fmt.Errorf("%w: routing key is not submit response", ErrInvalidSubmitResponsePublication)
	}
	return &envelope, nil
}

// DurableResponseInput contains already-correlated response data. The socket
// owner only calls this narrow lifecycle; persistence and publication remain
// outside the SMPP session.
type DurableResponseInput struct {
	PartKey        string
	AttemptID      int64
	Status         string
	SMSCMessageID  string
	ReplyEnabled   bool
	ReplyTo        string
	MessageID      string
	CreatedAt      string
	Body           []byte
	UserID         string
	BillID         string
	LateBillAmount string
	RetryAttempt   int
	RetryEnvelope  *amqpcompat.Envelope
}

type DurableResponseLifecycle struct {
	transactions *submittransaction.Service
	retry        *ErrorRetryPolicy
	now          func() time.Time
}

func NewDurableResponseLifecycle(transactions *submittransaction.Service, retry *ErrorRetryPolicy, now func() time.Time) (*DurableResponseLifecycle, error) {
	if transactions == nil || retry == nil {
		return nil, fmt.Errorf("%w: durable response dependencies", ErrInvalidSubmitResponsePublication)
	}
	if now == nil {
		now = time.Now
	}
	return &DurableResponseLifecycle{transactions: transactions, retry: retry, now: now}, nil
}

// Commit atomically creates the durable result, response publication and late
// billing intent. Duplicate responses return committed=false without creating
// duplicate local side effects.
func (lifecycle *DurableResponseLifecycle) Commit(ctx context.Context, input DurableResponseInput) (bool, error) {
	if input.PartKey == "" || input.AttemptID <= 0 || input.Status == "" || input.RetryAttempt < 1 {
		return false, ErrInvalidSubmitResponsePublication
	}
	decision, err := lifecycle.retry.Decide(input.Status, input.RetryAttempt)
	if err != nil {
		return false, err
	}
	kind := submittransaction.ResultFailure
	action := SubmitResponseAck
	now := lifecycle.now().UTC()
	availableAt := now
	if input.Status == "ESME_ROK" {
		kind = submittransaction.ResultSuccess
	} else if decision.Action == ErrorRetryRequeue {
		kind = submittransaction.ResultRetry
		action = SubmitResponseRequeue
		availableAt = now.Add(decision.RequeueDelay)
	}
	events := make([]submittransaction.OutboxEvent, 0, 4)
	publication, err := NewSubmitResponsePublication(input.ReplyEnabled, action, input.ReplyTo, input.MessageID, input.CreatedAt, input.Body)
	if err != nil {
		return false, err
	}
	if publication != nil {
		eventKey := fmt.Sprintf("%s:10-response-attempt-%06d", input.PartKey, input.RetryAttempt)
		event, err := submittransaction.NewEnvelopeEvent(eventKey, input.PartKey, submittransaction.EventSubmitResponse, SubmitResponseExchange, *publication, now)
		if err != nil {
			return false, err
		}
		events = append(events, event)
	}
	// Every final submit_sm_resp publishes a DLR to DLRLookup, regardless of the
	// ack/requeue decision (the legacy listener publishes it outside the
	// will_be_retried branch). This feeds level-1 callbacks and the smpp_msgid
	// -> msgid mapping used by later receipt correlation.
	dlrMessageID, err := dlrSubmitRespMessageID(input)
	if err != nil {
		return false, err
	}
	dlrPublication, err := newDLRSubmitRespPublication(dlrMessageID, input.Status, input.SMSCMessageID)
	if err != nil {
		return false, err
	}
	dlrEventKey := fmt.Sprintf("%s:15-dlr-submit-resp-%06d", input.PartKey, input.RetryAttempt)
	dlrEvent, err := submittransaction.NewEnvelopeEvent(dlrEventKey, input.PartKey, submittransaction.EventDLRState, SubmitResponseExchange, dlrPublication, now)
	if err != nil {
		return false, err
	}
	events = append(events, dlrEvent)
	if kind == submittransaction.ResultRetry {
		if input.RetryEnvelope == nil {
			return false, fmt.Errorf("%w: retry requires original submit envelope", ErrInvalidSubmitResponsePublication)
		}
		event, err := submittransaction.NewEnvelopeEvent(fmt.Sprintf("%s:retry-%06d", input.PartKey, input.RetryAttempt), input.PartKey, submittransaction.EventSubmitRequest, "messaging", *input.RetryEnvelope, availableAt)
		if err != nil {
			return false, err
		}
		events = append(events, event)
	}
	if kind == submittransaction.ResultSuccess && input.LateBillAmount != "" {
		billingEnvelope, err := newLateBillingIntent(input)
		if err != nil {
			return false, err
		}
		event, err := submittransaction.NewEnvelopeEvent(input.PartKey+":20-late-billing", input.PartKey, submittransaction.EventLateBilling, "billing", billingEnvelope, availableAt)
		if err != nil {
			return false, err
		}
		events = append(events, event)
	}
	return lifecycle.transactions.CommitResponse(ctx, submittransaction.Result{
		PartKey: input.PartKey, AttemptID: input.AttemptID, Kind: kind,
		SMPPStatus: input.Status, SMSCMessageID: input.SMSCMessageID,
	}, events...)
}

func dlrSubmitRespMessageID(input DurableResponseInput) (string, error) {
	if input.RetryEnvelope == nil {
		return input.MessageID, nil
	}
	properties := input.RetryEnvelope.Properties()
	headers := properties.Headers()
	aggregate, aggregateOK := headerString(headers, "aggregate-message-id")
	partNumber, partNumberOK := headerInteger(headers, "part-number")
	partCount, partCountOK := headerInteger(headers, "part-count")
	if !aggregateOK && !partNumberOK && !partCountOK {
		return input.MessageID, nil
	}
	if !aggregateOK || aggregate == "" || !partNumberOK || !partCountOK ||
		partNumber < 1 || partCount < 1 || partNumber > partCount {
		return "", fmt.Errorf("%w: invalid multipart DLR identity", ErrInvalidSubmitResponsePublication)
	}
	expectedMessageID := aggregate
	if partCount > 1 {
		expectedMessageID = fmt.Sprintf("%s/%06d", aggregate, partNumber)
	}
	if input.MessageID != expectedMessageID || properties.MessageID() != expectedMessageID {
		return "", fmt.Errorf("%w: multipart DLR message-id mismatch", ErrInvalidSubmitResponsePublication)
	}
	// The native front door publishes each segment with a durable part id,
	// while legacy keeps one queue id for the chain and requests a receipt only
	// on its last PDU. Reproject that last response to the aggregate id so the
	// one pending DLR request and the user-visible receipt keep legacy identity.
	if partCount > 1 && partNumber == partCount {
		return aggregate, nil
	}
	return input.MessageID, nil
}

func newLateBillingIntent(input DurableResponseInput) (amqpcompat.Envelope, error) {
	if input.UserID == "" || input.BillID == "" || input.PartKey == "" {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: missing late billing identity", ErrInvalidSubmitResponsePublication)
	}
	eventKey := input.PartKey + ":20-late-billing"
	properties, err := amqpcompat.NewProperties(eventKey, map[string]amqpcompat.Field{
		"user-id":   amqpcompat.StringField(input.UserID),
		"amount":    amqpcompat.StringField(input.LateBillAmount),
		"event-key": amqpcompat.StringField(eventKey),
	})
	if err != nil {
		return amqpcompat.Envelope{}, err
	}
	return amqpcompat.NewEnvelope("bill_request.submit_sm_resp."+input.UserID, properties, []byte(input.BillID))
}

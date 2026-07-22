package submittransaction

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

var (
	ErrInvalidInput               = errors.New("invalid submit transaction input")
	ErrPartNotFound               = errors.New("submit logical part not found")
	ErrAttemptNotFound            = errors.New("submit attempt not found")
	ErrAttemptFenced              = errors.New("active submit attempt fenced before redelivery")
	ErrProductionRequiresPostgres = errors.New("production submit transactions require PostgreSQL")
)

type PartState string

const (
	PartPending          PartState = "PENDING"
	PartAttempting       PartState = "ATTEMPTING"
	PartUnknownAfterSend PartState = "UNKNOWN_AFTER_SEND"
	PartResultCommitted  PartState = "RESULT_COMMITTED"
)

type AttemptState string

const (
	AttemptIntent           AttemptState = "INTENT"
	AttemptSent             AttemptState = "SENT"
	AttemptUnknownAfterSend AttemptState = "UNKNOWN_AFTER_SEND"
	AttemptResultCommitted  AttemptState = "RESULT_COMMITTED"
)

type ResultKind string

const (
	ResultSuccess ResultKind = "SUCCESS"
	ResultRetry   ResultKind = "RETRY"
	ResultFailure ResultKind = "FAILURE"
	ResultTimeout ResultKind = "TIMEOUT"
)

type EventKind string

const (
	EventSubmitRequest  EventKind = "SUBMIT_REQUEST"
	EventSubmitResponse EventKind = "SUBMIT_RESPONSE"
	EventLateBilling    EventKind = "LATE_BILLING"
	EventDLRState       EventKind = "DLR_STATE"
)

type LogicalPart struct {
	Key         string
	MessageID   string
	PartNumber  int
	ConnectorID string
	UserID      string
	BillID      string
	State       PartState
	CreatedAt   time.Time
}

type AggregateStatus struct {
	MessageID        string
	TotalParts       int
	Pending          int
	Attempting       int
	UnknownAfterSend int
	ResultCommitted  int
	State            PartState
}

func (status AggregateStatus) DerivedState() PartState {
	if status.UnknownAfterSend > 0 {
		return PartUnknownAfterSend
	}
	if status.TotalParts > 0 && status.ResultCommitted == status.TotalParts {
		return PartResultCommitted
	}
	if status.Attempting > 0 {
		return PartAttempting
	}
	return PartPending
}

type SendAttempt struct {
	ID         int64
	PartKey    string
	Number     int
	State      AttemptState
	CreatedAt  time.Time
	SentAt     *time.Time
	ResolvedAt *time.Time
}

type Result struct {
	PartKey       string
	AttemptID     int64
	Kind          ResultKind
	SMPPStatus    string
	SMSCMessageID string
	CommittedAt   time.Time
}

type OutboxEvent struct {
	Key          string
	PartKey      string
	Kind         EventKind
	Exchange     string
	RoutingKey   string
	Payload      []byte
	CreatedAt    time.Time
	AvailableAt  time.Time
	Attempts     int
	DispatchedAt *time.Time
}

type ResultCommit struct {
	Result Result
	Events []OutboxEvent
}

type EnvelopeSnapshot struct {
	MessageID  string                   `json:"message_id"`
	RoutingKey string                   `json:"routing_key"`
	ReplyTo    string                   `json:"reply_to,omitempty"`
	Priority   *uint8                   `json:"priority,omitempty"`
	Headers    map[string]SnapshotField `json:"headers,omitempty"`
	Body       []byte                   `json:"body"`
}

type SnapshotField struct {
	Kind    string `json:"kind"`
	String  string `json:"string,omitempty"`
	Integer int64  `json:"integer,omitempty"`
	Bytes   []byte `json:"bytes,omitempty"`
}

func SnapshotEnvelope(envelope amqpcompat.Envelope) ([]byte, error) {
	properties := envelope.Properties()
	snapshot := EnvelopeSnapshot{MessageID: properties.MessageID(), RoutingKey: envelope.RoutingKey(), Body: envelope.Body(), Headers: make(map[string]SnapshotField)}
	if replyTo, ok := properties.ReplyTo(); ok {
		snapshot.ReplyTo = replyTo
	}
	if priority, ok := properties.Priority(); ok {
		snapshot.Priority = &priority
	}
	for name, field := range properties.Headers() {
		switch field.Kind() {
		case amqpcompat.FieldString:
			value, _ := field.String()
			snapshot.Headers[name] = SnapshotField{Kind: "string", String: value}
		case amqpcompat.FieldInteger:
			value, _ := field.Integer()
			snapshot.Headers[name] = SnapshotField{Kind: "integer", Integer: value}
		case amqpcompat.FieldBytes:
			value, _ := field.Bytes()
			snapshot.Headers[name] = SnapshotField{Kind: "bytes", Bytes: value}
		default:
			return nil, fmt.Errorf("%w: unsupported header %q", ErrInvalidInput, name)
		}
	}
	return json.Marshal(snapshot)
}

func RestoreEnvelope(payload []byte) (amqpcompat.Envelope, error) {
	var snapshot EnvelopeSnapshot
	if err := json.Unmarshal(payload, &snapshot); err != nil {
		return amqpcompat.Envelope{}, fmt.Errorf("decode outbox envelope: %w", err)
	}
	headers := make(map[string]amqpcompat.Field, len(snapshot.Headers))
	for name, field := range snapshot.Headers {
		switch field.Kind {
		case "string":
			headers[name] = amqpcompat.StringField(field.String)
		case "integer":
			headers[name] = amqpcompat.IntegerField(field.Integer)
		case "bytes":
			headers[name] = amqpcompat.BytesField(field.Bytes)
		default:
			return amqpcompat.Envelope{}, fmt.Errorf("%w: unsupported snapshot field %q", ErrInvalidInput, field.Kind)
		}
	}
	options := make([]amqpcompat.PropertyOption, 0, 2)
	if snapshot.ReplyTo != "" {
		options = append(options, amqpcompat.WithReplyTo(snapshot.ReplyTo))
	}
	if snapshot.Priority != nil {
		options = append(options, amqpcompat.WithPriority(*snapshot.Priority))
	}
	properties, err := amqpcompat.NewProperties(snapshot.MessageID, headers, options...)
	if err != nil {
		return amqpcompat.Envelope{}, err
	}
	return amqpcompat.NewEnvelope(snapshot.RoutingKey, properties, snapshot.Body)
}

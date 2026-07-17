package smppc

import (
	"errors"
	"fmt"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
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

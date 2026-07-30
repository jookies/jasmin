package dlr

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

// ErrInvalidForward identifies a Forward the legacy content constructors would
// reject (InvalidParameterError in DLRContentForHttpapi / DLRContentForSmpps).
var ErrInvalidForward = fmt.Errorf("dlr: invalid forward")

// EncodeThrowerForward builds the legacy DLR thrower envelope for a Forward —
// the exact DLRContentForHttpapi / DLRContentForSmpps shape the legacy
// DLRThrower consumes: routing key dlr_thrower.http or dlr_thrower.smpps on
// the messaging exchange, body equal to the queue message id, and the full
// constructor header set (headers the legacy code defaults to ” are always
// present). The HTTP err header is always a string; the SMPPS err header is
// an integer where the legacy constructor default (99) or an
// integer-carrying leg applies, and a string on the deliver leg — Forward
// records the distinction in ErrIsInteger.
func EncodeThrowerForward(forward Forward) (amqpcompat.Envelope, error) {
	if !validMessageStatus(forward.Status) {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: invalid message_status: %s", ErrInvalidForward, forward.Status)
	}
	if forward.QueueMsgID == "" {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: empty queue msgid", ErrInvalidForward)
	}
	switch forward.Target {
	case ForwardHTTP:
		return encodeHTTPForward(forward)
	case ForwardSMPPS:
		return encodeSMPPSForward(forward)
	default:
		return amqpcompat.Envelope{}, fmt.Errorf("%w: unknown target %d", ErrInvalidForward, forward.Target)
	}
}

func encodeHTTPForward(forward Forward) (amqpcompat.Envelope, error) {
	if forward.Level < 1 || forward.Level > 3 {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: invalid dlr_level: %d", ErrInvalidForward, forward.Level)
	}
	if forward.Method != "POST" && forward.Method != "GET" {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: invalid method: %s", ErrInvalidForward, forward.Method)
	}
	connector := forward.Connector
	if connector == "" {
		connector = "unknown" // the legacy constructor default
	}
	headers := map[string]amqpcompat.Field{
		"try-count":      amqpcompat.IntegerField(0),
		"url":            amqpcompat.StringField(forward.URL),
		"method":         amqpcompat.StringField(forward.Method),
		"message_status": amqpcompat.StringField(forward.Status),
		"level":          amqpcompat.IntegerField(int64(forward.Level)),
		"id_smsc":        amqpcompat.StringField(forward.IDSMSC),
		"sub":            amqpcompat.StringField(forward.Sub),
		"dlvrd":          amqpcompat.StringField(forward.Dlvrd),
		"subdate":        amqpcompat.StringField(forward.SubmitDate),
		"donedate":       amqpcompat.StringField(forward.DoneDate),
		"err":            amqpcompat.StringField(forward.Err),
		"connector":      amqpcompat.StringField(connector),
		"text":           amqpcompat.StringField(forward.Text),
	}
	return throwerEnvelope("dlr_thrower.http", forward.QueueMsgID, headers)
}

func encodeSMPPSForward(forward Forward) (amqpcompat.Envelope, error) {
	err, encodeErr := smppsErrField(forward)
	if encodeErr != nil {
		return amqpcompat.Envelope{}, encodeErr
	}
	headers := map[string]amqpcompat.Field{
		"try-count":        amqpcompat.IntegerField(0),
		"message_status":   amqpcompat.StringField(forward.Status),
		"err":              err,
		"system_id":        amqpcompat.StringField(forward.SystemID),
		"source_addr":      amqpcompat.StringField(forward.SourceAddr),
		"destination_addr": amqpcompat.StringField(forward.DestinationAddr),
		"sub_date":         amqpcompat.StringField(forward.SubDate),
		"source_addr_ton":  amqpcompat.StringField(forward.SourceAddrTON),
		"source_addr_npi":  amqpcompat.StringField(forward.SourceAddrNPI),
		"dest_addr_ton":    amqpcompat.StringField(forward.DestAddrTON),
		"dest_addr_npi":    amqpcompat.StringField(forward.DestAddrNPI),
	}
	return throwerEnvelope("dlr_thrower.smpps", forward.QueueMsgID, headers)
}

// smppsErrField applies the legacy err semantics: the submit_sm_resp leg never
// passes err, so the constructor default 99 (an integer) applies; a decoded
// integer stays an integer; the deliver leg forwards the receipt's err string.
func smppsErrField(forward Forward) (amqpcompat.Field, error) {
	if forward.Err == "" && !forward.ErrIsInteger {
		return amqpcompat.IntegerField(99), nil
	}
	if forward.ErrIsInteger {
		value, err := strconv.ParseInt(forward.Err, 10, 64)
		if err != nil {
			return amqpcompat.Field{}, fmt.Errorf("%w: integer err %q", ErrInvalidForward, forward.Err)
		}
		return amqpcompat.IntegerField(value), nil
	}
	return amqpcompat.StringField(forward.Err), nil
}

func throwerEnvelope(routingKey, messageID string, headers map[string]amqpcompat.Field) (amqpcompat.Envelope, error) {
	properties, err := amqpcompat.NewProperties(messageID, headers)
	if err != nil {
		return amqpcompat.Envelope{}, err
	}
	return amqpcompat.NewEnvelope(routingKey, properties, []byte(messageID))
}

// AMQPPublisher is the transport dependency for forwarding receipts — the
// same publish contract the submit path uses.
type AMQPPublisher interface {
	Publish(ctx context.Context, exchange, routingKey string, message amqpcompat.Envelope) error
}

// ForwardPublisher publishes correlated receipts as legacy thrower envelopes
// on the messaging exchange. It implements Publisher for the Correlator.
type ForwardPublisher struct {
	publisher AMQPPublisher
}

func NewForwardPublisher(publisher AMQPPublisher) (*ForwardPublisher, error) {
	if publisher == nil {
		return nil, fmt.Errorf("%w: nil AMQP publisher", ErrInvalidForward)
	}
	return &ForwardPublisher{publisher: publisher}, nil
}

func (p *ForwardPublisher) PublishDLR(ctx context.Context, forward Forward) error {
	envelope, err := EncodeThrowerForward(forward)
	if err != nil {
		return err
	}
	return p.publisher.Publish(ctx, "messaging", envelope.RoutingKey(), envelope)
}

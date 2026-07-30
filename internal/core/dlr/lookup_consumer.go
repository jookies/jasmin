package dlr

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

// ErrInvalidLookupDelivery identifies a dlr.* message the legacy dispatcher or
// callbacks would reject before doing any correlation work: unknown routing
// key, or a header access the legacy code would KeyError on.
var ErrInvalidLookupDelivery = errors.New("dlr: invalid lookup delivery")

// LookupCorrelator is the correlation dependency of the consumer (implemented
// by *Correlator).
type LookupCorrelator interface {
	OnSubmitResp(ctx context.Context, ev SubmitRespEvent) error
	OnDeliverReceipt(ctx context.Context, ev DeliverReceiptEvent) error
}

// LookupConsumerConfig mirrors the [dlr] retry knobs.
type LookupConsumerConfig struct {
	MaxRetries int           // dlr_lookup_max_retries (default 2): total attempts, not extra retries
	RetryDelay time.Duration // dlr_lookup_retry_delay (default 10s) before a delayed requeue fires
}

func (c *LookupConsumerConfig) applyDefaults() {
	if c.MaxRetries == 0 {
		c.MaxRetries = 2
	}
	if c.RetryDelay == 0 {
		c.RetryDelay = 10 * time.Second
	}
}

// LookupConsumer ports the DLRLookup consumer shell: it decodes dlr.submit_sm_resp
// and dlr.deliver_sm deliveries, dispatches them to the correlator, and settles
// them with the legacy per-leg policy:
//
//   - success → ack (retrial tracker and any pending requeue timer cleared);
//   - ErrDLRMapInvalid, ErrForwardPublish, decode failures, unknown routing
//     keys → reject without requeue (the legacy DLRMapError / generic-exception
//     clauses);
//   - redis/transport errors → delayed requeue (RetryDelay) while the per-msgid
//     attempt count stays below MaxRetries, then reject;
//   - ErrDLRMapNotFound → reject on the submit_sm_resp leg, but the delayed
//     requeue policy on the deliver_sm leg (the terminal receipt can race the
//     mapping write).
//
// The attempt counter increments on every delivery of a message id, exactly like
// the legacy dispatcher, so MaxRetries bounds total attempts. The delayed requeue
// keeps the delivery unacked while its timer runs; a duplicate delivery of the
// same message id during that window resets the timer and leaves the newer
// delivery unsettled — a faithful port of the legacy timer-reset behavior.
type LookupConsumer struct {
	correlator LookupCorrelator
	cfg        LookupConsumerConfig

	mu       sync.Mutex
	retrials map[string]int
	timers   map[string]*time.Timer
}

func NewLookupConsumer(correlator LookupCorrelator, cfg LookupConsumerConfig) (*LookupConsumer, error) {
	if correlator == nil {
		return nil, errors.New("dlr: nil correlator")
	}
	cfg.applyDefaults()
	return &LookupConsumer{
		correlator: correlator,
		cfg:        cfg,
		retrials:   make(map[string]int),
		timers:     make(map[string]*time.Timer),
	}, nil
}

// Handle takes settlement ownership of the delivery. The returned error reports
// the outcome for logging; every path settles (or intentionally parks) the
// delivery.
func (c *LookupConsumer) Handle(ctx context.Context, delivery *amqpcompat.Delivery) error {
	if delivery == nil {
		return errors.New("dlr: nil delivery")
	}
	envelope := delivery.Envelope()
	messageID := envelope.Properties().MessageID()
	c.mu.Lock()
	c.retrials[messageID]++
	c.mu.Unlock()

	var err error
	var mapNotFoundRetries bool
	switch envelope.RoutingKey() {
	case "dlr.submit_sm_resp":
		var event SubmitRespEvent
		event, err = decodeSubmitRespDelivery(envelope)
		if err == nil {
			err = c.correlator.OnSubmitResp(ctx, event)
		}
	case "dlr.deliver_sm":
		mapNotFoundRetries = true
		var event DeliverReceiptEvent
		event, err = decodeDeliverReceiptDelivery(envelope)
		if err == nil {
			err = c.correlator.OnDeliverReceipt(ctx, event)
		}
	default:
		err = fmt.Errorf("%w: unknown routing key %q", ErrInvalidLookupDelivery, envelope.RoutingKey())
	}

	switch {
	case err == nil:
		c.settleFinal(messageID)
		_ = delivery.Ack()
		return nil
	case errors.Is(err, ErrInvalidLookupDelivery),
		errors.Is(err, ErrDLRMapInvalid),
		errors.Is(err, ErrForwardPublish):
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return err
	case errors.Is(err, ErrDLRMapNotFound) && !mapNotFoundRetries:
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return err
	default:
		// Redis/transport errors — and the deliver leg's map race — retry.
		c.requeueOrReject(messageID, delivery)
		return err
	}
}

// settleFinal clears the retrial tracker and any pending requeue timer, the
// legacy ackMessage / rejectMessage(requeue=0) bookkeeping.
func (c *LookupConsumer) settleFinal(messageID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.retrials, messageID)
	if timer, ok := c.timers[messageID]; ok {
		timer.Stop()
		delete(c.timers, messageID)
	}
}

// requeueOrReject applies the capped delayed-requeue policy.
func (c *LookupConsumer) requeueOrReject(messageID string, delivery *amqpcompat.Delivery) {
	c.mu.Lock()
	if c.retrials[messageID] >= c.cfg.MaxRetries {
		delete(c.retrials, messageID)
		if timer, ok := c.timers[messageID]; ok {
			timer.Stop()
			delete(c.timers, messageID)
		}
		c.mu.Unlock()
		_ = delivery.Reject(false)
		return
	}
	if timer, ok := c.timers[messageID]; ok {
		// Legacy timer-reset semantics: the pending requeue keeps its original
		// delivery; this newer duplicate stays unsettled.
		timer.Reset(c.cfg.RetryDelay)
		c.mu.Unlock()
		return
	}
	c.timers[messageID] = time.AfterFunc(c.cfg.RetryDelay, func() {
		c.mu.Lock()
		delete(c.timers, messageID)
		c.mu.Unlock()
		_ = delivery.Reject(true)
	})
	c.mu.Unlock()
}

// decodeSubmitRespDelivery maps the legacy DLR(pdu_type=submit_sm_resp) envelope:
// body is the command status name, message-id the queue msgid, and smpp_msgid is
// required exactly when the status is ESME_ROK (the legacy callback KeyErrors
// otherwise reading it in the success branch).
func decodeSubmitRespDelivery(envelope amqpcompat.Envelope) (SubmitRespEvent, error) {
	event := SubmitRespEvent{
		QueueMsgID: envelope.Properties().MessageID(),
		Status:     string(envelope.Body()),
	}
	if event.QueueMsgID == "" {
		return SubmitRespEvent{}, fmt.Errorf("%w: empty message-id", ErrInvalidLookupDelivery)
	}
	headers := envelope.Properties().Headers()
	smppMsgID, err := lookupHeaderText(headers, "smpp_msgid")
	if err == nil {
		event.SMPPMsgID = smppMsgID
	} else if event.Status == statusOK {
		return SubmitRespEvent{}, fmt.Errorf("%w: ESME_ROK without smpp_msgid", ErrInvalidLookupDelivery)
	}
	return event, nil
}

// decodeDeliverReceiptDelivery maps the legacy DLR(pdu_type=deliver_sm|data_sm)
// envelope: body is the receipt state, message-id the pre-coded lookup id, and
// the dlr_* headers carry the parsed receipt fields (all required — the legacy
// callback KeyErrors on any missing one).
func decodeDeliverReceiptDelivery(envelope amqpcompat.Envelope) (DeliverReceiptEvent, error) {
	messageID := envelope.Properties().MessageID()
	if messageID == "" {
		return DeliverReceiptEvent{}, fmt.Errorf("%w: empty message-id", ErrInvalidLookupDelivery)
	}
	headers := envelope.Properties().Headers()
	event := DeliverReceiptEvent{
		CodedID: messageID,
		Status:  string(envelope.Body()),
	}
	for name, destination := range map[string]*string{
		"cid":       &event.ConnectorID,
		"dlr_id":    &event.RawDLRID,
		"dlr_ddate": &event.DoneDate,
		"dlr_sdate": &event.SubmitDate,
		"dlr_sub":   &event.Sub,
		"dlr_err":   &event.Err,
		"dlr_text":  &event.Text,
		"dlr_dlvrd": &event.Dlvrd,
	} {
		value, err := lookupHeaderText(headers, name)
		if err != nil {
			return DeliverReceiptEvent{}, err
		}
		*destination = value
	}
	return event, nil
}

// lookupHeaderText reads a header that the legacy receipt dict may carry as a
// string or an integer (DLR forwards dlr_details values verbatim).
func lookupHeaderText(headers map[string]amqpcompat.Field, name string) (string, error) {
	field, ok := headers[name]
	if !ok {
		return "", fmt.Errorf("%w: missing header %q", ErrInvalidLookupDelivery, name)
	}
	if value, isString := field.String(); isString {
		return value, nil
	}
	if value, isInteger := field.Integer(); isInteger {
		return strconv.FormatInt(value, 10), nil
	}
	return "", fmt.Errorf("%w: header %q has wrong kind", ErrInvalidLookupDelivery, name)
}

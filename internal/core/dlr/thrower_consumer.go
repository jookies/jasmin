package dlr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

// SMPPSReceiptSink delivers a receipt PDU to a bound SMPPS session — the
// legacy smpp_dlr_callback's SMPPServerFactory access. No composition provides
// one yet; a nil sink makes SMPPS forwards fail into the retry-then-purge
// path, exactly like a legacy deployment with no SMPPS access attached.
type SMPPSReceiptSink interface {
	DeliverReceipt(ctx context.Context, params SMPPSReceiptParams) error
}

// ThrowerConsumerConfig mirrors the [dlr-thrower] retry knobs.
type ThrowerConsumerConfig struct {
	MaxRetries int           // max_retries (default 3); attempts retry while count <= MaxRetries
	RetryDelay time.Duration // retry_delay (default 30s) before the delayed requeue fires
}

func (c *ThrowerConsumerConfig) applyDefaults() {
	if c.MaxRetries == 0 {
		c.MaxRetries = 3
	}
	if c.RetryDelay == 0 {
		c.RetryDelay = 30 * time.Second
	}
}

// ThrowerConsumer ports the DLRThrower consumer: dlr_thrower.http forwards
// become HTTP callbacks (Q-005 semantics: 200 + exact ACK/Jasmin body), and
// dlr_thrower.smpps forwards become receipt deliveries when a sink exists.
//
// The retry lifecycle matches the legacy Thrower base: the attempt counter
// increments on every delivery of a message id, a failed throw requeues after
// RetryDelay while the count stays <= MaxRetries, then the message is purged
// (rejected without requeue). A redelivery first clears the message's pending
// requeue timer, like the legacy callback's clearRequeueTimer.
//
// The legacy code declares a no-retry list for "404" errors, but its
// comparison can never match (str(HttpApiError) is never the bare code), so a
// 404 retries like any other failure — KNOWN_QUIRKS Q-019; ported as the
// observable behavior, not the dead intent.
type ThrowerConsumer struct {
	http  HTTPDoer
	smpps SMPPSReceiptSink
	cfg   ThrowerConsumerConfig

	mu       sync.Mutex
	retrials map[string]int
	timers   map[string]*time.Timer
}

func NewThrowerConsumer(httpClient HTTPDoer, smpps SMPPSReceiptSink, cfg ThrowerConsumerConfig) (*ThrowerConsumer, error) {
	if httpClient == nil {
		return nil, errors.New("dlr: nil HTTP client")
	}
	cfg.applyDefaults()
	return &ThrowerConsumer{
		http:     httpClient,
		smpps:    smpps,
		cfg:      cfg,
		retrials: make(map[string]int),
		timers:   make(map[string]*time.Timer),
	}, nil
}

// Handle takes settlement ownership of the delivery: ack on a successful
// throw, delayed requeue or purge per the retry policy, and reject without
// requeue for envelopes outside the frozen thrower contract (the legacy code
// would crash on those before its try block and leak the delivery — rejecting
// is the conscious correction).
func (c *ThrowerConsumer) Handle(ctx context.Context, delivery *amqpcompat.Delivery) error {
	if delivery == nil {
		return errors.New("dlr: nil delivery")
	}
	envelope := delivery.Envelope()
	messageID := envelope.Properties().MessageID()

	c.mu.Lock()
	c.retrials[messageID]++
	if timer, ok := c.timers[messageID]; ok {
		timer.Stop()
		delete(c.timers, messageID)
	}
	c.mu.Unlock()

	forward, err := DecodeThrowerForward(envelope)
	if err != nil {
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return err
	}

	throwErr := c.throw(ctx, forward)
	if throwErr == nil {
		c.settleFinal(messageID)
		_ = delivery.Ack()
		return nil
	}
	c.retryOrPurge(messageID, delivery)
	return throwErr
}

func (c *ThrowerConsumer) throw(ctx context.Context, forward Forward) error {
	switch forward.Target {
	case ForwardHTTP:
		callback, err := HTTPDLRCallbackFromForward(forward)
		if err != nil {
			return err
		}
		return SendHTTPDLR(ctx, c.http, callback)
	case ForwardSMPPS:
		if c.smpps == nil {
			return errors.New("dlr: no SMPPS delivery access attached")
		}
		params, err := SMPPSReceiptParamsFromForward(forward)
		if err != nil {
			return err
		}
		return c.smpps.DeliverReceipt(ctx, params)
	default:
		return fmt.Errorf("dlr: unknown forward target %d", forward.Target)
	}
}

func (c *ThrowerConsumer) settleFinal(messageID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.retrials, messageID)
	if timer, ok := c.timers[messageID]; ok {
		timer.Stop()
		delete(c.timers, messageID)
	}
}

// retryOrPurge applies the legacy policy: requeue with delay while the attempt
// count is <= MaxRetries, else purge. The legacy variant cancels any existing
// timer and arms a new one carrying the current delivery.
func (c *ThrowerConsumer) retryOrPurge(messageID string, delivery *amqpcompat.Delivery) {
	c.mu.Lock()
	if c.retrials[messageID] > c.cfg.MaxRetries {
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
		timer.Stop()
	}
	c.timers[messageID] = time.AfterFunc(c.cfg.RetryDelay, func() {
		c.mu.Lock()
		delete(c.timers, messageID)
		c.mu.Unlock()
		_ = delivery.Reject(true)
	})
	c.mu.Unlock()
}

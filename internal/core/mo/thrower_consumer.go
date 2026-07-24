package mo

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
)

// ErrInvalidMODelivery identifies a deliver_sm_thrower message the legacy
// callback rejects before throwing: wrong routing key, non-http destination
// connector, undecodable content, or a missing message body.
var ErrInvalidMODelivery = errors.New("mo: invalid thrower delivery")

// RoutedDecoder projects the routed MO content (implemented by the bridge).
type RoutedDecoder interface {
	DecodeRoutedDeliverSM(ctx context.Context, dstConnectors, body []byte) (picklecompat.RoutedDeliverSM, error)
}

// ThrowerConsumerConfig mirrors the [deliversm-thrower] retry knobs.
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

// ThrowerConsumer ports the deliverSmThrower's HTTP leg: routed MO contents
// become HTTP callbacks with the legacy argument set and Q-005 semantics.
//
// Route policy matches the legacy callback: a simple route retries with the
// capped delayed requeue (attempt count per delivery, requeue while
// count <= MaxRetries, then purge — the 404 no-retry list is dead code,
// KNOWN_QUIRKS Q-019); a failover route iterates its connectors, acks on the
// first acknowledged throw and never requeues between connectors. A failover
// route whose every connector fails is rejected without requeue — the legacy
// code leaves that delivery unsettled forever, a leak consciously corrected.
// deliver_sm_thrower.smpps deliveries fail into the retry path: no SMPPS
// session delivery exists yet, like a legacy deployment without SMPPS access.
type ThrowerConsumer struct {
	decoder RoutedDecoder
	http    HTTPDoer
	cfg     ThrowerConsumerConfig

	mu       sync.Mutex
	retrials map[string]int
	timers   map[string]*time.Timer
}

func NewThrowerConsumer(decoder RoutedDecoder, httpClient HTTPDoer, cfg ThrowerConsumerConfig) (*ThrowerConsumer, error) {
	if decoder == nil {
		return nil, errors.New("mo: nil routed decoder")
	}
	if httpClient == nil {
		return nil, errors.New("mo: nil HTTP client")
	}
	cfg.applyDefaults()
	return &ThrowerConsumer{
		decoder:  decoder,
		http:     httpClient,
		cfg:      cfg,
		retrials: make(map[string]int),
		timers:   make(map[string]*time.Timer),
	}, nil
}

// Handle takes settlement ownership of the delivery.
func (c *ThrowerConsumer) Handle(ctx context.Context, delivery *amqpcompat.Delivery) error {
	if delivery == nil {
		return errors.New("mo: nil delivery")
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

	if envelope.Route().Kind() == amqpcompat.RouteDeliverSMSMPPS {
		// No SMPPS session delivery is composed yet; fail into the legacy
		// retry-then-purge path, like a deployment without SMPPS access.
		c.retryOrPurge(messageID, delivery)
		return fmt.Errorf("%w: no SMPPS delivery access attached", ErrInvalidMODelivery)
	}
	if envelope.Route().Kind() != amqpcompat.RouteDeliverSMHTTP {
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return fmt.Errorf("%w: routing key %s", ErrInvalidMODelivery, envelope.RoutingKey())
	}

	headers := envelope.Properties().Headers()
	routeType, err := stringHeaderValue(headers, "route-type")
	if err != nil {
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return err
	}
	sourceConnector, err := stringHeaderValue(headers, "src-connector-id")
	if err != nil {
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return err
	}
	dstField, ok := headers["dst-connectors"]
	dstConnectors, isBytes := dstField.Bytes()
	if !ok || !isBytes {
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return fmt.Errorf("%w: missing dst-connectors header", ErrInvalidMODelivery)
	}

	routed, err := c.decoder.DecodeRoutedDeliverSM(ctx, dstConnectors, envelope.Body())
	if err != nil {
		// The legacy callback crashes pre-try on undecodable content and leaks
		// the delivery; rejecting without requeue is the conscious correction.
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return err
	}
	if routed.Connectors[0].Type != "http" {
		// The legacy callback rejects when the first destination connector is
		// not http.
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return fmt.Errorf("%w: destination connector type %q", ErrInvalidMODelivery, routed.Connectors[0].Type)
	}
	baseDelivery, err := c.buildDelivery(messageID, sourceConnector, routed)
	if err != nil {
		// Content missing from the pdu: the legacy callback rejects.
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return err
	}

	switch routeType {
	case "simple":
		target := baseDelivery
		target.URL = routed.Connectors[0].BaseURL
		target.Method = routed.Connectors[0].Method
		if err := SendMO(ctx, c.http, target); err != nil {
			c.retryOrPurge(messageID, delivery)
			return err
		}
		c.settleFinal(messageID)
		_ = delivery.Ack()
		return nil
	case "failover":
		var lastErr error
		for _, connector := range routed.Connectors {
			target := baseDelivery
			target.URL = connector.BaseURL
			target.Method = connector.Method
			if lastErr = SendMO(ctx, c.http, target); lastErr == nil {
				c.settleFinal(messageID)
				_ = delivery.Ack()
				return nil
			}
		}
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return fmt.Errorf("mo: every failover connector failed: %w", lastErr)
	default:
		c.settleFinal(messageID)
		_ = delivery.Reject(false)
		return fmt.Errorf("%w: route-type %q", ErrInvalidMODelivery, routeType)
	}
}

// buildDelivery assembles the callback fields from the projected routed
// content: the wire-side adapter provides content selection, addressing, and
// TLV forwarding; the pickled path overrides validity with the legacy
// str(datetime) rendering and appends the PDU-attribute custom TLVs.
func (c *ThrowerConsumer) buildDelivery(messageID, sourceConnector string, routed picklecompat.RoutedDeliverSM) (Delivery, error) {
	body := routed.Body
	delivery, err := DeliveryFromDeliverSM(&body, messageID, sourceConnector, "", "")
	if err != nil {
		return Delivery{}, fmt.Errorf("%w: %v", ErrInvalidMODelivery, err)
	}
	delivery.Validity = routed.ValidityText
	for _, tuple := range routed.CustomTLVs {
		converted, convertErr := customTLVFromTuple(tuple)
		if convertErr != nil {
			return Delivery{}, fmt.Errorf("%w: %v", ErrInvalidMODelivery, convertErr)
		}
		delivery.CustomTLVs = append(delivery.CustomTLVs, converted)
	}
	return delivery, nil
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

func stringHeaderValue(headers map[string]amqpcompat.Field, name string) (string, error) {
	field, ok := headers[name]
	if !ok {
		return "", fmt.Errorf("%w: missing header %q", ErrInvalidMODelivery, name)
	}
	value, isString := field.String()
	if !isString {
		return "", fmt.Errorf("%w: header %q has wrong kind", ErrInvalidMODelivery, name)
	}
	return value, nil
}

// customTLVFromTuple renders one PDU-attribute custom TLV tuple the way the
// legacy thrower builds custom_tlvs_data entries: tag and length verbatim,
// bytes values hex-encoded, string values as-is.
func customTLVFromTuple(tuple tlv.TLV) (CustomTLV, error) {
	if tuple.Tag == nil || !tuple.Tag.IsInt64() {
		return CustomTLV{}, fmt.Errorf("custom TLV tag %v is not an integer", tuple.Tag)
	}
	entry := CustomTLV{Tag: int(tuple.Tag.Int64()), Type: tuple.Type}
	if tuple.Length != nil {
		entry.Length = *tuple.Length
	}
	switch value := tuple.Value.(type) {
	case []byte:
		entry.Value = hex.EncodeToString(value)
	case string:
		entry.Value = value
	default:
		return CustomTLV{}, fmt.Errorf("custom TLV value %T is not bytes or string", tuple.Value)
	}
	return entry, nil
}

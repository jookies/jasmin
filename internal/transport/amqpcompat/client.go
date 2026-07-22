package amqpcompat

import (
	"context"
	"errors"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	ErrDeliverySettled = errors.New("AMQP delivery already settled")
	ErrPublishNacked   = errors.New("AMQP publication negatively acknowledged")
	ErrPublishReturned = errors.New("AMQP publication returned as unroutable")
)

// Publisher sends persistent messages with broker confirms and mandatory
// routing. Publish calls are serialized so confirmations and basic.return
// notifications cannot be attributed to the wrong envelope.
type Publisher struct {
	conn    *amqp.Connection
	ch      *amqp.Channel
	returns <-chan amqp.Return
	mu      sync.Mutex
}

func NewPublisher(conn *amqp.Connection) (*Publisher, error) {
	if conn == nil {
		return nil, errors.New("nil AMQP connection")
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("enable AMQP publisher confirms: %w", err)
	}
	returns := ch.NotifyReturn(make(chan amqp.Return, 1))
	return &Publisher{conn: conn, ch: ch, returns: returns}, nil
}

func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ch.Close()
}

func (p *Publisher) Publish(ctx context.Context, exchange string, routingKey string, msg Envelope) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	properties := msg.Properties()
	publishing := amqp.Publishing{
		MessageId:    properties.MessageID(),
		Body:         msg.Body(),
		ContentType:  "application/octet-stream",
		DeliveryMode: amqp.Persistent,
		Headers:      toAMQPHeaders(properties.Headers()),
	}
	if replyTo, ok := properties.ReplyTo(); ok {
		publishing.ReplyTo = replyTo
	}
	if priority, ok := properties.Priority(); ok {
		publishing.Priority = priority
	}

	confirmation, err := p.ch.PublishWithDeferredConfirmWithContext(
		ctx,
		exchange,
		routingKey,
		true,  // mandatory: never silently drop an unroutable submit
		false, // immediate
		publishing,
	)
	if err != nil {
		return fmt.Errorf("publish AMQP envelope: %w", err)
	}
	if confirmation == nil {
		return errors.New("AMQP publisher confirm was not registered")
	}

	for {
		select {
		case returned, ok := <-p.returns:
			if !ok {
				return errors.New("AMQP return channel closed")
			}
			// A caller can time out after the broker accepted a previous publish
			// but before its basic.return reaches this listener. Ignore that stale
			// return instead of attributing it to the next serialized publication.
			if returned.MessageId != properties.MessageID() {
				continue
			}
			return fmt.Errorf("%w: %d %s exchange=%q routing-key=%q message-id=%q",
				ErrPublishReturned, returned.ReplyCode, returned.ReplyText,
				returned.Exchange, returned.RoutingKey, returned.MessageId)
		case <-confirmation.Done():
			if !confirmation.Acked() {
				return fmt.Errorf("%w: message-id=%q", ErrPublishNacked, properties.MessageID())
			}
			// Channel.dispatch processes basic.return and basic.ack serially on
			// the connection reader goroutine and synchronously writes returns to
			// this buffered channel. A return for this serialized publication is
			// therefore already available once its ACK becomes observable.
			if returned, matched := drainMatchingReturn(p.returns, properties.MessageID()); matched {
				return fmt.Errorf("%w: %d %s exchange=%q routing-key=%q message-id=%q",
					ErrPublishReturned, returned.ReplyCode, returned.ReplyText,
					returned.Exchange, returned.RoutingKey, returned.MessageId)
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func drainMatchingReturn(returns <-chan amqp.Return, messageID string) (amqp.Return, bool) {
	for {
		select {
		case returned, ok := <-returns:
			if !ok {
				return amqp.Return{}, false
			}
			if returned.MessageId == messageID {
				return returned, true
			}
		default:
			return amqp.Return{}, false
		}
	}
}

// Delivery owns one decoded envelope and its explicit broker settlement.
// Decoding never acknowledges the broker delivery implicitly.
type Delivery struct {
	envelope Envelope
	raw      amqp.Delivery

	mu      sync.Mutex
	settled bool
}

func NewDelivery(raw amqp.Delivery) (*Delivery, error) {
	options := make([]PropertyOption, 0, 2)
	if raw.ReplyTo != "" {
		options = append(options, WithReplyTo(raw.ReplyTo))
	}
	// AMQP does not expose a presence bit for priority. Preserve zero as absent,
	// matching legacy fixtures where priority is omitted unless explicitly set.
	if raw.Priority != 0 {
		options = append(options, WithPriority(raw.Priority))
	}
	props, err := NewProperties(raw.MessageId, fromAMQPHeaders(raw.Headers), options...)
	if err != nil {
		return nil, err
	}
	envelope, err := NewEnvelope(raw.RoutingKey, props, raw.Body)
	if err != nil {
		return nil, err
	}
	return &Delivery{envelope: envelope, raw: raw}, nil
}

func (delivery *Delivery) Envelope() Envelope {
	return delivery.envelope
}

func (delivery *Delivery) Ack() error {
	return delivery.settle(func() error { return delivery.raw.Ack(false) })
}

func (delivery *Delivery) Reject(requeue bool) error {
	return delivery.settle(func() error { return delivery.raw.Reject(requeue) })
}

// Abandon marks this local handle terminal without issuing a broker settlement.
// It is used after the consumer generation is known unusable and the broker owns
// redelivery through connection/channel teardown.
func (delivery *Delivery) Abandon() error {
	return delivery.settle(func() error { return nil })
}

func (delivery *Delivery) settle(operation func() error) error {
	delivery.mu.Lock()
	defer delivery.mu.Unlock()
	if delivery.settled {
		return ErrDeliverySettled
	}
	delivery.settled = true
	if err := operation(); err != nil {
		return fmt.Errorf("settle AMQP delivery: %w", err)
	}
	return nil
}

// Consumer receives unsettled messages from a queue. The downstream owner must
// explicitly ACK or reject each delivered handle after processing completes.
type Consumer struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewConsumer(conn *amqp.Connection) (*Consumer, error) {
	return NewConsumerWithPrefetch(conn, 1)
}

func NewConsumerWithPrefetch(conn *amqp.Connection, prefetch int) (*Consumer, error) {
	if conn == nil {
		return nil, errors.New("nil AMQP connection")
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	if prefetch < 1 || prefetch > 65535 {
		_ = ch.Close()
		return nil, fmt.Errorf("invalid AMQP prefetch count %d", prefetch)
	}
	// Legacy SMPP connector consumers set basic.qos before attaching
	// submit.sm.<CID>. RouterPB has a distinct topology path with no QoS.
	if err := ch.Qos(prefetch, 0, false); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("set AMQP consumer QoS: %w", err)
	}
	return &Consumer{conn: conn, ch: ch}, nil
}

func (c *Consumer) Close() error {
	return c.ch.Close()
}

func (c *Consumer) Consume(ctx context.Context, queue string) (<-chan *Delivery, error) {
	deliveries, err := c.ch.Consume(
		queue,
		"",    // consumer
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return nil, err
	}

	notifyClose := c.ch.NotifyClose(make(chan *amqp.Error, 1))

	out := make(chan *Delivery)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case err := <-notifyClose:
				if err != nil {
					// RI-005: Channel closed due to error.
					// In a full implementation, this would trigger a reconnect.
					// For now, we exit the loop.
				}
				return
			case d, ok := <-deliveries:
				if !ok {
					return
				}
				delivery, err := NewDelivery(d)
				if err != nil {
					// The consumer owns every raw delivery it receives. Malformed
					// properties/routing are poison, not an unsettled message that can
					// remain stuck until channel teardown.
					_ = d.Reject(false)
					continue
				}
				select {
				case out <- delivery:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return out, nil
}

func toAMQPHeaders(h map[string]Field) amqp.Table {
	res := make(amqp.Table)
	for k, v := range h {
		switch v.Kind() {
		case FieldString:
			s, _ := v.String()
			res[k] = s
		case FieldInteger:
			i, _ := v.Integer()
			res[k] = i
		case FieldBytes:
			b, _ := v.Bytes()
			res[k] = b
		}
	}
	return res
}

func fromAMQPHeaders(h amqp.Table) map[string]Field {
	res := make(map[string]Field)
	for k, v := range h {
		switch val := v.(type) {
		case string:
			res[k] = StringField(val)
		case int64:
			res[k] = IntegerField(val)
		case []byte:
			res[k] = BytesField(val)
		case int:
			res[k] = IntegerField(int64(val))
		}
	}
	return res
}

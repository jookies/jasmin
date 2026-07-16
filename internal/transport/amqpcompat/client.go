package amqpcompat

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Publisher sends messages to an exchange.
type Publisher struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewPublisher(conn *amqp.Connection) (*Publisher, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	return &Publisher{conn: conn, ch: ch}, nil
}

func (p *Publisher) Close() error {
	return p.ch.Close()
}

func (p *Publisher) Publish(ctx context.Context, exchange string, routingKey string, msg Envelope) error {
	return p.ch.PublishWithContext(ctx,
		exchange,
		routingKey,
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			MessageId:    msg.Properties().MessageID(),
			Body:         msg.Body(),
			ContentType:  "application/octet-stream",
			DeliveryMode: amqp.Persistent,
			Headers:      toAMQPHeaders(msg.Properties().Headers()),
		},
	)
}

// Consumer receives messages from a queue.
type Consumer struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewConsumer(conn *amqp.Connection) (*Consumer, error) {
	ch, err := conn.Channel()
	if err != nil {
		return nil, err
	}
	return &Consumer{conn: conn, ch: ch}, nil
}

func (c *Consumer) Close() error {
	return c.ch.Close()
}

func (c *Consumer) Consume(ctx context.Context, queue string) (<-chan Envelope, error) {
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

	out := make(chan Envelope)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case d, ok := <-deliveries:
				if !ok {
					return
				}
				props, err := NewProperties(d.MessageId, fromAMQPHeaders(d.Headers))
				if err != nil {
					// RI-005: Failure handling. Log and skip malformed.
					continue
				}
				env, err := NewEnvelope(d.RoutingKey, props, d.Body)
				if err != nil {
					continue
				}
				out <- env
				d.Ack(false)
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

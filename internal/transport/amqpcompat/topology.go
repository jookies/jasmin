package amqpcompat

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Topology handles declaration of exchanges, queues and bindings.
type Topology struct {
	conn *amqp.Connection
}

func NewTopology(conn *amqp.Connection) *Topology {
	return &Topology{conn: conn}
}

// Declare sets up the standard Jasmin AMQP topology.
func (t *Topology) Declare(ctx context.Context) error {
	ch, err := t.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	// A-001: Exchanges
	exchanges := []struct {
		name string
		kind string
	}{
		{"messaging", "topic"},
		{"billing", "topic"},
	}

	for _, e := range exchanges {
		if err := ch.ExchangeDeclare(
			e.name,
			e.kind,
			true,  // durable
			false, // auto-deleted
			false, // internal
			false, // no-wait
			nil,   // arguments
		); err != nil {
			return fmt.Errorf("exchange %s: %w", e.name, err)
		}
	}

	return nil
}

// DeclareQueue sets up a specific queue with bindings.
func (t *Topology) DeclareQueue(ctx context.Context, name string, exchange string, routingKey string) error {
	ch, err := t.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if _, err := ch.QueueDeclare(
		name,
		true,  // durable
		false, // auto-delete
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	); err != nil {
		return fmt.Errorf("queue %s: %w", name, err)
	}

	if err := ch.QueueBind(
		name,
		routingKey,
		exchange,
		false, // no-wait
		nil,   // arguments
	); err != nil {
		return fmt.Errorf("bind %s to %s via %s: %w", name, exchange, routingKey, err)
	}

	return nil
}

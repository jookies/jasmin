package amqpcompat

import (
	"context"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	RouterDeliverSMQueue       = "RouterPB_deliver_sm_all"
	RouterDeliverSMRoutingKey  = "deliver.sm.*"
	RouterDeliverSMConsumerTag = "RouterPB-delivers"
	RouterBillingQueue         = "RouterPB_bill_request_submit_sm_resp_all"
	RouterBillingRoutingKey    = "bill_request.submit_sm_resp.*"
	RouterBillingConsumerTag   = "RouterPB-billrequests"
)

func ConnectorSubmitQueue(cid string) string {
	return "submit.sm." + cid
}

func ConnectorSubmitRoutingKey(cid string) string {
	return "submit.sm." + cid
}

type topologyChannel interface {
	ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, arguments amqp.Table) error
	QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, arguments amqp.Table) (amqp.Queue, error)
	QueueBind(name, key, exchange string, noWait bool, arguments amqp.Table) error
	Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, arguments amqp.Table) (<-chan amqp.Delivery, error)
	Close() error
}

type topologyChannelOpener func() (topologyChannel, error)

// Topology owns channel creation for exact Jasmin-compatible declarations.
type Topology struct {
	open topologyChannelOpener
}

func NewTopology(conn *amqp.Connection) *Topology {
	return &Topology{open: func() (topologyChannel, error) { return conn.Channel() }}
}

func newTopology(open topologyChannelOpener) *Topology {
	return &Topology{open: open}
}

// RouterSubscriptions owns the channel and the two raw manual-ack delivery
// streams created by RouterPB.addAmqpBroker in the legacy implementation.
type RouterSubscriptions struct {
	DeliverSM            <-chan amqp.Delivery
	Billing              <-chan amqp.Delivery
	DeliverSMConsumerTag string
	BillingConsumerTag   string

	channel topologyChannel
}

func (subscriptions *RouterSubscriptions) Close() error {
	if subscriptions == nil || subscriptions.channel == nil {
		return nil
	}
	return subscriptions.channel.Close()
}

// OpenRouterSubscriptions declares and starts both fixed RouterPB consumers on
// one owned channel. It closes the channel if any operation fails.
func (topology *Topology) OpenRouterSubscriptions(ctx context.Context) (*RouterSubscriptions, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	channel, err := topology.open()
	if err != nil {
		return nil, fmt.Errorf("open RouterPB topology channel: %w", err)
	}
	subscriptions, err := declareRouterSubscriptions(ctx, channel)
	if err != nil {
		if closeErr := channel.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close RouterPB topology channel: %w", closeErr))
		}
		return nil, err
	}
	subscriptions.channel = channel
	return subscriptions, nil
}

func declareRouterSubscriptions(ctx context.Context, channel topologyChannel) (*RouterSubscriptions, error) {
	if err := declareExchange(ctx, channel, "messaging"); err != nil {
		return nil, err
	}
	if err := declareQueue(ctx, channel, RouterDeliverSMQueue); err != nil {
		return nil, err
	}
	if err := bindQueue(ctx, channel, RouterDeliverSMQueue, "messaging", RouterDeliverSMRoutingKey); err != nil {
		return nil, err
	}
	deliverSM, err := consumeQueue(ctx, channel, RouterDeliverSMQueue, RouterDeliverSMConsumerTag)
	if err != nil {
		return nil, err
	}

	if err := declareExchange(ctx, channel, "billing"); err != nil {
		return nil, err
	}
	if err := declareQueue(ctx, channel, RouterBillingQueue); err != nil {
		return nil, err
	}
	if err := bindQueue(ctx, channel, RouterBillingQueue, "billing", RouterBillingRoutingKey); err != nil {
		return nil, err
	}
	billing, err := consumeQueue(ctx, channel, RouterBillingQueue, RouterBillingConsumerTag)
	if err != nil {
		return nil, err
	}

	return &RouterSubscriptions{
		DeliverSM:            deliverSM,
		Billing:              billing,
		DeliverSMConsumerTag: RouterDeliverSMConsumerTag,
		BillingConsumerTag:   RouterBillingConsumerTag,
	}, nil
}

func declareExchange(ctx context.Context, channel topologyChannel, name string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("declare exchange %s: %w", name, err)
	}
	if err := channel.ExchangeDeclare(name, "topic", false, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("declare exchange %s: %w", name, err)
	}
	return nil
}

func declareQueue(ctx context.Context, channel topologyChannel, name string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("declare queue %s: %w", name, err)
	}
	if _, err := channel.QueueDeclare(name, false, false, false, false, nil); err != nil {
		return fmt.Errorf("declare queue %s: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("declare queue %s: %w", name, err)
	}
	return nil
}

func bindQueue(ctx context.Context, channel topologyChannel, queue, exchange, routingKey string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("bind queue %s: %w", queue, err)
	}
	if err := channel.QueueBind(queue, routingKey, exchange, false, nil); err != nil {
		return fmt.Errorf("bind queue %s to %s via %s: %w", queue, exchange, routingKey, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("bind queue %s to %s via %s: %w", queue, exchange, routingKey, err)
	}
	return nil
}

func consumeQueue(ctx context.Context, channel topologyChannel, queue, consumerTag string) (<-chan amqp.Delivery, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("consume queue %s: %w", queue, err)
	}
	deliveries, err := channel.Consume(queue, consumerTag, false, false, false, false, nil)
	if err != nil {
		return nil, fmt.Errorf("consume queue %s as %s: %w", queue, consumerTag, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("consume queue %s as %s: %w", queue, consumerTag, err)
	}
	return deliveries, nil
}

// Declare sets up the two legacy topic exchanges with their non-durable defaults.
func (topology *Topology) Declare(ctx context.Context) (resultErr error) {
	channel, err := topology.open()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := channel.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close topology channel: %w", closeErr))
		}
	}()
	for _, name := range []string{"messaging", "billing"} {
		if err := declareExchange(ctx, channel, name); err != nil {
			return err
		}
	}
	return nil
}

// DeclareQueue creates one legacy-default non-durable queue and binding.
func (topology *Topology) DeclareQueue(ctx context.Context, name, exchange, routingKey string) (resultErr error) {
	channel, err := topology.open()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := channel.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close topology channel: %w", closeErr))
		}
	}()
	if err := declareQueue(ctx, channel, name); err != nil {
		return err
	}
	return bindQueue(ctx, channel, name, exchange, routingKey)
}

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

// DLRLookupRoutingKey is the legacy DLRLookup.subscribe binding.
const DLRLookupRoutingKey = "dlr.*"

// DLRLookupQueue names the legacy per-pid lookup queue ('DLRLookup-%s' % pid).
func DLRLookupQueue(pid string) string { return "DLRLookup-" + pid }

// DLRLookupConsumerTag matches the legacy consumer tag, identical to the queue name.
func DLRLookupConsumerTag(pid string) string { return "DLRLookup-" + pid }

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
// durable=false reproduces the legacy txamqp defaults (the Python stack never
// passes durable); durable=true hardens a Go-native broker so queued submits
// survive a broker restart. The two modes MUST NOT meet on one vhost: AMQP
// 0-9-1 answers a redeclare with different durability with a channel-level
// PRECONDITION_FAILED (406).
type Topology struct {
	open    topologyChannelOpener
	durable bool
}

func NewTopology(conn *amqp.Connection, durable bool) *Topology {
	return &Topology{open: func() (topologyChannel, error) { return conn.Channel() }, durable: durable}
}

func newTopology(open topologyChannelOpener, durable bool) *Topology {
	return &Topology{open: open, durable: durable}
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
	subscriptions, err := declareRouterSubscriptions(ctx, channel, topology.durable)
	if err != nil {
		if closeErr := channel.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close RouterPB topology channel: %w", closeErr))
		}
		return nil, err
	}
	subscriptions.channel = channel
	return subscriptions, nil
}

func declareRouterSubscriptions(ctx context.Context, channel topologyChannel, durable bool) (*RouterSubscriptions, error) {
	if err := declareExchange(ctx, channel, "messaging", durable); err != nil {
		return nil, err
	}
	if err := declareQueue(ctx, channel, RouterDeliverSMQueue, durable); err != nil {
		return nil, err
	}
	if err := bindQueue(ctx, channel, RouterDeliverSMQueue, "messaging", RouterDeliverSMRoutingKey); err != nil {
		return nil, err
	}
	deliverSM, err := consumeQueue(ctx, channel, RouterDeliverSMQueue, RouterDeliverSMConsumerTag)
	if err != nil {
		return nil, err
	}

	if err := declareExchange(ctx, channel, "billing", durable); err != nil {
		return nil, err
	}
	if err := declareQueue(ctx, channel, RouterBillingQueue, durable); err != nil {
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

func declareExchange(ctx context.Context, channel topologyChannel, name string, durable bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("declare exchange %s: %w", name, err)
	}
	if err := channel.ExchangeDeclare(name, "topic", durable, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("declare exchange %s: %w", name, err)
	}
	return nil
}

func declareQueue(ctx context.Context, channel topologyChannel, name string, durable bool) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("declare queue %s: %w", name, err)
	}
	if _, err := channel.QueueDeclare(name, durable, false, false, false, nil); err != nil {
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

// DLRThrower topology constants: the legacy DLRThrower binds a fixed queue to
// dlr_thrower.* with a fixed consumer tag.
const (
	DLRThrowerQueue       = "dlr_thrower"
	DLRThrowerRoutingKey  = "dlr_thrower.*"
	DLRThrowerConsumerTag = "DLRThrower"
)

// DLRThrowerSubscription owns the channel and raw manual-ack delivery stream
// of the legacy DLRThrower.addAmqpBroker topology.
type DLRThrowerSubscription struct {
	Deliveries  <-chan amqp.Delivery
	ConsumerTag string

	channel topologyChannel
}

func (subscription *DLRThrowerSubscription) Close() error {
	if subscription == nil || subscription.channel == nil {
		return nil
	}
	return subscription.channel.Close()
}

// OpenDLRThrowerSubscription declares and starts the DLRThrower consumer on
// one owned channel: messaging exchange, the dlr_thrower queue bound to
// dlr_thrower.*, and a named manual-ack consumer with no QoS.
func (topology *Topology) OpenDLRThrowerSubscription(ctx context.Context) (*DLRThrowerSubscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	channel, err := topology.open()
	if err != nil {
		return nil, fmt.Errorf("open DLRThrower topology channel: %w", err)
	}
	subscription, err := func() (*DLRThrowerSubscription, error) {
		if err := declareExchange(ctx, channel, "messaging", topology.durable); err != nil {
			return nil, err
		}
		if err := declareQueue(ctx, channel, DLRThrowerQueue, topology.durable); err != nil {
			return nil, err
		}
		if err := bindQueue(ctx, channel, DLRThrowerQueue, "messaging", DLRThrowerRoutingKey); err != nil {
			return nil, err
		}
		deliveries, err := consumeQueue(ctx, channel, DLRThrowerQueue, DLRThrowerConsumerTag)
		if err != nil {
			return nil, err
		}
		return &DLRThrowerSubscription{Deliveries: deliveries, ConsumerTag: DLRThrowerConsumerTag}, nil
	}()
	if err != nil {
		if closeErr := channel.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close DLRThrower topology channel: %w", closeErr))
		}
		return nil, err
	}
	subscription.channel = channel
	return subscription, nil
}

// MO thrower topology constants: the legacy deliverSmThrower binds a fixed
// queue to deliver_sm_thrower.* with a fixed consumer tag.
const (
	MOThrowerQueue       = "deliver_sm_thrower"
	MOThrowerRoutingKey  = "deliver_sm_thrower.*"
	MOThrowerConsumerTag = "deliverSmThrower"
)

// MOThrowerSubscription owns the channel and raw manual-ack delivery stream of
// the legacy deliverSmThrower.addAmqpBroker topology.
type MOThrowerSubscription struct {
	Deliveries  <-chan amqp.Delivery
	ConsumerTag string

	channel topologyChannel
}

func (subscription *MOThrowerSubscription) Close() error {
	if subscription == nil || subscription.channel == nil {
		return nil
	}
	return subscription.channel.Close()
}

// OpenMOThrowerSubscription declares and starts the deliverSmThrower consumer
// on one owned channel: messaging exchange, the deliver_sm_thrower queue bound
// to deliver_sm_thrower.*, and a named manual-ack consumer with no QoS.
func (topology *Topology) OpenMOThrowerSubscription(ctx context.Context) (*MOThrowerSubscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	channel, err := topology.open()
	if err != nil {
		return nil, fmt.Errorf("open deliverSmThrower topology channel: %w", err)
	}
	subscription, err := func() (*MOThrowerSubscription, error) {
		if err := declareExchange(ctx, channel, "messaging", topology.durable); err != nil {
			return nil, err
		}
		if err := declareQueue(ctx, channel, MOThrowerQueue, topology.durable); err != nil {
			return nil, err
		}
		if err := bindQueue(ctx, channel, MOThrowerQueue, "messaging", MOThrowerRoutingKey); err != nil {
			return nil, err
		}
		deliveries, err := consumeQueue(ctx, channel, MOThrowerQueue, MOThrowerConsumerTag)
		if err != nil {
			return nil, err
		}
		return &MOThrowerSubscription{Deliveries: deliveries, ConsumerTag: MOThrowerConsumerTag}, nil
	}()
	if err != nil {
		if closeErr := channel.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close deliverSmThrower topology channel: %w", closeErr))
		}
		return nil, err
	}
	subscription.channel = channel
	return subscription, nil
}

// DLRLookupSubscription owns the channel and raw manual-ack delivery stream of
// the legacy DLRLookup.subscribe topology: the messaging exchange, the
// DLRLookup-<pid> queue bound to dlr.*, and a named manual-ack consumer with no
// QoS (the legacy subscribe sets none).
type DLRLookupSubscription struct {
	Deliveries  <-chan amqp.Delivery
	ConsumerTag string

	channel topologyChannel
}

func (subscription *DLRLookupSubscription) Close() error {
	if subscription == nil || subscription.channel == nil {
		return nil
	}
	return subscription.channel.Close()
}

// OpenDLRLookupSubscription declares and starts the DLRLookup consumer on one
// owned channel. It closes the channel if any operation fails.
func (topology *Topology) OpenDLRLookupSubscription(ctx context.Context, pid string) (*DLRLookupSubscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	channel, err := topology.open()
	if err != nil {
		return nil, fmt.Errorf("open DLRLookup topology channel: %w", err)
	}
	queue, tag := DLRLookupQueue(pid), DLRLookupConsumerTag(pid)
	subscription, err := func() (*DLRLookupSubscription, error) {
		if err := declareExchange(ctx, channel, "messaging", topology.durable); err != nil {
			return nil, err
		}
		if err := declareQueue(ctx, channel, queue, topology.durable); err != nil {
			return nil, err
		}
		if err := bindQueue(ctx, channel, queue, "messaging", DLRLookupRoutingKey); err != nil {
			return nil, err
		}
		deliveries, err := consumeQueue(ctx, channel, queue, tag)
		if err != nil {
			return nil, err
		}
		return &DLRLookupSubscription{Deliveries: deliveries, ConsumerTag: tag}, nil
	}()
	if err != nil {
		if closeErr := channel.Close(); closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close DLRLookup topology channel: %w", closeErr))
		}
		return nil, err
	}
	subscription.channel = channel
	return subscription, nil
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
		if err := declareExchange(ctx, channel, name, topology.durable); err != nil {
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
	if err := declareQueue(ctx, channel, name, topology.durable); err != nil {
		return err
	}
	return bindQueue(ctx, channel, name, exchange, routingKey)
}

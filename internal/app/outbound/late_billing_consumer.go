package outbound

import (
	"context"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

// lateBillingConsumer consumes only RouterPB's billing queue. The outbound
// runtime must not subscribe to deliver.sm.* while MO/DLR dispatch remains a
// Macro 2 stub, otherwise it would steal and requeue inbound traffic.
type lateBillingConsumer struct {
	channel *amqp.Channel
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func newLateBillingConsumer(
	parent context.Context,
	connection *amqp.Connection,
	processor core.LateBillingDecisionProcessor,
) (*lateBillingConsumer, error) {
	if connection == nil || processor == nil {
		return nil, fmt.Errorf("late billing consumer requires connection and processor")
	}
	channel, err := connection.Channel()
	if err != nil {
		return nil, fmt.Errorf("open late billing channel: %w", err)
	}
	closeOnError := func(err error) (*lateBillingConsumer, error) {
		_ = channel.Close()
		return nil, err
	}
	if err := channel.ExchangeDeclare("billing", "topic", false, false, false, false, nil); err != nil {
		return closeOnError(fmt.Errorf("declare billing exchange: %w", err))
	}
	if _, err := channel.QueueDeclare(amqpcompat.RouterBillingQueue, false, false, false, false, nil); err != nil {
		return closeOnError(fmt.Errorf("declare billing queue: %w", err))
	}
	if err := channel.QueueBind(amqpcompat.RouterBillingQueue, amqpcompat.RouterBillingRoutingKey, "billing", false, nil); err != nil {
		return closeOnError(fmt.Errorf("bind billing queue: %w", err))
	}
	deliveries, err := channel.Consume(
		amqpcompat.RouterBillingQueue,
		"jasmin-go-httpapi-billing",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return closeOnError(fmt.Errorf("consume billing queue: %w", err))
	}
	ctx, cancel := context.WithCancel(parent)
	consumer := &lateBillingConsumer{channel: channel, cancel: cancel}
	consumer.wg.Add(1)
	go consumer.run(ctx, processor, deliveries)
	return consumer, nil
}

func (consumer *lateBillingConsumer) run(
	ctx context.Context,
	processor core.LateBillingDecisionProcessor,
	deliveries <-chan amqp.Delivery,
) {
	defer consumer.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-deliveries:
			if !ok {
				return
			}
			delivery, err := amqpcompat.NewDelivery(raw)
			if err != nil {
				_ = raw.Reject(false)
				continue
			}
			action, processErr := processor.Process(delivery.Envelope())
			if processErr != nil {
				_ = delivery.Reject(true)
				continue
			}
			switch action {
			case core.LateBillingAck, core.LateBillingNone:
				_ = delivery.Ack()
			case core.LateBillingReject:
				_ = delivery.Reject(false)
			default:
				_ = delivery.Reject(false)
			}
		}
	}
}

func (consumer *lateBillingConsumer) Close() error {
	if consumer == nil {
		return nil
	}
	consumer.cancel()
	closeErr := consumer.channel.Close()
	consumer.wg.Wait()
	return closeErr
}

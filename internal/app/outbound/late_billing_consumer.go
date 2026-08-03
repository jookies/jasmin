package outbound

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/synevyr/internal/core"
	"github.com/pumpitspace/synevyr/internal/core/cdr"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
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
	durableTopology bool,
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
	if err := channel.ExchangeDeclare("billing", "topic", durableTopology, false, false, false, nil); err != nil {
		return closeOnError(fmt.Errorf("declare billing exchange: %w", err))
	}
	if _, err := channel.QueueDeclare(amqpcompat.RouterBillingQueue, durableTopology, false, false, false, nil); err != nil {
		return closeOnError(fmt.Errorf("declare billing queue: %w", err))
	}
	if err := channel.QueueBind(amqpcompat.RouterBillingQueue, amqpcompat.RouterBillingRoutingKey, "billing", false, nil); err != nil {
		return closeOnError(fmt.Errorf("bind billing queue: %w", err))
	}
	deliveries, err := channel.Consume(
		amqpcompat.RouterBillingQueue,
		"synevyr-gateway-billing",
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
				// Dropped without requeue, so it has to be reported: this is a
				// billing intent, and losing one silently is lost revenue.
				slog.Error("discarding an undecodable late-billing delivery",
					"routing_key", raw.RoutingKey, "message_id", raw.MessageId,
					"error", err.Error())
				_ = raw.Reject(false)
				continue
			}
			action, processErr := processor.Process(delivery.Envelope())
			// A CDR that has already reached a terminal billing outcome can never
			// accept this intent, so requeueing it is an infinite loop -- which is
			// exactly what happened, silently, to every intent raised for a
			// zero-value late charge. Discard it instead, and say so.
			if errors.Is(processErr, cdr.ErrLateBillingSettled) {
				slog.Warn("discarding a late-billing intent its CDR has already settled",
					"message_id", delivery.Envelope().Properties().MessageID(),
					"error", processErr.Error())
				_ = delivery.Reject(false)
				continue
			}
			if processErr != nil {
				// Requeued after a pause, not instantly. The usual cause is the
				// database being unreachable, and an immediate requeue turned
				// that into a full-rate spin between this consumer and the
				// broker for the whole outage -- competing for the same
				// connection the submit path uses. The charge is delayed either
				// way; only the churn is avoidable.
				slog.Warn("late billing intent deferred",
					"message_id", delivery.Envelope().Properties().MessageID(),
					"retry_in", lateBillingRetryDelay.String(), "error", processErr.Error())
				timer := time.NewTimer(lateBillingRetryDelay)
				select {
				case <-timer.C:
				case <-ctx.Done():
				}
				timer.Stop()
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

// lateBillingRetryDelay paces redelivery after a processing failure. The failure
// is almost always the billing store being unreachable, which does not resolve
// in microseconds.
const lateBillingRetryDelay = 5 * time.Second

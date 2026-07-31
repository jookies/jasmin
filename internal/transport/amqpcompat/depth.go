package amqpcompat

import (
	"context"
	"errors"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ErrNoBrokerConnection reports a depth observation attempted without a broker.
var ErrNoBrokerConnection = errors.New("amqpcompat: no broker connection")

// QueueDepths observes how many messages are waiting in each named queue.
//
// The observation is a PASSIVE queue.declare. Passive is not an optimization
// here, it is the whole safety property: an active declare creates the queue if
// it is missing, so a depth probe for a connector whose queue was never declared
// — a typo in a metrics list, a connector removed while the observer still knows
// its name — would silently conjure an unbound queue that nothing consumes and
// report it as healthily empty. A passive declare of a missing queue is an
// error, which is the honest answer.
//
// The count is the broker's queue.declare-ok message count: READY messages only.
// Deliveries already handed to a consumer and not yet acknowledged are NOT
// included, so a stuck consumer holding one message shows depth 0 for it.
//
// Queues that could not be observed are simply absent from the result rather
// than reported as zero: a metric that cannot distinguish "empty" from
// "unmeasured" is how a backlog alert learns to stay quiet.
//
// A failed passive declare closes the channel (AMQP answers NOT_FOUND with a
// channel exception), so the loop opens a fresh one for the next queue.
func QueueDepths(ctx context.Context, connection *amqp.Connection, queues []string) (map[string]int, error) {
	if connection == nil || connection.IsClosed() {
		return nil, ErrNoBrokerConnection
	}
	depths := make(map[string]int, len(queues))
	var channel *amqp.Channel
	defer func() {
		if channel != nil {
			_ = channel.Close()
		}
	}()
	var errs []error
	for _, queue := range queues {
		if ctx.Err() != nil {
			return depths, ctx.Err()
		}
		if queue == "" {
			continue
		}
		if channel == nil {
			opened, err := connection.Channel()
			if err != nil {
				// The connection itself is gone; further queues will not fare
				// better, so stop rather than emit one error per queue.
				return depths, errors.Join(append(errs, err)...)
			}
			channel = opened
		}
		state, err := channel.QueueDeclarePassive(queue, false, false, false, false, nil)
		if err != nil {
			errs = append(errs, err)
			// The broker closed this channel with the exception. Dropping the
			// handle makes the next iteration open a working one.
			_ = channel.Close()
			channel = nil
			continue
		}
		depths[queue] = state.Messages
	}
	return depths, errors.Join(errs...)
}

package amqpcompat

import (
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestDrainMatchingReturnIgnoresStaleMultipartReturnAfterConfirm(t *testing.T) {
	returns := make(chan amqp.Return, 2)
	returns <- amqp.Return{MessageId: "aggregate/000001", ReplyCode: 312}

	if returned, matched := drainMatchingReturn(returns, "aggregate/000002"); matched {
		t.Fatalf("stale return matched current publication: %+v", returned)
	}

	returns <- amqp.Return{MessageId: "aggregate/000002", ReplyCode: 312}
	if returned, matched := drainMatchingReturn(returns, "aggregate/000002"); !matched || returned.MessageId != "aggregate/000002" {
		t.Fatalf("current return=(%+v,%v)", returned, matched)
	}
}

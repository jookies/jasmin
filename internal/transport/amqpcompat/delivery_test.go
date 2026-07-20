package amqpcompat

import (
	"errors"
	"sync"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
)

type recordingAcknowledger struct {
	ackCalls    int
	rejectCalls int
	nackCalls   int
	tag         uint64
	multiple    bool
	requeue     bool
	err         error
}

func (a *recordingAcknowledger) Ack(tag uint64, multiple bool) error {
	a.ackCalls++
	a.tag = tag
	a.multiple = multiple
	return a.err
}

func (a *recordingAcknowledger) Nack(tag uint64, multiple, requeue bool) error {
	a.nackCalls++
	a.tag = tag
	a.multiple = multiple
	a.requeue = requeue
	return a.err
}

func (a *recordingAcknowledger) Reject(tag uint64, requeue bool) error {
	a.rejectCalls++
	a.tag = tag
	a.requeue = requeue
	return a.err
}

func validRawDelivery(acknowledger amqp.Acknowledger) amqp.Delivery {
	return amqp.Delivery{
		Acknowledger: acknowledger,
		DeliveryTag:  42,
		RoutingKey:   "bill_request.submit_sm_resp.alice_1",
		MessageId:    "bill-1",
		Headers: amqp.Table{
			"user-id": "alice_1",
			"amount":  "0.75",
		},
		Body: []byte("bill-1"),
	}
}

func TestDeliveryRequiresExplicitSingleSettlement(t *testing.T) {
	acknowledger := &recordingAcknowledger{}
	delivery, err := NewDelivery(validRawDelivery(acknowledger))
	if err != nil {
		t.Fatal(err)
	}
	if acknowledger.ackCalls != 0 || acknowledger.rejectCalls != 0 || acknowledger.nackCalls != 0 {
		t.Fatal("decoding settled delivery implicitly")
	}
	if got := delivery.Envelope().Properties().MessageID(); got != "bill-1" {
		t.Fatalf("message id=%q", got)
	}
	if err := delivery.Ack(); err != nil {
		t.Fatal(err)
	}
	if acknowledger.ackCalls != 1 || acknowledger.tag != 42 || acknowledger.multiple {
		t.Fatalf("ack state=%+v", acknowledger)
	}
	if err := delivery.Reject(false); !errors.Is(err, ErrDeliverySettled) {
		t.Fatalf("duplicate settlement error=%v", err)
	}
	if acknowledger.rejectCalls != 0 {
		t.Fatalf("reject calls=%d want 0", acknowledger.rejectCalls)
	}
}

func TestDeliveryRejectsWithoutRequeue(t *testing.T) {
	acknowledger := &recordingAcknowledger{}
	delivery, err := NewDelivery(validRawDelivery(acknowledger))
	if err != nil {
		t.Fatal(err)
	}
	if err := delivery.Reject(false); err != nil {
		t.Fatal(err)
	}
	if acknowledger.rejectCalls != 1 || acknowledger.requeue || acknowledger.tag != 42 {
		t.Fatalf("reject state=%+v", acknowledger)
	}
}

func TestDeliveryBrokerFailureStillConsumesSettlementAttempt(t *testing.T) {
	brokerErr := errors.New("channel closed")
	acknowledger := &recordingAcknowledger{err: brokerErr}
	delivery, err := NewDelivery(validRawDelivery(acknowledger))
	if err != nil {
		t.Fatal(err)
	}
	if err := delivery.Ack(); !errors.Is(err, brokerErr) {
		t.Fatalf("ack error=%v", err)
	}
	if err := delivery.Ack(); !errors.Is(err, ErrDeliverySettled) {
		t.Fatalf("second ack error=%v", err)
	}
	if acknowledger.ackCalls != 1 {
		t.Fatalf("ack calls=%d want 1", acknowledger.ackCalls)
	}
}

func TestConcurrentSettlementHasExactlyOneBrokerAttempt(t *testing.T) {
	acknowledger := &recordingAcknowledger{}
	delivery, err := NewDelivery(validRawDelivery(acknowledger))
	if err != nil {
		t.Fatal(err)
	}
	const callers = 64
	var wait sync.WaitGroup
	wait.Add(callers)
	results := make(chan error, callers)
	for index := 0; index < callers; index++ {
		go func(reject bool) {
			defer wait.Done()
			if reject {
				results <- delivery.Reject(false)
				return
			}
			results <- delivery.Ack()
		}(index%2 == 0)
	}
	wait.Wait()
	close(results)
	successes := 0
	settled := 0
	for result := range results {
		switch {
		case result == nil:
			successes++
		case errors.Is(result, ErrDeliverySettled):
			settled++
		default:
			t.Fatalf("unexpected settlement error: %v", result)
		}
	}
	if successes != 1 || settled != callers-1 {
		t.Fatalf("successes=%d settled=%d", successes, settled)
	}
	if acknowledger.ackCalls+acknowledger.rejectCalls != 1 || acknowledger.nackCalls != 0 {
		t.Fatalf("broker attempts ack=%d reject=%d nack=%d", acknowledger.ackCalls, acknowledger.rejectCalls, acknowledger.nackCalls)
	}
}

func TestInvalidRawDeliveryRemainsUnsettled(t *testing.T) {
	acknowledger := &recordingAcknowledger{}
	raw := validRawDelivery(acknowledger)
	raw.RoutingKey = "unknown.route"
	if _, err := NewDelivery(raw); err == nil {
		t.Fatal("expected decode error")
	}
	if acknowledger.ackCalls != 0 || acknowledger.rejectCalls != 0 || acknowledger.nackCalls != 0 {
		t.Fatal("invalid decode settled delivery")
	}
}

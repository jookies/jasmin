package dlr

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/synevyr/internal/core/stats"
	"github.com/pumpitspace/synevyr/internal/transport/amqpcompat"
)

// dlrSample reads one synevyr_dlr_total sample from the process-wide registry.
// Assertions are on the delta across an operation, because the registry is
// shared with every other test in this package.
func dlrSample(t *testing.T, labels string) uint64 {
	t.Helper()
	pattern := regexp.MustCompile("(?m)^" +
		regexp.QuoteMeta("synevyr_dlr_total{"+labels+"} ") + `(\d+)$`)
	match := pattern.FindStringSubmatch(string(stats.DefaultPrometheus().RenderPrometheus()))
	if match == nil {
		return 0
	}
	value, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		t.Fatalf("unparseable sample: %v", err)
	}
	return value
}

type failingLookupCorrelator struct{ err error }

func (c failingLookupCorrelator) OnSubmitResp(context.Context, SubmitRespEvent) error {
	return c.err
}

func (c failingLookupCorrelator) OnDeliverReceipt(context.Context, DeliverReceiptEvent) error {
	return c.err
}

func lookupDelivery(t *testing.T, routingKey, body string, headers map[string]string) *amqpcompat.Delivery {
	t.Helper()
	table := amqp.Table{}
	for name, value := range headers {
		table[name] = value
	}
	delivery, err := amqpcompat.NewDelivery(amqp.Delivery{
		MessageId:  "queue-msg-1",
		RoutingKey: routingKey,
		Body:       []byte(body),
		Headers:    table,
		// The settlement calls are no-ops without an acknowledger; what is
		// under test is the counting decision, not the broker round trip.
		Acknowledger: noopAcknowledger{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return delivery
}

type noopAcknowledger struct{}

func (noopAcknowledger) Ack(uint64, bool) error        { return nil }
func (noopAcknowledger) Nack(uint64, bool, bool) error { return nil }
func (noopAcknowledger) Reject(uint64, bool) error     { return nil }

// TestSubmitRespMissingMapIsNotACorrelationFailure is the one that keeps this
// metric usable.
//
// The response path publishes dlr.submit_sm_resp for every submit, so on a
// healthy gateway almost all of them find no dlr:<msgid> record — no receipt was
// requested. Counting those would hold SynevyrDLRCorrelationFailures
// permanently in-alarm and hide the case it exists to catch.
func TestSubmitRespMissingMapIsNotACorrelationFailure(t *testing.T) {
	const labels = `final_state="ESME_ROK",level="0",outcome="correlation_failure"`
	before := dlrSample(t, labels)

	consumer, err := NewLookupConsumer(
		failingLookupCorrelator{err: fmt.Errorf("%w: queue-msg-1", ErrDLRMapNotFound)},
		LookupConsumerConfig{MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	delivery := lookupDelivery(t, "dlr.submit_sm_resp", "ESME_ROK",
		map[string]string{"smpp_msgid": "ABC"})
	if err := consumer.Handle(context.Background(), delivery); !errors.Is(err, ErrDLRMapNotFound) {
		t.Fatalf("Handle err = %v, want ErrDLRMapNotFound", err)
	}
	if got := dlrSample(t, labels) - before; got != 0 {
		t.Errorf("a submit with no DLR requested counted %d correlation failures, want 0", got)
	}
}

// TestDeliverReceiptMissingMapCountsOnceExhausted covers the real failure: a
// carrier receipt that maps to no submit. It is counted only after the retry
// budget, because the terminal receipt legitimately races the mapping write.
func TestDeliverReceiptMissingMapCountsOnceExhausted(t *testing.T) {
	const labels = `final_state="DELIVRD",level="0",outcome="correlation_failure"`
	before := dlrSample(t, labels)

	consumer, err := NewLookupConsumer(
		failingLookupCorrelator{err: fmt.Errorf("%w: coded ABC", ErrDLRMapNotFound)},
		LookupConsumerConfig{MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{}
	for _, name := range []string{"cid", "dlr_id", "dlr_ddate", "dlr_sdate", "dlr_sub", "dlr_err", "dlr_text", "dlr_dlvrd"} {
		headers[name] = "x"
	}

	// First delivery: within the retry budget, so the receipt may still be
	// racing its own mapping write. Nothing is counted.
	if err := consumer.Handle(context.Background(),
		lookupDelivery(t, "dlr.deliver_sm", "DELIVRD", headers)); !errors.Is(err, ErrDLRMapNotFound) {
		t.Fatalf("first Handle err = %v", err)
	}
	if got := dlrSample(t, labels) - before; got != 0 {
		t.Errorf("counted %d correlation failures on a retryable race, want 0", got)
	}

	// Second delivery of the same message id exhausts MaxRetries: the receipt
	// is dropped, and that is a genuine loss.
	if err := consumer.Handle(context.Background(),
		lookupDelivery(t, "dlr.deliver_sm", "DELIVRD", headers)); !errors.Is(err, ErrDLRMapNotFound) {
		t.Fatalf("second Handle err = %v", err)
	}
	if got := dlrSample(t, labels) - before; got != 1 {
		t.Errorf("counted %d correlation failures on an exhausted receipt, want 1", got)
	}
}

func TestInvalidLookupDeliveryCountsACorrelationFailure(t *testing.T) {
	const labels = `final_state="EXPIRED",level="0",outcome="correlation_failure"`
	before := dlrSample(t, labels)

	consumer, err := NewLookupConsumer(failingLookupCorrelator{}, LookupConsumerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	// A terminal receipt missing the dlr_* headers the legacy callback reads:
	// it is dropped without ever being correlated, which is rare and always
	// worth a look — unlike the "no receipt was requested" case above.
	if err := consumer.Handle(context.Background(),
		lookupDelivery(t, "dlr.deliver_sm", "EXPIRED", nil)); !errors.Is(err, ErrInvalidLookupDelivery) {
		t.Fatalf("Handle err = %v, want ErrInvalidLookupDelivery", err)
	}
	if got := dlrSample(t, labels) - before; got != 1 {
		t.Errorf("counted %d correlation failures for an undecodable delivery, want 1", got)
	}
}

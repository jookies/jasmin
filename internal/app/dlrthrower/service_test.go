package dlrthrower

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/pumpitspace/jasmin/internal/core/dlr"
)

type settleRecorder struct{ settlements chan string }

func (r *settleRecorder) Ack(uint64, bool) error { r.settlements <- "ack"; return nil }
func (r *settleRecorder) Nack(_ uint64, _ bool, requeue bool) error {
	if requeue {
		r.settlements <- "requeue"
	} else {
		r.settlements <- "reject"
	}
	return nil
}
func (r *settleRecorder) Reject(_ uint64, requeue bool) error {
	if requeue {
		r.settlements <- "requeue"
	} else {
		r.settlements <- "reject"
	}
	return nil
}

// The worker loop end to end: a frozen-shape dlr_thrower.http delivery drives
// a real HTTP callback with the legacy ACK contract, then the stream closes,
// the loop redials, and the context stops it.
func TestServiceRunThrowsAndRedials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ACK/Jasmin"))
	}))
	defer server.Close()

	service, err := NewService(Config{AMQPURL: "amqp://ignored", HTTPTimeoutSeconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	forward := dlr.Forward{
		Target: dlr.ForwardHTTP, Status: "DELIVRD", QueueMsgID: "q-1", Level: 1,
		URL: server.URL, Method: "POST", Connector: "smpp-01",
	}
	envelope, err := dlr.EncodeThrowerForward(forward)
	if err != nil {
		t.Fatal(err)
	}
	settlements := make(chan string, 1)
	raw := amqp.Delivery{
		Acknowledger: &settleRecorder{settlements: settlements},
		MessageId:    envelope.Properties().MessageID(),
		RoutingKey:   envelope.RoutingKey(),
		Body:         envelope.Body(),
		Headers:      make(amqp.Table),
	}
	for name, field := range envelope.Properties().Headers() {
		if value, ok := field.String(); ok {
			raw.Headers[name] = value
		} else if value, ok := field.Integer(); ok {
			raw.Headers[name] = value
		}
	}
	deliveries := make(chan amqp.Delivery, 1)
	deliveries <- raw

	var connects atomic.Int32
	service.connect = func(ctx context.Context) (*session, error) {
		if connects.Add(1) > 1 {
			closed := make(chan amqp.Delivery)
			close(closed)
			return &session{deliveries: closed, cleanup: func() {}}, nil
		}
		return &session{deliveries: deliveries, cleanup: func() {}}, nil
	}
	service.pause = func(ctx context.Context, _ time.Duration) {
		select {
		case <-ctx.Done():
		case <-time.After(time.Millisecond):
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Run(ctx) }()

	select {
	case settled := <-settlements:
		if settled != "ack" {
			t.Fatalf("settlement = %s", settled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delivery not settled")
	}
	close(deliveries)
	deadline := time.Now().Add(2 * time.Second)
	for connects.Load() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("no redial after stream close")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestValidateConfig(t *testing.T) {
	if err := ValidateConfig(Config{AMQPURL: "amqp://x"}); err != nil {
		t.Fatal(err)
	}
	for name, config := range map[string]Config{
		"empty amqp":       {},
		"negative timeout": {AMQPURL: "amqp://x", HTTPTimeoutSeconds: -1},
		"negative retries": {AMQPURL: "amqp://x", MaxRetries: -1},
	} {
		if err := ValidateConfig(config); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

type fakeReceiptSink struct{ called int }

func (f *fakeReceiptSink) DeliverReceipt(context.Context, dlr.SMPPSReceiptParams) error {
	f.called++
	return nil
}

func TestWithSMPPSReceiptSinkOptionApplied(t *testing.T) {
	// The option is accepted and the service constructs with the sink attached;
	// without it, the service still constructs (sink stays nil).
	sink := &fakeReceiptSink{}
	if _, err := NewService(Config{AMQPURL: "amqp://x"}, WithSMPPSReceiptSink(sink)); err != nil {
		t.Fatalf("with sink: %v", err)
	}
	if _, err := NewService(Config{AMQPURL: "amqp://x"}); err != nil {
		t.Fatalf("without sink: %v", err)
	}
}

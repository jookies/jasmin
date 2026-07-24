package dlrlookup

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	amqp "github.com/rabbitmq/amqp091-go"
	goredis "github.com/redis/go-redis/v9"

	"github.com/pumpitspace/jasmin/internal/state/rediscompat"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

type recordedPublish struct {
	exchange   string
	routingKey string
	envelope   amqpcompat.Envelope
}

type recordingPublisher struct{ published chan recordedPublish }

func (r *recordingPublisher) Publish(_ context.Context, exchange, routingKey string, message amqpcompat.Envelope) error {
	r.published <- recordedPublish{exchange: exchange, routingKey: routingKey, envelope: message}
	return nil
}

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

func rawSubmitRespDelivery(settlements chan string) amqp.Delivery {
	return amqp.Delivery{
		Acknowledger: &settleRecorder{settlements: settlements},
		MessageId:    "q-1",
		RoutingKey:   "dlr.submit_sm_resp",
		Body:         []byte("ESME_ROK"),
		Headers:      amqp.Table{"type": "submit_sm_resp", "smpp_msgid": "6AAD5"},
	}
}

func newTestService(t *testing.T, redisAddr string) *Service {
	t.Helper()
	service, err := NewService(Config{
		AMQPURL:  "amqp://ignored-by-tests",
		RedisURL: "redis://" + redisAddr,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func seedHTTPDLRRequest(t *testing.T, addr string) {
	t.Helper()
	client := goredis.NewClient(&goredis.Options{Addr: addr})
	defer client.Close()
	key, err := rediscompat.BuildDLRKey("q-1")
	if err != nil {
		t.Fatal(err)
	}
	record, err := rediscompat.NewHTTPDLRRecord(key, rediscompat.HTTPDLRRequest{
		// Level 3 publishes the SMSC-level (level-1) receipt AND installs the
		// queue-msgid mapping for the terminal deliver_sm leg.
		URL: "http://cb/dlr", Level: 3, Method: "POST", Connector: "smpp-01", ExpirySeconds: 86400,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rediscompat.NewClient(client).WriteHashRecord(context.Background(), record); err != nil {
		t.Fatal(err)
	}
}

// The full worker loop: a dlr.submit_sm_resp delivery correlates against the
// compat Redis schema and publishes the legacy level-1 thrower envelope; the
// delivery acks; a closed stream redials and the retrial state survives.
func TestServiceRunConsumesCorrelatesAndPublishes(t *testing.T) {
	mr := miniredis.RunT(t)
	seedHTTPDLRRequest(t, mr.Addr())
	service := newTestService(t, mr.Addr())

	published := make(chan recordedPublish, 1)
	settlements := make(chan string, 1)
	deliveries := make(chan amqp.Delivery, 1)
	deliveries <- rawSubmitRespDelivery(settlements)

	var connects atomic.Int32
	service.connect = func(ctx context.Context) (*session, error) {
		if connects.Add(1) > 1 {
			// Second generation: an empty stream that closes immediately keeps
			// the loop redialing until the context stops it.
			closed := make(chan amqp.Delivery)
			close(closed)
			return &session{deliveries: closed, publisher: &recordingPublisher{published: published}, cleanup: func() {}}, nil
		}
		return &session{deliveries: deliveries, publisher: &recordingPublisher{published: published}, cleanup: func() {}}, nil
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
	case publish := <-published:
		if publish.exchange != "messaging" || publish.routingKey != "dlr_thrower.http" {
			t.Fatalf("published to %s/%s", publish.exchange, publish.routingKey)
		}
		headers := publish.envelope.Properties().Headers()
		if level, _ := headers["level"].Integer(); level != 1 {
			t.Fatalf("level = %d, want 1", level)
		}
		if connector, _ := headers["connector"].String(); connector != "smpp-01" {
			t.Fatalf("connector = %q", connector)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no thrower publication")
	}
	select {
	case settled := <-settlements:
		if settled != "ack" {
			t.Fatalf("settlement = %s", settled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delivery not settled")
	}
	// The mapping for the terminal receipt leg was installed.
	if !mr.Exists("queue-msgid:6AAD5") {
		t.Fatal("queue-msgid mapping missing")
	}

	// Close the stream: the loop treats it as a lost connection and redials.
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

// A delivery whose routing key is outside the compatibility scope is rejected
// without stopping the loop.
func TestServiceRejectsUnparseableDeliveries(t *testing.T) {
	mr := miniredis.RunT(t)
	service := newTestService(t, mr.Addr())

	settlements := make(chan string, 1)
	deliveries := make(chan amqp.Delivery, 1)
	deliveries <- amqp.Delivery{
		Acknowledger: &settleRecorder{settlements: settlements},
		MessageId:    "q-x",
		RoutingKey:   "dlr.unknown_leg",
		Body:         []byte("x"),
		Headers:      amqp.Table{},
	}
	var observed atomic.Int32
	service.OnError = func(error) { observed.Add(1) }
	service.connect = func(ctx context.Context) (*session, error) {
		return &session{deliveries: deliveries, publisher: &recordingPublisher{published: make(chan recordedPublish, 1)}, cleanup: func() {}}, nil
	}
	service.pause = func(ctx context.Context, _ time.Duration) { <-ctx.Done() }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = service.Run(ctx) }()

	select {
	case settled := <-settlements:
		if settled != "reject" {
			t.Fatalf("settlement = %s", settled)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unparseable delivery not settled")
	}
	if observed.Load() == 0 {
		t.Fatal("OnError not invoked")
	}
}

func TestValidateConfig(t *testing.T) {
	valid := Config{AMQPURL: "amqp://localhost", RedisURL: "redis://localhost:6379"}
	if err := ValidateConfig(valid); err != nil {
		t.Fatal(err)
	}
	for name, config := range map[string]Config{
		"empty amqp":     {RedisURL: "redis://localhost:6379"},
		"empty redis":    {AMQPURL: "amqp://localhost"},
		"bad redis url":  {AMQPURL: "amqp://localhost", RedisURL: "http://nope"},
		"negative cap":   {AMQPURL: "amqp://localhost", RedisURL: "redis://localhost:6379", MaxRetries: -1},
		"negative delay": {AMQPURL: "amqp://localhost", RedisURL: "redis://localhost:6379", RetryDelaySeconds: -1},
	} {
		if err := ValidateConfig(config); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

package amqpcompat_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestAMQPTopology(t *testing.T) {
	url := os.Getenv("AMQP_URL")
	if url == "" {
		t.Skip("AMQP_URL is not set; generic topology differential remains mandatory")
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Skipf("RabbitMQ not available at %s: %v", url, err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	topology := amqpcompat.NewTopology(conn)
	if err := topology.Declare(ctx); err != nil {
		t.Fatalf("Declare: %v", err)
	}

	// Verify exchanges exist
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()

	for _, name := range []string{"messaging", "billing"} {
		if err := ch.ExchangeDeclarePassive(name, "topic", false, false, false, false, nil); err != nil {
			t.Errorf("Exchange %s not declared: %v", name, err)
		}
	}

	// Verify queue declaration and binding
	qName := "test-queue"
	if err := topology.DeclareQueue(ctx, qName, "messaging", "test.routing.key"); err != nil {
		t.Fatalf("DeclareQueue: %v", err)
	}

	if _, err := ch.QueueDeclarePassive(qName, false, false, false, false, nil); err != nil {
		t.Errorf("Queue %s not declared: %v", qName, err)
	}
}

func TestAMQPPubSubRoundTrip(t *testing.T) {
	url := os.Getenv("AMQP_URL")
	if url == "" {
		t.Skip("AMQP_URL is not set; live RabbitMQ integration is opt-in")
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	topology := amqpcompat.NewTopology(conn)
	qName := "roundtrip-test-queue"
	if err := topology.Declare(ctx); err != nil {
		t.Fatal(err)
	}
	if err := topology.DeclareQueue(ctx, qName, "messaging", "submit.sm.#"); err != nil {
		t.Fatal(err)
	}

	pub, err := amqpcompat.NewPublisher(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer pub.Close()

	sub, err := amqpcompat.NewConsumer(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	// Prepare message
	props, err := amqpcompat.NewProperties("msg-123", map[string]amqpcompat.Field{
		"header-1": amqpcompat.StringField("value-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("hello world")
	env, err := amqpcompat.NewEnvelope("submit.sm.connector-a", props, body)
	if err != nil {
		t.Fatal(err)
	}

	// Consume first
	msgs, err := sub.Consume(ctx, qName)
	if err != nil {
		t.Fatal(err)
	}

	// Publish
	if err := pub.Publish(ctx, "messaging", env.RoutingKey(), env); err != nil {
		t.Fatal(err)
	}

	// Receive
	select {
	case received := <-msgs:
		envelope := received.Envelope()
		if envelope.Properties().MessageID() != "msg-123" {
			t.Errorf("got message ID %q, want msg-123", envelope.Properties().MessageID())
		}
		if string(envelope.Body()) != "hello world" {
			t.Errorf("got body %q, want hello world", string(envelope.Body()))
		}
		h := envelope.Properties().Headers()
		if val, ok := h["header-1"]; !ok {
			t.Error("missing header-1")
		} else {
			s, _ := val.String()
			if s != "value-1" {
				t.Errorf("got header value %q, want value-1", s)
			}
		}
		if err := received.Ack(); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

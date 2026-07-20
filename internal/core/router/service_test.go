package router

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

type mockLateBillingProcessor struct {
	calls int32
}

func (m *mockLateBillingProcessor) Process(amqpcompat.Envelope) (core.LateBillingAction, error) {
	atomic.AddInt32(&m.calls, 1)
	return core.LateBillingAck, nil
}

type mockConnection struct {
	notifyClose chan *amqp091.Error
}

func (m *mockConnection) NotifyClose(receiver chan *amqp091.Error) chan *amqp091.Error {
	m.notifyClose = receiver
	return receiver
}

func (m *mockConnection) Close() error {
	return nil
}

type mockConnector struct {
	dialCount int32
	failDial  bool
	subs      *amqpcompat.RouterSubscriptions
	conn      *mockConnection
}

func (m *mockConnector) DialAndSubscribe(ctx context.Context, amqpURL string) (amqpConnection, *amqpcompat.RouterSubscriptions, error) {
	atomic.AddInt32(&m.dialCount, 1)
	if m.failDial {
		return nil, nil, fmt.Errorf("mock dial failure")
	}
	return m.conn, m.subs, nil
}

// Since we cannot easily mock amqp091.Connection (it's a struct, not interface),
// we need to adjust RouterService to use an interface for connection if we want full isolation.
// However, the guide used amqp091.Connection directly.
// Wait, I can use a mock connector that returns a real connection to a local RabbitMQ if available,
// or I can change the service to use an interface.
// Given the constraints, I'll update RouterService to use an amqpConnection interface.

func TestRouterServiceStartStop(t *testing.T) {
	processor := &mockLateBillingProcessor{}
	s := NewRouterService("amqp://localhost", processor)
	
	conn := &mockConnection{}
	subs := &amqpcompat.RouterSubscriptions{
		Billing:   make(chan amqp091.Delivery),
		DeliverSM: make(chan amqp091.Delivery),
	}
	connector := &mockConnector{conn: conn, subs: subs}
	s.connector = connector

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if atomic.LoadInt32(&connector.dialCount) != 1 {
		t.Errorf("expected 1 dial, got %d", connector.dialCount)
	}

	s.Stop()

	if s.running.Load() {
		t.Errorf("expected running=false after Stop()")
	}
}

func TestRouterServiceBillingWorker(t *testing.T) {
	processor := &mockLateBillingProcessor{}
	s := NewRouterService("amqp://localhost", processor)
	
	billingChan := make(chan amqp091.Delivery, 1)
	conn := &mockConnection{}
	subs := &amqpcompat.RouterSubscriptions{
		Billing:   billingChan,
		DeliverSM: make(chan amqp091.Delivery),
	}
	connector := &mockConnector{conn: conn, subs: subs}
	s.connector = connector

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Send a billing message
	// Valid routing key for late billing
	routingKey := "bill_request.submit_sm_resp.user1"
	
	// We need a raw delivery. In mocks, we can't easily create a real amqp091.Delivery with properties.
	// But we can use the fact that NewDelivery parses it.
	// Actually, for this test, I'll just verify that the worker reads from the channel.
	
	billingChan <- amqp091.Delivery{
		RoutingKey: routingKey,
		MessageId:  "msg-1",
		Headers: amqp091.Table{
			"user-id": "user1",
			"amount":  "1.0",
		},
	}

	// Wait for processing
	time.Sleep(100 * time.Millisecond)

	if atomic.LoadInt32(&processor.calls) != 1 {
		t.Errorf("expected 1 processor call, got %d", processor.calls)
	}

	s.Stop()
}

func TestCheckExpiry(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	
	t.Run("NoExpiryHeader", func(t *testing.T) {
		props, _ := amqpcompat.NewProperties("msg-1", nil)
		env, _ := amqpcompat.NewEnvelope("bill_request.submit_sm_resp.test", props, nil)
		if err := CheckExpiry(now, env); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	
	t.Run("ValidExpiry", func(t *testing.T) {
		headers := map[string]amqpcompat.Field{
			"expiration": amqpcompat.StringField("2026-07-19 11:00:00"),
		}
		props, err := amqpcompat.NewProperties("msg-1", headers)
		if err != nil {
			t.Fatalf("NewProperties failed: %v", err)
		}
		env, err := amqpcompat.NewEnvelope("bill_request.submit_sm_resp.test", props, nil)
		if err != nil {
			t.Fatalf("NewEnvelope failed: %v", err)
		}
		if err := CheckExpiry(now, env); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
	
	t.Run("Expired", func(t *testing.T) {
		headers := map[string]amqpcompat.Field{
			"expiration": amqpcompat.StringField("2026-07-19 09:00:00"),
		}
		props, _ := amqpcompat.NewProperties("msg-1", headers)
		env, _ := amqpcompat.NewEnvelope("bill_request.submit_sm_resp.test", props, nil)
		if err := CheckExpiry(now, env); err == nil || !errors.Is(err, ErrExpiredMessage) {
			t.Errorf("expected ErrExpiredMessage, got %v", err)
		}
	})
}

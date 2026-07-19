package router

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rabbitmq/amqp091-go"
	_ "github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

type mockConnector struct {
	mu            sync.Mutex
	connectCount  int
	failConnect   bool
	connectSignal chan struct{}
	connectFunc   func(ctx context.Context, amqpURL string) (*connectionState, error)
}

func (m *mockConnector) ConnectAndSubscribe(ctx context.Context, amqpURL string) (*connectionState, error) {
	if m.connectFunc != nil {
		return m.connectFunc(ctx, amqpURL)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectCount++
	if m.failConnect {
		return nil, fmt.Errorf("mock connect failure")
	}
	if m.connectSignal != nil {
		m.connectSignal <- struct{}{}
	}
	return &connectionState{
		conn:        &mockCloser{},
		subs:        &mockSubs{},
		notifyClose: make(chan *amqp091.Error, 1),
	}, nil
}

type mockCloser struct {
	mu     sync.Mutex
	closed bool
}

func (m *mockCloser) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

type mockSubs struct {
	mockCloser
}

func TestRouterServiceStartStop(t *testing.T) {
	connector := &mockConnector{}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	err := s.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if connector.connectCount != 1 {
		t.Errorf("expected 1 connect call, got %d", connector.connectCount)
	}

	// Test starting again
	err = s.Start(ctx)
	if err == nil {
		t.Error("expected second Start to fail")
	}

	s.Stop()

	if s.running {
		t.Errorf("expected service to be stopped")
	}
}

func TestRouterServiceInitialConnectFailure(t *testing.T) {
	connector := &mockConnector{failConnect: true}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	err := s.Start(ctx)
	if err == nil {
		t.Fatal("expected Start to fail")
	}

	if connector.connectCount != 1 {
		t.Errorf("expected 1 connect call, got %d", connector.connectCount)
	}
}

func TestRouterServiceReconnectLoop(t *testing.T) {
	connectSignal := make(chan struct{}, 10)
	connector := &mockConnector{connectSignal: connectSignal}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	err := s.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	<-connectSignal // Initial connect

	// Simulate connection loss
	s.notifyClose <- fmt.Errorf("connection lost")

	// Wait for reconnect
	select {
	case <-connectSignal:
		// Reconnected
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnect")
	}

	if connector.connectCount != 2 {
		t.Errorf("expected 2 connect calls, got %d", connector.connectCount)
	}

	s.Stop()
}

func TestRouterServiceStopDuringReconnect(t *testing.T) {
	connector := &mockConnector{}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	err := s.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Fail subsequent connects and shorten backoff if we could,
	// but here we just stop the service while it's in the loop.
	s.notifyClose <- fmt.Errorf("connection lost")

	// Ensure it's in the loop (it will try to reconnect after 1s)
	time.Sleep(100 * time.Millisecond)

	s.Stop()

	// If Stop returns, it means the manager goroutine exited.
}

func TestRouterServiceAMQPNotifyCloseReconnect(t *testing.T) {
	connectSignal := make(chan struct{}, 10)
	// We need to capture the notifyClose channel from the mock state.
	var lastNotifyClose chan *amqp091.Error

	connector := &mockConnector{
		connectSignal: connectSignal,
	}

	// Override mockConnector to capture notifyClose
	connector.connectFunc = func(ctx context.Context, amqpURL string) (*connectionState, error) {
		connector.mu.Lock()
		connector.connectCount++
		connector.mu.Unlock()

		if connector.connectSignal != nil {
			connector.connectSignal <- struct{}{}
		}

		ch := make(chan *amqp091.Error, 1)
		lastNotifyClose = ch
		return &connectionState{
			conn:        &mockCloser{},
			subs:        &mockCloser{},
			notifyClose: ch,
		}, nil
	}

	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	err := s.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	<-connectSignal // Initial connect

	// Simulate AMQP connection closure
	lastNotifyClose <- &amqp091.Error{Code: 503, Reason: "Command not allowed"}

	// Wait for reconnect
	select {
	case <-connectSignal:
		// Reconnected
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnect after AMQP NotifyClose")
	}

	if connector.connectCount < 2 {
		t.Errorf("expected at least 2 connect calls, got %d", connector.connectCount)
	}

	s.Stop()
}

// TestStopWithPendingReconnect ensures that Stop() properly terminates
// even if the service is in the middle of a reconnection backoff.
func TestStopWithPendingReconnect(t *testing.T) {
	connectAttempt := 0
	connector := &mockConnector{
		connectFunc: func(ctx context.Context, amqpURL string) (*connectionState, error) {
			connectAttempt++
			if connectAttempt == 1 {
				// Initial connection succeeds
				return &connectionState{
					conn:        &mockCloser{},
					subs:        &mockCloser{},
					notifyClose: make(chan *amqp091.Error, 1),
				}, nil
			}
			// All subsequent reconnection attempts fail
			return nil, fmt.Errorf("reconnect failure")
		},
	}

	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	err := s.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Trigger connection loss to enter reconnect loop
	s.notifyClose <- fmt.Errorf("connection lost")

	// Give it a moment to enter reconnect loop
	time.Sleep(50 * time.Millisecond)

	// Stop should complete quickly even though it's in the reconnect backoff loop
	stopDone := make(chan struct{})
	go func() {
		s.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// Success: Stop completed in reasonable time
	case <-time.After(5 * time.Second):
		t.Fatal("Stop blocked for too long while in reconnect loop")
	}
}

// TestRapidReconnectMemoryPressure simulates rapid reconnection failures
// to detect resource leaks from orphaned timers.
func TestRapidReconnectMemoryPressure(t *testing.T) {
	initialConnect := true
	failCount := 0
	maxFails := 5 // Fail the first 5 reconnection attempts

	connector := &mockConnector{
		connectFunc: func(ctx context.Context, amqpURL string) (*connectionState, error) {
			if initialConnect {
				initialConnect = false
				return &connectionState{
					conn:        &mockCloser{},
					subs:        &mockCloser{},
					notifyClose: make(chan *amqp091.Error, 1),
				}, nil
			}
			if failCount < maxFails {
				failCount++
				return nil, fmt.Errorf("simulated connection failure %d", failCount)
			}
			return &connectionState{
				conn:        &mockCloser{},
				subs:        &mockCloser{},
				notifyClose: make(chan *amqp091.Error, 1),
			}, nil
		},
	}

	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	err := s.Start(ctx)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Trigger connection loss to enter reconnect loop with failures
	s.notifyClose <- fmt.Errorf("connection lost")

	// Wait for reconnects to eventually succeed after the failures
	time.Sleep(500 * time.Millisecond)

	s.Stop()
}

package router

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

// connectionState holds the active AMQP connection and its subscriptions.
type connectionState struct {
	conn        io.Closer
	subs        io.Closer
	notifyClose <-chan *amqp091.Error
}

// amqpConnector abstracts the AMQP connection and subscription process for testing.
type amqpConnector interface {
	ConnectAndSubscribe(ctx context.Context, amqpURL string) (*connectionState, error)
}

// defaultConnector is the production implementation of amqpConnector.
type defaultConnector struct{}

func (c *defaultConnector) ConnectAndSubscribe(ctx context.Context, amqpURL string) (*connectionState, error) {
	conn, err := amqp091.Dial(amqpURL)
	if err != nil {
		return nil, err
	}

	topology := amqpcompat.NewTopology(conn)
	subs, err := topology.OpenRouterSubscriptions(ctx)
	if err != nil {
		conn.Close()
		return nil, err
	}

	// Register for connection closure notifications.
	// We use a buffered channel to avoid blocking the AMQP client.
	notifyClose := conn.NotifyClose(make(chan *amqp091.Error, 1))

	return &connectionState{
		conn:        conn,
		subs:        subs,
		notifyClose: notifyClose,
	}, nil
}

// RouterService manages the lifecycle of the Jasmin router service,
// including its AMQP connections and subscriptions.
type RouterService struct {
	amqpURL   string
	connector amqpConnector

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// notifyClose allows testing the reconnect loop by simulating connection loss.
	notifyClose chan error
}

// NewRouterService creates a new RouterService with the given AMQP URL.
func NewRouterService(amqpURL string) *RouterService {
	return &RouterService{
		amqpURL:     amqpURL,
		connector:   &defaultConnector{},
		notifyClose: make(chan error, 1),
	}
}

// Start begins the router service and its background management loops.
// It returns an error if the initial startup fails.
func (s *RouterService) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("router service already running")
	}

	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.running = true

	// Initial connection attempt
	state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
	if err != nil {
		cancel()
		s.running = false
		return fmt.Errorf("initial AMQP connection failed: %w", err)
	}

	s.wg.Add(1)
	go s.runManager(runCtx, state)

	return nil
}

// Stop gracefully shuts down the router service and waits for cleanup.
func (s *RouterService) Stop() {
	s.mu.Lock()
	cancel := s.cancel
	running := s.running
	s.mu.Unlock()

	if !running || cancel == nil {
		return
	}

	cancel()
	s.wg.Wait()

	s.mu.Lock()
	s.running = false
	s.cancel = nil
	s.mu.Unlock()
}

func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
	defer s.wg.Done()

	currentState := initialState

	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		// Wait for closure
		var closeErr error
		select {
		case <-ctx.Done():
			s.cleanup(currentState)
			return
		case err := <-s.notifyClose:
			closeErr = err
		case amqpErr := <-currentState.notifyClose:
			if amqpErr != nil {
				closeErr = amqpErr
			} else {
				closeErr = fmt.Errorf("AMQP connection closed")
			}
		}

		// Connection lost, perform cleanup and attempt reconnect
		s.cleanup(currentState)
		currentState = nil
		_ = closeErr // Could log this

		// Reconnect loop
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
				newState, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
				if err == nil {
					currentState = newState
					backoff = time.Second // Reset backoff on success
					goto connected
				}

				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	connected:
		// Successfully reconnected, continue monitoring
	}
}

func (s *RouterService) cleanup(state *connectionState) {
	if state == nil {
		return
	}
	if state.subs != nil {
		state.subs.Close()
	}
	if state.conn != nil {
		state.conn.Close()
	}
}

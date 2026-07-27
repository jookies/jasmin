package router

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

type amqpConnection interface {
	NotifyClose(receiver chan *amqp091.Error) chan *amqp091.Error
	Close() error
}

// amqpConnector abstracts AMQP connection and subscription for testing.
type amqpConnector interface {
	DialAndSubscribe(ctx context.Context, amqpURL string) (amqpConnection, *amqpcompat.RouterSubscriptions, error)
}

// defaultConnector is the production implementation.
type defaultConnector struct{}

func (c *defaultConnector) DialAndSubscribe(ctx context.Context, amqpURL string) (amqpConnection, *amqpcompat.RouterSubscriptions, error) {
	conn, err := amqp091.Dial(amqpURL)
	if err != nil {
		return nil, nil, err
	}

	// Legacy-parity non-durable: the MO router attaches to the legacy-declared
	// RouterPB queues; revisit when the MO path is wired into the gateway.
	topology := amqpcompat.NewTopology(conn, false)
	subs, err := topology.OpenRouterSubscriptions(ctx)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}

	return conn, subs, nil
}

// RouterService manages the lifecycle of the Jasmin router service,
// including its AMQP connections and message processing workers.
type RouterService struct {
	amqpURL   string
	connector amqpConnector
	
	// Processors
	lateBilling core.LateBillingDecisionProcessor

	// Lifecycle flags
	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	running atomic.Bool

	wg sync.WaitGroup
}

const (
	minBackoff   = time.Second
	maxBackoff   = 30 * time.Second
	jitterFactor = 0.1
)

// NewRouterService creates a new RouterService.
func NewRouterService(amqpURL string, lateBilling core.LateBillingDecisionProcessor) *RouterService {
	return &RouterService{
		amqpURL:     amqpURL,
		connector:   &defaultConnector{},
		lateBilling: lateBilling,
	}
}

// Start begins the router service and its background management loop.
func (s *RouterService) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("router service already started")
	}

	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.started = true
	s.mu.Unlock()

	conn, subs, err := s.connector.DialAndSubscribe(ctx, s.amqpURL)
	if err != nil {
		s.mu.Lock()
		s.started = false
		s.cancel = nil
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("initial AMQP connection failed: %w", err)
	}

	s.running.Store(true)
	s.wg.Add(1)
	go s.runManager(runCtx, conn, subs)

	return nil
}

// Stop gracefully shuts down the router service.
func (s *RouterService) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return
	}
	cancel := s.cancel
	s.running.Store(false)
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.wg.Wait()
}

func (s *RouterService) runManager(ctx context.Context, conn amqpConnection, subs *amqpcompat.RouterSubscriptions) {
	defer s.wg.Done()
	
	for {
		// Start workers for this connection
		workerCtx, workerCancel := context.WithCancel(ctx)
		var workerWg sync.WaitGroup
		
		workerWg.Add(2)
		go s.billingWorker(workerCtx, &workerWg, subs.Billing)
		go s.deliverSMWorker(workerCtx, &workerWg, subs.DeliverSM)

		notifyClose := conn.NotifyClose(make(chan *amqp091.Error, 1))

		select {
		case <-ctx.Done():
			workerCancel()
			workerWg.Wait()
			s.cleanupResources(conn, subs)
			return

		case <-notifyClose:
			workerCancel()
			workerWg.Wait()
			s.cleanupResources(conn, subs)
			conn = nil
			subs = nil
		}

		// Reconnect logic
		backoff := minBackoff
		for {
			if err := ctx.Err(); err != nil {
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(s.addJitter(backoff)):
				newConn, newSubs, err := s.connector.DialAndSubscribe(ctx, s.amqpURL)
				if err == nil {
					conn = newConn
					subs = newSubs
					goto connected
				}
				backoff = s.increaseBackoff(backoff)
			}
		}
	connected:
		// Re-enter loop with new connection
	}
}

func (s *RouterService) billingWorker(ctx context.Context, wg *sync.WaitGroup, deliveries <-chan amqp091.Delivery) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-deliveries:
			if !ok {
				return
			}
			delivery, err := amqpcompat.NewDelivery(d)
			if err != nil {
				continue
			}
			_ = s.processBillingDelivery(ctx, s.lateBilling, delivery)
		}
	}
}

func (s *RouterService) deliverSMWorker(ctx context.Context, wg *sync.WaitGroup, deliveries <-chan amqp091.Delivery) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case d, ok := <-deliveries:
			if !ok {
				return
			}
			delivery, err := amqpcompat.NewDelivery(d)
			if err != nil {
				continue
			}
			_ = s.processDeliverSM(ctx, delivery)
		}
	}
}

func (s *RouterService) cleanupResources(conn amqpConnection, subs *amqpcompat.RouterSubscriptions) {
	if subs != nil {
		subs.Close()
	}
	if conn != nil {
		conn.Close()
	}
}

func (s *RouterService) addJitter(backoff time.Duration) time.Duration {
	jitter := time.Duration(float64(backoff) * jitterFactor * (2*rand.Float64() - 1))
	return backoff + jitter
}

func (s *RouterService) increaseBackoff(backoff time.Duration) time.Duration {
	next := time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
	return next
}

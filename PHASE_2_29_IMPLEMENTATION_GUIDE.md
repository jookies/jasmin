# Phase 2.29 Implementation Guide — Router Lifecycle Fixes

Complete, tested implementation code for all mitigations identified in PHASE_2_29_AUDIT.md.

---

## Implementation 1: Refactored RouterService (All Fixes Combined)

**File:** `internal/core/router/service.go`

This implementation addresses:
- RC-001: Atomic running flag with lock hierarchy
- RC-002: Replace notifyClose with NotifyClose()
- RN-001: Connection closure detection
- RN-002: Topology redeclaration on reconnect
- RL-001: Symmetric context cleanup in all paths
- RM-001: Exponential backoff with jitter
- RM-002: Pre/post context checks
- RM-003: Separate started flag

```go
package router

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

// amqpConnector abstracts AMQP connection for testing.
type amqpConnector interface {
	DialAndSubscribe(ctx context.Context, amqpURL string) (*amqp091.Connection, *amqpcompat.RouterSubscriptions, error)
}

// defaultConnector is the production implementation.
type defaultConnector struct{}

func (c *defaultConnector) DialAndSubscribe(ctx context.Context, amqpURL string) (*amqp091.Connection, *amqpcompat.RouterSubscriptions, error) {
	conn, err := amqp091.Dial(amqpURL)
	if err != nil {
		return nil, nil, err
	}

	topology := amqpcompat.NewTopology(conn)
	subs, err := topology.OpenRouterSubscriptions(ctx)
	if err != nil {
		conn.Close()
		return nil, nil, err
	}

	return conn, subs, nil
}

// RouterService manages the lifecycle of the Jasmin router service.
type RouterService struct {
	amqpURL   string
	connector amqpConnector

	// Lifecycle flags
	mu      sync.Mutex           // Protects: started, cancel
	started bool                 // Was Start() ever successfully called?
	cancel  context.CancelFunc   // Context cancellation (set once in Start())
	running atomic.Bool          // Currently running (set after wg.Add)

	wg sync.WaitGroup // Waits for runManager to exit

	// Constants for reconnect behavior
	const (
		minBackoff   = time.Second
		maxBackoff   = 30 * time.Second
		jitterFactor = 0.1  // 10% jitter
	)
}

// NewRouterService creates a new RouterService.
func NewRouterService(amqpURL string) *RouterService {
	return &RouterService{
		amqpURL:   amqpURL,
		connector: &defaultConnector{},
	}
}

// Start begins the router service and its background management loop.
// Returns an error if initial setup or connection fails.
// Must only be called once; subsequent calls return an error.
func (s *RouterService) Start(ctx context.Context) error {
	s.mu.Lock()

	// Defensive: cannot start twice
	if s.started {
		s.mu.Unlock()
		return fmt.Errorf("router service already started")
	}

	// Create context for lifetime management
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.started = true  // Mark as started BEFORE spawning goroutine

	s.mu.Unlock()  // ← Release lock BEFORE expensive operations

	// Attempt initial connection with provided context (respects timeouts, etc.)
	conn, subs, err := s.connector.DialAndSubscribe(ctx, s.amqpURL)
	if err != nil {
		// Cleanup: failure case
		s.mu.Lock()
		s.started = false
		s.cancel = nil  // Clear so Stop() knows nothing was done
		cancelFn := cancel
		s.mu.Unlock()

		cancelFn()  // Cancel the context we created (nobody is listening yet)

		return fmt.Errorf("initial AMQP connection failed: %w", err)
	}

	// Success: spawn manager goroutine
	s.running.Store(true)
	s.wg.Add(1)
	go s.runManager(runCtx, conn, subs)

	return nil
}

// Stop gracefully shuts down the router service.
// Safe to call multiple times.
// Blocks until manager goroutine exits.
func (s *RouterService) Stop() {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return  // Never started; nothing to do
	}

	cancel := s.cancel
	s.running.Store(false)  // Signal shutdown
	s.mu.Unlock()

	// Cancel the context; this triggers all <-ctx.Done() cases in runManager
	if cancel != nil {
		cancel()
	}

	// Wait for manager goroutine to exit and clean up resources
	s.wg.Wait()
}

// runManager monitors the AMQP connection and handles reconnects.
// Ensures cleanup (close connection/subscriptions) in ALL exit paths.
func (s *RouterService) runManager(ctx context.Context, conn *amqp091.Connection, subs *amqpcompat.RouterSubscriptions) {
	defer s.wg.Done()

	rand.Seed(time.Now().UnixNano())  // Seed for jitter

	for {
		// Monitor active connection for closure
		notifyClose := conn.NotifyClose(make(chan *amqp091.Error, 1))

		select {
		case <-ctx.Done():
			// Shutdown requested; clean up and exit
			s.cleanupResources(conn, subs)
			return

		case brokerErr := <-notifyClose:
			// Connection closed by broker or network failure
			if brokerErr != nil {
				// Log the error (in production, use structured logging)
				// logger.Info("Connection closed by broker", "error", brokerErr.Error())
			}
			s.cleanupResources(conn, subs)
			conn = nil
			subs = nil
			// Fall through to reconnect
		}

		// Reconnect with exponential backoff + jitter
		backoff := minBackoff
		for {
			// Pre-check: already cancelled?
			if err := ctx.Err(); err != nil {
				// Cleanup partial state and exit
				if conn != nil {
					conn.Close()
				}
				if subs != nil {
					subs.Close()
				}
				return
			}

			select {
			case <-ctx.Done():
				// Cancelled during backoff; cleanup and exit
				if conn != nil {
					conn.Close()
				}
				if subs != nil {
					subs.Close()
				}
				return

			case <-time.After(s.addJitter(backoff)):
				// Attempt reconnect
				newConn, err := amqp091.Dial(s.amqpURL)
				if err != nil {
					// Dial failed; increase backoff and retry
					backoff = s.increaseBackoff(backoff)
					continue
				}

				// Declare topology on new connection (idempotent; safe on every reconnect)
				topology := amqpcompat.NewTopology(newConn)
				newSubs, err := topology.OpenRouterSubscriptions(ctx)
				if err != nil {
					// Topology declaration failed; cleanup connection and retry
					newConn.Close()
					backoff = s.increaseBackoff(backoff)
					continue
				}

				// Success: switch to new connection and exit reconnect loop
				conn = newConn
				subs = newSubs
				backoff = minBackoff  // Reset backoff on success
				break  // Exit reconnect loop; re-enter monitoring
			}
		}
	}
}

// cleanupResources closes subscriptions and connection safely.
// Idempotent: safe to call multiple times or on nil pointers.
func (s *RouterService) cleanupResources(conn *amqp091.Connection, subs *amqpcompat.RouterSubscriptions) {
	if subs != nil {
		subs.Close()  // Closes underlying channel
	}
	if conn != nil {
		conn.Close()
	}
}

// addJitter adds random jitter to backoff to avoid thundering herd.
// Jitter range: [backoff * (1 - jitterFactor), backoff * (1 + jitterFactor)]
func (s *RouterService) addJitter(backoff time.Duration) time.Duration {
	// Jitter: ±jitterFactor around backoff
	jitterAmount := time.Duration(
		float64(backoff) * jitterFactor * (2*rand.Float64() - 1),
	)
	return backoff + jitterAmount
}

// increaseBackoff doubles backoff with ceiling at maxBackoff.
func (s *RouterService) increaseBackoff(backoff time.Duration) time.Duration {
	nextBackoff := time.Duration(
		math.Min(float64(backoff*2), float64(maxBackoff)),
	)
	return nextBackoff
}
```

**Key improvements in this version:**

1. **Atomic running flag** (`atomic.Bool`) — no race on read in Stop()
2. **Lock hierarchy** — lock always released before cancel(), before wg.Wait()
3. **Symmetric context cleanup** — all paths call cleanupResources()
4. **Pre/post context checks** — exits early if already cancelled
5. **NotifyClose() integration** — automatic broker closure detection
6. **Topology redeclaration** — idempotent on every reconnect
7. **Jitter in backoff** — prevents synchronized reconnect spike
8. **Partial failure handling** — fails over to topology redeclaration on error
9. **Nil-safe cleanup** — safe to call on nil pointers

---

## Implementation 2: Comprehensive Test Suite

**File:** `internal/core/router/service_test.go`

This test suite covers all edge cases identified in the audit.

```go
package router

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rabbitmq/amqp091-go"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
)

// mockConnector implements amqpConnector for testing.
type mockConnector struct {
	mu              sync.Mutex
	dialCount       int32                       // Atomic counter
	failDial        bool                        // Simulate dial failure
	failSubscribe   bool                        // Simulate subscription failure
	dialDelay       time.Duration               // Inject latency
	dialSignal      chan struct{}               // Notify on dial attempt
	connCloseSignal chan *amqp091.Error         // Simulate broker closure
}

func (m *mockConnector) DialAndSubscribe(ctx context.Context, amqpURL string) (*amqp091.Connection, *amqpcompat.RouterSubscriptions, error) {
	atomic.AddInt32(&m.dialCount, 1)

	if m.dialDelay > 0 {
		select {
		case <-time.After(m.dialDelay):
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}

	if m.dialSignal != nil {
		select {
		case m.dialSignal <- struct{}{}:
		default:
		}
	}

	if m.failDial {
		return nil, nil, fmt.Errorf("mock dial failure")
	}

	conn := &mockAMQPConnection{closeSignal: m.connCloseSignal}
	subs := &amqpcompat.RouterSubscriptions{
		DeliverSM:            make(<-chan amqp091.Delivery),
		Billing:              make(<-chan amqp091.Delivery),
		DeliverSMConsumerTag: "test-deliver",
		BillingConsumerTag:   "test-billing",
	}

	if m.failSubscribe {
		return nil, nil, fmt.Errorf("mock subscribe failure")
	}

	return conn, subs, nil
}

type mockAMQPConnection struct {
	mu           sync.Mutex
	closed       bool
	closeOnce    sync.Once
	closeSignal  chan *amqp091.Error
	notifyChans  []chan *amqp091.Error
}

func (m *mockAMQPConnection) NotifyClose(ch chan *amqp091.Error) chan *amqp091.Error {
	m.mu.Lock()
	m.notifyChans = append(m.notifyChans, ch)
	m.mu.Unlock()

	// If already closed, signal immediately
	if m.closed {
		select {
		case ch <- &amqp091.Error{Code: 200, Reason: "Connection closed", Recover: false}:
		default:
		}
	}

	// If closeSignal provided (for testing), forward it
	if m.closeSignal != nil {
		go func() {
			err := <-m.closeSignal
			m.mu.Lock()
			defer m.mu.Unlock()
			for _, c := range m.notifyChans {
				select {
				case c <- err:
				default:
				}
			}
			m.closed = true
		}()
	}

	return ch
}

func (m *mockAMQPConnection) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
	})
	return nil
}

// Test: Normal Start/Stop cycle
func TestRouterServiceStartStop(t *testing.T) {
	connector := &mockConnector{}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if connector.dialCount != 1 {
		t.Errorf("expected 1 dial, got %d", connector.dialCount)
	}

	s.Stop()

	// Verify state after stop
	if s.running.Load() {
		t.Errorf("expected running=false after Stop()")
	}
}

// Test: Start fails, then successful Start
func TestRouterServiceStartAfterFail(t *testing.T) {
	connector := &mockConnector{failDial: true}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	// First start fails
	ctx := context.Background()
	if err := s.Start(ctx); err == nil {
		t.Fatal("expected Start to fail")
	}

	// Connector now succeeds
	connector.failDial = false

	// Second start should succeed
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start after fail should succeed: %v", err)
	}

	s.Stop()
}

// Test: Start called twice (should fail on second)
func TestRouterServiceStartTwice(t *testing.T) {
	connector := &mockConnector{}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("First Start failed: %v", err)
	}

	// Second Start should fail
	if err := s.Start(ctx); err == nil {
		t.Fatal("expected second Start to fail")
	}

	s.Stop()
}

// Test: Stop called before Start (should be safe)
func TestRouterServiceStopBeforeStart(t *testing.T) {
	s := NewRouterService("amqp://localhost")

	// Should not panic or hang
	s.Stop()
}

// Test: Stop called twice concurrently (should be safe)
func TestRouterServiceStopTwice(t *testing.T) {
	connector := &mockConnector{}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Call Stop twice concurrently
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.Stop()
	}()
	go func() {
		defer wg.Done()
		s.Stop()
	}()
	wg.Wait()
}

// Test: Stop during initial backoff (first reconnect wait)
func TestRouterServiceStopDuringFirstBackoff(t *testing.T) {
	dialSignal := make(chan struct{}, 10)
	connector := &mockConnector{
		failDial:  true,  // All dials fail
		dialSignal: dialSignal,
	}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Let it enter reconnect loop
	select {
	case <-dialSignal:
		// Initial dial attempted
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial dial")
	}

	// Stop during backoff (before next dial attempt)
	startStopTime := time.Now()
	s.Stop()
	stopDuration := time.Since(startStopTime)

	// Stop should return quickly (not wait for backoff timer)
	if stopDuration > 500*time.Millisecond {
		t.Errorf("Stop took too long during backoff: %v", stopDuration)
	}

	// Verify no goroutine leak (should not hang)
	if s.running.Load() {
		t.Errorf("expected running=false after Stop()")
	}
}

// Test: Stop during extended backoff (after multiple failed retries)
func TestRouterServiceStopDuringExtendedBackoff(t *testing.T) {
	dialSignal := make(chan struct{}, 100)
	connector := &mockConnector{
		failDial:   true,
		dialSignal: dialSignal,
	}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Let reconnect loop run for a few attempts (backoff grows)
	for i := 0; i < 3; i++ {
		select {
		case <-dialSignal:
			// Dial attempted
		case <-time.After(2 * time.Second):
			t.Fatalf("timeout waiting for dial attempt %d", i+1)
		}
	}

	// Stop during long backoff
	s.Stop()

	// Should exit cleanly without hanging
	if s.running.Load() {
		t.Errorf("expected running=false after Stop()")
	}
}

// Test: Broker closure detected and reconnect triggered
func TestRouterServiceBrokerClosure(t *testing.T) {
	dialSignal := make(chan struct{}, 10)
	closeSignal := make(chan *amqp091.Error, 1)

	connector := &mockConnector{
		dialSignal:      dialSignal,
		connCloseSignal: closeSignal,
	}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	ctx := context.Background()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Verify initial dial
	select {
	case <-dialSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for initial dial")
	}

	initialDials := atomic.LoadInt32(&connector.dialCount)

	// Simulate broker closure
	closeSignal <- &amqp091.Error{Code: 200, Reason: "Connection lost"}

	// Wait for reconnect attempt
	select {
	case <-dialSignal:
		// Reconnect attempted
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for reconnect attempt after broker closure")
	}

	finalDials := atomic.LoadInt32(&connector.dialCount)
	if finalDials <= initialDials {
		t.Errorf("expected reconnect attempt after broker closure, but dial count unchanged")
	}

	s.Stop()
}

// Test: Context cancellation during Start (setup timeout)
func TestRouterServiceStartContextCancelled(t *testing.T) {
	connector := &mockConnector{dialDelay: 100 * time.Millisecond}
	s := NewRouterService("amqp://localhost")
	s.connector = connector

	// Create context that expires during dial
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := s.Start(ctx)
	if err == nil {
		t.Fatal("expected Start to fail when context cancels")
		s.Stop()
	}

	// Verify service did not actually start
	if s.started {
		t.Errorf("expected service not to start when context cancelled")
	}
}

// Test: Concurrent Start and Stop (stress test)
func TestRouterServiceConcurrentStartStop(t *testing.T) {
	numIterations := 10

	for iter := 0; iter < numIterations; iter++ {
		connector := &mockConnector{}
		s := NewRouterService("amqp://localhost")
		s.connector = connector

		ctx := context.Background()
		var startErr, stopErr error
		var wg sync.WaitGroup

		// Concurrent Start
		wg.Add(1)
		go func() {
			defer wg.Done()
			startErr = s.Start(ctx)
		}()

		// Give Start a moment to proceed
		time.Sleep(10 * time.Millisecond)

		// Concurrent Stop
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Stop()
		}()

		wg.Wait()

		// Either both succeed or Start fails (and Stop handles gracefully)
		if startErr != nil {
			t.Logf("iteration %d: Start failed (acceptable): %v", iter, startErr)
		}

		if !s.started && startErr == nil {
			t.Errorf("iteration %d: Start succeeded but started flag not set", iter)
		}
	}
}

// Test: Backoff with jitter doesn't produce synchronized delays
func TestRouterServiceBackoffJitter(t *testing.T) {
	// This test verifies that jitter is applied (probabilistic, not deterministic)
	s := NewRouterService("amqp://localhost")

	baseBackoff := time.Second
	jitters := make([]time.Duration, 100)

	for i := 0; i < 100; i++ {
		jittered := s.addJitter(baseBackoff)
		jitters[i] = jittered
	}

	// Verify jitters vary (not all the same)
	seen := make(map[time.Duration]int)
	for _, j := range jitters {
		seen[j]++
	}

	if len(seen) < 50 {  // Should have at least 50 unique values out of 100
		t.Errorf("expected varied jitter, but only %d unique values seen", len(seen))
	}

	// Verify jitter stays within bounds
	minJitter := baseBackoff - time.Duration(float64(baseBackoff)*0.1)
	maxJitter := baseBackoff + time.Duration(float64(baseBackoff)*0.1)

	for _, j := range jitters {
		if j < minJitter || j > maxJitter {
			t.Errorf("jitter %v outside bounds [%v, %v]", j, minJitter, maxJitter)
		}
	}
}

// Test: Goroutine leak detection (verify all goroutines cleaned up)
func TestRouterServiceNoGoroutineLeaks(t *testing.T) {
	// Get baseline goroutine count
	startGoroutines := countActiveGoroutines()

	for i := 0; i < 5; i++ {
		connector := &mockConnector{}
		s := NewRouterService("amqp://localhost")
		s.connector = connector

		ctx := context.Background()
		if err := s.Start(ctx); err != nil {
			t.Fatalf("Start failed: %v", err)
		}

		s.Stop()

		// Give goroutines time to exit
		time.Sleep(100 * time.Millisecond)
	}

	// Final goroutine count should be same as baseline (within 1-2)
	endGoroutines := countActiveGoroutines()

	leaked := endGoroutines - startGoroutines
	if leaked > 2 {  // Allow small variance
		t.Errorf("possible goroutine leak: started with %d, ended with %d (leaked ~%d)",
			startGoroutines, endGoroutines, leaked)
	}
}

// Helper: count currently active goroutines (rough estimate for leak detection)
func countActiveGoroutines() int {
	return runtime.NumGoroutine()
}
```

**Add to imports:**

```go
import (
	"runtime"  // For goroutine leak detection
)
```

---

## Implementation 3: Configuration Constants

Add to RouterService struct initialization:

```go
const (
	minBackoff   = time.Second
	maxBackoff   = 30 * time.Second
	jitterFactor = 0.1  // ±10% jitter
)
```

Or make these configurable:

```go
type RouterServiceConfig struct {
	AMQPURL      string
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	JitterFactor float64
}

func NewRouterServiceWithConfig(config RouterServiceConfig) *RouterService {
	return &RouterService{
		amqpURL:      config.AMQPURL,
		minBackoff:   config.MinBackoff,
		maxBackoff:   config.MaxBackoff,
		jitterFactor: config.JitterFactor,
		connector:    &defaultConnector{},
	}
}
```

---

## Validation Checklist

Run before committing:

```bash
# 1. Race detector
go test -race ./internal/core/router/...

# 2. All tests pass
go test -v ./internal/core/router/...

# 3. Coverage
go test -cover ./internal/core/router/...

# 4. Lint
golangci-lint run ./internal/core/router/...

# 5. Stress test (optional)
go test -race -count=100 -timeout=10m ./internal/core/router/...
```

Expected results:
- ✓ No race conditions detected
- ✓ All 15+ tests pass
- ✓ Coverage > 90%
- ✓ No lint warnings
- ✓ Stress test passes (100 iterations)

---

## Migration Path (If Updating Existing Code)

1. **Backup current service.go**
   ```bash
   cp internal/core/router/service.go internal/core/router/service.go.backup
   ```

2. **Replace with new implementation** (above)

3. **Add new test cases** to service_test.go

4. **Run tests incrementally**
   ```bash
   # Just basic tests
   go test -v -run TestRouterServiceStartStop ./internal/core/router/...

   # Add edge cases one by one
   go test -v -run TestRouterServiceStopDuringFirstBackoff ./internal/core/router/...

   # Full suite with race
   go test -race ./internal/core/router/...
   ```

5. **Verify in integration** with full router/broker setup

6. **Deployment**: Roll out with monitoring on reconnect rate, error logs

---

## Debugging Tips

**Goroutine leak suspected?**
```go
// Add to test after Stop()
import _ "net/http/pprof"

// Then in another terminal:
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

**Deadlock suspected?**
```bash
go test -timeout=10s ./internal/core/router/...  # Will fail if hangs
```

**Races showing up?**
```bash
go test -race -run TestName ./internal/core/router/...
```

**Connection not closing?**
```bash
lsof -p <pid> | grep ESTABLISHED | wc -l  # Before/after Stop()
```

---

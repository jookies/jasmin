# Audit: RouterService & LateBillingService Integration

**Audit Date:** 2026-07-19  
**Auditor:** Autonomous subagent  
**Repository:** jasmin-go  
**Scope:** Integration audit for wiring `LateBillingService` into `RouterService` to handle `bill_request.submit_sm_resp.*` messages

---

## Executive Summary

This audit identifies **6 critical and high-severity issues** that MUST be resolved before wiring `LateBillingService` into `RouterService`. The services are well-designed individually, but the integration has significant gaps in:

1. **Message consumption and ACK/Reject lifecycle** – No message pump exists
2. **Race conditions in lifecycle transitions** – Missed synchronization points
3. **Error recovery semantics** – Processing errors leave messages unsettled
4. **Resource cleanup deadlock risks** – Unbuffered channels and goroutine leaks
5. **Context propagation** – Stale contexts bypass cancellation
6. **Monitoring and observability** – No logging/metrics for integration health

**Status:** ⚠️ **NOT READY FOR INTEGRATION** – Requires significant design work

---

## Detailed Findings

### 🔴 CRITICAL: Missing Message Pump / Consumer Loop

**Location:** `internal/core/router/service.go:runManager()`  
**Severity:** CRITICAL

#### Problem

`RouterService.runManager()` manages AMQP connection lifecycle but **does not consume messages** from the subscription channels. The topology declares both `DeliverSM` and `Billing` channels (topology.go lines 46-49), but the router service never reads from them:

```go
type RouterSubscriptions struct {
    DeliverSM            <-chan amqp.Delivery  // ← Never read by runManager()
    Billing              <-chan amqp.Delivery  // ← Never read by runManager()
    DeliverSMConsumerTag string
    BillingConsumerTag   string
    channel topologyChannel
}
```

When `LateBillingService` is wired in, there is **no place to actually invoke `ProcessLateBillingDelivery()`**. Messages will pile up in the `Billing` channel and be redelivered by RabbitMQ indefinitely.

#### Impact

- Messages never processed
- No ACK/Reject settlement → broker timeouts and redelivery storms
- Consumer prefetch (QoS=1) blocks forever waiting for handler
- Memory leak: queued messages accumulate unbounded

#### Required Fix

Create a message pump inside `runManager()` or add separate worker goroutines:

```go
// Pseudocode structure
func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    // ... existing connection management code ...
    
    s.wg.Add(2) // Track message pump goroutines
    go s.deliverSMWorker(ctx, initialState)
    go s.billingWorker(ctx, initialState)  // ← Calls LateBillingService
    
    // ... existing lifecycle logic ...
}

func (s *RouterService) billingWorker(ctx context.Context, state *connectionState) {
    defer s.wg.Done()
    for {
        select {
        case <-ctx.Done():
            return
        case delivery, ok := <-state.subs.Billing:  // ← Need access to subscriptions
            if !ok {
                return
            }
            if err := s.processBillingDelivery(delivery); err != nil {
                // Error handling + rejection
            }
        }
    }
}
```

**Current Blocker:** `connectionState.subs` is an `io.Closer`, not `*RouterSubscriptions`. Cannot access channels.

---

### 🔴 CRITICAL: Race Condition in Start() → runManager() Transition

**Location:** `internal/core/router/service.go:Start() / runManager()`  
**Severity:** CRITICAL

#### Problem

There is an unsynchronized window between when `Start()` returns and `runManager()` actually starts:

```go
func (s *RouterService) Start(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.running { 
        return fmt.Errorf("router service already running")
    }

    runCtx, cancel := context.WithCancel(context.Background())
    s.cancel = cancel
    s.running = true
    
    state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
    if err != nil {
        cancel()
        s.running = false
        return fmt.Errorf("initial AMQP connection failed: %w\", err)
    }

    s.wg.Add(1)
    go s.runManager(runCtx, state)  // ← Goroutine launched, mu unlocked
    
    return nil  // ← Caller continues immediately
    //          runManager() may not have started yet!
}
```

#### Race Scenario 1: Immediate Stop

```go
s := NewRouterService("amqp://...")
s.Start(ctx)          // Returns immediately
s.Stop()              // Caller races to Stop() before runManager() starts
// Result: runManager() starts AFTER cancel() already called
//         Goroutine exits immediately without managing connection
```

#### Race Scenario 2: Reconnect Before First Start

```go
s.Start(ctx)
s.notifyClose <- fmt.Errorf("simulated loss")  // Race! runManager might not be waiting yet
```

#### Impact

- Silent service start failures (goroutine never actually monitoring connection)
- Unpredictable connection lifecycle
- Test flakiness (see TestRouterServiceReconnectLoop – already sensitive to timing)
- Integration failures when called from concurrent context

#### Required Fix

Add explicit synchronization barrier in `runManager()`:

```go
type RouterService struct {
    // ... existing fields ...
    started sync.WaitGroup  // ← New: signal that manager is running
}

func (s *RouterService) Start(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.running {
        return fmt.Errorf("router service already running")
    }

    runCtx, cancel := context.WithCancel(context.Background())
    s.cancel = cancel
    s.running = true
    
    state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
    if err != nil {
        cancel()
        s.running = false
        return fmt.Errorf("initial AMQP connection failed: %w", err)
    }

    s.started.Add(1)  // ← New: expect manager to signal start
    s.wg.Add(1)
    go s.runManager(runCtx, state)
    
    // Block until manager has initialized and started the main loop
    s.started.Wait()
    
    return nil
}

func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    defer s.wg.Done()
    defer s.cleanup(initialState)
    
    s.started.Done()  // ← New: signal that we're in the main loop
    
    // ... existing loop ...
}
```

---

### 🔴 CRITICAL: Error Handling Leaves Messages Unsettled

**Location:** Integration point (LateBillingService → RouterService)  
**Severity:** CRITICAL

#### Problem

`LateBillingService.Process()` can return errors in several scenarios:

```go
func (service *LateBillingService) Process(envelope amqpcompat.Envelope) (LateBillingAction, error) {
    // ... validation ...
    
    user, err := service.users.GetUserByID(userID)
    if errors.Is(err, billing.ErrUserNotFound) {
        return LateBillingReject, nil  // ← OK: returns action
    }
    if err != nil {
        return LateBillingNone, err    // ← ERROR: non-ErrUserNotFound database error
    }
    // ...
    default:
        return LateBillingNone, err    // ← ERROR: other billing errors
}
```

When `LateBillingAction` is `LateBillingNone` (due to error), `ProcessLateBillingDelivery()` returns the message **unsettled**:

```go
func ProcessLateBillingDelivery(processor LateBillingDecisionProcessor, delivery LateBillingDelivery) error {
    action, err := processor.Process(delivery.Envelope())
    if err != nil {
        return err  // ← Unsettled! Caller must handle retry
    }
    switch action {
    case LateBillingAck:
        return delivery.Ack()
    case LateBillingReject:
        return delivery.Reject(false)
    case LateBillingNone:
        return nil  // ← Unsettled! Message stays in broker
    default:
        return ErrInvalidLateBillingEnvelope
    }
}
```

#### Impact

- **Transient errors** (database timeout, user service unavailable) → message stuck indefinitely
- **No retry strategy** – without explicit handling, messages accumulate
- **Broker resource exhaustion** – memory pressure from queued messages
- **Delivery guarantee violation** – messages not in deterministic state

#### Scenarios

1. **User directory temporarily unavailable:**
   ```
   Process() → err=ErrDatabaseUnavailable → LateBillingNone, error
   ProcessLateBillingDelivery() → return error → delivery unsettled
   Broker: Message redelivered (configurable retry logic, but unbounded)
   ```

2. **Billing system error (e.g., ErrInsufficientFunds but with database error):**
   ```
   user.ApplyLateCharge(amount) → database commit fails → returns error
   Process() → LateBillingNone, error
   Message stuck in broker awaiting settlement
   ```

#### Required Fix

Define explicit error handling policy and implement in message pump:

```go
func (s *RouterService) processBillingDelivery(delivery LateBillingDelivery) error {
    action, err := s.lateBillingService.Process(delivery.Envelope())
    
    // Errors are critical: user directory unavailable, database errors
    if err != nil {
        // Option 1: Reject and manual intervention
        if isTransientError(err) {
            return delivery.Reject(true)  // Requeue for broker retry
        }
        // Option 2: Dead-letter queue via reject
        return delivery.Reject(false)     // No requeue
    }
    
    // Action-based settlement
    switch action {
    case core.LateBillingAck:
        return delivery.Ack()
    case core.LateBillingReject:
        return delivery.Reject(false)
    case core.LateBillingNone:
        // Unsettled – need explicit handling policy
        // Option 1: Log and ACK (optimistic: assume will retry via async process)
        // Option 2: Reject to broker (conservative: let broker retry)
        s.logger.Warnf("late billing returned NONE for message %s", delivery.Envelope().ID())
        return delivery.Reject(true)
    }
}
```

---

### 🟠 HIGH: State Leakage on Connection Loss During Processing

**Location:** `internal/core/router/service.go:runManager()`  
**Severity:** HIGH

#### Problem

If AMQP connection is lost while a message is being processed, the unsettled delivery **is lost**:

```go
func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    currentState := initialState
    
    for {
        select {
        case <-ctx.Done():
            s.cleanup(currentState)
            return
        case amqpErr := <-currentState.notifyClose:
            // ← Connection lost while message might be in flight!
            closeErr = amqpErr
        }
        
        // Connection lost, perform cleanup and attempt reconnect
        s.cleanup(currentState)  // ← Closes channel; all in-flight deliveries abandoned
        currentState = nil
    }
}
```

When `currentState.subs.Close()` is called, the AMQP channel is closed, and:
- All in-flight messages lose their settlement context
- Delivery handlers cannot ACK/Reject anymore
- Broker eventually redelivers due to timeout

#### Scenario

```
1. Message M1 arrives on billing channel
2. Worker starts: action, err := s.lateBillingService.Process(M1.Envelope())
3. Processing takes 500ms (database call)
4. AMQP connection drops (network issue, broker restart)
5. connection.notifyClose fires → cleanup() → channel.Close()
6. Worker tries: delivery.Ack() or Reject() → channel closed → error
7. Message M1 in limbo: broker thinks it's still pending, but settlement failed
```

#### Impact

- **Delivery guarantee violation** – at-most-once becomes at-least-once
- **Duplicate processing risk** – when reconnected, M1 redelivered
- **Billing correctness** – user might be charged twice or not at all

#### Required Fix

Implement graceful connection loss handling:

```go
func (s *RouterService) billingWorker(ctx context.Context, state *connectionState) {
    defer s.wg.Done()
    
    for {
        select {
        case <-ctx.Done():
            return
        case err := <-state.notifyClose:
            // Connection closing – exit worker
            // Any in-flight deliveries will be auto-requeued by broker
            s.logger.Warnf("billing channel closed: %v, exiting worker", err)
            return
        case delivery, ok := <-state.subs.Billing:
            if !ok {
                return
            }
            if err := s.processBillingDelivery(delivery); err != nil {
                s.logger.Errorf("billing delivery processing failed: %v", err)
            }
        }
    }
}
```

Better approach: **Don't let workers overlap with lifecycle transitions**. If a connection is lost, restart all workers with the new connection state.

---

### 🟠 HIGH: Unbuffered Channel Deadlock Risk in Reconnect Loop

**Location:** `internal/core/router/service.go:65, 140`  
**Severity:** HIGH

#### Problem

`notifyClose` is declared as unbuffered in production but buffered in tests:

```go
// Line 65: Production
notifyClose: make(chan error, 1)  // ← BUFFERED

// Test (line 104)
s.notifyClose <- fmt.Errorf("connection lost")  // ← Can send even if no receiver
```

However, if multiple goroutines try to signal close simultaneously, or if signaling happens during a locked section, deadlock risks emerge:

```go
func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    for {
        select {
        case <-ctx.Done():
            s.cleanup(currentState)
            return
        case err := <-s.notifyClose:  // ← Waits here
            closeErr = err
        case amqpErr := <-currentState.notifyClose:
            // ...
        }
    }
}
```

If `s.notifyClose` buffer fills:
- `Stop()` calls `cancel()` but runManager waits on `s.notifyClose`
- `s.wg.Wait()` blocks indefinitely

#### Scenario

```
Thread 1: s.notifyClose <- error  (buffer full, blocks)
Thread 1: (stuck in channel send)
Thread 2: s.Stop() → s.cancel() → s.wg.Wait() (blocks forever, cancel() signal ignored)
Deadlock: Thread 1 waiting for reader, Thread 2 waiting for goroutine exit
```

#### Required Fix

Make `notifyClose` truly async or remove it:

```go
// Option 1: Remove manual notify (use AMQP notifyClose only)
// Simplifies code and avoids channel deadlock

// Option 2: Use async send
func (s *RouterService) notifyConnectionLoss(err error) {
    select {
    case s.notifyClose <- err:
    default:
        s.logger.Warnf("notifyClose buffer full, dropping signal: %v", err)
    }
}
```

---

### 🟠 HIGH: Context Propagation Bypass in ConnectAndSubscribe

**Location:** `internal/core/router/service.go:92`  
**Severity:** HIGH

#### Problem

`Start()` passes the caller's `ctx` to the initial connection attempt, but the manager runs with a separate `runCtx`:

```go
func (s *RouterService) Start(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    // ...
    runCtx, cancel := context.WithCancel(context.Background())  // ← New context!
    s.cancel = cancel
    s.running = true
    
    state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)  // ← Caller's ctx
    if err != nil {
        cancel()
        s.running = false
        return fmt.Errorf("initial AMQP connection failed: %w", err)
    }

    s.wg.Add(1)
    go s.runManager(runCtx, state)  // ← runCtx is context.Background()
    
    return nil
}
```

Result:
- **Initial connection respects caller's deadline** (good)
- **Reconnections ignore caller's deadline** (bad – runs forever after Start returns)
- **Message pump ignores caller's deadline** (timeout in caller doesn't propagate)

If caller uses `ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)`:
```
StartTx, cancel := context.WithTimeout(ctx, 30*time.Second)
s.Start(startCtx)  // Returns after 30s, but runManager still running
s.Stop()           // Must explicitly stop; can't rely on context
```

#### Impact

- **Resource cleanup unpredictable** – orphan goroutines if caller context expires
- **Integration complexity** – caller can't use context deadline for shutdown
- **Test flakiness** – tests must always call `s.Stop()` even with cancellation

#### Required Fix

Use a cancellation model that integrates with caller's context:

```go
func (s *RouterService) Start(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.running {
        return fmt.Errorf("router service already running")
    }

    // Create cancellable context but also tie to caller
    runCtx, cancel := context.WithCancel(ctx)  // ← Tie to caller's ctx
    s.cancel = cancel
    s.running = true
    
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
```

**Risk:** If caller's context expires, `runCtx` also cancels, triggering cleanup. Caller must be aware this will stop service.

---

### 🟡 MEDIUM: No Observability / Logging for Integration

**Location:** All integration points  
**Severity:** MEDIUM

#### Problem

Neither `RouterService` nor `LateBillingService` have logging. When integration fails, there's no visibility:

```go
// No logs in runManager reconnect loop
for {
    select {
    case <-ctx.Done():
        // Silent exit
    case err := <-s.notifyClose:
        closeErr = err  // ← Unused, no logging
    case amqpErr := <-currentState.notifyClose:
        // ← Silent connection loss
    }
    
    s.cleanup(currentState)
    currentState = nil
    _ = closeErr  // ← Explicitly discarding error info!
}
```

#### Impact

- **Production debugging impossible** – no visibility into reconnects, failures, or billing drops
- **Integration testing opaque** – tests can't verify internal state transitions
- **SRE blind** – no way to monitor billing message throughput or latency

#### Required Fix

Add structured logging to all critical paths:

```go
type RouterService struct {
    // ... existing fields ...
    logger Logger  // slog, logrus, etc.
}

func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    defer s.wg.Done()
    
    currentState := initialState
    backoff := time.Second
    
    for {
        select {
        case <-ctx.Done():
            s.logger.Info("router service stopping", "reason", "context cancelled")
            s.cleanup(currentState)
            return
        case err := <-s.notifyClose:
            s.logger.Warn("connection loss signaled", "error", err)
            closeErr = err
        case amqpErr := <-currentState.notifyClose:
            s.logger.Warn("AMQP connection closed", "error", amqpErr)
            closeErr = amqpErr
        }
        
        s.cleanup(currentState)
        currentState = nil
        
        // Reconnect with backoff
        for {
            select {
            case <-ctx.Done():
                return
            case <-time.After(backoff):
                s.logger.Info("reconnect attempt", "backoff", backoff)
                newState, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
                if err != nil {
                    s.logger.Error("reconnect failed", "error", err)
                    backoff *= 2
                    if backoff > maxBackoff {
                        backoff = maxBackoff
                    }
                    continue
                }
                s.logger.Info("reconnected successfully")
                currentState = newState
                backoff = time.Second
                goto connected
            }
        }
        connected:
    }
}
```

---

### 🟡 MEDIUM: Goroutine Leak on Rapid Reconnects

**Location:** `internal/core/router/service.go:runManager()`  
**Severity:** MEDIUM

#### Problem

The test `TestRapidReconnectMemoryPressure` (line 265-310) hints at potential goroutine leaks:

```go
// Test setup: 5 failed reconnects, then succeeds
connector := &mockConnector{
    connectFunc: func(ctx context.Context, amqpURL string) (*connectionState, error) {
        if initialConnect {
            initialConnect = false
            return &connectionState{...}, nil
        }
        if failCount < maxFails {
            failCount++
            return nil, fmt.Errorf("simulated connection failure %d", failCount)
        }
        return &connectionState{...}, nil
    },
}
```

If `time.After(backoff)` timers are not cleaned up on context cancellation, goroutines leak:

```go
for {
    select {
    case <-ctx.Done():
        return  // Timer goroutine still running!
    case <-time.After(backoff):  // ← Creates new timer on every iteration
        // Attempt reconnect
    }
}
```

Each `time.After()` creates a goroutine that runs until the timer fires or the context closes. If `ctx.Done()` fires first, the timer is orphaned.

#### Impact

- **Memory leak** in long-running services with frequent reconnects
- **Goroutine explosion** on chaotic networks (frequent disconnects)
- **Eventually crashes** due to resource exhaustion

#### Required Fix

Use `time.NewTimer()` and explicitly stop on context cancellation:

```go
func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    defer s.wg.Done()

    currentState := initialState
    backoff := time.Second
    const maxBackoff = 30 * time.Second

    for {
        // Wait for closure
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

        // Connection lost, cleanup
        s.cleanup(currentState)
        currentState = nil

        // Reconnect loop
        for {
            timer := time.NewTimer(backoff)  // ← Explicit timer
            select {
            case <-ctx.Done():
                timer.Stop()  // ← Clean up timer
                return
            case <-timer.C:  // ← Timer fired
                newState, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
                if err == nil {
                    currentState = newState
                    backoff = time.Second
                    goto connected
                }
                backoff *= 2
                if backoff > maxBackoff {
                    backoff = maxBackoff
                }
                // Continue loop, which will create new timer
            }
        }
    connected:
        // Successfully reconnected, continue monitoring
    }
}
```

---

## Integration Checklist

Before wiring `LateBillingService` into `RouterService`, ensure:

- [ ] **Message pump implemented** – separate worker goroutine for `Billing` channel
- [ ] **Start() synchronization** – `WaitGroup` or channel barrier to ensure manager started
- [ ] **Error handling policy** – defined behavior for transient vs. permanent errors
- [ ] **Connection loss during processing** – handled gracefully without abandoning deliveries
- [ ] **Async close signaling** – unbuffered channel deadlock eliminated
- [ ] **Context propagation** – manager context tied to caller's context (or explicit lifecycle model)
- [ ] **Observability** – structured logging for reconnects, processing, errors
- [ ] **Timer cleanup** – `time.After()` replaced with explicit `time.NewTimer()` + `Stop()`
- [ ] **Tests updated** – race detector enabled, goroutine leak detection
- [ ] **Integration test** – end-to-end test with mock broker and simulated billing processing

---

## Code Architecture Recommendation

### Proposed Structure

```go
// router/service.go
type RouterService struct {
    amqpURL   string
    connector amqpConnector
    logger    Logger
    
    mu       sync.Mutex
    running  bool
    cancel   context.CancelFunc
    wg       sync.WaitGroup
    started  sync.WaitGroup  // Lifecycle barrier
    
    // Dependency injection (populated on Start)
    billingService core.LateBillingDecisionProcessor
}

func (s *RouterService) Start(ctx context.Context, billingService core.LateBillingDecisionProcessor) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // ... validation and connection ...
    
    runCtx, cancel := context.WithCancel(ctx)
    s.cancel = cancel
    s.running = true
    s.billingService = billingService
    
    s.started.Add(1)
    s.wg.Add(2)  // Connection manager + message workers
    go s.runManager(runCtx, state)
    // Workers spawned by manager once subscriptions ready
    
    s.started.Wait()  // Block until manager started
    return nil
}

func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    // Lifecycle management: connect, spawn workers, reconnect
}

func (s *RouterService) billingWorker(ctx context.Context, subs *RouterSubscriptions) {
    // Message pump: read Billing channel, call LateBillingService
}
```

---

## Testing Recommendations

1. **Add race detector tests** – `go test -race`
2. **Test connection loss during processing** – inject failure in LateBillingService
3. **Test rapid reconnect scenarios** – goroutine leak detection
4. **Test error handling** – verify ACK/Reject semantics for all error paths
5. **Test context cancellation** – verify cleanup on context deadline
6. **Integration smoke test** – real broker with mock billing service

---

## References

- `internal/core/router/service.go` – Connection lifecycle management
- `internal/core/late_billing_service.go` – Billing decision logic
- `internal/core/late_billing_delivery.go` – Settlement interface
- `internal/transport/amqpcompat/topology.go` – Subscription topology
- `internal/transport/amqpcompat/client.go` – Delivery settlement
- Test files: `service_test.go`, `late_billing_service_test.go`

---

## Conclusion

The individual components are well-tested and functionally sound. However, their integration requires careful attention to lifecycle management, error handling, and resource cleanup. The 6 issues identified above must be addressed to ensure:

1. **Reliability** – Messages processed or explicitly failed, never silently lost
2. **Safety** – No race conditions, goroutine leaks, or deadlocks
3. **Observability** – Full visibility into integration health
4. **Correctness** – Billing transactions settled correctly under failure scenarios

**Estimated effort:** 2-3 days of focused design and implementation, plus integration testing.

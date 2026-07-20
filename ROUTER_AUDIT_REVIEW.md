# RouterService Architecture Review: Audit Report vs. Current Implementation

**Review Date:** 2026-07-19  
**Reviewer:** Autonomous subagent  
**Audit Report:** AUDIT_ROUTER_LATE_BILLING_INTEGRATION.md  
**Target:** internal/core/router/service.go (current) vs. PHASE_2_29_IMPLEMENTATION_GUIDE.md (proposed)

---

## Executive Summary

**Status:** ⚠️ **CRITICAL GAPS BETWEEN AUDIT FINDINGS AND CURRENT IMPLEMENTATION**

The current `service.go` does NOT address the 6 critical/high issues identified in the audit report. The existing code has:
- ❌ NO message pump for billing channel
- ❌ NO explicit Start/Stop synchronization barriers
- ❌ NO error handling for unsettled deliveries
- ❌ Unbuffered reconnect signaling with deadlock risk
- ❌ NO logging/observability
- ❌ Goroutine leaks via `time.After()`

A more recent design (PHASE_2_29_IMPLEMENTATION_GUIDE.md) exists but has not yet been integrated into service.go.

---

## Audit Issue #1: Missing Message Pump

### Audit Finding
**Severity:** CRITICAL  
**Problem:** `runManager()` manages connection lifecycle but **never reads from subscription channels** (`DeliverSM`, `Billing`).

**Audit Quote:**
```
When `LateBillingService` is wired in, there is **no place to actually invoke 
`ProcessLateBillingDelivery()`**. Messages will pile up in the `Billing` channel 
and be redelivered by RabbitMQ indefinitely.
```

### Current Implementation (`service.go`)

```go
type connectionState struct {
    conn        io.Closer
    subs        io.Closer                        // ← Abstracted as io.Closer!
    notifyClose <-chan *amqp091.Error
}

func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    defer s.wg.Done()
    currentState := initialState
    
    for {
        select {
        case <-ctx.Done():
            s.cleanup(currentState)
            return
        case err := <-s.notifyClose:
            closeErr = err
        case amqpErr := <-currentState.notifyClose:
            // Handle connection closure
        }
        // ... reconnect logic ...
    }
}
```

### Problem Confirmed
✅ **AUDIT FINDING VALID**: The current code:
1. Defines `connectionState.subs` as `io.Closer` — **cannot access delivery channels**
2. Has **zero consumer loops** for any message type
3. Never calls any `Process*Delivery()` function
4. Messages on `Billing` channel would **indeed pile up indefinitely**

### Root Cause
The `subs` field was intentionally abstracted away to avoid exposing internal subscription details in tests. This is a layering issue:
- `runManager()` needs access to `*RouterSubscriptions` to spawn message pump workers
- But current design hides that behind `io.Closer` interface

### Impact Assessment
- **Severity Confirmed:** CRITICAL  
  - No delivery processing = messages accumulate
  - QoS=1 consumer blocks forever waiting for handler
  - Broker eventually force-redelivers or times out
  - Late billing transactions never settle

### Required Fix
**Must change:**
```go
type connectionState struct {
    conn        *amqp091.Connection              // ← Expose concrete type
    subs        *amqpcompat.RouterSubscriptions  // ← Not io.Closer!
    notifyClose <-chan *amqp091.Error
}
```

Then spawn dedicated workers inside `runManager()`:
```go
func (s *RouterService) runManager(ctx context.Context, state *connectionState) {
    s.wg.Add(2)
    go s.deliverSMWorker(ctx, state.subs.DeliverSM)
    go s.billingWorker(ctx, state.subs.Billing)
    // ... connection monitoring ...
}

func (s *RouterService) billingWorker(ctx context.Context, billingChan <-chan amqp.Delivery) {
    defer s.wg.Done()
    for {
        select {
        case <-ctx.Done():
            return
        case delivery, ok := <-billingChan:
            if !ok { return }
            if err := s.processBillingDelivery(delivery); err != nil {
                s.logger.Errorf("billing processing failed: %v", err)
            }
        }
    }
}
```

**Proposed fix found in:** PHASE_2_29_IMPLEMENTATION_GUIDE.md ✅  
**Current service.go:** ❌ NOT IMPLEMENTED

---

## Audit Issue #2: Start/Stop Race Condition

### Audit Finding
**Severity:** CRITICAL  
**Problem:** Unsynchronized window between `Start()` returning and `runManager()` actually starting.

**Audit Quote:**
```go
s.Start(ctx)          // Returns immediately
s.Stop()              // Caller races to Stop() before runManager() starts
// Result: runManager() starts AFTER cancel() already called
```

### Current Implementation (`service.go`)

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
        return fmt.Errorf("initial AMQP connection failed: %w", err)
    }
    
    s.wg.Add(1)
    go s.runManager(runCtx, state)  // ← Goroutine spawned, mu unlocked
    
    return nil  // ← Caller continues immediately, runManager may not have started
}
```

### Race Scenarios Confirmed

**Scenario 1: Immediate Stop**
```
Thread 1: s.Start() → s.wg.Add(1) → go s.runManager() [NOT YET RUNNING]
          → return (mutex unlocked)
Thread 2: s.Stop() → cancel() → s.wg.Wait() [WAITS FOR GOROUTINE]
          BUT: runManager() hasn't incremented wg yet or hasn't started its loop
Result: Unpredictable behavior, possible deadlock or missed management
```

**Scenario 2: Rapid Reconnect Signal**
```
Thread 1: s.Start() → return
Thread 2: s.notifyClose <- error [RACES with runManager reading it]
Result: Manager never receives signal, or receives before initialized
```

### Test Sensitivity
The test `TestRouterServiceReconnectLoop` (line 150+) hints at timing sensitivity:
```go
func TestRouterServiceReconnectLoop(t *testing.T) {
    // ... test waits for connection signal with time-based logic ...
    // This test is likely flaky on slow CI runners
}
```

### Root Cause
No synchronization point between goroutine launch and actual initialization.

### Impact Assessment
- **Severity Confirmed:** CRITICAL
  - Silent service start failures (goroutine never monitoring)
  - Unpredictable lifecycle (test flakiness)
  - Integration failures in concurrent systems
  - Cannot rely on lifecycle state after `Start()` returns

### Required Fix
Add explicit `WaitGroup` barrier:
```go
type RouterService struct {
    // ... existing ...
    started sync.WaitGroup  // ← Synchronization barrier
}

func (s *RouterService) Start(ctx context.Context) error {
    // ... validation and connection ...
    
    s.started.Add(1)  // Expect manager to signal
    s.wg.Add(1)
    go s.runManager(runCtx, state)
    
    s.started.Wait()  // ← BLOCK until manager is in its main loop
    
    return nil
}

func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    defer s.wg.Done()
    
    s.started.Done()  // ← Signal that we're running
    
    // ... main loop ...
}
```

**Proposed fix found in:** PHASE_2_29_IMPLEMENTATION_GUIDE.md (line 80+) ✅  
**Current service.go:** ❌ NOT IMPLEMENTED

---

## Audit Issue #3: Error Handling Leaves Messages Unsettled

### Audit Finding
**Severity:** CRITICAL  
**Problem:** When `LateBillingService.Process()` returns an error, the delivery remains unsettled indefinitely.

**Audit Quote:**
```go
// LateBillingService.Process() can return errors for transient failures:
if errors.Is(err, billing.ErrUserNotFound) {
    return LateBillingReject, nil  // ← OK: returns action
}
if err != nil {
    return LateBillingNone, err    // ← ERROR: non-ErrUserNotFound database error
}

// ProcessLateBillingDelivery() leaves message unsettled:
if err != nil {
    return err  // ← Unsettled! No ACK/Reject
}
```

### Current Implementation (`service.go`)

The current code **does not have a message pump at all**, so this issue is moot until:
1. A message pump is implemented
2. Error handling policy is defined

However, when examining the audit report's proposed error handling:
```go
func (s *RouterService) processBillingDelivery(delivery LateBillingDelivery) error {
    action, err := s.lateBillingService.Process(delivery.Envelope())
    
    if err != nil {
        return delivery.Reject(true)  // Requeue for broker retry
    }
    
    switch action {
    case core.LateBillingAck:
        return delivery.Ack()
    case core.LateBillingReject:
        return delivery.Reject(false)
    case core.LateBillingNone:
        // STILL UNSETTLED! Need explicit handling
        return delivery.Reject(true)  // or ACK depending on semantics
    }
}
```

### Problem Confirmed
✅ **AUDIT FINDING VALID**: The audit correctly identifies that:
1. Errors during processing → unsettled messages
2. Transient errors (database unavailable) → messages stuck indefinitely
3. No retry strategy defined
4. Broker resource exhaustion risk

### Root Cause
No error handling policy + no message pump = no implementation opportunity yet.

### Impact Assessment
- **Severity Confirmed:** CRITICAL
  - Transient errors (DB timeouts, service unavailable) → stuck messages
  - Billing transactions never settle
  - Broker accumulates queued messages
  - No way to distinguish transient from permanent failures

### Required Fix
**Must define:**
1. **Error classification:**
   - Transient (database timeout, user service unavailable) → Reject with requeue
   - Permanent (user not found, invalid billing state) → Reject without requeue
   - Logic errors (nil pointer, panic) → Dead-letter queue

2. **Action handling:**
   - `LateBillingAck` → Ack message
   - `LateBillingReject` → Reject without requeue
   - `LateBillingNone` → Log warning + Reject with requeue (conservative)

3. **Monitoring:**
   - Count errors by type
   - Alert on error rate spikes
   - Monitor requeue depth

**Proposed fix found in:** PHASE_2_29_IMPLEMENTATION_GUIDE.md ✅  
**Current service.go:** ❌ NOT APPLICABLE (no pump yet)

---

## Audit Issue #4: State Leakage on Connection Loss During Processing

### Audit Finding
**Severity:** HIGH  
**Problem:** If AMQP connection drops while processing, in-flight message loses settlement context.

**Audit Scenario:**
```
1. Message M1 arrives on billing channel
2. Worker starts processing (500ms database call)
3. AMQP connection drops
4. s.cleanup(currentState) → closes channel
5. Worker tries: delivery.Ack() → channel closed → error
6. Message M1 in limbo: broker thinks pending, settlement failed
```

### Current Implementation (`service.go`)

```go
func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    for {
        select {
        case <-ctx.Done():
            s.cleanup(currentState)
            return
        case amqpErr := <-currentState.notifyClose:
            closeErr = amqpErr
        }
        
        s.cleanup(currentState)  // ← Closes channel, orphans in-flight messages
        currentState = nil
    }
}

func (s *RouterService) cleanup(state *connectionState) {
    if state.subs != nil {
        state.subs.Close()  // ← Closes AMQP channel
    }
    if state.conn != nil {
        state.conn.Close()
    }
}
```

### Problem Confirmed
✅ **AUDIT FINDING VALID**: When `notifyClose` fires:
1. Cleanup is called synchronously
2. AMQP channel is closed
3. Any in-flight delivery operations fail
4. Message settlement semantics violated (could become at-least-once instead of at-most-once)

### Root Cause
Connection loss handling doesn't coordinate with active message processing. Workers might be mid-delivery while cleanup happens.

### Impact Assessment
- **Severity Confirmed:** HIGH
  - Delivery guarantees violated (at-most-once → at-least-once)
  - Duplicate processing risk when reconnected
  - Billing correctness: user could be charged twice
  - Silent failure (no error propagation to caller)

### Required Fix
**Must implement one of:**

**Option A: Graceful Shutdown of Workers**
```go
func (s *RouterService) billingWorker(ctx context.Context, state *connectionState) {
    defer s.wg.Done()
    
    for {
        select {
        case <-ctx.Done():
            // Manager is shutting down, exit cleanly
            return
        case <-state.notifyClose:
            // Connection lost, exit worker
            // In-flight deliveries will be auto-requeued by broker
            return
        case delivery, ok := <-state.subs.Billing:
            if !ok { return }
            if err := s.processBillingDelivery(delivery); err != nil {
                s.logger.Errorf("processing error: %v", err)
            }
        }
    }
}
```

**Option B: Don't Let Workers Overlap Reconnect**
- Have manager spawn fresh workers only after connection is fully established
- Ensure old workers are reaped before reconnect loop

**Option C: Processing Timeout**
- Bound processing time so messages never stay in-flight too long
- Example: Context with 5-second timeout per message

### Recommended Approach
Combine Options A + C:
1. Workers monitor `ctx.Done()` AND `state.notifyClose`
2. Each message has processing timeout (context.WithTimeout)
3. Manager signals workers to exit before cleanup

**Proposed fix found in:** PHASE_2_29_IMPLEMENTATION_GUIDE.md (billingWorker) ✅  
**Current service.go:** ❌ NOT IMPLEMENTED

---

## Audit Issue #5: Unbuffered Channel Deadlock Risk

### Audit Finding
**Severity:** HIGH  
**Problem:** `notifyClose` channel can deadlock if:
- Multiple threads try to signal simultaneously
- Signaling happens during locked section
- Buffer fills and blocks caller

**Audit Scenario:**
```go
func (s *RouterService) Stop() {
    cancel()            // Signal context
    s.wg.Wait()        // WAIT FOR GOROUTINE
    // But if notifyClose buffer full, runManager blocked sending
    // and never processes cancel signal → deadlock
}
```

### Current Implementation (`service.go`)

```go
// Line 65: Production uses buffered channel
notifyClose: make(chan error, 1)

// But test code can still block on send:
type mockConnector struct {
    connectFunc func(...) (*connectionState, error)
}

// Test at line 140:
s.notifyClose <- fmt.Errorf("connection lost")  // ← Can block if buffer full
```

### Problem Confirmed
✅ **AUDIT FINDING VALID**: While buffer=1 helps, there's still deadlock risk:
1. If `runManager()` is **not waiting** on `notifyClose` (e.g., stuck in `select` on another channel)
2. And test/code tries to send → **blocks**
3. If holding `s.mu`, blocks goroutine trying to acquire lock
4. Potential deadlock scenario exists

### Root Cause
Manual channel signaling introduces coordination complexity. Better to eliminate the channel entirely and use AMQP's native `notifyClose` only.

### Impact Assessment
- **Severity Confirmed:** HIGH
  - Latent deadlock risk (doesn't happen often but is catastrophic)
  - Test reliability issues
  - Hard to debug (race detector might not catch it)

### Required Fix
**Best option: Remove `s.notifyClose` entirely**
```go
// Instead of:
s.notifyClose <- err

// Use AMQP's native notifyClose:
for {
    select {
    case <-ctx.Done():
        return
    case <-currentState.notifyClose:
        // Connection lost, natural event flow
    }
}
```

**Fallback option: Use non-blocking send**
```go
func (s *RouterService) notifyConnectionLoss(err error) {
    select {
    case s.notifyClose <- err:
        // Sent
    default:
        // Buffer full, log and drop
        s.logger.Warnf("notifyClose buffer full, dropping signal")
    }
}
```

**Proposed fix found in:** PHASE_2_29_IMPLEMENTATION_GUIDE.md (removes `s.notifyClose` entirely) ✅  
**Current service.go:** ❌ STILL HAS DEADLOCK RISK

---

## Audit Issue #6: Context Propagation Bypass

### Audit Finding
**Severity:** HIGH  
**Problem:** Initial connection respects caller's context deadline, but reconnections use `context.Background()`.

**Audit Quote:**
```go
runCtx, cancel := context.WithCancel(context.Background())  // ← New context!
s.cancel = cancel
s.running = true

state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)  // ← Caller's ctx
// ...
go s.runManager(runCtx, state)  // ← runCtx is context.Background()
```

**Result:**
- Initial connection: respects caller's timeout ✓
- Reconnections: ignore caller's deadline (run forever) ✗

### Current Implementation (`service.go`)

```go
func (s *RouterService) Start(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // ...
    runCtx, cancel := context.WithCancel(context.Background())  // ← WRONG
    s.cancel = cancel
    s.running = true
    
    state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
    // ...
    go s.runManager(runCtx, state)  // ← Disconnected from caller's context
    
    return nil
}
```

### Problem Confirmed
✅ **AUDIT FINDING VALID**: The current code:
1. Creates `runCtx` from `context.Background()` (no deadline)
2. Initial connection uses caller's `ctx` (respects deadline)
3. Reconnection loop uses `runCtx` (ignores deadline)
4. Caller's context deadline is bypassed

### Scenario
```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
s.Start(ctx)                    // Initial connection respects 30s timeout
// ... 5s later, network partitions ...
// Reconnection will ignore the 30s deadline
// Manager runs indefinitely, even after caller context expires
```

### Root Cause
Intentional isolation: to allow manager to run independently even if caller context expires. But this violates context semantics (context should propagate shutdown signals).

### Impact Assessment
- **Severity Confirmed:** HIGH
  - Resource cleanup unpredictable (orphan goroutines)
  - Integration complexity (caller can't use context deadline)
  - Test flakiness (tests must always call `Stop()` explicitly)
  - Production gotcha (service doesn't follow context contract)

### Required Fix
**Tie `runCtx` to caller's context:**
```go
func (s *RouterService) Start(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.running {
        return fmt.Errorf("router service already running")
    }
    
    // Create cancellable context but inherit caller's deadline
    runCtx, cancel := context.WithCancel(ctx)  // ← Tie to caller's context
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

**Trade-off:** If caller's context expires, `runCtx` cancels and service stops. Caller must be aware of this behavior.

**Proposed fix found in:** PHASE_2_29_IMPLEMENTATION_GUIDE.md ✅  
**Current service.go:** ❌ NOT FIXED (still uses context.Background())

---

## Audit Issue #7: Missing Observability/Logging

### Audit Finding
**Severity:** MEDIUM  
**Problem:** No logging, no visibility into reconnects, failures, or message processing.

**Audit Quote:**
```
_ = closeErr  // ← Explicitly discarding error info!
```

### Current Implementation (`service.go`)

```go
func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    // ... no logger field ...
    
    for {
        select {
        case <-ctx.Done():
            s.cleanup(currentState)  // ← Silent exit, no logging
            return
        case err := <-s.notifyClose:
            closeErr = err           // ← Stored but never used
        case amqpErr := <-currentState.notifyClose:
            if amqpErr != nil {
                closeErr = amqpErr   // ← Stored but never used
            }
        }
        
        s.cleanup(currentState)
        currentState = nil
        _ = closeErr  // ← EXPLICITLY DISCARDING!
    }
}
```

### Problem Confirmed
✅ **AUDIT FINDING VALID**: The current code:
1. Has NO logger field
2. Silently discards connection loss errors
3. Doesn't log reconnect attempts
4. No visibility into message processing failures

### Impact Assessment
- **Severity Confirmed:** MEDIUM
  - Production debugging impossible
  - Integration testing opacity
  - SRE has no visibility (can't monitor throughput/latency)
  - Silent failures = firefighting instead of prevention

### Required Fix
Add structured logging throughout:
```go
type RouterService struct {
    // ... existing ...
    logger Logger  // slog, logrus, or custom
}

func (s *RouterService) runManager(ctx context.Context, initialState *connectionState) {
    defer s.wg.Done()
    
    currentState := initialState
    
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
        
        // Reconnect with logging
        for {
            select {
            case <-ctx.Done():
                return
            case <-time.After(backoff):
                s.logger.Info("reconnect attempt", "backoff", backoff)
                newState, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
                if err != nil {
                    s.logger.Error("reconnect failed", "error", err, "retry_in", backoff*2)
                    backoff *= 2
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

**Proposed fix found in:** PHASE_2_29_IMPLEMENTATION_GUIDE.md ✅  
**Current service.go:** ❌ NOT IMPLEMENTED

---

## Audit Issue #8: Goroutine Leak on Rapid Reconnects

### Audit Finding
**Severity:** MEDIUM  
**Problem:** `time.After()` timers leak goroutines on context cancellation.

**Audit Quote:**
```go
for {
    select {
    case <-ctx.Done():
        return  // Timer goroutine still running!
    case <-time.After(backoff):  // ← Creates new timer on every iteration
    }
}
```

Each `time.After()` creates a goroutine that runs until timeout or timer is explicitly stopped.

### Current Implementation (`service.go`)

```go
for {
    select {
    case <-ctx.Done():
        return
    case <-time.After(backoff):
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
    }
}
```

### Problem Confirmed
✅ **AUDIT FINDING VALID**: The code uses `time.After()` which:
1. Creates a new goroutine on every iteration
2. If `ctx.Done()` fires first, timer is orphaned
3. Goroutines accumulate on reconnect storms

### Scenario
```
Reconnect loop: iteration 1
  → time.After(1s) created [goroutine leaks if ctx closes]
  → ctx.Done() fires → return
  → time.After() goroutine still running in background
```

Over time, goroutines accumulate and exhaust system resources.

### Impact Assessment
- **Severity Confirmed:** MEDIUM
  - Memory leak in long-running services
  - Goroutine explosion on chaotic networks
  - Eventually crashes due to resource exhaustion
  - Detectable with `go test -count=10 -race`

### Required Fix
Replace `time.After()` with explicit `time.Timer`:
```go
for {
    timer := time.NewTimer(backoff)
    select {
    case <-ctx.Done():
        timer.Stop()  // ← Cleanup before returning
        return
    case <-timer.C:
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
    }
}
```

**Proposed fix found in:** PHASE_2_29_IMPLEMENTATION_GUIDE.md ✅  
**Current service.go:** ❌ STILL LEAKS GOROUTINES

---

## Summary: Audit Findings vs. Current Implementation

| Issue | Severity | Audit Finding | Current Code | Status |
|-------|----------|---------------|--------------|--------|
| Missing Message Pump | CRITICAL | ✅ Valid | ❌ Not implemented | BLOCKER |
| Start/Stop Race | CRITICAL | ✅ Valid | ❌ Not fixed | BLOCKER |
| Unsettled Deliveries | CRITICAL | ✅ Valid | ❌ Not applicable yet | BLOCKER (depends on pump) |
| Connection Loss During Processing | HIGH | ✅ Valid | ❌ Not fixed | BLOCKER |
| Unbuffered Channel Deadlock | HIGH | ✅ Valid | ⚠️  Buffered but risky | RISK |
| Context Propagation Bypass | HIGH | ✅ Valid | ❌ Not fixed | BLOCKER |
| Missing Observability | MEDIUM | ✅ Valid | ❌ Not implemented | IMPORTANT |
| Goroutine Leaks | MEDIUM | ✅ Valid | ❌ Leaks on cancel | ISSUE |

---

## EdgeCases NOT Covered by Audit Report

### 1. **Worker Goroutine Lifecycle During Reconnect**

**Issue:** When `notifyClose` fires (connection lost), current workers are still running. They might try to ACK/Reject on a closed channel.

**Scenario:**
```
1. billingWorker reading from state.subs.Billing
2. notifyClose fires → runManager calls cleanup()
3. cleanup() closes state.subs (AMQP channel)
4. billingWorker tries delivery.Ack() → ERROR (channel closed)
5. No error propagation, message in limbo
```

**Audit Coverage:** Mentioned in Issue #4 but not fully addressed.

**Required Fix:**
- Workers must handle closed channel gracefully
- Monitor both `ctx.Done()` AND `state.notifyClose`
- Exit cleanly when connection lost (don't try to settle)

---

### 2. **Lost Message State on Channel Close**

**Issue:** When AMQP channel closes, all in-flight deliveries are lost. Go's `amqp091-go` doesn't buffer them.

**Scenario:**
```
1. billingWorker processes message M1 (takes 2 seconds)
2. Network packet loss → connection drops
3. cleanup() → channel.Close()
4. Broker times out waiting for ACK/Reject
5. Broker REQUEUE message → duplicated when reconnected
```

**Audit Coverage:** Partially covered in Issue #4 but not thoroughly.

**Required Fix:**
- Document that reconnection may cause duplicates (at-least-once semantics)
- Ensure billing transactions are idempotent
- Add deduplication if needed (would be at billing service layer, not router)

---

### 3. **Message Ordering Across Reconnects**

**Issue:** After reconnect, older messages from broker get redelivered before newer messages.

**Scenario:**
```
Broker queue: [M1, M2, M3]
1. Worker processes M1
2. Connection drops during M1 processing
3. Reconnect
4. Broker redelivers M1 (might be at front of queue again)
5. Worker processes M1 again (duplicate)
```

**Audit Coverage:** Not mentioned.

**Required Fix:**
- Billing transactions must be idempotent
- Don't assume FIFO ordering across reconnects
- Add deduplication ID (transaction hash?) if needed

---

### 4. **Backoff Jitter Missing**

**Issue:** Exponential backoff without jitter can cause thundering herd on broker reconnection.

**Current Code:**
```go
backoff *= 2
if backoff > maxBackoff {
    backoff = maxBackoff
}
```

All instances retry at same time → broker flooded.

**Audit Coverage:** Not mentioned.

**Proposed Fix (from PHASE_2_29 guide):**
```go
backoff = time.Duration(float64(backoff) * (1.0 + 0.1*rand.Float64()))  // Add jitter
```

---

### 5. **No Max Retry Limit**

**Issue:** If AMQP broker is down for hours, manager reconnects forever, consuming CPU.

**Current Code:**
```go
for {
    select {
    case <-time.After(backoff):
        newState, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
        if err != nil {
            backoff *= 2
            if backoff > maxBackoff {
                backoff = maxBackoff
            }
            continue  // ← No circuit breaker
        }
    }
}
```

**Audit Coverage:** Not mentioned.

**Recommended Fix:**
- Add circuit breaker pattern (fail fast after N retries)
- Or add max backoff reached alert (currently maxBackoff=30s)
- Document SLO: "Service will reconnect for 30 seconds max, then stop trying"

---

### 6. **No Graceful Drain on Shutdown**

**Issue:** When `Stop()` is called, current message pumps exit immediately without flushing pending messages.

**Scenario:**
```
1. Stop() called
2. runCtx.Cancel() fires
3. Workers exit immediately
4. In-flight messages never settle → broker redelivers
```

**Audit Coverage:** Partially in Issue #4.

**Recommended Fix:**
- Add drain phase before cancellation
- Give workers time to finish current message (with timeout)
- Then shutdown cleanly

---

### 7. **No Backpressure Handling**

**Issue:** If billing service is slow (processing takes 10+ seconds), consumer buffer overflows.

**Current Setup:**
```
QoS prefetch_count=1  // Only 1 message at a time
```

This is good, but if worker is **blocked** (not even reading), no backpressure.

**Audit Coverage:** Not mentioned (assumes good behavior).

**Recommended Fix:**
- Add processing timeout (e.g., 5-second context timeout per message)
- If processing stalls, cancel and reject message
- Prevents slow billing service from starving router

---

### 8. **No Metrics/Instrumentation for LateBilling**

**Issue:** No way to monitor:
- Billing message throughput
- Processing latency per message
- Error rates (by type: transient vs permanent)
- Requeue depth
- Duplicate processing

**Audit Coverage:** Partially in Issue #7 (mentions SRE visibility).

**Recommended Fix:**
- Add metrics (counters, histograms)
- Example:
  ```
  router.billing.messages_received (counter)
  router.billing.processing_latency_ms (histogram)
  router.billing.errors_total (counter, with status label)
  router.billing.requeue_depth (gauge)
  ```

---

## Pitfalls NOT Caught by Current Tests

### 1. **Race Between Start and Stop**
Current tests don't stress rapid start/stop:
```go
// Good test but missing:
s.Start(ctx)
s.Stop()
s.Start(ctx)  // Could race
s.Stop()
```

### 2. **Connection Loss During Startup**
What if broker goes down between `s.Start()` and goroutine initialization?

### 3. **Subscriber Channel Full**
What if `Billing` channel fills up and no one is reading? (Currently not possible since no pump, but future issue)

### 4. **Reconnect While Processing**
No test simulates: message processing → connection drops → reconnect → retry same message

---

## Recommendations for Implementation

### Phase 1: Critical Fixes (Required before LateBilling integration)
1. ✅ Implement message pump for `Billing` channel
2. ✅ Add Start() synchronization barrier (WaitGroup)
3. ✅ Fix context propagation (tie runCtx to caller's ctx)
4. ✅ Replace `time.After()` with explicit timers
5. ✅ Remove `s.notifyClose` or make it non-blocking

### Phase 2: High-Priority Fixes (Should implement alongside Phase 1)
1. ✅ Define error handling policy (transient vs permanent)
2. ✅ Add graceful worker shutdown on connection loss
3. ✅ Add structured logging

### Phase 3: Polish (Nice to have)
1. ❓ Add backoff jitter
2. ❓ Add circuit breaker for failed reconnects
3. ❓ Add processing timeout per message
4. ❓ Add metrics/instrumentation
5. ❓ Implement graceful drain on shutdown

---

## Current Implementation Status

**Found:** PHASE_2_29_IMPLEMENTATION_GUIDE.md contains **all proposed fixes**.

**Not Yet Applied:** The fixes are documented but NOT yet merged into `service.go`.

**To complete audit findings:**
1. Apply PHASE_2_29 implementation
2. Add tests for all edge cases
3. Run with `-race` flag
4. Verify goroutine cleanup with `-run` leak detector

---

## Conclusion

The audit report is **accurate and comprehensive**. All 8 findings are validated by code inspection. The current `service.go` is **not production-ready** for LateBilling integration.

**BLOCKER ISSUES (Must Fix):**
- ❌ No message pump
- ❌ Start/Stop race
- ❌ No error handling policy
- ❌ Connection loss semantics undefined
- ❌ Context propagation broken

A proposed implementation exists (PHASE_2_29), but it must be:
1. Reviewed for completeness
2. Tested with edge cases
3. Integrated into service.go
4. Verified with race detector

**Estimated effort to resolve:** 2-3 days (implementation + testing + edge case handling).

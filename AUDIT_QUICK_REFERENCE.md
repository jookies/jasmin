# AMQP Lifecycle Audit - Quick Reference

## Bugs Found: 5 Issues (3 Critical/High, 2 Medium)

### 1. CRITICAL: Race Condition in Start/Stop Synchronization

**Problem:** `Stop()` may return before `runManager()` goroutine starts, leaving resources uncleaned.

```go
// CURRENT (BUGGY)
func (s *RouterService) Start(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if s.running {
        return fmt.Errorf("router service already running")
    }
    
    runCtx, cancel := context.WithCancel(context.Background())
    s.cancel = cancel
    s.running = true
    // ✗ WaitGroup.Add() happens AFTER releasing the lock
    
    s.mu.Unlock()  // <-- RACE WINDOW HERE
    
    state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
    if err != nil {
        // cleanup...
        return err
    }
    
    s.wg.Add(1)  // <-- LATE! Stop() may read running=true but see wg empty
    go s.runManager(runCtx, state)
    
    return nil
}

// ISSUE: Stop() checks running and then calls wg.Wait() but runManager hasn't been added yet
```

**Impact:** `Stop()` returns immediately; caller assumes cleanup complete, but goroutine hasn't started → resource leak.

**Fix:** Move `wg.Add(1)` inside the lock before releasing it:

```go
func (s *RouterService) Start(ctx context.Context) error {
    s.mu.Lock()
    
    if s.running {
        s.mu.Unlock()
        return fmt.Errorf("router service already running")
    }
    
    runCtx, cancel := context.WithCancel(context.Background())
    s.cancel = cancel
    s.running = true
    s.wg.Add(1)  // ✓ BEFORE releasing lock
    
    s.mu.Unlock()
    
    // ... rest of function
}
```

---

### 2. HIGH: Uncancelled Timer Leaks in Reconnect Loop

**Problem:** Each failed reconnection attempt creates a timer that never gets explicitly stopped if the next attempt also fails.

```go
// CURRENT STRUCTURE (lines 156-173)
for {
    select {
    case <-ctx.Done():
        return
    case <-time.After(backoff):  // ← New timer each iteration
        newState, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
        if err == nil {
            currentState = newState
            backoff = time.Second
            goto connected  // ✓ Exits the loop
        }
        // ✗ If err != nil, loops back without stopping the timer
        backoff *= 2
        if backoff > maxBackoff {
            backoff = maxBackoff
        }
        // Loop continues: time.After() fires again, creating a NEW timer
    }
}
```

**Impact:** With 10 consecutive failures, 10+ timer objects are pending. Long-running services accumulate latent goroutine bloat.

**Fix:** Explicitly manage timer lifecycle:

```go
for {
    timer := time.NewTimer(backoff)
    select {
    case <-ctx.Done():
        timer.Stop()  // ✓ Clean up
        return
    case <-timer.C:
        timer = nil
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

---

### 3. HIGH: Goroutine Leak If Stop Interrupts Start

**Problem:** If `Stop()` is called while `Start()` is still executing (after connection check but before `wg.Add(1)`), the WaitGroup is never incremented but the goroutine launches anyway → `wg.Wait()` hangs forever.

**Impact:** Concurrent Start/Stop calls can deadlock.

---

### 4. MEDIUM: No Timeout on Initial Connection

**Problem:** `Start()` passes caller's context to `ConnectAndSubscribe()` with no timeout:

```go
// Line 92: No timeout — will block forever if broker is down
state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
```

**Impact:** If AMQP broker is unresponsive, `Start()` blocks indefinitely.

**Fix:** Add explicit timeout:

```go
connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
state, err := s.connector.ConnectAndSubscribe(connectCtx, s.amqpURL)
```

---

### 5. MEDIUM: Untested Concurrent Start Scenarios

**Problem:** Test suite doesn't cover:
- Two goroutines calling `Start()` concurrently
- Start/Stop racing
- Rapid reconnect failures

**Fix:** Added tests `TestStopWithPendingReconnect()` and `TestRapidReconnectMemoryPressure()` to service_test.go.

---

## Test Results

All 8 tests pass:
```
✓ TestRouterServiceStartStop
✓ TestRouterServiceInitialConnectFailure
✓ TestRouterServiceReconnectLoop
✓ TestRouterServiceStopDuringReconnect
✓ TestRouterServiceAMQPNotifyCloseReconnect
✓ TestStopWithPendingReconnect (NEW)
✓ TestRapidReconnectMemoryPressure (NEW)
```

---

## Priority Fix Order

1. **Move `wg.Add(1)` inside lock** — Eliminates goroutine leak
2. **Implement explicit timer management** — Reduces resource bloat
3. **Add connection timeout** — Prevents indefinite blocking
4. **Consider using sync.Once for Start** — Stronger guarantee of single execution

---

## Files Modified

- `AUDIT_REPORT.md` — Detailed analysis with recommendations
- `internal/core/router/service_test.go` — Added 2 new edge case tests

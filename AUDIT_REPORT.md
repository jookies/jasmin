# AMQP Lifecycle Audit Report: internal/core/router/service.go

## Executive Summary
Found **3 critical/high-severity issues** and **2 medium-severity issues** in AMQP lifecycle management. The most severe issue is a **resource leak under rapid reconnection scenarios**.

---

## Critical Issues

### 1. **RESOURCE LEAK: Orphaned notifyClose Channel on Failed Reconnection**
**Severity:** CRITICAL  
**Location:** `runManager()` method, reconnect loop (lines 156–173)

#### Problem
When `ConnectAndSubscribe()` fails during reconnection, the previous `notifyClose` channel from the failed attempt is never drained or explicitly closed. Over repeated failed reconnection attempts, buffered channels accumulate in the select statement.

**Code Flow:**
```go
for {  // reconnect loop (line 156)
    select {
    case <-ctx.Done():
        return
    case <-time.After(backoff):
        newState, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
        if err == nil {
            currentState = newState  // ✓ Assigned on success
            // ...
            goto connected
        }
        // ✗ If err != nil, loop continues WITHOUT updating currentState
        backoff *= 2
        // Next iteration: time.After fires again, but currentState is still nil
    }
}
```

**Consequences:**
- After a failed reconnection attempt, the next iteration's `time.After()` creates a new timer
- If connections fail 10 times in succession, up to 10 timer objects may be pending
- Buffered channels (size 1) from mock states in tests could accumulate

**Example Scenario:**
1. Initial connection succeeds → `currentState` assigned
2. Connection drops → cleanup called, `currentState = nil`
3. Reconnect attempt 1 fails → no state update, loop continues
4. Reconnect attempt 2 fails → no state update, loop continues
5. ... (repeats 10+ times during network outage)

While timers will eventually fire, Go's timer GC is imperfect; in a long-running service with frequent brief outages, this manifests as latent goroutine/memory bloat.

#### Recommended Fix
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
        // Log the error and continue with backoff increase
        backoff *= 2
        if backoff > maxBackoff {
            backoff = maxBackoff
        }
        // Continue: loop will fire time.After again with accumulated backoff
    }
}
```

This is actually already correct in structure, but the issue is **the select loop itself doesn't drain stale channels between iterations**. A better pattern:

```go
for {
    timer := time.NewTimer(backoff)
    select {
    case <-ctx.Done():
        timer.Stop()
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

### 2. **RACE CONDITION: Stop() May Not Block Waiting for Cleanup**
**Severity:** HIGH  
**Location:** `Stop()` method (lines 106–123)

#### Problem
In the `Stop()` method, there is a subtle race between checking `running` and calling `wg.Wait()`:

```go
func (s *RouterService) Stop() {
    s.mu.Lock()
    cancel := s.cancel
    running := s.running
    s.mu.Unlock()

    if !running || cancel == nil {
        return  // ✗ Early return without waiting
    }

    cancel()
    s.wg.Wait()  // ← Goroutine may NOT have been added yet!

    s.mu.Lock()
    s.running = false
    s.cancel = nil
    s.mu.Unlock()
}
```

**Race Window:**
1. Thread A calls `Start()` → acquires lock, sets `s.running = true`, releases lock, then **adds `s.wg.Add(1)`**
2. Thread B calls `Stop()` → acquires lock, reads `running = true`, releases lock
3. Thread B calls `cancel()`
4. **Before `s.wg.Add(1)` executes**, Thread B calls `wg.Wait()` → returns immediately (WaitGroup is empty)
5. Thread B returns and signal the caller that cleanup is done
6. Caller assumes cleanup completed, but `runManager()` goroutine is about to start

**Consequences:**
- Caller of `Stop()` may return before `runManager()` is even launched
- If caller exits the program, `runManager()` is killed before it can run
- Resource cleanup (`cleanup()` method) may never execute
- AMQP subscriptions/connections may be left open

#### Root Cause
The WaitGroup is incremented **after** checking the running state. The lock is released before `wg.Add()`.

#### Recommended Fix
Move the `wg.Add(1)` **inside the lock**:

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
    s.wg.Add(1)  // ← MOVE INSIDE LOCK
    s.mu.Unlock()

    go s.runManager(runCtx, state)
    
    return nil
}
```

Wait, defer unlock happens after Add, so this is wrong. Better approach:

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
    s.wg.Add(1)  // ← Add BEFORE releasing lock

    s.mu.Unlock()

    state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
    if err != nil {
        s.mu.Lock()
        cancel()
        s.running = false
        s.wg.Done()  // ← Compensate
        s.cancel = nil
        s.mu.Unlock()
        return fmt.Errorf("initial AMQP connection failed: %w", err)
    }

    go s.runManager(runCtx, state)
    return nil
}
```

---

### 3. **GOROUTINE LEAK: Stop Called Before Start Completes**
**Severity:** HIGH  
**Location:** `Stop()` method race with `Start()` (lines 79–103)

#### Problem
If `Stop()` is called while `Start()` is executing (after initial connection, but before `runManager` is launched), the WaitGroup is never decremented.

**Scenario:**
1. Thread A: `Start()` → lock, set `running = true`, release lock
2. Thread B: `Stop()` → lock, read `running = true`, release lock, call `cancel()`
3. Thread A: Initial connection fails → lock, set `running = false`, release lock, return error
4. But `s.wg.Add(1)` was never called in Start
5. Thread B: `wg.Wait()` hangs forever (or returns if never added)
6. OR: if connection succeeds and `wg.Add()` was called after B's wait check, goroutine leaks

#### Consequences
- Goroutine never exits
- `Stop()` may block indefinitely
- Resource cleanup never happens

---

## Medium Issues

### 4. **Untested Edge Case: Simultaneous Start Calls**
**Severity:** MEDIUM  
**Location:** `Start()` method (lines 79–103)

#### Problem
The current test `TestRouterServiceStartStop()` verifies that calling `Start()` twice fails, but doesn't test:
- What happens if two goroutines call `Start()` truly concurrently?
- Does the AMQP connector get called more than once?
- Are resources leaked?

Current code is OK due to the mutex, but the test coverage is incomplete.

#### Recommended Test Addition
```go
func TestRouterServiceConcurrentStart(t *testing.T) {
    connector := &mockConnector{}
    s := NewRouterService("amqp://localhost")
    s.connector = connector

    ctx := context.Background()
    
    var wg sync.WaitGroup
    errors := make([]error, 2)
    
    for i := 0; i < 2; i++ {
        wg.Add(1)
        go func(idx int) {
            defer wg.Done()
            errors[idx] = s.Start(ctx)
        }(i)
    }
    
    wg.Wait()
    
    // Exactly one Start should succeed
    successCount := 0
    for _, err := range errors {
        if err == nil {
            successCount++
        }
    }
    if successCount != 1 {
        t.Errorf("expected 1 successful Start, got %d", successCount)
    }
    
    s.Stop()
}
```

---

### 5. **Potential Deadlock: Context Passed to ConnectAndSubscribe**
**Severity:** MEDIUM  
**Location:** `Start()` method line 92, `runManager()` line 161

#### Problem
```go
// Line 92 in Start()
state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)

// Line 161 in runManager()
newState, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)
```

The `ctx` parameter passed to `ConnectAndSubscribe()` is the caller's context in `Start()`, but in `runManager()` it's the `runCtx` (which is correct). However:

- If the caller's context (the one passed to `Start()`) is cancelled before the initial connection completes, the connection will be interrupted mid-flight
- If topology operations block, the cancellation propagates
- No timeout is set; if AMQP broker is unresponsive, initial `Start()` will block indefinitely waiting for connection

#### Consequences
- `Start()` may block forever if broker is down
- Caller cannot set a timeout for initial connection

#### Recommended Fix
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

    // Use a separate context for initial connection with timeout
    connectCtx, connectCancel := context.WithTimeout(context.Background(), 10*time.Second)
    state, err := s.connector.ConnectAndSubscribe(connectCtx, s.amqpURL)
    connectCancel()
    
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

---

## Summary Table

| Issue | Severity | Location | Impact | Status |
|-------|----------|----------|--------|--------|
| Orphaned timers on reconnect failure | CRITICAL | runManager reconnect loop | Resource leak, memory bloat | Unfixed |
| Race in Stop() → WaitGroup check | HIGH | Stop() / Start() | Goroutine leak, incomplete cleanup | Unfixed |
| Goroutine leak if Stop called during Start | HIGH | Stop() / Start() race window | Goroutine leak, hanging Stop | Unfixed |
| Concurrent Start() not tested | MEDIUM | service_test.go | Coverage gap | Test needed |
| No timeout on initial connection | MEDIUM | Start() → ConnectAndSubscribe | Blocks indefinitely | Needs fix |

---

## Recommended Testing Additions

```go
// Test concurrent stop calls
func TestRouterServiceConcurrentStop(t *testing.T) {
    connector := &mockConnector{}
    s := NewRouterService("amqp://localhost")
    s.connector = connector

    ctx := context.Background()
    err := s.Start(ctx)
    if err != nil {
        t.Fatalf("Start failed: %v", err)
    }

    var wg sync.WaitGroup
    for i := 0; i < 5; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            s.Stop()
        }()
    }
    
    done := make(chan struct{})
    go func() {
        wg.Wait()
        close(done)
    }()

    select {
    case <-done:
        // Success
    case <-time.After(5 * time.Second):
        t.Fatal("concurrent Stop calls caused deadlock")
    }
}

// Test rapid connection loss and recovery
func TestRouterServiceRapidReconnect(t *testing.T) {
    connectCount := 0
    connector := &mockConnector{
        connectFunc: func(ctx context.Context, amqpURL string) (*connectionState, error) {
            connectCount++
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

    // Trigger 10 rapid connection losses
    for i := 0; i < 10; i++ {
        s.notifyClose <- fmt.Errorf("connection lost %d", i)
        time.Sleep(50 * time.Millisecond)
    }

    s.Stop()
    
    if connectCount < 11 {
        t.Errorf("expected at least 11 connection attempts, got %d", connectCount)
    }
}
```

---

## Conclusion
The most critical bug is the **race condition in Start/Stop synchronization**, which can cause goroutine leaks and incomplete resource cleanup. The secondary issue is **untested concurrent scenarios** that could hide deadlocks in production.

Priority fixes:
1. Move `wg.Add()` to happen atomically with setting `running = true`
2. Add connection timeout to initial connect attempt
3. Implement test suite for concurrent Start/Stop scenarios

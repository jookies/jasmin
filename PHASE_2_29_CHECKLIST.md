# Phase 2.29 Router Lifecycle — Code Review Checklist

Pre-merge verification checklist for Phase 2.29 implementation.

---

## A. Race Condition Detection

### A.1: Concurrency Analysis

- [ ] **Mutex usage**: All shared mutable state protected by mutex?
  - `started` flag (mut protected)
  - `cancel` function (mutex protected)
  - `running` flag (atomic, lock-free)
  - Fields in runManager (all stack-local, no sharing)

- [ ] **Lock acquisition order** consistent?
  - Lock acquired in Start(), released BEFORE spawn
  - Lock acquired in Stop(), released BEFORE cancel()
  - No nested locks (only one mutex: the service-level mu)

- [ ] **Atomic operations** for concurrent reads?
  - `running` field uses `atomic.Bool` (safe concurrent reads/writes)
  - No manual CAS (compare-and-swap) needed

- [ ] **Channel safety**?
  - notifyClose channel removed ✓ (was source of RC-002)
  - Uses amqp.Connection.NotifyClose() instead (safe, owned by runManager goroutine)

### A.2: Race Detector Pass

Run:
```bash
go test -race -v ./internal/core/router/... -timeout=10s
```

Expected: **All tests pass with no race warnings**

- [ ] No race detected in Start()
- [ ] No race detected in Stop()
- [ ] No race detected in runManager()
- [ ] No race detected in test helpers (mockConnector, mockAMQPConnection)

---

## B. Resource Leak Detection

### B.1: Connection Lifecycle

- [ ] **AMQP connection opened** in Start() or reconnect ✓
- [ ] **AMQP connection closed** in:
  - [ ] Successful startup? (no, owned by runManager)
  - [ ] Start() failure path? (no new conn created on failure)
  - [ ] ctx.Done() in monitoring loop? ✓ (calls cleanupResources)
  - [ ] ctx.Done() in reconnect backoff? ✓ (calls cleanupResources)
  - [ ] Broker closure detected? ✓ (calls cleanupResources on NotifyClose)
  - [ ] Failed reconnect attempt? ✓ (newConn.Close() on topology error)

### B.2: Subscription Lifecycle

- [ ] **Subscriptions opened** only on successful Dial+Topology ✓
- [ ] **Subscriptions closed** in all error paths
  - [ ] Start() failure? (no subscriptions created)
  - [ ] reconnect backoff exit? ✓ (cleanupResources)
  - [ ] topology declaration failure? ✓ (newSubs = nil, not stored)
  - [ ] broker closure? ✓ (cleanupResources)

### B.3: Goroutine Lifecycle

- [ ] **Goroutine spawned** with wg.Add(1) BEFORE spawn ✓
- [ ] **Goroutine exits** in:
  - [ ] ctx.Done() in monitoring? ✓ (defer wg.Done())
  - [ ] ctx.Done() in reconnect? ✓ (defer wg.Done())
  - [ ] All paths return or panic? ✓ (defer wg.Done() in ALL paths)

### B.4: File Descriptor Leak Test

Run:
```bash
# Before test
lsof -p $$ | grep ESTABLISHED | wc -l

# Run test
go test -v -run TestRouterServiceNoGoroutineLeaks ./internal/core/router/...

# After test (should be same or +1-2)
lsof -p $$ | grep ESTABLISHED | wc -l
```

Expected:
- [ ] File descriptor count same before and after (±2)
- [ ] No CLOSE_WAIT connections (would indicate partial close)
- [ ] No TIME_WAIT connections (should fully close)

---

## C. AMQP Best Practices

### C.1: Connection Closure Detection

- [ ] **NotifyClose() wired?** ✓ (line in runManager)
- [ ] **Error channel buffered?** ✓ (capacity 1)
- [ ] **Closure detected immediately** (not polled)?
  - [ ] Not waiting for next backoff timer ✓
  - [ ] Not waiting for wg.Wait() ✓

### C.2: Topology Redeclaration

- [ ] **Topology redeclared on every reconnect?** ✓ (idempotent)
- [ ] **Durable settings correct?** (Match legacy: non-durable)
  - [ ] Exchanges: `durable=false`?
  - [ ] Queues: `durable=false`?
  - [ ] Check against amqpcompat/topology.go for constants

### C.3: Context Cancellation

- [ ] **Pre-operation context check?** ✓ (before dial timeout)
- [ ] **Post-operation context check?** ✓ (after NotifyClose, after dial)
- [ ] **Cancellation observed in backoff loop?** ✓ (select on ctx.Done)

---

## D. Test Coverage

### D.1: Happy Path

- [ ] TestRouterServiceStartStop: Basic lifecycle ✓
- [ ] TestRouterServiceBrokerClosure: Connection loss detection ✓
- [ ] TestRouterServiceConcurrentStartStop: Interleaved operations ✓

### D.2: Error Paths

- [ ] TestRouterServiceStartAfterFail: Failed start, retry succeeds ✓
- [ ] TestRouterServiceStartTwice: Double start rejected ✓
- [ ] TestRouterServiceStopBeforeStart: Early stop safe ✓

### D.3: Shutdown Scenarios

- [ ] TestRouterServiceStopDuringFirstBackoff: Early backoff exit ✓
- [ ] TestRouterServiceStopDuringExtendedBackoff: Late backoff exit ✓
- [ ] TestRouterServiceStopTwice: Idempotent stop ✓

### D.4: Context & Cancellation

- [ ] TestRouterServiceStartContextCancelled: Setup timeout ✓
- [ ] Context cancelled during dial? (covered by mockConnector.dialDelay)
- [ ] Context cancelled during reconnect backoff? ✓

### D.5: Resource Cleanup

- [ ] TestRouterServiceNoGoroutineLeaks: All goroutines exit ✓
- [ ] Connection/subscription close verified? (via mock inspection)
- [ ] No listener goroutines left? (NotifyClose cleanup)

### D.6: Resilience

- [ ] TestRouterServiceBackoffJitter: Jitter is random ✓
- [ ] Multiple reconnect attempts logged? (observable via dialCount)
- [ ] Backoff caps at maxBackoff? (const in implementation)

### Coverage Target

- [ ] Line coverage ≥ 90%
- [ ] Branch coverage ≥ 85% (all select branches taken)
- [ ] All error paths exercised

Run:
```bash
go test -cover -coverprofile=coverage.out ./internal/core/router/...
go tool cover -html=coverage.out
```

---

## E. Code Quality

### E.1: Style & Readability

- [ ] **Comments explain WHY, not WHAT**?
  - [ ] "Atomic flag prevents double-start" ✓
  - [ ] "Lock released before cancel to prevent race" ✓
  - [ ] "All paths call cleanupResources" ✓

- [ ] **Variable names clear**?
  - [ ] `notifyClose` (broker closure detector) ✓
  - [ ] `currentConn`, `currentSubs` (local state) ✓
  - [ ] `backoff`, `jitterAmount` (self-documenting) ✓

- [ ] **Function names reflect responsibility**?
  - [ ] `addJitter` (clear purpose) ✓
  - [ ] `increaseBackoff` (clear purpose) ✓
  - [ ] `cleanupResources` (idempotent cleanup) ✓

### E.2: Error Handling

- [ ] **All errors logged or propagated?**
  - [ ] Start() failure returned to caller ✓
  - [ ] Dial failure increments backoff (logged by caller) ✓
  - [ ] Topology failure triggers reconnect (logged by caller) ✓

- [ ] **Error messages include context?**
  - [ ] "initial AMQP connection failed" ✓
  - [ ] "router service already started" ✓

### E.3: Defensive Programming

- [ ] **Nil checks where needed?**
  - [ ] cleanupResources: nil-safe on conn, subs ✓
  - [ ] Stop(): checks cancel != nil ✓
  - [ ] Start(): clears cancel on error ✓

- [ ] **Bounds checks?**
  - [ ] Backoff capped at maxBackoff ✓
  - [ ] Jitter fraction in range [-1, +1] ✓

---

## F. Performance & Latency

### F.1: Connection Latency

- [ ] **Initial connection delay** reasonable?
  - [ ] Single dial attempt (no retry loop) ✓
  - [ ] Provided context honored (timeout respected) ✓

- [ ] **Reconnect latency** bounded?
  - [ ] First retry: 1s backoff
  - [ ] Max retry: 30s backoff
  - [ ] Average: ~10s (exponential growth)

### F.2: Broker Closure Detection

- [ ] **Latency from closure to reconnect attempt** < 100ms?
  - [ ] NotifyClose fires on next select ✓
  - [ ] No polling delay ✓
  - [ ] No listener goroutine stall ✓

### F.3: Shutdown Latency

- [ ] **Stop() returns quickly** (not blocked on backoff)?
  - [ ] ctx.Done() fires immediately ✓
  - [ ] Pre-check prevents backoff timer creation ✓
  - [ ] Expected: < 100ms

---

## G. Logging & Observability

### G.1: Log Points Identified

- [ ] **At startup**: "Router service started" (caller responsibility)
- [ ] **At broker closure**: "Broker closed connection" (runManager logs)
- [ ] **At reconnect failure**: "Reconnect attempt failed" (caller/logger)
- [ ] **At successful reconnect**: "Reconnected to broker" (caller/logger)
- [ ] **At shutdown**: "Router service stopped" (caller responsibility)

Note: Current implementation doesn't include logging (caller passes logger if needed via callback or wrapping).

### G.2: Metrics Points

- [ ] Dial attempt counter (via dialCount in tests)
- [ ] Reconnect trigger count (broker closure vs backoff expiry)
- [ ] Connection uptime
- [ ] Backoff multiplier (diagnostic for retry behavior)

---

## H. Compatibility & Backward Compatibility

### H.1: API Changes

- [ ] **Start(ctx Context) error**: Same signature ✓
- [ ] **Stop()**: Same signature ✓
- [ ] **Return values**: Unchanged ✓

- [ ] **Consumer of RouterService**: Any callers impacted?
  - [ ] Check git blame for Start/Stop calls
  - [ ] Verify no direct access to `running`, `cancel` (private fields)

### H.2: Behavior Changes

- [ ] **Start behavior**: Unchanged (initial connection required) ✓
- [ ] **Stop behavior**: Now waits for wg (was already waiting) ✓
- [ ] **Reconnect trigger**: Now automatic via NotifyClose (vs manual signal) ✓

---

## I. Integration Testing

### I.1: With Real Broker (if possible)

- [ ] Can start, connect to real RabbitMQ ✓
- [ ] Receives messages from subscribed queues ✓
- [ ] Reconnects after broker restart ✓
- [ ] Gracefully stops after Stop() ✓

### I.2: With Minimal Broker

- [ ] Works with mock AMQP (via tests) ✓
- [ ] No hard dependency on real RabbitMQ in tests ✓

---

## J. Security & Validation

### J.1: Input Validation

- [ ] **AMQP URL validated?** (Dial will fail if invalid) ✓
- [ ] **No injection risks** (URL is config, not user input) ✓

### J.2: Resource Limits

- [ ] **No unbounded backoff** (capped at 30s) ✓
- [ ] **No goroutine leak on repeated Stop()** (idempotent) ✓
- [ ] **No connection leak on Start() failure** (closed on error) ✓

---

## K. Documentation

### K.1: Code Comments

- [ ] **Function docstrings** present?
  - [ ] Start(): "begins the router service..." ✓
  - [ ] Stop(): "gracefully shuts down..." ✓
  - [ ] runManager(): "monitors the AMQP connection..." ✓

- [ ] **Lifecycle invariants documented**?
  - [ ] "start flag is set BEFORE wg.Add(1)" ✓
  - [ ] "lock released before cancel()" ✓
  - [ ] "all paths call cleanupResources()" ✓

### K.2: External Documentation

- [ ] AUDIT document created? ✓ (PHASE_2_29_AUDIT.md)
- [ ] IMPLEMENTATION_GUIDE created? ✓
- [ ] Known issues/limitations documented?

---

## L. Merge Readiness Checklist

**All of the following must be YES before merge:**

- [ ] Passes `go test -race ./...` (no race conditions)
- [ ] Passes `go test -v ./...` (all tests pass)
- [ ] Coverage ≥ 90% (run `go test -cover ./...`)
- [ ] Lint clean (run `golangci-lint run ./...`)
- [ ] No goroutine leaks (TestRouterServiceNoGoroutineLeaks passes)
- [ ] No resource leaks (TestRouterServiceStopDuringExtendedBackoff passes)
- [ ] API unchanged (Start/Stop signatures same)
- [ ] Backward compatible (no breaking changes for callers)
- [ ] Documentation complete (comments + external docs)
- [ ] Review feedback addressed (all comments resolved)
- [ ] Integration tests pass (if real broker available)
- [ ] Staging deployment successful (if applicable)

---

## M. Sign-Off

| Role | Name | Date | Signature |
|------|------|------|-----------|
| Author | __ | __ | __ |
| Reviewer (Go/Concurrency) | __ | __ | __ |
| Reviewer (AMQP/Networking) | __ | __ | __ |
| Maintainer | __ | __ | __ |

---

## Post-Merge: Monitoring

After merging to production:

- [ ] **Reconnect rate monitored** (should be ~0 if broker stable)
- [ ] **Error logs monitored** (no unexpected reconnects)
- [ ] **Memory usage stable** (no leak over time)
- [ ] **File descriptor count stable** (no leak over time)
- [ ] **Goroutine count stable** (no leak over time)
- [ ] **Alert configured** for excessive reconnects (> 1/minute)

---

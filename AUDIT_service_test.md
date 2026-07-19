# Audit Report: internal/core/router/service_test.go

## Summary
The test suite for `RouterService` has good coverage of basic happy paths and reconnect scenarios, but contains several **edge case gaps** and **potential test defects** that could lead to flaky or incomplete testing.

---

## Issues Found

### 1. **CRITICAL: No Goroutine Leak Detection**
**Severity:** High  
**Issue:** None of the tests validate goroutine cleanup. The `runManager` goroutine must be properly terminated when `Stop()` is called.

**Current Risk:**
- Tests using `testing.M` or integration suites could accumulate goroutines
- The `s.wg.Wait()` call is tested only implicitly (if test hangs, it fails), not explicitly

**Evidence:** Service spawns a goroutine in `Start()` via `go s.runManager(...)` but no test verifies the goroutine count before/after.

---

### 2. **Race Condition in TestRouterServiceStopDuringReconnect**
**Severity:** Medium  
**Issue:** The test has a timing-dependent race condition:
```go
time.Sleep(100 * time.Millisecond)  // Attempt to sync, but non-deterministic
s.Stop()
```

**Problem:**
- No guarantee the service has entered the reconnect loop
- No synchronization mechanism (e.g., waitgroup or signal)
- Test may pass/fail randomly depending on CPU scheduling
- The reconnect backoff is 1s; the sleep is only 100ms

**Risk:** Flaky test that's hard to debug in CI/CD environments.

---

### 3. **Missing Test: Double Stop**
**Severity:** Medium  
**Issue:** No test verifies idempotency of `Stop()`.

**Current Code Behavior (Safe):**
```go
func (s *RouterService) Stop() {
    // Early return if not running
    if !running || cancel == nil {
        return
    }
```

**Gap:** Test suite never validates calling `Stop()` twice in a row, which should be safe but should be explicitly tested.

---

### 4. **Missing Test: Multiple Start Calls**
**Severity:** Medium  
**Issue:** `TestRouterServiceStartStop` starts once and stops. No test verifies the error case of calling `Start()` twice.

**Implementation:** Service correctly returns error:
```go
if s.running {
    return fmt.Errorf("router service already running")
}
```

**Gap:** This defensive logic is untested.

---

### 5. **Missing Test: Context Cancellation During Initial Connect**
**Severity:** Medium  
**Issue:** No test covers the case where the context passed to `Start()` is already cancelled or times out.

**Gap:** 
- Line 92 in service.go: `state, err := s.connector.ConnectAndSubscribe(ctx, s.amqpURL)`
- The `ctx` timeout behavior is never tested
- A cancelled context should be handled gracefully

---

### 6. **Missing Test: Backoff Exponential Growth**
**Severity:** Low-Medium  
**Issue:** The reconnect backoff implementation (lines 168-171) uses exponential backoff with a 30s cap, but no test validates:
- Backoff doubles correctly after each failed attempt
- Backoff caps at 30 seconds
- Backoff resets to 1 second on successful reconnect

**Current Test:** `TestRouterServiceReconnectLoop` only validates 2 connect calls, not backoff behavior.

---

### 7. **Missing Test: Cleanup Resource Leaks**
**Severity:** Medium  
**Issue:** `cleanup()` method calls `Close()` on both `subs` and `conn`. No test verifies:
- Both closers are actually called
- Errors during close are handled (currently ignored with `_`)
- Close is not called on nil pointers (defended against, but untested)

**Evidence:** mockCloser tracks `closed` state but tests never read it.

---

### 8. **Incomplete Mock: connectFunc Not Thread-Safe**
**Severity:** Low  
**Issue:** The `connectFunc` override in `TestRouterServiceAMQPNotifyCloseReconnect` manually manages `connectCount` without proper synchronization:
```go
connector.connectFunc = func(ctx context.Context, amqpURL string) (*connectionState, error) {
    connector.mu.Lock()
    connector.connectCount++  // Only this part is locked
    connector.mu.Unlock()
    
    if connector.connectSignal != nil {
        connector.connectSignal <- struct{}{}  // Not locked, but safe in this case
    }
```

**Risk:** If extended, could introduce data races.

---

### 9. **Missing Test: Multiple Connection Closures in Rapid Succession**
**Severity:** Low  
**Issue:** No test covers the scenario where `notifyClose` channel receives multiple errors rapidly, or the AMQP notifyClose fires while in reconnect backoff.

**Current Tests:**
- `TestRouterServiceReconnectLoop`: Single closure, waits for reconnect
- `TestRouterServiceAMQPNotifyCloseReconnect`: Single AMQP closure

**Gap:** Concurrent/repeated closures never tested.

---

## Recommended Additional Test Case

### **TestRouterServiceDoubleStop**
```go
func TestRouterServiceDoubleStop(t *testing.T) {
    connector := &mockConnector{}
    s := NewRouterService("amqp://localhost")
    s.connector = connector

    ctx := context.Background()
    err := s.Start(ctx)
    if err != nil {
        t.Fatalf("Start failed: %v", err)
    }

    // First stop should succeed
    s.Stop()
    if s.running {
        t.Errorf("expected service to be stopped after first Stop()")
    }

    // Second stop should be idempotent and not panic
    s.Stop()
    if s.running {
        t.Errorf("expected service to remain stopped after second Stop()")
    }
}
```

**Rationale:**
- Tests idempotency of `Stop()`, a critical property for graceful shutdown
- Verifies no panic or deadlock on double-stop
- Simple, deterministic, addresses gap #3
- Complements existing `TestRouterServiceStartStop` which only tests stop once

---

## Suggested Priority Improvements

1. **Add goroutine leak detection** (use `runtime.NumGoroutine()` before/after each test)
2. **Fix `TestRouterServiceStopDuringReconnect` timing** (add synchronization signal or use mock backoff)
3. **Add `TestRouterServiceDoubleStop`** (recommended test case above)
4. **Add test for double `Start()` calls** with assertion on error message
5. **Add backoff validation test** (verify exponential growth and 30s cap)
6. **Add context cancellation test** for initial connect

---

## Summary of Test Coverage Gaps

| Scenario | Tested? | Severity |
|----------|---------|----------|
| Happy path start/stop | ✅ | — |
| Initial connect failure | ✅ | — |
| Single reconnect | ✅ | — |
| AMQP notify close | ✅ | — |
| Stop during reconnect | ⚠️ (flaky) | Medium |
| **Double stop (idempotency)** | ❌ | Medium |
| **Multiple Start() calls** | ❌ | Medium |
| **Goroutine cleanup** | ❌ | High |
| **Backoff exponential growth** | ❌ | Low-Medium |
| **Cleanup resource verification** | ❌ | Medium |
| **Context cancellation** | ❌ | Medium |
| **Rapid repeated closures** | ❌ | Low |

---

## Files Analyzed
- `/Users/minibot/Projects/bots-agents/jasmin-go/internal/core/router/service_test.go` (203 lines)
- `/Users/minibot/Projects/bots-agents/jasmin-go/internal/core/router/service.go` (189 lines)

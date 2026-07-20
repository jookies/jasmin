# Audit Summary: RouterService & LateBillingService Integration

**Date:** 2026-07-19  
**Status:** ⚠️ NOT READY FOR INTEGRATION  
**Severity:** 6 Issues (1 CRITICAL × 3, HIGH × 2, MEDIUM × 1)

---

## Quick Summary

The `RouterService` and `LateBillingService` are well-designed individually but **cannot be integrated as-is**. Critical gaps in message consumption, lifecycle synchronization, and error handling must be addressed.

---

## Critical Issues (Must Fix Before Integration)

### 1. **Missing Message Pump**
- RouterService manages connection but never reads from `Billing` channel
- No entry point to invoke `LateBillingService.Process()`
- Messages accumulate in broker → redelivery storms

**Fix:** Add `billingWorker()` goroutine that reads channel and processes deliveries

---

### 2. **Race Condition in Start/Stop Lifecycle**
- Unsynchronized window between `Start()` returning and `runManager()` starting
- Immediate `Stop()` after `Start()` can cancel context before manager runs
- Existing test uses `time.Sleep()` workaround (TestRouterServiceStopDuringReconnect)

**Fix:** Add `WaitGroup` barrier; manager signals when ready

---

### 3. **Unsettled Messages on Error**
- `LateBillingService.Process()` returns errors with `LateBillingNone` action
- `ProcessLateBillingDelivery()` leaves messages unsettled on error
- No retry/dead-letter strategy for transient failures

**Fix:** Classify errors (transient vs. permanent); explicit ACK/Reject policy

---

## High Severity Issues

### 4. **Connection Loss During Processing**
- Worker processing message while AMQP drops
- Settlement attempt fails (channel closed)
- Message in broker limbo → eventual redelivery

**Fix:** Isolate message workers from lifecycle; detect channel closure in worker

---

### 5. **Unbuffered Channel Deadlock Risk**
- `notifyClose` channel can deadlock on concurrent access
- `Stop()` waits on `wg.Wait()` while signal blocked

**Fix:** Use async send or dedicated signal channel

---

## Medium Severity Issues

### 6. **Missing Observability**
- No logging in router or billing service
- Integration failures invisible in production
- Cannot debug reconnect or processing issues

**Fix:** Add structured logging (slog) for all critical paths

---

## Estimated Effort

| Task | Effort | Priority |
|------|--------|----------|
| Add message pump (billingWorker) | 4h | P0 |
| Implement lifecycle synchronization | 2h | P0 |
| Define error handling & settlement policy | 3h | P0 |
| Add observability (logging) | 3h | P1 |
| Connection loss resilience | 2h | P1 |
| Integration testing | 4h | P1 |
| **Total** | **~18h** | - |

---

## Deliverables Created

1. **AUDIT_ROUTER_LATE_BILLING_INTEGRATION.md** (855 lines)
   - Executive summary, detailed findings, code locations
   - Impact analysis for each issue
   - Integration checklist
   - Architecture recommendation

2. **AUDIT_TECHNICAL_DEEP_DIVE.md** (1168 lines)
   - Detailed technical analysis with code examples
   - Race condition scenario timelines
   - Working code examples (complete revised RouterService)
   - Integration test patterns

---

## Next Steps

1. **Review audit documents** (parent agent / team review)
2. **Design message pump** (define workers, error handling)
3. **Implement lifecycle synchronization** (WaitGroup barrier)
4. **Add integration tests** (with mock broker)
5. **Performance testing** (under high throughput)
6. **Merge when all critical issues resolved**

---

## Files Analyzed

- `internal/core/router/service.go` – Connection lifecycle
- `internal/core/late_billing_service.go` – Billing logic
- `internal/core/late_billing_delivery.go` – Settlement interface
- `internal/transport/amqpcompat/topology.go` – AMQP subscriptions
- `internal/transport/amqpcompat/client.go` – Delivery handling
- Tests: `service_test.go`, `late_billing_service_test.go`

---

## Key Takeaway

**The integration requires explicit architecture work.** Neither service has race conditions or errors individually, but wiring them requires:

- Explicit message pump (not implemented)
- Lifecycle synchronization (missing)
- Error recovery policy (undefined)
- Observability (absent)

Once these are added, integration will be robust and maintainable. Estimated 2-3 days of focused development.

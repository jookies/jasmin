# Phase 2.31 — Router Integration and Late Billing Wiring

## Goal

Connect the fixture-proven `LateBillingService` and `LateBillingDecisionProcessor` to the live `RouterService` consumers. Address critical architectural defects identified in the Phase 2.29/2.30 audit and implement the A-011 (Message Expiry) check for the Router layer.

## Scope

- **Refactor `RouterService`**: Apply lifecycle fixes (race conditions, context propagation, NotifyClose integration) as designed in the Phase 2.29 guide.
- **Message Dispatching**: Implement worker goroutines for both `DeliverSM` (MO/DLR) and `Billing` (Late Billing) consumers.
- **Late Billing Wiring**: Connect `LateBillingDeliveryProcessor` to the `Billing` consumer loop.
- **A-011 Expiry Check**: Add expiration detection to both Router consumers. Expired messages are rejected without requeue.
- **Error Handling**: Implement an explicit settlement policy for processing errors (Requeue=true) to prevent message loss during transient failures.
- **Metrics**: Add counters for processed and expired messages.

## Non-goals

- Implementing the full MO/DLR routing logic (Macro 2).
- Persistence/Deduplication for billing events (B-009/B-010).
- Live SMPP connector integration.
- Changing the frozen Python oracle or fixture set.

## Legacy Contract (A-011)

- **Source**: `jasmin/managers/listeners.py:182-188` (Reference implementation)
- **Field**: AMQP `expiration` header (ISO 8601 string).
- **Behavior**: Discard (Reject, Requeue=0) if `expiration < now`.
- **Log**: `Discarding expired message[msgid]: expiration is ...`

## Acceptance Criteria

1. `RouterService` correctly manages its lifecycle under high-concurrency Start/Stop races (verified via race detector).
2. Late billing messages from AMQP are correctly processed by `LateBillingService` and settled (ACK/Reject) on the broker.
3. Expired messages (A-011) are detected and discarded without routing or billing mutation.
4. Transient processing errors (e.g., user directory timeout) trigger a Requeue to allow retry.
5. Integration tests prove the full flow from `RouterSubscriptions` to settlement using a mock broker or `amqpcompat.Topology`.
6. Focused tests pass 20 consecutive times, followed by full Go, race, vet, build, and fixture-integrity gates.

## LoopKey
116f3241411f

- `internal/core/router/service.go`
- `internal/core/router/service_test.go`
- `internal/core/router/logic.go` (new)
- `spec/compatibility/AMQP_REDIS_MATRIX.md` (Update A-011 status)
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

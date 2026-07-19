# Phase 2.29 — Router Process Lifecycle & AMQP Recovery

## Goal

Implement the Router service process lifecycle including managed AMQP connection 
ownership, automatic topology declaration (from Phase 2.28), and consumer 
re-attachment after network or broker failure.

## Scope

- Implement `RouterService` with `Start(ctx)` and `Stop()` methods.
- Integrate `amqpcompat.OpenRouterSubscriptions` for automatic topology setup.
- Implement a bounded exponential backoff reconnect loop for the AMQP connection.
- Ensure that `Stop()` gracefully cancels consumers, closes the connection, 
  and waits for cleanup before returning.
- Verify that every reconnection triggers a fresh topology declaration to 
  ensure the broker state matches the Go configuration (A-012).

## Non-goals

- Implementing the actually message handlers for `deliver.sm.*` or billing requests.
- High-level orchestration (this is the service-level implementation).
- Persisting in-flight message state (handled by AMQP manual-ack).

## Acceptance criteria

1. `Start()` establishes a connection and declares topology successfully.
2. Connection loss triggers a background reconnect loop with backoff.
3. Every reconnect successfully redeclares exchanges, queues, and bindings.
4. `Stop()` cancels all active consumers and shuts down the connection gracefully.
5. Focused tests with mocked AMQP client pass race and leak detectors.

LoopKey: 1601d71c607f

## Affected paths

- `internal/core/router/service.go`
- `internal/core/router/service_test.go`
- `spec/implementation/PHASE2_29_ROUTER_PROCESS_LIFECYCLE_SLICE.md`

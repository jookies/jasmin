# Phase 2.30 — Router QoS and Message Expiry

## Goal

Implement AMQP QoS (prefetch) and message expiry handling for the Router service 
to match Jasmin's delivery semantics and prevent consumer overload.

## Scope

- Implement `BasicQos` on the Router AMQP channels (A-010).
- Support `prefetch_count` configuration for Router consumers.
- Implement message expiry detection and handling for `deliver.sm.*` and billing requests (A-011).
- Add metrics for QoS throttling and expired messages.
- Verify parity with Jasmin's `prefetch_count=1` default for certain consumers.

## Non-goals

- Implementing the actually message handlers (already non-goal in 2.29).
- Persistent message state (handled by AMQP).

## Acceptance criteria

1. Router consumers honor configured `prefetch_count`.
2. Expired messages are rejected/dropped according to configuration.
3. Tests verify that prefetch limits are enforced under high load.
4. Metrics correctly track QoS and expiry events.

## Affected paths

- `internal/core/router/service.go`
- `internal/transport/amqpcompat/topology.go`
- `spec/implementation/PHASE2_30_ROUTER_QOS_EXPIRY_SLICE.md`

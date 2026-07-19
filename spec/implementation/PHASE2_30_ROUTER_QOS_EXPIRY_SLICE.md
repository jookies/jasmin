26. Metrics correctly track QoS and expiry events.

LoopKey: 6c933f8374f1

## Affected paths

Implement AMQP QoS (prefetch) and message expiry handling for the Router service 
26. Metrics correctly track QoS and expiry events.

LoopKey: 6c933f8374f1

## Affected paths

- Implement `BasicQos` on the Router AMQP channels (A-010).
- Support `prefetch_count` configuration for Router consumers.
- Implement message expiry detection and handling for `deliver.sm.*` and billing requests (A-011).
- Add metrics for QoS throttling and expired messages.
26. Metrics correctly track QoS and expiry events.

LoopKey: 6c933f8374f1

## Affected paths

- Implementing the actually message handlers (already non-goal in 2.29).
26. Metrics correctly track QoS and expiry events.

LoopKey: 6c933f8374f1

## Affected paths

1. Router consumers honor configured `prefetch_count`.
2. Expired messages are rejected/dropped according to configuration.
3. Tests verify that prefetch limits are enforced under high load.
26. Metrics correctly track QoS and expiry events.

LoopKey: 6c933f8374f1

## Affected paths

- `internal/core/router/service.go`
- `internal/transport/amqpcompat/topology.go`
- `spec/implementation/PHASE2_30_ROUTER_QOS_EXPIRY_SLICE.md`

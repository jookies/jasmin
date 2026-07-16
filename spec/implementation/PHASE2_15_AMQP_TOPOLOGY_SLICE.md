# Phase 2.15 — AMQP Topology and Basic Delivery Semantics

This phase implements the RabbitMQ topology (exchanges, queues, bindings) and basic message delivery semantics for MO/MT routables.

## Included
- A-001: RabbitMQ exchanges (`messaging`, `billing`) declaration and properties.
- A-002: MT Submit route: queue/binding for `submit.sm.<CID>`.
- A-004: MO Ingest: queue/binding for `deliver.sm.<CID>`.
- A-008: Billing requests: queue/binding for `bill_request.submit_sm_resp.<UID>`.
- Basic connection management and reconnection logic (A-012).
- Envelope serialization/deserialization with basic properties (A-002, A-003, A-004).

## Excluded
- Advanced delivery semantics: ACK/reject timing (A-009), QoS/prefetch (A-010), expiry (A-011).
- Pickle bridge (A-013) - deferred to Phase 2.16.
- DLR lookup and throwers (A-006, A-007).

## Production API

### package `internal/transport/amqpcompat`
- `type Topology struct { ... }`
- `func (t *Topology) Declare(ctx context.Context) error`
- `type Publisher struct { ... }`
- `type Consumer struct { ... }`

## Tasks
1. [x] Define AMQP topology structures for exchanges and queues.
2. [x] Implement topology declaration logic.
3. [x] Implement basic Publisher for MT Submit.
4. [x] Implement basic Consumer for MO Ingest.
5. [x] Implement connection/channel recovery logic.
6. [ ] Verification with a live RabbitMQ (container-based).
7. [ ] Ralph audit.

## Verification
- `TestAMQPTopology`
- `TestAMQPPubSubRoundTrip`

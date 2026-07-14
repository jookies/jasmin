# ADR-0001: Contract-first strangler rewrite

- Status: accepted
- Date: 2026-07-14
- Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`

## Context

Jasmin combines Twisted, Perspective Broker, Python pickle, RabbitMQ, Redis, SMPP, HTTP/REST, Celery, jCli and executable Python interceptors. A direct line-by-line port would preserve implementation accidents while missing externally observable behavior. A big-bang cutover would make message loss, duplicate sends and duplicate billing difficult to detect.

## Decision

Use a contract-first strangler migration:

1. Freeze the Python baseline and inventory every public/wire/operational contract.
2. Capture golden fixtures and run the Python implementation as behavioral oracle.
3. Build a clean Go domain core behind protocol and persistence ports.
4. Replace leaf workers first, then ingress, router/control plane and persistence last.
5. Partition cutover by connector/user/CID; exactly one runtime owns side effects per partition.
6. Shadow execution computes decisions only and cannot enqueue, charge, mutate DLR state or emit callbacks.
7. Remove Python bridges only after all relevant matrix rows are `MATCH` or `APPROVED_DEVIATION`.

## Consequences

### Positive

- Measurable definition of parity.
- Rollback by partition.
- Internal schemas and storage can improve without changing public behavior.
- Go code is not coupled to Twisted/Pickle.

### Negative

- Temporary Python bridges and duplicate test infrastructure are required.
- Migration is longer than a big-bang port.
- Exact legacy quirks must be maintained in compatibility mode.

## Rejected alternatives

- Line-by-line Twisted-to-goroutine port.
- Native Go pickle decoder.
- Immediate replacement of RabbitMQ/Redis.
- Concurrent Python and Go consumers performing side effects on the same queue.

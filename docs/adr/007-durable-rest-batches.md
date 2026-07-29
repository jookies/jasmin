# ADR-007 — PostgreSQL-backed REST batch execution

- **Status:** accepted
- **Date:** 2026-07-29

## Context

The frozen REST daemon accepts `/secure/sendbatch` asynchronously. Its Celery
queue survives a REST process restart; the first Go implementation used timers
and a process-local channel, so an acknowledged scheduled batch disappeared
when the gateway stopped.

Batch payloads include the caller's password in the legacy Celery message.
Copying that design into PostgreSQL would turn the durable queue into a
plaintext credential database. Recovery also needs a stable identity: a crash
after `/send` durably admits a message but before the batch worker marks its row
complete must not charge and enqueue a second logical submit.

## Decision

`rest_batches` and `rest_batch_tasks` in PostgreSQL are the admission, work and
callback queue. The complete expanded destination set commits before the REST
response acknowledges the batch. Workers claim due tasks and callbacks with
`FOR UPDATE SKIP LOCKED`, an owner token and an expiring lease. Startup returns
expired claims to the pending state.

Each task gets a stable UUID that becomes its internal submit message ID.
Recovery checks the durable submit ledger before routing or charging; an
already-admitted task returns that same ID. The queue stores the username and
the SHA-256 proof captured at acceptance, never the password. The internal HTTP
handler compares that proof in constant time with the user's current digest
and still rejects deleted, disabled, regrouped or password-changed accounts.

Admission is bounded by `max_pending_tasks`. A PostgreSQL transaction advisory
lock serializes the backlog count and insert across processes. Submit retries
are bounded with exponential backoff for explicit transient responses.
Terminal callback/errback payloads commit with task completion; callback
delivery is retried with the same query fields until its configured attempt
limit. Callback delivery is therefore at-least-once, as no HTTP callback
protocol can atomically commit both the remote side effect and the local row.

The combined gateway handler retains legacy `/ping` bytes and mounts
`/secure/*`. An optional separate REST listener exposes the historical
JSON-wrapped `/ping` without changing the public legacy HTTP contract.

## Consequences

- Accepted immediate and scheduled batches survive clean or unclean restart.
- A task recovered after durable submit admission is not charged or published
  twice.
- Passwords do not enter durable batch rows, logs or callback requests.
- Several workers/processes may share the queue without sharing in-memory
  counters.
- Callback consumers must tolerate duplicate delivery after a network
  ambiguity, as they did with Celery retries.
- PostgreSQL is mandatory for production REST batching; this introduces no new
  database because the submit outbox and billing already require it.

## Evidence boundary

Unit and race tests cover expansion, bounded admission, restart recovery,
credential redaction, stable submit identity, QoS, retries and callbacks. A
PostgreSQL integration test covers migration, leasing, expired-claim recovery,
terminal state and callback claims. No `R-001`–`R-009` registry row is promoted:
the committed frozen REST macro/fixture set is still incomplete.

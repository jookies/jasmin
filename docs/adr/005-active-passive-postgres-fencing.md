# ADR-005 — PostgreSQL advisory-lock fencing for active-passive gateway HA

- **Date:** 2026-07-29
- **Status:** accepted
- **Summary:** The supported HA topology is active-passive: one fenced active
  gateway, one or more in-process waiting standbys, and a shared,
  namespace-isolated PostgreSQL control plane.

## Context

ADR-001 deliberately made runtime provisioning node-local SQLite. ADR-004 moved spent billing quotas to PostgreSQL but retained an in-process live balance, explicitly documenting that two active gateways could each spend the full quota. Starting multiple unfenced processes would therefore create a money bug before it created useful availability.

The existing PostgreSQL service is mandatory for the outbound submit transaction and quota stores. PostgreSQL session advisory locks provide a narrow first HA primitive without introducing another coordinator: exactly one session can own a namespace lock, and PostgreSQL releases it atomically if that session disappears.

## Decision

Implement active-passive HA on a dedicated PostgreSQL leadership connection:

- the lock key is a stable SHA-256-derived signed 64-bit value scoped by an operator deployment namespace;
- acquisition uses `pg_try_advisory_lock`; a contending node remains alive and
  retries in process until it can promote;
- the dedicated connection is periodically probed;
- connection loss closes a `Lost` fencing signal;
- orderly shutdown explicitly unlocks and closes the session;
- a standby acquires the lock after the previous session releases it;
- the gateway acquires leadership before constructing mutable billing objects,
  workers, or admission listeners and releases it only after those resources
  stop;
- a standby serves `GET /live` as 200 and `GET /ready` as 503; the active
  process serves `/live` as 200 and dependency-backed `/ready` as 200/503;
- when HA is enabled, admin entities and named profiles live in PostgreSQL,
  isolated in a deterministic schema derived from the HA namespace. SQLite
  remains the local/test and supported single-node adapter.

The primitive lives in `internal/infra/storage/postgres_leader.go`. The gateway
acquires it before opening workers/listeners, exposes the loss signal to the
executable, stops public/admin/PB admission immediately on loss, and releases
the lock only after its message workers have stopped. The executable compose
drill in `docker-compose.gateway-ha.yml` runs two identical nodes behind
HAProxy; HAProxy admits only the node whose `/ready` succeeds.

## Rejected alternatives

### Multi-active over the current live quota objects

Rejected because balances and group ceilings are mutated in process memory. PostgreSQL persistence restores state but is not a compare-and-swap charge ledger, so concurrent active nodes can double-spend.

### Treat node-local SQLite as a replicated control plane

Rejected. Independent files can diverge, and file copying has no safe conflict
or live-apply protocol. PostgreSQL is used for the HA control plane instead.

### A time-based lease row

Deferred. A lease row needs clock/expiry policy and robust fencing tokens. The session advisory lock has connection-lifetime semantics and is sufficient for the initial active-passive process fence.

## Consequences

- Item #21 is functionally complete for the supported active-passive topology.
  Unit tests cover retry, cancellation and shutdown races; PostgreSQL
  integration tests cover mutual exclusion, in-process promotion, shared admin
  state, atomic profile restore and namespace isolation.
- A promoted standby replays the same connectors, users, groups, routes,
  filters, interceptors, SMPPs credentials and profiles before it becomes
  ready.
- Leadership-loss handling is fail-closed. The old active closes all admission
  listeners immediately; it never attempts to keep serving on a lost fence.
- Multi-active is explicitly unsupported, not an unfinished mode. It would
  require centralized atomic billing admission (or end-to-end fencing tokens)
  and is rejected while quotas remain live in process.

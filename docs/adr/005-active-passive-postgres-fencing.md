# ADR-005 — PostgreSQL advisory-lock fencing for active-passive gateway HA

- **Date:** 2026-07-29
- **Status:** active, phase 1
- **Summary:** Multi-node work starts with one active gateway per deployment namespace, fenced by a session-level PostgreSQL advisory lock. This prevents two processes from concurrently spending the same in-memory billing quota while the shared control-plane design remains unfinished.

## Context

ADR-001 deliberately made runtime provisioning node-local SQLite. ADR-004 moved spent billing quotas to PostgreSQL but retained an in-process live balance, explicitly documenting that two active gateways could each spend the full quota. Starting multiple unfenced processes would therefore create a money bug before it created useful availability.

The existing PostgreSQL service is mandatory for the outbound submit transaction and quota stores. PostgreSQL session advisory locks provide a narrow first HA primitive without introducing another coordinator: exactly one session can own a namespace lock, and PostgreSQL releases it atomically if that session disappears.

## Decision

Implement an active-passive leadership lease on a dedicated PostgreSQL connection:

- the lock key is a stable SHA-256-derived signed 64-bit value scoped by an operator deployment namespace;
- acquisition uses `pg_try_advisory_lock` and fails fast when another node is active;
- the dedicated connection is periodically probed;
- connection loss closes a `Lost` fencing signal;
- orderly shutdown explicitly unlocks and closes the session;
- a standby can acquire the lock after the previous session releases it.

The primitive lives in `internal/infra/storage/postgres_leader.go`. The gateway
acquires it before opening workers/listeners, exposes the loss signal to the
executable, stops public/admin/PB admission immediately on loss, and releases
the lock only after its message workers have stopped. A contending node fails
startup fast and can acquire after the old session releases. This remains a
phase-one active-passive fence, not a claim of complete production HA.

## Rejected alternatives

### Multi-active over the current live quota objects

Rejected because balances and group ceilings are mutated in process memory. PostgreSQL persistence restores state but is not a compare-and-swap charge ledger, so concurrent active nodes can double-spend.

### Treat node-local SQLite as a replicated control plane

Rejected. Independent files can diverge, and file copying has no safe conflict or live-apply protocol. An approved shared control-plane store or replication design is still required before multi-active administration.

### A time-based lease row

Deferred. A lease row needs clock/expiry policy and robust fencing tokens. The session advisory lock has connection-lifetime semantics and is sufficient for the initial active-passive process fence.

## Consequences and remaining work

- This starts item #21 but does not complete it.
- Standby health/readiness semantics, retry-in-process behavior, and a real
  orchestrator takeover drill remain.
- Admin SQLite remains node-local. A shared control-plane ADR and migration are still required for seamless failover of live provisioning changes.
- Multi-active requires centralized atomic billing admission (or fencing tokens carried through every charge), not merely this leader lock.

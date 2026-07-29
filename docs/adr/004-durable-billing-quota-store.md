# ADR-004 — PostgreSQL for durable billing quotas, with a provisioned-baseline precedence rule

- **Date:** 2026-07-29
- **Status:** active
- **Summary:** Prepaid balances and `submit_sm_count` quotas persist to PostgreSQL keyed by the legacy username/gid, and at boot a durable row wins over the provisioned spec only while the provisioned baseline it was written with still matches.

## Context

Balances and `submit_sm_count` quotas lived only in process memory. `internal/app/outbound/config.go` `applyUser` called `billing.User.SetBalance` from the provisioned spec on every boot, and nothing wrote the mutated value anywhere. A customer who spent 900 of 1000 got the full 1000 back on every deploy.

The machinery that looked like it solved this did not:

- `billing.NewQuotaPersistenceService` (`internal/core/billing/persistence.go`) is the frozen-oracle replica of `RouterPB.persistenceTimerExpired` for matrix row `B-009`. `spec/implementation/WAVE1E_BILLING_PERSISTENCE_TIMER_SLICE.md` explicitly scopes PostgreSQL bootstrap and recovery *out* of it, and its persist-one-dirty-user-per-tick scan is a fidelity artifact, not a durability mechanism.
- `billing.NewPersistWorker` plus `billing.UserRepository` / `storage.SQLiteUserRepository` key on `billing.User.UID()`. For config-provisioned users that uid is the account's index in the config array, so reordering `users[]` would move one customer's spent balance onto another.

Neither had a single production caller.

Legacy solves this differently and cannot be copied directly: `RouterPB.perspective_persist` pickles the whole users/groups object graph to `<store_path>/<profile>.router-users`, and jCli `load` restores it. There the persisted file is authoritative for balance *and* provisioning — there is no config spec competing with it. The Go gateway does have such a spec, and operators use it to top accounts up, so a durable value cannot simply always win.

## Decision

Persist the mutable quota only — balance and `submit_sm_count` — to **PostgreSQL**, in a single `billing_quotas` table keyed by `(scope, principal_key)` where `principal_key` is the legacy username or gid.

Each row also stores the **provisioned baseline** in effect when it was written. At boot, per field:

- no row → the provisioned value (new account, first boot);
- row whose baseline equals what is provisioned now → the durable value wins (a plain restart; the customer keeps what they spent);
- row whose baseline differs → the provisioned value wins (the operator edited the spec since the last flush; that edit is the re-grant).

Restoration is consumed once per key, so it applies to a key's first provisioning (boot, including the admin plane replaying its stored users) and never to a later online edit.

A `billing.QuotaPersister` flushes every dirty user, and the group backing each dirty user, as one transaction on `quota_persist_interval_seconds` (default 10s), started with and joined by the outbound runtime, with a bounded final flush on `Close`.

## Alternatives considered

### The node-local admin SQLite of ADR-001

Rejected. ADR-001 keeps SQLite for *provisioning* state precisely because provisioning is node-local operational state. Spent balance is the mirror image: it is consumption state in the same class as the submit outbox, and two gateway nodes each holding their own copy would each grant the customer the full balance — the same money bug, distributed. PostgreSQL is already mandatory for the outbound runtime (`validateConfig` requires `postgres_dsn`; `NewRuntime` opens, migrates and recovers it), so this adds no deployment surface.

### Reusing `billing.UserRepository` / `SQLiteUserRepository` as they stand

Rejected on the key. The numeric uid is positional for config users, so durable money would re-key on a config reorder. They also persist provisioning fields (`early_decrement_percent`, `gid`); restoring those would let a stale row override a deliberate config change. The narrow `QuotaStore` carries only what a charge mutates.

### Provisioned spec always wins at boot / durable row always wins at boot

The first is the bug. The second breaks the only top-up mechanism the config path has. The baseline comparison is what lets both gestures coexist without a separate "top-up" API.

### A monotonic config generation counter instead of a stored baseline

Rejected as more state for the same outcome: the baseline is self-describing, needs no coordination, and is idempotent if the process dies before re-anchoring.

## Consequences

### Positive

- A restart, deploy or crash no longer refunds spending; the exposure is bounded by the flush interval, and an orderly shutdown flushes once more.
- Editing a balance in the config or admin spec still tops an account up.
- Group ceilings survive alongside user balances, written in the same transaction, so a customer cannot spend the group's money twice across a crash.
- Online user and group edits keep their live billing objects: non-quota edits preserve spent-down values, while changing a quota field remains the explicit top-up/reset gesture.
- The live edit and the node-local admin-store write form one logical transaction: their locks remain held through persistence, and a failed write restores the exact pre-edit balance, count, group, credential and quota-version state before waiting charges resume.
- Deleting an admin user/group prunes its durable quota row after the admin-store deletion commits. Boot repeats the prune after all config and persisted principals replay, covering interrupted deletes and old orphan rows; the unconsumed in-memory restore index is discarded at the same boundary, so same-process name reuse cannot resurrect it either.
- `B-009` gains its production half without disturbing the frozen `B-009` timer corpus.

### Negative / accepted trade-offs

- **Re-granting the exact same number the account was last provisioned with is a no-op across a restart** — by construction nothing about the spec changed. Operators must change the number or top up online through the admin plane.
- **Single-writer assumption.** The live balance is in process memory, so two gateway nodes serving the same account already double-spend; this store makes state recoverable, not multi-node-safe. Fencing or a CAS-per-charge design is a separate decision.
- A second small PostgreSQL pool (`MaxOpenConns(2)`) is opened per outbound runtime rather than sharing the submit repository's handle, to avoid widening that type's API.

### Follow-ups

- `RuntimeDependencies.QuotaPersistErrors` is wired to the named router logger; flush failures stay dirty, are visible to operations, and retry on the next cadence.
- Active-passive PostgreSQL session fencing has started under ADR-005. Revisit `B-009` once the remaining standby/control-plane behavior is proven.

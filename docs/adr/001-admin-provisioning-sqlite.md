# ADR-001 — Embedded SQLite for the admin provisioning store

- **Date:** 2026-07-26
- **Status:** accepted for single-node; HA storage superseded by ADR-005
- **Summary:** The runtime provisioning plane uses embedded pure-Go SQLite for
  local, test and single-node deployments. ADR-005 selects namespace-isolated
  PostgreSQL for the active-passive HA control plane.

## Context

Until now the Go gateway froze routes/users/connectors at boot from `--config`. Macro-3 item 4 adds a runtime provisioning API. That needs a store that (a) survives restart, (b) is transactional, (c) models relational entities (connectors, and later routes/users/groups/filters), and (d) does not complicate the deployment. The gateway already runs Postgres (durable submit outbox) and Redis (DLR correlation); the legacy Python stack persists jCli/PB provisioning to Redis.

## Decision

Persist admin provisioning state in **embedded SQLite** via the pure-Go `modernc.org/sqlite` driver, one file per gateway node (`admin.db`), applied live through the existing `smppc.Manager` (and later the route/user holders).

- **Pure-Go driver, not `mattn/go-sqlite3`:** the gateway image builds `CGO_ENABLED=0`; the CGO driver would compile but panic at runtime. `modernc.org/sqlite` needs no CGO, so the image stays static and small.
- **Additive to `--config`:** config connectors remain config-owned and are *reserved* — admin cannot create/modify/delete a cid that config defines. Admin owns a separate set. This keeps the frozen-config path and the strangler oracle unaffected.
- **Live-apply, apply-first:** a create applies to the manager (which validates and binds) *before* persisting; a persist failure rolls the manager back. So the store and the live runtime never diverge.

## Alternatives considered

- **Postgres (already present).** Rejected as the default: it couples provisioning to the outbox DB and to a shared server, and provisioning is node-local operational state, not message-durability state. Revisit for a multi-node control plane.
- **Redis (legacy parity).** The legacy stack keeps jCli/PB state in Redis. Rejected here because our provisioning is relational and node-local, and sharing the messaging Redis would entangle control state with DLR/correlation state during shadow. A Redis-backed store is the natural choice *if/when* the Go admin plane must interoperate with a live legacy jCli.

## Consequences

- **Gateway-node-local provisioning.** SQLite remains single-node. When HA is
  configured, the gateway automatically opens the PostgreSQL adapter approved
  by ADR-005 and ignores `admin.db_path`.
- **Single writer.** SQLite allows one writer; the store uses `SetMaxOpenConns(1)` and the service serialises mutations. The admin plane is low-traffic, so this is a non-issue.
- **New dependency** `modernc.org/sqlite` (pure Go) plus its transitive modules. No CGO, no system libsqlite.
- **jCli byte-parity console remains deferred** — this ADR covers the HTTP provisioning API and its store, not the legacy interactive console.

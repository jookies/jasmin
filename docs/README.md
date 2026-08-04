# Synevyr documentation

Start from what you are trying to do.

## I want to send or receive messages through Synevyr

You are integrating against a running gateway.

| Document | What it answers |
|---|---|
| [`getting-started.md`](getting-started.md) | **Start here.** Your first hour, hands on: start the stack, send, receive, get a receipt, terminate. |
| [`learning-path.md`](learning-path.md) | The full curriculum with schemas — SMPP roles, binds, MT/MO/DLR, addressing, encoding, then operating this gateway. |
| [`api/http.md`](api/http.md) | The HTTP API: `/send`, `/balance`, `/rate`, `/ping`. Every parameter, every error body. |
| [`api/rest.md`](api/rest.md) | The JSON `/secure/*` API, including batches and scheduling. |
| [`api/callbacks.md`](api/callbacks.md) | **What your server must implement** to receive delivery receipts and inbound SMS. |
| [`api/smpp.md`](api/smpp.md) | Binding your own ESME over SMPP 3.4. |
| [`api/messages.md`](api/messages.md) | **Receiving terminated messages**: the `http-push` sink and the `GET /messages` pull API, with every parameter. |
| [`glossary.md`](glossary.md) | SMPP and telecom terminology. Start here if `TON`, `UDH` or `ESME` are new. |

Two things that catch integrators out, so they are worth knowing before you
write code:

- A delivery-receipt or inbound-MO callback is only successful if it returns
  **HTTP 2xx *and* a body that trims to exactly `ACK/Jasmin`**. Anything else is
  a failure and gets retried.
- A per-user throughput quota of **`0` means unlimited**, not blocked.

Both are inherited behaviours kept on purpose because live integrations depend on
them. [`reference/legacy-behaviours.md`](reference/legacy-behaviours.md) has the
full list.

## I am running Synevyr in production

| Document | What it answers |
|---|---|
| [`../README.md#deployment`](../README.md#deployment) | First deployment, from clone to healthy. |
| [`operations/running-the-platform.md`](operations/running-the-platform.md) | **The operator handbook.** Onboarding customers and carriers, routing, receipts, suspending a non-payer, answering "was my message delivered?". Start here. |
| [`operations/multi-carrier-and-dlr-flow.md`](operations/multi-carrier-and-dlr-flow.md) | **Connecting several carriers**, how outbound traffic is split across them, and why all their delivery receipts converge on one correlation path. Has the end-to-end diagram. |
| [`operations/configuration.md`](operations/configuration.md) | Every configuration field, its default, and what it does. |
| [`operations/monitoring.md`](operations/monitoring.md) | Metrics, health endpoints, logs, alerts — **and which metrics are not yet wired**. |
| [`operations/dlr-registry-gate.md`](operations/dlr-registry-gate.md) | **Per-user DLR registry gate** — reporting delivery from a short-lived allowlist of destinations instead of from the upstream, and the boundaries that keeps. |
| [`operations/security.md`](operations/security.md) | Trust boundaries, listener exposure, secrets, TLS, hardening checklist. |
| [`operations/scaling.md`](operations/scaling.md) | Throughput controls, HA, queue durability, capacity. |
| [`../deploy/BACKUP.md`](../deploy/BACKUP.md) | Backup and restore. PostgreSQL holds billing and CDRs. |
| [`../configs/README.md`](../configs/README.md) | The shipped config templates. |

**Read `operations/security.md` before exposing any port.** The default
deployment binds the admin API, web console and jCli to loopback for a reason,
and `/admin/` can create users and start connectors.

## Something is broken right now

Go straight to [`runbooks/`](runbooks/). Each is symptom → confirm → fix →
verify, written against this system's actual endpoints and log lines.

| Symptom | Runbook |
|---|---|
| Connector unbound or flapping | [`smsc-connection-lost-or-flapping.md`](runbooks/smsc-connection-lost-or-flapping.md) |
| Queues growing, nothing sending | [`rabbitmq-down-or-backed-up.md`](runbooks/rabbitmq-down-or-backed-up.md), [`stuck-or-growing-queue.md`](runbooks/stuck-or-growing-queue.md) |
| Database down or slow | [`postgresql-down-or-slow.md`](runbooks/postgresql-down-or-slow.md) |
| Receipts not arriving | [`dlrs-not-arriving.md`](runbooks/dlrs-not-arriving.md) |
| Balances look wrong | [`billing-mismatch-or-balance-drift.md`](runbooks/billing-mismatch-or-balance-drift.md) |
| Credential leaked, needs rotating | [`credential-compromise-or-rotation.md`](runbooks/credential-compromise-or-rotation.md) |
| Gateway will not start | [`gateway-will-not-start.md`](runbooks/gateway-will-not-start.md) |

## I am working on the code

| Document | What it answers |
|---|---|
| [`architecture.md`](architecture.md) | How the system is built and **why** — layers, queue topology, storage model, concurrency, the two non-obvious design decisions. |
| [`adr/`](adr/) | Architecture decisions, with the alternatives that lost. |
| [`reference/legacy-behaviours.md`](reference/legacy-behaviours.md) | 22 inherited behaviours, several of them bugs matched on purpose. **Read before "fixing" anything that looks wrong.** |
| [`reference/deviations.md`](reference/deviations.md) | Where Synevyr deliberately differs from the reference, and why. |
| [`admin-surface-audit.md`](admin-surface-audit.md) | Capability matrix across jCli, the REST API and the web console, with the gaps and a prioritised plan. |
| [`plans/`](plans/) | Implementation plans, newest highest. Historical record of how the system got here. |
| [`worklog.md`](worklog.md) | Session-by-session history, newest first. |

Start with `architecture.md`, then read
`internal/app/gateway/runtime.go` — it is the composition root and shows what
actually runs.

## What state is this in?

[`plans/017-smpp-production-readiness.md`](plans/017-smpp-production-readiness.md)
is the authority. It is a gate checklist, honest about what is proven and what is
not.

The short version: the message path works end to end and is verified against an
independent SMPP implementation on every commit. It has **not** run on a real
carrier link, metrics instrumentation is incomplete, and there is no load or soak
evidence. [`STATUS.md`](STATUS.md) has the longer breakdown.

## How this documentation is organised

- **`api/`** — contracts for people outside the system. Changing these breaks
  integrations, so they are written as contracts rather than descriptions.
- **`operations/`** — for whoever is on call.
- **`runbooks/`** — one file per incident, actionable under pressure.
- **`reference/`** — inherited behaviour and deliberate divergence. Explains
  *why*, and exists so nobody "fixes" a deliberate quirk.
- **`adr/`** — decisions, immutable once accepted. Superseded rather than edited.
- **`plans/`** — how work was sequenced. Historical; may lag the code, in which
  case the code wins.
- **`specs/`** — what a piece of work must do, written before it is built.
  Includes audit findings awaiting fixes, such as
  [`specs/004-correctness-audit-findings.md`](specs/004-correctness-audit-findings.md).

`adr/` and `plans/` are append-only by convention: a superseded document gets its
status changed and a link to its replacement, never a rewrite. That way the
reasoning behind a past decision survives the decision.

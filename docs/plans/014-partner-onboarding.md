# Partner onboarding — one guided plan for both SMPP directions

- **Date:** 2026-07-28
- **Status:** superseded by [018-admin-plane-and-onboarding.md](018-admin-plane-and-onboarding.md) step 4. The freeze below was correct when written and no longer applies: it withheld the backend because the admin plane could not provision atomically, and it now can -- all six services share one store with transaction support. The prototype and its design reasoning remain accurate and are the starting point.
- **Frontend prototype:** `/partners/onboarding`
- **Related:** [012-admin-plane-full-coverage.md](012-admin-plane-full-coverage.md), [011-admin-web-ui.md](011-admin-web-ui.md)

## Goal

Give an operator one safe workflow for preparing a new telecom partner, whether the partner binds to our SMPPs server, we bind to the partner's SMSC, or both directions are required. A future implementation should provision all dependent resources as one auditable, retry-safe operation instead of asking the operator to create users, connectors, routes and credentials independently.

The current deliverable is intentionally smaller: an interactive frontend-only wizard that explains the intended workflow and produces an in-memory mock plan. It does not call an onboarding API, persist a partner, create credentials, change routes, or touch the gateway runtime.

## Planned user flow

1. **Partner** — record the partner name, stable code and technical contact.
   The contact channel may be email, Telegram, Microsoft Teams, or `N/A`.
2. **Connection** — choose `partner → us`, `us → partner`, or bidirectional; collect the matching SMPP endpoint, bind mode, TLS and network allowlist details.
3. **Traffic policy** — plan prefixes, throughput, DLR handling and optional MO delivery.
4. **Review** — show the exact resources the production workflow would create and require an operator readiness check before execution.

## Future production scope

### Domain and API

- Add first-class `Partner` and `PartnerOnboardingRun` records with explicit ownership links to every generated resource.
- Define a versioned onboarding API contract for plan, validate, apply, status and retry operations.
- Make every apply operation idempotent. Repeating a request with the same key must converge on the same resources.
- Execute onboarding as a saga with a durable step log and compensating actions; never leave silent half-created configurations.
- Keep a human-readable diff between the approved plan and the resources actually applied.

### Provisioning by direction

- **Partner connects to us:** create or attach a group, gateway/SMPPs account, permissions and limits; configure bind mode, TLS policy, source IP allowlist and maximum sessions.
- **We connect to partner:** create an SMPPc connector with secret references, TLS verification, reconnect policy and bind pacing; create the required MT/MO routes and filters.
- **Bidirectional:** provision both sides under one partner and one onboarding run, with independent readiness and rollback state.

### Security and operations

- Store generated and imported credentials only through a secret manager or KMS-backed reference. Do not persist plaintext partner passwords in SQLite or duplicate them in config profiles.
- Add RBAC for planning, approving, applying and rotating partner access.
- Write an immutable audit trail with actor, timestamps, redacted diffs and onboarding-run correlation IDs.
- Provide DNS, TCP, TLS and SMPP bind preflight checks before apply.
- Add per-partner bind, throughput, reject, DLR and MO visibility rather than relying on global SMPPs counters.
- Define credential rotation, partner suspension and clean deprovisioning flows.

### Commercial and platform prerequisites

- Finish enforceable HTTP/SMPP throughput controls and confirm user/group quota precedence.
- Decide the CDR, billing and settlement contract before commercial traffic is enabled.
- Define the production network exposure model for SMPPs; the current compose port is loopback-only and suitable for local testing, not partner access.
- Decide the HA/control-plane model before onboarding becomes a multi-instance workflow; current local SQLite assumptions are insufficient for a distributed coordinator.

## Readiness gate

Frontend API wiring remains blocked until all of the following are true:

1. The partner/onboarding domain model and versioned API contract are approved.
2. Secrets have a non-plaintext storage and rotation path.
3. Idempotency, saga recovery and partial-failure tests pass.
4. Network and SMPP preflight checks are available.
5. Per-partner observability, RBAC and audit events are implemented.
6. The commercial policy for quotas, CDRs and settlement is approved.
7. An end-to-end staging run covers inbound, outbound and bidirectional onboarding.

Until this gate passes, `/partners/onboarding` must remain visibly marked as a prototype and its final action must only produce local mock state.

## Completion criteria

- A successful production run creates or attaches every planned resource exactly once.
- A failed run is resumable and exposes which step failed, with no hidden partial configuration.
- Secrets never appear in API responses, logs, audit diffs or the admin database.
- The partner can complete a controlled bind and message exchange in the selected direction.
- Operators can trace traffic and failures by partner and onboarding run.
- Suspension and deprovisioning are tested and leave no live credentials or routes behind.

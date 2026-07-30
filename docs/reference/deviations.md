# Where this gateway deliberately differs from the reference

Preserved from `spec/compatibility/DEVIATIONS.md` before the Python reference
implementation was removed. The reference commit was
`0aac58e466d583d0f0436df7b8afa3dc96191263`; `jasmin/...` paths are provenance
only.

These were originally framed as deviations needing sign-off against a parity
gate. That gate is gone: `docs/plans/017-smpp-production-readiness.md` made SMPP
3.4 conformance and independent interop the standard, and the reference is no
longer authoritative. So these are now simply **design decisions with their
rationale recorded** — which is what they should have been.

Later divergences not in this file, decided while hardening for production:

- **Reconnect backoff.** The reference retries at a fixed interval forever, which
  hammers a recovering SMSC. We back off exponentially with jitter to a cap.
- **Bounded delivery window.** The reference has no server-side outstanding-request
  limit, so an ESME that never acknowledges is invisible. We bound it and time out.
- **MO interception on segments and the whole.** The reference intercepts each
  arriving PDU and never the reassembled whole. We do both: intercepting only the
  whole let a reject be bypassed after segments had already shipped.
- **Liberal inbound optional parameters.** The reference rejects some malformed
  optional TLVs. On a carrier-originated PDU we skip the parameter and keep the
  message — a failed decode there becomes a reconnect loop and an MO outage.
- **Separate admin listener.** `/admin/` shared the public send port; it now has
  its own.

## D-001 — Filters are inline, not named reusable objects

- **Matrix rows:** J-007 (`filter`), and the filter portions of J-008/J-009/J-010/J-011.
- **Legacy:** jCli creates filters as first-class objects with an fid (`filter -a`);
  routes and interceptors reference them by fid.
- **New:** filters are embedded in the route or interceptor that uses them
  (`outbound.RouteConfig.Filters`, `modispatch.RouteConfig.Filters`,
  `outbound.InterceptorConfig.Filters`). There is no filter registry.
- **Reason:** see [ADR-003](../../docs/adr/003-filter-model.md). Inline avoids a
  second identity space and dangling-reference handling; nothing in the current
  deployment reuses a filter across routes.
- **Impact:** editing a repeated filter means editing each copy. No security
  impact — the same filter engine and matching semantics apply either way.
- **Migration:** a jCli-era configuration expands each referenced filter into the
  referencing route. Rollback is to build the registry (the stored specs remain
  valid, since inline filters are a subset).
- **Proving test:** the routing and interception filter tests
  (`internal/app/outbound/filters_test.go`, `internal/core/routingfilter`) cover
  matching semantics; the deviation is representational, not behavioural.
- **Owner approval:** _pending_.

## D-002 — WITHDRAWN (2026-07-28): `persist` / `load` are implemented

Originally recorded as "obsolete by design", on the reasoning that the Go admin
plane persists every mutation as it applies it, so there is nothing to flush.
That is true of `persist` without a profile — and it misses what the commands
are actually for. `persist -p known-good` / `load -p known-good` is how an
operator keeps a configuration to roll back to, and no amount of
apply-then-persist provides that.

`admin.ProfileService` now implements named snapshots: `persist -p NAME` writes
every admin table under that name, `load -p NAME` restores them in one
transaction and re-applies every service. J-015, J-016 and J-017 are `MATCH`.

The original text is kept below for the record.

### Original record

## D-002 (original) — `persist` / `load` have no semantic role

- **Matrix rows:** J-015 (`persist`), J-016 (`load`), J-017 (autoload).
- **Legacy:** jCli mutations live in memory until `persist` writes a profile to
  disk; `load` reads one back, and `jcli-prod` autoloads at startup.
- **New:** every admin mutation applies live **and** is written to SQLite in the
  same operation (apply-first-then-persist), and is re-applied at boot by
  `LoadAndApply`. There is no unsaved state to persist and no profile to load.
- **Reason:** the store is the source of truth; a separate save step can only
  introduce a window where live state and persisted state disagree.
- **Impact:** operationally safer (no lost changes on restart). A script ending
  in `persist` must keep working — see plan 013 Step 7, which proposes accepting
  `persist` as a no-op returning the oracle's success text, while `load` with an
  explicit profile returns a clear error rather than silently doing nothing.
- **Migration:** drop `persist` from provisioning scripts (harmless if left).
  Rollback is not applicable — this follows from the storage design.
- **Proving test:** restart-survival is covered by each service's `LoadAndApply`
  path and verified live (admin connectors, MT/MO routes, interceptors, users).
- **Owner approval:** _pending_.

## D-003 — `smppccm -s` prints the bind password, matching the oracle

**Surface:** JCLI (J-012) · **Status:** accepted, pending owner sign-off

The frozen console prints a connector's bind password in clear in `smppccm -s`
(see `fixtures/jcli/J-012-smppccm.jsonl`). An earlier Go implementation redacted
it. Byte-parity and redaction are mutually exclusive, and the Go console now
matches the oracle.

**Why match rather than redact:** the console is reachable only after
authentication and is already a full privilege boundary — it mints user
credentials and starts connectors — so redacting one field buys little, while
any script that reads `password` from a `smppccm -s` transcript breaks silently
if the field disappears.

**Risk accepted:** the password appears in any terminal scrollback, session
recording or log that captures console output.

**Rollback invariant:** redacting the field again is a one-line change in
`connectorFieldValue` (`internal/app/jcli/managers_smppccm.go`), and it makes
J-012 fail — which is the point: the deviation cannot be taken silently.

**Revisit when:** the console grows a per-field redaction mode, or an operator
requires secret-free transcripts for compliance.

## Required record format

Every future deviation must include:

- ID and affected compatibility matrix rows;
- legacy behavior and evidence;
- new behavior and reason;
- security/operational impact;
- migration and rollback instructions;
- owner approval date;
- differential fixture/test proving the boundary.

Security hardening is delivered through an explicit `secure` profile until the owner approves changing compatibility defaults.

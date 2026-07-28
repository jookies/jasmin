# Approved compatibility deviations

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.

The deviations below are **proposed** by the Go rewrite and await owner approval;
none is yet signed off. Each states the legacy behaviour, the new behaviour and
the reason, per the record format at the bottom of this file.

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

## D-002 — `persist` / `load` have no semantic role

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

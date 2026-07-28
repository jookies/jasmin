# Routing filters stay inline, not named reusable objects

- **Date:** 2026-07-27
- **Status:** active
- **Summary:** Keep filters embedded in the route/interceptor that uses them, rather than porting jCli's named `filter` registry, and record the divergence as an approved deviation.
- **Related:** [plans/012-admin-plane-full-coverage.md](../plans/012-admin-plane-full-coverage.md) Step 7, [plans/013-jcli-console.md](013 — jCli console), `spec/compatibility/JCLI_MATRIX.md` J-007

## Context

jCli models filters as **first-class named objects**: `filter -a` creates one with an fid, and routes and interceptors then reference it by that fid (`JCLI_MATRIX.md` J-007). The Go rewrite instead embeds filters directly in the thing that uses them — `outbound.RouteConfig.Filters`, `modispatch.RouteConfig.Filters`, `outbound.InterceptorConfig.Filters` — and has done since the routing engine was written.

Closing the jCli coverage gap forced the question: does the admin plane grow a filter registry to match, or does the inline model stand?

## Decision

**Filters stay inline.** No filter registry, no fids, no cross-references. A route or interceptor carries its own filter list, and the same logical filter used in two places is written twice.

The divergence from J-007 is recorded in `spec/compatibility/DEVIATIONS.md`. If the jCli console implements `filter` commands later, they operate on a synthetic view rather than a stored registry — or that surface is declared out of scope, which is the same decision made explicit.

## Alternatives considered

### Port the named-filter registry

Faithful to jCli and genuinely useful when one non-trivial filter is reused across many routes and must be edited in one place. Rejected for now because it buys that single benefit at a real cost:

- **Referential integrity becomes a runtime concern.** Deleting a referenced filter must be refused, which means every delete walks every route and interceptor, and the admin store grows a dependency graph it does not otherwise need.
- **Two-step provisioning.** Creating a route becomes "create the filter, then reference it", which is worse in a form-based UI than filling in the filter where it is used.
- **A second identity space.** fids must be unique, validated, and kept stable across restarts — the same class of problem the user-uid work already had to solve.
- **No demand evidence.** Nothing in the current deployment reuses a filter across routes. Building the registry now would be speculative.

### Support both (inline or by reference)

The worst of both: every consumer needs a resolution step, the UI needs a mode switch, and two ways to express the same thing diverge in behaviour over time.

## Consequences

### Positive

- The admin plane, the JSON API and the UI keep one obvious representation; the route form already renders it (`web/src/components/FilterList.tsx`, shared by MT routes, MO routes and interceptors).
- No dangling references, no delete-protection logic, no fid allocation.
- The stored spec is self-contained, so a route can be read, copied or replayed without resolving anything.

### Negative / accepted trade-offs

- **Editing a repeated filter means editing every copy.** With a handful of routes this is a non-issue; if an operator ever maintains the same regex across a dozen routes it will hurt, and that is the signal to revisit.
- **A jCli `filter` command has no backing store**, so J-007 cannot reach MATCH as written. This is the deviation being recorded, not an oversight.
- Operators migrating from jCli lose a concept they may have organised their configuration around.

### Revisit when

Any of: an operator asks for it; the same filter appears in more than ~5 routes; or jCli parity for J-007 becomes a hard cutover requirement rather than a nice-to-have.

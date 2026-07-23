# Wave 1E — Billing quota persistence timer parity

## Goal

Implement the bounded `B-009` timer/dirty-state contract: user MT-quota mutations become dirty, a periodic tick persists the first observed dirty user's group then user state, and completed legacy-compatible persistence clears only the captured mutation generation.

## Scope

- Primary row: `B-009` (promotion only to `GO-PARTIAL`).
- Regression dependencies: `B-001`–`B-008`.
- Frozen baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.

## Oracle/test harness

1. Capture `RouterPB.persistenceTimerExpired` at the real legacy method boundary without changing frozen source.
2. Cover a clean tick, one dirty user, two dirty users (first-only clearing), and persistence-return failure.
3. Couple capture runner, fixture, schema, verifier fingerprint/case registry, fixture coverage, and generic Go differential tests.

## Production implementation

1. Track a monotonically increasing quota mutation generation on each billing user.
2. Expose immutable quota snapshots and conditional generation clearing so a concurrent mutation cannot be cleared by an older persist.
3. Add a context-cancellable periodic persistence service with a durable store boundary.
4. Preserve the legacy scan/order contour while treating persistence errors honestly: do not clear dirty state after a failed durable write.
5. Keep PostgreSQL production wiring and restart/bootstrap authority out of this bounded timer slice; `B-009` remains `GO-PARTIAL`.

## Non-goals

- PostgreSQL authoritative quota bootstrap/recovery and multi-process fencing.
- Independent group-credential administration outside a dirty user MT credential.
- Duplicate/reordered late-billing event closure (`B-010`).
- SMPP-server parity (`B-011`).
- Mutation of `jasmin/` or `tests/`.

## Acceptance criteria

1. Fixture regeneration is byte-reproducible and verifier/schema checks reject drift.
2. Generic Go differential tests have no skips and reproduce the captured first-dirty scan and clearing behavior.
3. Concurrent mutation during persistence remains dirty; failed persistence remains dirty; ticker shutdown is context-cancellable and race-clean.
4. Focused repetition, full Go/race/vet/build, Python fixture/unit/schema/registry/manifest, frozen-tree, and secret gates pass.
5. Stable candidate receives an exact-candidate Ralph boundary audit; supported findings are fixed before publication.
6. Plan and implementation publish together; exact-SHA GitHub Actions passes 4/4 before terminal closure.

## Affected paths

- `scripts/compat/capture_billing_persistence_golden.py`
- `scripts/compat/capture_all.sh`
- `scripts/compat/verify_fixtures.py` and tests
- `compat/fixtures/billing-persistence/*`
- `internal/core/billing/*`
- `spec/compatibility/{ROUTING_BILLING_MATRIX.md,FIXTURE_COVERAGE.csv}`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

## Verification

- Frozen oracle: `4` billing-persistence cases preserve baseline `0aac58e466d583d0f0436df7b8afa3dc96191263`; fixture/schema/registry checks pass with `195` coverage rows and `205 = 127 INVENTORIED + 56 GO-PARTIAL + 19 MATCH + 3 GO-COMPLETE`.
- Focused implementation gates: billing package `x20`, completion-relative rearm regression `x20`, and billing race `x5` pass.
- Full local gates after the audit correction: `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`, `29` fixture-verifier unit tests, schemas, registry, manifest (`1,039` tests / `55` files), and frozen-tree checks all pass.
- Ralph boundary audit found one Medium cadence mismatch: `time.Ticker` could queue intervals during a slow write and persist the next dirty user immediately. The old implementation failed the new discriminator by starting the second write about `65µs` after completion; the correction uses a one-shot timer rearmed only after `PersistOnce` returns, binds the fixture's `rearm_count`, and passed the focused final council with no remaining High/Medium finding.
- Published implementation `727bf87858a7c661ea3ee2df8ad07f878c99c1f7` passed exact-SHA GitHub Actions run `30029391530` with `4/4` jobs. Published correction `b6cc42b297482b695d4797f44a8df346820bc2f2` passed exact-SHA run `30036837910` with `4/4` jobs; local, tracking, and remote refs matched with a clean workspace.
- Scope remains deliberately partial: PostgreSQL bootstrap/recovery, multi-process fencing, independent group administration, `B-010` redelivery, and `B-011` SMPP-server parity are not claimed.

Implementation candidate LoopKey: 47b5603cb3e8

This marker identifies the executable implementation evidence only; it is not the terminal publication-tree LoopKey. Terminal closure is determined separately by the main orchestrator's post-publication exact-SHA CI, ref-equality, and clean-workspace gate for this final documentation descendant.

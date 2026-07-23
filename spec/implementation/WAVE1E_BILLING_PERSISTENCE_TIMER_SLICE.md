# Wave 1E — Billing quota persistence timer parity

## Goal

Implement the bounded `B-009` timer/dirty-state contract: quota mutations become dirty, a periodic tick persists the first observed dirty user together with group/user state, and successful persistence clears only the captured mutation generation.

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

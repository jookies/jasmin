# Phase 2.20 — SMPP Client Readiness and Queue-Age Decision

## Goal

Implement a bounded, fixture-proven portion of `SC-005`: the legacy outbound
`submit_sm` readiness decision for expired, disconnected, and unbound messages,
including its maximum-age boundary and configured requeue delay.

## Included

- Frozen-oracle capture through `SMPPClientSMListener.submit_sm_callback` for
  expiry, disconnected, and unbound terminal paths.
- Exact strict maximum-age boundary (`age.seconds > max_age`) and legacy
  `timedelta.seconds` modulo-day behavior.
- Delayed and immediate requeue outcomes from listener configuration.
- A generic, no-skip Go differential harness over every readiness fixture.
- A typed, deterministic, concurrency-safe Go readiness policy suitable for a
  future queue consumer.
- Listener defaults of 1200 seconds maximum age and 30 seconds retry delay.

## Non-goals

- AMQP queue ownership, consumer lifecycle, ACK/reject execution, or timers.
- Socket submission and submit response correlation.
- SMPP status-based retry rules (`SC-006`).
- Correcting legacy modulo-day or future-created-at quirks; compatibility is
  captured before any separately approved deviation.
- Claiming full `SC-005` parity; this slice remains `GO-PARTIAL`.

## Acceptance criteria

1. Oracle capture executes the frozen callback and asserts its terminal path and
   post-state instead of reimplementing the decision.
2. Two captures are byte-identical; schema, integrity, source-boundary, and
   trusted-corpus checks pass.
3. The generic Go differential test executes every fixture without skips.
4. Focused tests pass 20 times, including concurrent policy evaluation and
   invalid-input boundaries.
5. Full Go, race, vet, build, relevant fuzz, Python verifier/schema/unit,
   manifest, frozen-baseline, secret-scan, and workspace-contamination gates
   pass on a stable candidate.
6. `SMPP_MATRIX.md` marks only the fixture-proven subset of `SC-005` as
   `GO-PARTIAL`; unsupported AMQP execution remains inventoried.
7. Final Ralph audit has no unresolved high-severity finding; committed SHA is
   pushed and GitHub Actions succeeds 4/4 on that exact SHA.

## Affected paths

- `scripts/compat/capture_smpp_client_readiness_golden.py`
- `scripts/compat/capture_all.sh`
- `compat/fixtures/smpp-client-readiness/baseline.json`
- `compat/fixtures/schema/smpp-client-readiness-golden.schema.json`
- `scripts/compat/verify_fixtures.py`
- `scripts/compat/validate_json_schemas.py`
- `internal/core/smppc/readiness.go`
- `internal/core/smppc/readiness_golden_test.go`
- `internal/core/smppc/readiness_test.go`
- `spec/compatibility/FIXTURE_COVERAGE.csv`
- `spec/compatibility/SMPP_MATRIX.md`

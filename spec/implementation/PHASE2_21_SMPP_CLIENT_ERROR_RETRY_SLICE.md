# Phase 2.21 — SMPP Client Submit Error Retry Decision

## Goal

Implement a bounded, fixture-proven portion of `SC-006`: the legacy outbound
`submit_sm_resp` error retry decision, including default status rules, exact
attempt-count boundary, configured delay, and final ACK behavior.

## Included

- Frozen-oracle capture through `SMPPClientSMListener.submit_sm_resp_event` for
  configured and unconfigured SMPP error statuses.
- Listener defaults for `ESME_RSYSERR`, `ESME_RTHROTTLED`, `ESME_RMSGQFUL`, and
  `ESME_RINVSCHED`.
- Exact `current_attempt < configured_count` retry boundary and configured delay.
- Finalization behavior: retry requeues without ACK; exhausted configured errors
  clear retry state and ACK; unconfigured errors ACK while preserving the legacy
  retry-state entry.
- A generic, no-skip Go differential harness over every error-retry fixture.
- A typed, deterministic, immutable, concurrency-safe Go retry policy suitable
  for a future AMQP response consumer.

## Non-goals

- AMQP queue ownership, timer execution, actual ACK/reject calls, or publishing.
- Successful `ESME_ROK` response handling, DLR publication, billing, response
  publication, or multipart response correlation.
- Socket submission and timeout/transport exception retries.
- Validation or normalization of arbitrary legacy configuration-file literals.
- Correcting the legacy unconfigured-status retry-state retention; compatibility
  is captured before any separately approved deviation.
- Claiming full `SC-006` parity; this slice remains `GO-PARTIAL`.

## Acceptance criteria

1. Oracle capture executes the frozen callback and asserts terminal path and
   retry-state post-state instead of reimplementing the decision.
2. Two captures are byte-identical; schema, integrity, source-boundary, and
   trusted-corpus checks pass.
3. The generic Go differential test executes every fixture without skips.
4. Focused tests pass 20 times, including concurrent policy evaluation and
   invalid status/count/delay boundaries.
5. Full Go, race, vet, build, relevant fuzz, Python verifier/schema/unit,
   manifest, frozen-baseline, secret-scan, and workspace-contamination gates
   pass on a stable candidate.
6. `SMPP_MATRIX.md` marks only the fixture-proven subset of `SC-006` as
   `GO-PARTIAL`; unsupported AMQP execution and response side effects remain
   inventoried.
7. Final Ralph audit has no unresolved high-severity finding; committed SHA is
   pushed and GitHub Actions succeeds 4/4 on that exact SHA.

## Affected paths

- `scripts/compat/capture_smpp_client_error_retry_golden.py`
- `scripts/compat/capture_all.sh`
- `compat/fixtures/smpp-client-error-retry/baseline.json`
- `compat/fixtures/schema/smpp-client-error-retry-golden.schema.json`
- `scripts/compat/verify_fixtures.py`
- `scripts/compat/validate_json_schemas.py`
- `internal/core/smppc/error_retry.go`
- `internal/core/smppc/error_retry_golden_test.go`
- `internal/core/smppc/error_retry_test.go`
- `spec/compatibility/FIXTURE_COVERAGE.csv`
- `spec/compatibility/SMPP_MATRIX.md`

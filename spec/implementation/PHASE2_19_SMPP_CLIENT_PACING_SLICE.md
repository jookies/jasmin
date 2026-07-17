# Phase 2.19 — SMPP Client Throughput Pacing

## Goal

Implement a bounded, fixture-proven portion of `SC-004`: the legacy outbound
`submit_sm` pacing decision and a concurrency-safe Go pacer suitable for the
connector queue consumer.

## Included

- Frozen-oracle capture of `SMPPClientConfig.submit_sm_throughput` defaults and
  ordinary JSON number/string type validation.
- Frozen-oracle capture of the wait selected by
  `SMPPClientSMListener.submit_sm_callback` for unlimited, epoch-sentinel first-message,
  boundary, integer, fractional, and sub-1-MPS cases.
- A generic, no-skip Go differential harness over every pacing fixture.
- A typed, context-cancellable, concurrency-safe pacer using an injected clock
  for deterministic tests.
- `Config.SubmitSMThroughput` with the legacy default of `1` message/second and
  numeric-domain validation.

## Non-goals

- AMQP queue ownership, ACK/requeue policy, or message expiry (`SC-005`).
- SMPP status-based retries (`SC-006`).
- Socket-level `submit_sm` request/response correlation.
- Claiming full `SC-004` parity; this slice remains `GO-PARTIAL`.
- Correcting the legacy whole-second truncation behavior for rates below 1 MPS;
  compatibility is captured and documented before deciding on a deviation.
- Python's `bool`-as-`int` configuration quirk and non-finite Python floats;
  these remain inventoried rather than silently claimed by this bounded slice.

## Acceptance criteria

1. Oracle capture executes the frozen Python config and listener callback; it
   does not reimplement their pacing formula in the capture script.
2. Two captures are byte-identical and fixture schema/integrity checks pass.
3. The generic Go differential test executes every fixture without skips.
4. Focused tests pass 20 times, including cancellation and concurrent callers.
5. Full Go, race, vet, build, relevant fuzz, Python verifier/schema/unit,
   manifest, frozen-baseline, secret-scan, and workspace-contamination gates
   pass on a stable candidate.
6. `SMPP_MATRIX.md` marks only `SC-004` as `GO-PARTIAL` with an exact scope note.
7. Final Ralph audit has no unresolved high-severity finding; the committed SHA
   is pushed and GitHub Actions succeeds 4/4 on that exact SHA.

## Affected paths

- `scripts/compat/capture_smpp_client_pacing_golden.py`
- `scripts/compat/capture_all.sh`
- `compat/fixtures/smpp-client-pacing/baseline.json`
- `compat/fixtures/schema/smpp-client-pacing-golden.schema.json`
- `scripts/compat/verify_fixtures.py`
- `scripts/compat/validate_json_schemas.py`
- `internal/core/smppc/config.go`
- `internal/core/smppc/pacer.go`
- `internal/core/smppc/pacer_golden_test.go`
- `internal/core/smppc/pacer_test.go`
- `spec/compatibility/FIXTURE_COVERAGE.csv`
- `spec/compatibility/SMPP_MATRIX.md`

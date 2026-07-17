# Phase 2.22 — SMPP Client Submit Response Publication

## Goal

Implement a bounded, fixture-proven portion of `SC-008`: the legacy listener's
optional publication of `submit_sm_resp` results to the request's `reply-to`
routing key, including enabled/disabled behavior and success/error/retry paths.

## Included

- Frozen-oracle capture through `SMPPClientSMListener.submit_sm_resp_event`.
- Exact optional-publication decision for enabled and disabled configuration.
- Publication on final success, final SMPP error, and an error selected for retry.
- Exact `messaging` exchange, request `reply-to` routing key, message ID, opaque
  protocol-2 response body, and AMQP properties created by `SubmitSmRespContent`.
- A generic, no-skip Go differential harness over every response-publication fixture.
- A typed, bounded Go publication projection that composes with the existing
  immutable `amqpcompat.Envelope` and never decodes legacy pickle.

## Non-goals

- Live AMQP publishing, confirms, retries, queue declaration, or consumer ownership.
- DLR lookup publication, billing publication, ACK/reject/timer execution, or logs.
- Generating Python pickle in Go; fixture-proven legacy response bodies remain opaque.
- Arbitrary routing keys outside the fixture-proven `submit.sm.resp.<target>` family.
- Socket submission, response correlation, multipart response handling, or DLR state.
- Claiming full `SC-008` parity; this slice remains `GO-PARTIAL`.

## Acceptance criteria

1. Oracle capture executes the frozen callback, asserts the terminal path, and
   records the actual response publication passed to the production broker API.
2. Two captures are byte-identical; schema, integrity, source-boundary, trusted-
   corpus, and frozen-manifest checks pass.
3. The generic Go differential test executes every fixture without skips.
4. Focused tests pass 20 times, including concurrent projection, defensive-copy,
   malformed routing-key, oversized-body, and invalid-property boundaries.
5. Full Go, race, vet, build, relevant fuzz, Python verifier/schema/unit, frozen
   baseline, secret scan, and workspace-contamination gates pass on one candidate.
6. `SMPP_MATRIX.md` marks only the fixture-proven subset of `SC-008` as
   `GO-PARTIAL`; live AMQP execution and remaining response side effects stay inventoried.
7. Final Ralph audit has no unresolved high-severity finding; committed SHA is
   pushed and GitHub Actions succeeds 4/4 on that exact SHA.

## Affected paths

- `scripts/compat/capture_smpp_client_response_publish_golden.py`
- `scripts/compat/capture_all.sh`
- `compat/fixtures/smpp-client-response-publish/baseline.json`
- `compat/fixtures/schema/smpp-client-response-publish-golden.schema.json`
- `scripts/compat/verify_fixtures.py`
- `scripts/compat/validate_json_schemas.py`
- `internal/core/smppc/response_publish.go`
- `internal/core/smppc/response_publish_golden_test.go`
- `internal/core/smppc/response_publish_test.go`
- `spec/compatibility/FIXTURE_COVERAGE.csv`
- `spec/compatibility/SMPP_MATRIX.md`

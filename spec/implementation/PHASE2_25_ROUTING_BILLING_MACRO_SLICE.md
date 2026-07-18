# Phase 2.25 — Macro 1.3a: Atomic submit billing and MT orchestration

## Goal

Add one independently publishable outbound-MT vertical slice that composes the
already fixture-backed interceptor, routing, segmentation, billing, and AMQP
envelope boundaries. Close the state-mutation gap in billing without claiming
live RabbitMQ topology or a complete Python-pickle producer.

## Scope

- Capture the public legacy `RouterPB.chargeUserForSubmitSms` boundary with
  preconditions, result, and quota post-state. Preserve the HTTP/SMPP caller's
  total-balance authorization rule while applying only the early
  `submit_sm` amount at enqueue time.
- Add a generic, no-skip Go differential harness for every captured case.
- Add one linearizable user/group authorization-and-apply operation. It must
  check all required quotas and mutate user/group state under one lock set.
- Add a concurrency-safe in-memory user registry for the orchestration boundary.
- Add `core.SubmitService`: user lookup → routable → interceptor → route →
  segmentation → envelope construction → atomic submit charge → ordered AMQP
  publish.
- Keep envelope bytes opaque and require an injected envelope builder. Existing
  AMQP golden tests remain the byte-exact authority for `submit.sm.<CID>` and
  `bill_request.submit_sm_resp.<UID>` content.
- Add deterministic IDs/references through injected generators in tests; do not
  derive protocol identities from wall-clock nanoseconds.
- Update fixture registry, coverage, matrices, and macro roadmap truthfully.

## Non-goals

- Declaring live RabbitMQ exchanges/queues, reconnect policy, confirms, outbox,
  or retries (A-001/A-009/A-010/A-012 remain inventoried).
- Generating Python `SubmitSM` pickle bodies in Go. A-002/A-013 remain
  `GO-PARTIAL`; the injected builder is the explicit mixed-mode boundary.
- Consuming late `submit_sm_resp` bills, deduplication, persistence timers, or
  ledger improvements (B-007/B-009/B-010 remain partial/inventoried).
- Wiring a production process/container or changing the frozen Python oracle.
- Refunding the early charge after an AMQP error. Legacy charges before calling
  the client manager and does not roll the quota back on downstream failure.

## Legacy contract

Authoritative source boundaries:

- `jasmin/protocols/http/endpoints/send.py:319-355`
- `jasmin/protocols/smpp/factory.py:448-466`
- `jasmin/routing/router.py:319-363`
- `jasmin/routing/Routes.py:getBillFor`
- `jasmin/managers/clients.py:570-595`
- `jasmin/managers/content.py:SubmitSmContent`

Important operation order:

1. Callers authorize against total early + late balance and total segment count.
2. Router applies only `submit_sm` balance and count at enqueue time.
3. Late `submit_sm_resp` balance is charged only on a successful response.
4. The downstream manager call follows charging; failure does not refund.

## Acceptance criteria

1. A frozen-oracle fixture records exact acceptance/error and user quota
   post-state for equality, below-boundary, multipart, unlimited, split-billing,
   and no-partial-mutation authorization cases.
2. Fixture regeneration twice is byte-identical and the integrity verifier has
   an explicit case registry and trusted corpus fingerprint.
3. The Go differential harness is generic, executes every fixture case, and has
   no skip/fallback path.
4. Concurrent authorization cannot make user or shared-group quotas negative;
   exact success counts and final post-state are asserted under `go test -race`.
5. SubmitService tests prove route/interceptor errors, multipart envelope order,
   exact bill passed to the builder, publish failure semantics, and no billing
   mutation when envelope construction fails.
6. Focused tests pass 20 consecutive times, then full Go, race, vet, build,
   Python schema/integrity/unit, fixture regeneration, secret scan, and workspace
   contamination gates pass.
7. A stable candidate identity receives a final Ralph code audit; accepted fixes
   rerun invalidated gates.
8. Plan and implementation commits are pushed together. Exact remote SHA must
   reach all configured GitHub checks before a LoopKey is emitted.

## Affected paths

- `scripts/compat/capture_billing_enforcement_golden.py`
- `scripts/compat/capture_all.sh`
- `scripts/compat/capture_http_golden.py` (reproducibility repair for the preceding macro)
- `compat/fixtures/billing-enforcement/baseline.json`
- `compat/fixtures/schema/billing-enforcement-golden.schema.json`
- `compat/fixtures/schema/segmentation-golden.schema.json` (schema repair for the preceding macro)
- `scripts/compat/verify_fixtures.py`
- `scripts/compat/test_verify_fixtures.py`
- `scripts/compat/validate_json_schemas.py`
- `spec/compatibility/FIXTURE_COVERAGE.csv`
- `spec/compatibility/ROUTING_BILLING_MATRIX.md`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`
- `internal/core/billing/billing.go`
- `internal/core/billing/enforcement_golden_test.go`
- `internal/core/billing/atomic_test.go`
- `internal/core/submit_service.go`
- `internal/core/submit_service_test.go`

## Verification

- Terminal candidate: `f82a12695158c0548152f013ceb5a3c4bde9a439`
- Exact-SHA GitHub Actions: run `29641886661`, `4 / 4` successful
- Local gates: focused `x20`, full Go, race, vet, build, fixture/schema/integrity/unit, secret scan, and clean workspace passed
- Ralph: normal council timed out without a synthesized verdict; degraded recovery used two independent local symbol-scoped critics, with findings verified by the main orchestrator

LoopKey: c766c44be033

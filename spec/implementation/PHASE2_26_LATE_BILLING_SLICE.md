# Phase 2.26 — Macro 1.3b: Late submit response billing consumption

## Goal

Add one independently publishable outbound-MT slice for the legacy
`bill_request.submit_sm_resp.*` consumer boundary: parse a validated billing
envelope, find the target user, apply the late balance decrement atomically, and
return the legacy ACK/reject decision without claiming live RabbitMQ ownership.

## Scope

- Capture `RouterPB.bill_request_submit_sm_resp_callback` at the actual callback
  boundary, including message properties, ACK/reject outcome, and balance
  post-state.
- Preserve the legacy decisions for a missing user, insufficient finite balance,
  exact/sufficient balance, zero amount, opaque alphanumeric user IDs, and
  unlimited balance.
- Add a generic no-skip Go differential harness over every captured case.
- Add a concurrency-safe late-charge state transition on `billing.User`.
- Add a bounded `core.LateBillingService` that validates the AMQP route,
  message/header fields, amount syntax/range, and user identity before mutation.
- Keep broker consume loops, delivery tags, topology, reconnects, persistence,
  and redelivery outside this slice.
- Update fixture registry, schema/integrity checks, coverage, matrices, and macro
  roadmap counts from authoritative matrix rows.

## Non-goals

- Declaring or consuming the live `billing` exchange/queue (A-001/A-009/A-010/A-012).
- ACK/reject execution against a broker delivery or retry/redelivery semantics.
- Deduplication, idempotency improvements, durable ledger, or crash recovery
  (B-009/B-010 and P-010 remain inventoried).
- Changing the frozen Python implementation or fixing its unlimited-balance
  no-ACK behavior; parity records that behavior truthfully.
- Wiring a production process/container.

## Legacy contract

Authoritative boundaries:

- `jasmin/routing/router.py:97-109`
- `jasmin/routing/router.py:235-263`
- `jasmin/managers/content.py:201-212`
- `tests/routing/test_router.py:1539-1566`
- `tests/managers/test_contents.py:354-365`

Important behavior:

1. The callback reads `message-id`, string `headers.amount`, and
   `headers.user-id`.
2. Missing users and insufficient finite balances are rejected.
3. Finite sufficient balances are decremented and ACKed.
4. An unlimited (`None`) balance takes neither ACK nor reject branch in the
   frozen implementation.
5. Duplicate deliveries are not deduplicated by the legacy callback.

## Acceptance criteria

1. The frozen-oracle fixture is reproducible twice byte-for-byte and has an
   explicit case registry plus trusted corpus fingerprint.
2. The generic Go differential harness executes every fixture case without a
   skip/fallback path.
3. The production service rejects malformed/wrong-route envelopes without
   mutation and reproduces every valid legacy decision/post-state.
4. Concurrent late-charge calls cannot drive a finite balance negative; exact
   success/reject counts and final state pass under `go test -race`.
5. Focused tests pass 20 consecutive times, followed by full Go, race, vet,
   build, Python schema/integrity/unit, fixture regeneration, secret scan, and
   workspace-contamination gates.
6. A stable candidate identity receives a final Ralph code audit. Accepted
   findings rerun invalidated gates.
7. Plan and implementation publish together; exact-SHA GitHub Actions reaches
   4/4, local/remote SHA match, workspace is clean, and the main orchestrator
   emits the final LoopKey.

## Affected paths

- `scripts/compat/capture_late_billing_golden.py`
- `scripts/compat/capture_all.sh`
- `compat/fixtures/late-billing/baseline.json`
- `compat/fixtures/schema/late-billing-golden.schema.json`
- `scripts/compat/verify_fixtures.py`
- `scripts/compat/test_verify_fixtures.py`
- `scripts/compat/validate_json_schemas.py`
- `spec/compatibility/FIXTURE_COVERAGE.csv`
- `spec/compatibility/ROUTING_BILLING_MATRIX.md`
- `spec/compatibility/AMQP_REDIS_MATRIX.md`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`
- `internal/core/billing/billing.go`
- `internal/core/billing/late_charge_test.go`
- `internal/core/late_billing_service.go`
- `internal/core/late_billing_service_test.go`

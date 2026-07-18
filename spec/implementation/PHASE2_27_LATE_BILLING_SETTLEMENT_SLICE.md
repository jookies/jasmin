# Phase 2.27 — Macro 1.3c: Late billing AMQP settlement boundary

## Goal

Connect the fixture-proven late `submit_sm_resp` billing decision to explicit
AMQP ACK/reject ownership without allowing the generic consumer to ACK before
downstream processing completes.

## Scope

- Reuse and regenerate the frozen `late-billing` oracle corpus captured at
  `RouterPB.bill_request_submit_sm_resp_callback`; its expected `ack`, `reject`,
  and `none` actions are the semantic authority.
- Replace the generic consumer's eager-ACK envelope stream with an unsettled
  delivery handle that exposes the immutable envelope and exactly one explicit
  terminal ACK or reject operation.
- Add a bounded late-billing delivery processor that invokes
  `core.LateBillingService` once and maps `ack` to broker ACK, `reject` to
  non-requeue reject, and the frozen unlimited-balance `none` action to no
  terminal broker operation.
- Add a generic no-skip differential test that runs every committed
  `late-billing` fixture through the processor and verifies balance post-state
  plus exact settlement calls.
- Reject duplicate or conflicting settlement attempts in-process and preserve
  broker errors for the caller.
- Update A-009 and the Macro 1.3 roadmap description truthfully while retaining
  partial status for every row whose full consumer/recovery contract is not
  proven.

## Non-goals

- Declaring the live billing exchange, queue, or wildcard binding (A-001).
- QoS/prefetch, reconnect/recovery, consumer cancellation, retry/redelivery,
  deduplication, or durable ledger semantics (A-010/A-012/B-010).
- Defining a terminal policy for malformed deliveries: decode or processing
  failures remain unsettled, and this bounded API only returns processing errors
  to its direct caller.
- Fixing the frozen unlimited-balance no-terminal-action behavior.
- Wiring a production process/container or claiming full A-008/A-009 parity.
- Changing the frozen Python implementation or fixture case set.

## Legacy contract

Authoritative boundaries:

- `jasmin/routing/router.py:97-117`
- `jasmin/routing/router.py:235-263`
- `scripts/compat/capture_late_billing_golden.py`
- `compat/fixtures/late-billing/baseline.json`

Important behavior:

1. The queue uses manual acknowledgement.
2. Missing users and insufficient finite balances call reject with `requeue=0`.
3. Successful finite charges call ACK only after balance mutation.
4. Unlimited balances produce neither ACK nor reject in the frozen callback.
5. The callback schedules its next queue read before the billing decision; this
   slice does not claim prefetch, concurrency, or recovery parity.

## Acceptance criteria

1. The committed `late-billing` fixture regenerates twice byte-for-byte and its
   registry/fingerprint/schema/integrity checks remain green.
2. Consumer decoding never ACKs implicitly; every delivered valid envelope is
   unsettled until the downstream owner explicitly settles it.
3. An unsettled delivery permits exactly one ACK or non-requeue reject attempt,
   rejects duplicate/conflicting attempts, and returns the broker's error.
4. The generic no-skip processor test executes all seven oracle cases and proves
   exact action, mutation post-state, single service invocation, and settlement.
5. Focused tests pass 20 consecutive times, then full Go, race, vet, build,
   Python schema/integrity/unit, fixture regeneration, secret scan, and workspace
   contamination gates pass on one stable executable candidate.
6. A stable candidate identity receives a final Ralph code audit; supported
   findings are fixed and invalidated gates rerun.
7. Plan and implementation publish together; exact-SHA GitHub Actions reaches
   4 / 4, local/remote SHA match, workspace is clean, and the main orchestrator
   emits the final LoopKey.

## Affected paths

- `spec/implementation/PHASE2_27_LATE_BILLING_SETTLEMENT_SLICE.md`
- `internal/transport/amqpcompat/client.go`
- `internal/transport/amqpcompat/client_test.go`
- `internal/core/late_billing_delivery.go`
- `internal/core/late_billing_delivery_test.go`
- `scripts/compat/verify_fixtures.py`
- `spec/compatibility/FIXTURE_COVERAGE.csv`
- `spec/compatibility/AMQP_REDIS_MATRIX.md`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

## Verification

- Published implementation candidate: `f1f71423c425ac306fb9dc8bbc257c0496552668`
- Exact-SHA GitHub Actions: run `29653973072`, `4 / 4` successful
- Oracle/test harness: 7 frozen late-billing cases; aggregate regeneration passed twice with zero diff and fixture-tree SHA-256 `360dd6611fcc99d0506afd5c83a1495dd2868ce1cd1df7ce71145f1a022c88bd`
- Production implementation: focused `x20`, full Go, race, vet, and build passed; explicit settlement is tested with 64 concurrent alternating ACK/reject callers and exactly one broker attempt
- Integrity: 14 JSON schemas, 24 Python verifier tests, 179 coverage rows, manifest `1039 / 55`, matrix recount `204 = 126 INVENTORIED + 60 GO-PARTIAL + 18 MATCH`, frozen-tree, secret, and workspace gates passed
- Ralph: stable candidate `9764dc07201dec622e9d01859a7fcdf31627a275aaa3265a4e61d5213310612a` received a synthesized `ralph-code` PASS. One direct local critic made a falsified multi-settlement claim despite the mutex; the main orchestrator rejected it using the passing 64-caller race test, while the second focused local critic returned PASS

LoopKey: 7146a0bc93cb

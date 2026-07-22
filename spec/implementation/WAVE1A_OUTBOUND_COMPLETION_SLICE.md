# Wave 1A — Outbound Completion A

## Goal

Close the first bounded production-flow slice from HTTP admission through a real SMPP socket response and durable local response/outbox state, without claiming complete outbound parity.

## Scope

- Allowlisted legacy protocol-2 `SubmitSM` decode before SMPP wire encoding.
- Role-aware HTTP + SMPPc gateway lifecycle.
- PostgreSQL-only production submit attempt/result/outbox repository.
- Live single-part path: HTTP `/send` → RabbitMQ → SMPPc → simulated SMSC → `submit_sm_resp` → durable result/response/late-billing intents → source ACK.
- Same-part outbox predecessor ordering across delayed retries and concurrent workers.
- Opaque SMSC message IDs may repeat; result idempotency remains keyed by attempt/final logical part.
- Timeout, connection-loss, and ambiguous write paths durably close the old attempt as `UNKNOWN_AFTER_SEND` before a redelivery receives a new attempt identity.

## Explicit non-goals

- Multipart atomic admission and aggregate/per-part accounting (Task 1.4).
- Reconnect, readiness, TLS, and connector failover closure (Task 1.5).
- Durable authoritative user/control-state balances (Task 2.1).
- Inbound DLR/MO halves and final MS-1 row closure.
- Exactly-once external SMSC delivery.
- Operator-controlled signed macro-closure evidence; the candidate-owned runner remains fail-closed.

## Verification

Executable parent published before the final correction batch: `e564ea65166da2e4b85525ee2f1d0df6ebae50f4` (tree `5f910cae5520adfebc8721ac32a56ce3f7494cf1`); exact-SHA GitHub Actions run `29891783995` passed 4/4 jobs.

Final executable correction packet: `2f50c2c92fe4e0f0bff7cd4312ac7da9d9b96191d2cd1eb17893ffe124715211` (relative to the published parent and excluding only this verification document).

Local stable-candidate gates:

- `outbound-a candidate`: 275 tests, 0 skipped, 0 failed.
- Focused corrected packages x20: PASS.
- Full `go test ./...`, full `go test -race ./...`, `go vet ./...`, and `go build ./...`: PASS.
- Contract registry: 205 total, 184 unfinished = 129 `INVENTORIED` + 55 `GO-PARTIAL` + 18 `MATCH` + 3 `GO-COMPLETE`; structural evidence only, no macro closure claimed.
- Fixture, JSON Schema, test-manifest, shell syntax, and 58 Python compatibility unit tests: PASS.
- Secret-pattern scan: PASS after excluding only explicit fixed test/service credentials from the broad URL heuristic.

The exact-candidate Ralph review of the published parent returned FAIL with two evidence-supported findings: globally unique SMSC IDs could livelock legitimate responses, and timeout redelivery reused a `SENT` attempt. The first correction removed global SMSC-ID uniqueness and added an explicit ambiguity transition. A focused review then found two remaining upgrade/failure-path holes: upgraded SQLite stores retained the old unique index, and a failed immediate ambiguity transition could still expose the active attempt to a later write. The final correction drops the legacy SQLite index during `Init`, adds an upgrade regression, and makes `BeginAttempt` atomically fence any still-active attempt and force one no-write requeue before allocating the next attempt identity. SQLite and PostgreSQL fence regressions plus timeout/redelivery coverage are mandatory candidate/release tests. All invalidated gates above were rerun after the complete correction batch.

Implementation candidate LoopKey: ccdb7a4aea65

# Wave 1B — Durable Multipart Outbound

## Goal

Remove the production single-part restriction while preserving one aggregate HTTP authorization/debit, atomic durable admission of all parts, explicit aggregate/per-part identity, per-part SMPP correlation, and idempotent local response/billing effects.

## Contract scope

- Primary rows: `A-002`, `B-001`–`B-006`, `B-008`.
- Dependency rows (no promotion in this slice): `SP-001`, `SE-001`–`SE-005`, `B-007`, `B-010`.
- Frozen legacy segmentation and billing fixtures remain unchanged.

## Oracle/test harness

1. Reuse the frozen SAR/UDH, GSM7-extension, UCS2-boundary, binary, and billing corpora; do not modify legacy Python.
2. Add production-path tests proving every part carries an aggregate ID plus a one-based part number/count, the last part alone requests DLR, and each durable part owns an independent attempt/result lifecycle.
3. Add live HTTP → durable outbox → RabbitMQ → SMPPc → fake SMSC tests for two-part success and per-part responses.
4. Cover admission rollback, outbox partial-dispatch retry, restart/redelivery, duplicate response, and ambiguous attempt fencing without claiming exactly-once SMSC delivery.

## Production implementation

1. Keep the HTTP-visible `message-id` as the aggregate ID.
2. Add validated AMQP headers `aggregate-message-id`, `part-number`, and `part-count` to every production envelope.
3. Atomically persist the complete contiguous part set and all submit outbox rows before HTTP success.
4. Derive the session durable part key from validated aggregate/part metadata instead of the former `/000001` assumption.
5. Keep per-part bill amounts in each envelope; authorize/debit the aggregate exactly once before durable admission.
6. Keep response, late-billing, retry, and DLR intents keyed by the durable part key. DLR registration remains last-part-only.

## Non-goals

- Exactly-once external SMSC submission.
- Connector failover/TLS/reconnect closure (Task 1.5).
- Inbound DLR/MO execution or Redis correlation.
- Authoritative PostgreSQL user balances (Task 2.1).
- Promotion of rows whose full legacy surface is not yet executable.

## Acceptance criteria

1. RED production tests fail on the current multipart guard and hardcoded session key.
2. All multipart envelopes have identical aggregate IDs, unique contiguous part numbers, a consistent part count, and last-part-only registered delivery.
3. `AdmitSubmit` rejects malformed, duplicate, sparse, mixed-aggregate, or inconsistent-count sets before repository mutation.
4. PostgreSQL admits all logical parts and submit outbox events in one transaction; an injected failure leaves neither parts nor events.
5. Each part can independently reach `RESULT_COMMITTED` or `UNKNOWN_AFTER_SEND`; replay does not create a second local billing application.
6. Focused multipart/storage/session tests pass x20; full Go, race, vet, build, relevant fuzz, Python fixture/schema/manifest/registry, frozen fixture diff, secret scan, and workspace-contamination gates pass.
7. A stable exact candidate receives a boundary Ralph code audit with no unresolved High/Medium finding.
8. Plan plus implementation are pushed to `go-rewrite`; exact-SHA GitHub Actions passes 4/4, local/tracking/remote SHA match, workspace is clean, and the main orchestrator emits the final LoopKey.

## Affected paths

- `internal/core/submit_service.go`
- `internal/app/outbound/submit_envelope_builder.go`
- `internal/core/submittransaction/{model,service}.go`
- `internal/core/smppc/session.go`
- matching unit/integration tests under `internal/`
- `spec/compatibility/{GO_MACRO_TESTS.csv,AMQP_REDIS_MATRIX.md,ROUTING_BILLING_MATRIX.md}`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

## Verification

- Published executable parent `dcdb3313518cdea876cfef5a40a11c4245e63768` (tree `74bcb80d4a10c43542835f1894556015016f972b`) passed exact-SHA GitHub Actions run `29923159045` with `4/4` jobs.
- The final runner-policy correction configures the required `outbound-b` focused/candidate/release modes instead of leaving the declared Task 1.4 gate unavailable. Exact pre-documentation candidate patch: `b8f6a0c5bbdd5d0c3b16bf52a8a31ad4c427a4959ad47e4b72a3341b1ab1a59d`.
- `outbound-b candidate`: `281` tests, `0` skipped, `0` failed; output SHA-256 `ac3281b3d5951573170c270ebdad4519dfa1bfee5a753350e4c028755b03ef65`. Focused affected packages passed `20` times.
- Full `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...` passed. All `58` Python compatibility tests, fixture integrity, JSON Schemas, the `1,039`-test manifest, and the `205`-row registry passed.
- The exact-candidate Ralph code council returned `PASS` with no unresolved High or Medium finding after auditing multipart identity, transactional admission, exact part/result correlation, per-part billing, stale-return filtering, and ambiguous-attempt fencing.
- Matrix totals remain `205 = 129 INVENTORIED + 55 GO-PARTIAL + 18 MATCH + 3 GO-COMPLETE`; no row is promoted because authoritative balances, inbound DLR aggregation, complete float/error parity, connector failover, and exactly-once external SMSC delivery remain outside this slice.

Implementation candidate LoopKey: 2541e45c94af

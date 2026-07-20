# Phase 2.33A — Outbound Production Wiring and CI Recovery

## Goal

Close the audited gap between fixture-backed outbound libraries and an executable Go HTTP → routing/billing → RabbitMQ path, while restoring the exact frozen-fixture CI gate broken by the misplaced RouterPB QoS operation.

## Scope

- Provide a concrete protocol-2 `SubmitSM`/`SubmitSmBill` envelope builder through the allowlisted Python bridge.
- Preserve opaque external user IDs, single-PDU billing, AMQP `reply-to`, priority, headers, and mandatory publisher-confirm behavior.
- Add a fail-fast outbound runtime and `cmd/jasmin-go-httpapi` composition root that owns RabbitMQ, bridge, HTTP dependencies, and the shared late-billing service.
- Add a real RabbitMQ HTTP → submit queue → late-billing integration test.
- Move `prefetch_count=1` to the SMPP connector consumer channel, where the legacy manager applies it, and restore RouterPB's frozen eight-operation subscription fixture.
- Correct HTTP mapping for the real submit service's no-route error.

## Non-goals

- Completing SMPP Session timer/correlation defects still inventoried after Phase 2.33.
- Starting an SMPP connector process from the HTTP executable.
- DLR/MO processing, persistence, TLS, deployment manifests, or full configuration parity.
- Atomic multipart publication/outbox semantics. The production builder rejects multipart before charging or publishing until a durable atomic design lands; generic per-PDU orchestration remains library-only and `A-002` stays `GO-PARTIAL`.
- Claiming full A-010 parity: configurable counts and concurrency effects remain unproven.

## Acceptance criteria

1. A real single-part HTTP `/send` request publishes a protocol-2 `smpp.pdu.operations.SubmitSM` to `submit.sm.<CID>` with opaque UID routing and exact Basic.Properties; multipart fails closed before charging/publishing.
2. The same runtime's late-billing consumer mutates the same in-memory user registry and settles the billing delivery.
3. Mandatory unroutable publication returns `ErrPublishReturned`; routed publication is broker-confirmed.
4. The executable rejects unknown/invalid configuration and shuts down owned resources.
5. `capture_all.sh` regenerates frozen fixtures byte-for-byte; integrity/schema/unit checks pass.
6. Focused live RabbitMQ, full Go, race, vet, build, secret scan, and final Ralph audit pass.
7. The published exact SHA reaches all 4/4 GitHub Actions jobs; local/remote SHA match and workspace is clean before LoopKey.

## Affected paths

- `cmd/jasmin-go-httpapi/`
- `internal/app/outbound/`
- `internal/core/billing/manager.go`
- `internal/core/submit_service.go`
- `internal/transport/amqpcompat/`
- `internal/transport/httpcompat/handler.go`
- `internal/transport/picklecompat/`
- `scripts/pickle_bridge.py`
- `compat/fixtures/router-amqp-subscriptions/baseline.json`
- `spec/compatibility/AMQP_REDIS_MATRIX.md`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

## Verification status

- Frozen oracle tree remained pinned at `0aac58e466d583d0f0436df7b8afa3dc96191263` (`201` files; SHA-256 `8e7c1439068bfbbdef29a1bcc6b2c155db36a8763a1012ded059a05b9e6c87c6`).
- Aggregate fixture regeneration produced the eight-operation RouterPB fixture (`cases_sha256=156f8890326e8871e8901448367845edcbcf0527c46f499290993fe383b9c4b0`) and the verifier accepted `180` coverage rows.
- Production encoder test exercises the real sidecar and asserts protocol-2 `SubmitSM`/`SubmitSmBill` plus opaque UID/bill identity.
- Live RabbitMQ tests cross HTTP → routed submit → exact AMQP properties/body → shared late billing and verify mandatory unroutable handling.
- Full Go, race, vet, build, secret scan, final council, publication, and exact-SHA CI evidence are required before a LoopKey may be appended.

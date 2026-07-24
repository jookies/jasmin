# Phase 2 — DLR thrower projection integration slice

Frozen legacy-oracle baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Go candidate base at slice start: `dc5bea4f7f76c089a49842e8a4dad26a1b0ba665`.
Fixture: `compat/fixtures/amqp/baseline.json` (`dlr_http_thrower`, `dlr_smpps_thrower`).

## Goal

Integrate the externally merged DLR correlation, HTTP callback, SMPPS receipt-builder, Redis-client, and message-ID components behind one typed AMQP thrower projection. The slice must prove that the exact frozen `dlr_thrower.http` and `dlr_thrower.smpps` envelopes become the existing production callback/PDU inputs without test-only translation.

## Scope

- Decode only the two allowlisted DLR thrower routing keys from `amqpcompat.Envelope`.
- Require the exact fixture-backed headers, kinds, message ID/body identity, receipt levels, HTTP method and addressing enums. The existing `amqpcompat.Envelope` boundary enforces the aggregate `64 KiB` header-table and `1 MiB` body limits before projection.
- Convert the SMPPS forward into `SMPPSReceiptParams`, including explicit legacy `AddrTon.*`/`AddrNpi.*` mappings, then build and wire-round-trip a `deliver_sm` receipt.
- Differential tests load the committed AMQP fixture and require both cases with zero skips.
- Promote only the frozen level-3 POST subsets HC-004 and HC-007 to `GO-PARTIAL`; retain HC-003 level-1, HC-005 callback-response, HC-006 retry, and SS-004 production session delivery as `INVENTORIED`.

## Non-goals

- AMQP consumer/reconnect/retry topology for `DLRLookup-*` or `dlr_thrower`.
- Protocol-2 DLR content encoding/publication, confirms, delayed retries, duplicate/reordered receipts, or missing/expired policy closure.
- SMPP server bind/session selection or socket delivery.
- Complete Macro 2 closure or promotion to `MATCH`/`GO-COMPLETE`.

## Acceptance

1. `2 / 2` frozen thrower cases are decoded from the committed fixture with no hardcoded replacement envelope.
2. Wrong routes, missing/wrong-kind headers, body/message-ID mismatch, and invalid levels/methods/enums are rejected; an oversized header table is proven unable to reach projection through `amqpcompat.NewProperties`.
3. HTTP projection preserves all level-3 callback fields.
4. SMPPS projection preserves address/TON/NPI values and produces a valid round-tripped `deliver_sm` receipt.
5. Focused x20, full Go, race, vet, build, fixture verifier, frozen-oracle tree, secret scan, stable candidate audit, exact-SHA CI, clean/ref equality, and LoopKey gates pass before terminal closure.

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

## Executable candidate evidence

- Candidate: `7a5d3d1df03470f22d4f1f2c74bba361af06457c`.
- Local gates: focused golden differential x20, `go test ./...`, DLR race, `go vet ./...`, and `go build ./...` passed.
- Compatibility gates: `195` fixture-coverage rows valid, all JSON schemas valid, and `62` verifier unit tests passed.
- Frozen oracle: `jasmin/` tree `9d513481b80b6fd42998cb2c4c402810145839ee` equals baseline `0aac58e466d583d0f0436df7b8afa3dc96191263`.
- Security: the slice diff passed the redacted gitleaks scan. Existing frozen upstream/documentation findings are outside this slice and were not promoted as new secrets.
- Boundary council: Ralph session `20260723_213530_720dd7` returned `PASS` with no actionable correctness findings for this exact candidate.
- Publication: exact-SHA workflow run `30063205757` completed successfully with `4 / 4` jobs.
- Implementation candidate LoopKey: `887110769788`.

This key binds only the executable candidate and its evidence. The documentation descendant requires its own exact-SHA CI/ref-equality/clean-workspace terminal gate before the slice is considered published.

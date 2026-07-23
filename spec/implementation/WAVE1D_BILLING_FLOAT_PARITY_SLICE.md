# Wave 1D — Exact Binary64 Billing Parity

## Goal

Close the bounded `B-008` residual on the outbound HTTP submit path by preserving Jasmin's unit-first binary64 operation order across authorization, early debit, per-part late billing, and the externally visible late-amount header.

## Scope

- Primary row: `B-008`.
- Regression dependencies only: `B-001`–`B-007`, `A-002`, `A-008`, `A-009`.
- Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
- Starting Go candidate: `55e078ce564b6ba432069509fac9dbe014cbaaa3`.

## Oracle/test harness

1. Extend the frozen billing-enforcement capture at the real `Route.getBillFor → Bill.getTotalAmounts → RouterPB.chargeUserForSubmitSms` boundary with fixed-width binary64 bits for inputs, unit split, required total, early debit, and post-state.
2. Cover previous/equal/next-ULP authorization balances for the `0.01 / 7% / 3 parts` discriminator and an accepted `0.1 / 33% / 3 parts` early-debit discriminator.
3. Extend the frozen late-billing callback capture with previous/equal/next-ULP balance cases and fixed-width bits for parsed amount and post-state.
4. Add a scientific-notation case proving the exact Python-visible late amount text on the production envelope header.
5. Update schemas, fixture verifier registries/hashes/semantic checks, verifier tamper tests, Go differential tests, fixture coverage, and aggregate regeneration compatibility as one contract.

## Production implementation

1. Calculate a unit bill first: `early = rate * percent / 100`, `late = rate - early`.
2. Preserve legacy authorization order: `required = (early + late) * segments`.
3. Preserve legacy mutation order: `early_total = early * segments`; keep every outbound part's embedded bill at unit values.
4. Carry an explicit aggregate authorization amount so submit-time authorization does not reconstruct it from separately multiplied fields.
5. Format the late-amount header with Python-compatible shortest general binary64 text.

## Non-goals

- Authoritative persistent balances (`B-009`).
- Duplicate/reordered event behavior (`B-010`).
- SMPP-server/HTTP parity (`B-011`).
- Exactly-once external SMSC delivery, DLR aggregation, or protocol-specific error mapping.
- Any mutation of the locked legacy source under `jasmin/` or `tests/`.

## Acceptance criteria

1. Frozen fixture regeneration is reproducible and produces exact binary64 bit evidence.
2. Generic Go differential tests fail before the production fix and pass after it with no skips.
3. The real submit path accepts/rejects the ULP boundary exactly like Python, debits exact legacy bits, emits unit per-part bill bits, and uses the exact Python-visible late amount string.
4. Focused tests pass repeatedly; full Go, race, vet, build, Python verifier/schema/unit, fixture regeneration diff, manifest/registry, and secret scan pass.
5. A stable candidate receives an exact-candidate Ralph code boundary audit; supported findings are fixed and invalidated gates rerun.
6. Plan plus implementation are committed and pushed together; exact-SHA GitHub Actions passes `4/4`; local/tracking/remote SHA match and workspace is clean before the orchestrator emits the terminal LoopKey.

## Affected paths

- `scripts/compat/capture_billing_enforcement_golden.py`
- `scripts/compat/capture_late_billing_golden.py`
- `scripts/compat/verify_fixtures.py` and verifier tests
- `compat/fixtures/{billing-enforcement,late-billing}/baseline.json`
- `compat/fixtures/schema/{billing-enforcement,late-billing}-golden.schema.json`
- `internal/core/billing/{billing,enforcement_golden_test,late_charge_test}.go`
- `internal/core/submit_service.go` and tests
- `internal/app/outbound/submit_envelope_builder.go` and tests
- `spec/compatibility/{ROUTING_BILLING_MATRIX.md,FIXTURE_COVERAGE.csv}`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

## Verification

- Frozen oracle: billing-enforcement `12` cases and late-billing `10` cases regenerated twice, byte-matched the committed fixtures, and retained baseline `0aac58e466d583d0f0436df7b8afa3dc96191263`.
- Focused repetition: billing/outbound `x20` and core `x5` PASS; post-audit outbound `x20` plus race `x5` PASS.
- Full local gates: `go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`, `60` Python compatibility tests, fixture/schema/registry/manifest validation PASS.
- Live macro gate: `outbound-a focused` passed `4/4`; stable post-correction `outbound-a candidate` passed `379/379`, zero skips/failures.
- Float-text differential: `5,009` finite binary64 values, including notation thresholds, exactly matched Python 3 `str(float)`.
- Ralph boundary audit found one Medium mismatch for integral visible amounts (`0` versus Python `0.0`). The correction added Python-compatible fixed/scientific notation and boundary tests; the exact-candidate focused final council returned `PASS` with no remaining High/Medium finding.
- Registry after promotion: `205` total = `128 INVENTORIED` + `55 GO-PARTIAL` + `19 MATCH` + `3 GO-COMPLETE`; `183` unfinished.

Publication is pending exact-SHA GitHub Actions and terminal ref/workspace equality; no terminal LoopKey is claimed here.

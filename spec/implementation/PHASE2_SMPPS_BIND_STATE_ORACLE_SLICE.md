# Phase 2 — SMPPS bind-state oracle integration slice

Frozen legacy-oracle baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Go candidate base at slice start: `89765d7d46732ce098e1ed3e28e34b030bda5c4a`.
Primary row: `S-003`.

## Goal

Integrate the externally merged `internal/core/smpps/bindstate.go` helper with a deterministic frozen-oracle contract for Jasmin's server-side inbound-command gate. Prove the Jasmin-specific rejection/delegation boundary through the real `SMPPServerProtocol.PDURequestReceived` and `PDUDataRequestReceived` methods, then keep the row partial until an executable SMPPS server/session composition root applies the gate.

## Scope

- Capture unsupported inbound PDU rejection as `ESME_RSYSERR` through `PDURequestReceived`.
- Capture `submit_sm` and `data_sm` rejection while `BOUND_RX` as `ESME_RINVBNDSTS` through `PDUDataRequestReceived`.
- Capture accepted PDU delegation to the vendored Twisted SMPP server base without claiming the base library's complete state machine.
- Add a schema, trusted corpus fingerprint, verifier negatives, coverage rows, and a generic no-skip Go differential test over every committed case.
- Correct helper behavior only where the frozen capture or exact source chain disproves it.
- Promote `S-003` from `INVENTORIED` to `GO-PARTIAL` only.

## Non-goals

- A production SMPPS socket listener, session lifecycle owner, or transport-to-gate wiring.
- Authentication, IP allowlisting, bind limits, duplicate-session ownership, unbind/disconnect behavior, timers, or delivery selection.
- Complete inherited `python-smpp` state-machine parity or promotion to `MATCH`/`GO-COMPLETE`.
- Integration or matrix promotion of the other externally merged helper packages.

## Acceptance

1. The capture script invokes the frozen Jasmin protocol methods and records exact action/status for every approved case.
2. Aggregate fixture regeneration reproduces the committed bytes, and fixture/schema/verifier integrity rejects unknown IDs and self-consistent forged outcomes.
3. The Go differential harness loads the committed fixture, executes every case with zero skips, and asserts exact allowed/status results for rejection cases plus accepted-command delegation cases.
4. Focused x20, full Go, race, vet, build, Python verifier/schema/unit, frozen-tree, secret-delta, stable-candidate Ralph, publication, exact-SHA `4 / 4` CI, clean/ref equality, and terminal LoopKey gates pass.

## Affected paths

- `scripts/compat/capture_smpps_bind_state_golden.py`
- `scripts/compat/capture_all.sh`
- `scripts/compat/verify_fixtures.py`
- `scripts/compat/test_verify_fixtures.py`
- `scripts/compat/validate_json_schemas.py`
- `compat/fixtures/smpps-bind-state/baseline.json`
- `compat/fixtures/schema/smpps-bind-state-golden.schema.json`
- `internal/core/smpps/bindstate_golden_test.go`
- `spec/compatibility/FIXTURE_COVERAGE.csv`
- `spec/compatibility/SMPP_MATRIX.md`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

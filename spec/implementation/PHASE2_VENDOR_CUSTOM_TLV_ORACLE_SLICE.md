# Phase 2 — vendor custom-TLV oracle integration slice

Frozen legacy-oracle baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Go candidate base at slice start: `67c59541a45249df979813798caf31166fe765b8`.
Primary row: `SP-007`.

## Goal

Integrate the externally merged `internal/core/tlv` helper with a deterministic frozen-oracle contract for Jasmin's pure vendor custom-TLV pipeline. Prove tag parsing, connector type resolution, exact value/wire encoding, and required/max-length validation against `jasmin/tools/tlv_encoder.py`, while keeping the row partial until concrete SMPPc/SMPPs production paths apply the rules and preserve vendor TLVs end to end.

## Scope

- Capture exact outputs and errors from `_parse_tag_key`, `resolve_tlv_types`, `encode_tlv_value`, `encode_custom_tlvs`, and `validate_custom_tlvs`.
- Cover decimal/hex tags, explicit and connector-resolved types, unknown-tag fallback, Int1/2/4/8 boundaries, OctetString/COctetString, raw bytes, required tags, encoded max-length boundaries, duplicate-tag last-wins behavior, and malformed/overflow errors.
- Add a schema, trusted corpus fingerprint, verifier negatives, fixture coverage, and one generic no-skip Go differential harness over every committed case.
- Correct the helper only where the frozen capture disproves its behavior.
- Promote `SP-007` from `INVENTORIED` to `GO-PARTIAL` only.

## Non-goals

- HTTP/REST request-shape normalization beyond the typed tuple boundary.
- Connector config parsing, persistence, or dynamic updates.
- Production injection into SMPPc session encoding, inbound decoder patches, MO/DLR forwarding, or an executable SMPPS server.
- Full PDU round-trip parity, unknown vendor-tag policy, or promotion to `MATCH`/`GO-COMPLETE`.
- Changes to the frozen Python oracle under `jasmin/`.

## Acceptance

1. The capture script invokes only frozen `jasmin.tools.tlv_encoder` functions and records deterministic JSON-safe inputs, outputs, exact encoded hex, and normalized exception classes/messages.
2. Aggregate fixture regeneration reproduces committed bytes; schema/verifier integrity rejects unknown IDs and self-consistent forged outputs.
3. The Go differential harness executes every fixture case with zero skips and asserts exact structured results, bytes, or error class semantics.
4. Focused x20, full Go, race, vet, build, Python verifier/schema/unit, frozen-tree, secret-delta, stable-candidate Ralph, publication, exact-SHA `4 / 4` CI, clean/ref equality, and terminal LoopKey gates pass.

## Affected paths

- `scripts/compat/capture_vendor_tlv_golden.py`
- `scripts/compat/capture_all.sh`
- `scripts/compat/verify_fixtures.py`
- `scripts/compat/test_verify_fixtures.py`
- `scripts/compat/validate_json_schemas.py`
- `compat/fixtures/vendor-tlv/baseline.json`
- `compat/fixtures/schema/vendor-tlv-golden.schema.json`
- `internal/core/tlv/tlv.go`
- `internal/core/tlv/tlv_golden_test.go`
- `spec/compatibility/FIXTURE_COVERAGE.csv`
- `spec/compatibility/SMPP_MATRIX.md`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

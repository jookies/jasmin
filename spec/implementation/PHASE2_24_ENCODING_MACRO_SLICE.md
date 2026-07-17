# Phase 2.24 — Macro 1.2: Advanced Segmentation & Encoding

## Goal

Achieve full parity for SMS encoding and segmentation (Multipart) logic. This includes GSM 03.38 septet packing, UCS2 handling, and both SAR (TLV) and UDH (header) concatenation mechanisms. This macro-slice ensures that Go-generated PDUs for long messages are byte-identical to Python's for all data codings.

## Included

- **HE-001 / SE-001: GSM 03.38 septet parity.** Extension table handling, escaping, and 160-char / 153-char boundaries.
- **HE-002 / SE-002: UCS2 fidelity.** 16-bit encoding, surrogate pair safety, and 70-unit / 67-unit boundaries.
- **HE-003 / SE-003: Binary / Hex.** Byte-exact payload preservation and Data Coding Scheme (DCS) mapping.
- **HE-004 / SE-004: SAR (Segmentation and Reassembly).** TLV-based concatenation (ref, total, seq).
- **HE-005 / SE-005: UDH (User Data Header).** Header-based concatenation (IEI 00 or 08), reference rollover, and charge count.
- **HE-006: Custom TLVs.** Tag/Value fidelity for optional parameters passed from HTTP.

## Non-goals

- Live SMPP session management.
- Live RabbitMQ routing (handled in Macro 1.3).
- Advanced reassembly (only segmentation is covered here).

## Acceptance criteria

1. **Oracle Capture:** Capture complex multi-part fixtures for all codings, including UDH vs SAR selection logic.
2. **Boundary Testing:** Exact byte-for-byte match at segment limits (160/153, 140/134, 70/67).
3. **Reference Determinism:** Go must match Python's concatenation reference generation or accept it from the oracle.
4. **Fuzzing:** Fuzz input strings to ensure no panics during segmentation or encoding.
5. **Ralph Audit:** Verify bitwise septet packing and UDH byte ordering.

## Affected paths

- `internal/core/segmentation/`
- `scripts/compat/capture_segmentation_golden.py` (updating/expanding)
- `compat/fixtures/segmentation/baseline.json`
- `internal/core/segmentation/golden_test.go`
- `spec/compatibility/HTTP_MATRIX.md`
- `spec/compatibility/SMPP_MATRIX.md`

LoopKey: 692f007b87a3

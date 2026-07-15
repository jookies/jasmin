# Phase 2.5 — byte payload segmentation compatibility slice

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Oracle: `jasmin.protocols.smpp.operations.SMPPOperationFactory.SubmitSM`.

## Decision

Implement the frozen Jasmin byte-payload segmentation boundary before any text transcoding layer.

This slice accepts already encoded bytes plus SMPP `data_coding`, split method, maximum part count and a caller-supplied reference number. It classifies the DCS exactly as the frozen Python factory, slices the payload, and projects either SAR metadata or a six-byte concatenation UDH.

## Fixture scope

The committed corpus contains 14 deterministic cases:

1. DCS 0, 160 bytes, single part.
2. DCS 0, 161 bytes, SAR.
3. DCS 0, 161 bytes, UDH.
4. invalid DCS 255 using the legacy 7-bit fallback.
5. concatenation reference rollover from 255 to 1.
6. `long_content_max_parts` truncating the payload after two parts.
7. DCS 3, 140 bytes, single part.
8. DCS 3, 141 bytes, SAR.
9. DCS 3, 141 bytes, UDH.
10. binary DCS 4 using the legacy 16-bit class and 134-byte multipart slices.
11. DCS 8, 70 two-byte units, single part.
12. DCS 8, 71 units, SAR.
13. DCS 8, 71 units, UDH.
14. DCS 8 with an odd payload byte count, preserving the frozen oracle's final odd byte.

Every case records input bytes and SHA-256, DCS classification, exact part payload bytes, exact emitted `short_message` bytes, SAR values, UDH bytes, and whether the configured part cap dropped an input suffix.

## Exact legacy rules

- DCS `3, 6, 7, 10`: 8-bit class, single limit `140` bytes, multipart slice `134` bytes.
- DCS `2, 4, 5, 8, 9, 13, 14`: legacy 16-bit class, single limit `70` two-byte units, multipart slice `134` bytes (`67` units).
- DCS `0, 1` and every other value: legacy 7-bit class, single limit `160` bytes, multipart slice `153` bytes.
- A 16-bit-class payload length is measured as `len(payload) / 2`; odd lengths are accepted.
- Multipart part count is `ceil(logical_length / sliced_limit)`, capped by `long_content_max_parts`. The frozen implementation silently drops the remaining suffix after the cap.
- Concatenation references are 8-bit and cycle `1..255`; zero is not emitted.
- SAR leaves payload bytes untouched and adds reference/total/sequence metadata.
- UDH prefixes every slice with `05 00 03 <ref> <total> <sequence>` and marks the message UDHI.

## Production API

Package: `internal/core/segmentation`.

- Immutable request/result/part values.
- `Segment(Request) (Result, error)` has no global reference counter; the caller supplies a reference in `1..255` when multipart output is possible.
- Defensive copies on input and every byte-slice accessor.
- Hard payload cap `1 MiB`, part cap `255`, and stable errors for invalid split method, reference, part cap and oversized payload.
- Result exposes DCS class, exact emitted part bytes, consumed input byte count and truncation state.

## Non-goals

- Unicode text to GSM 03.38/UCS2 encoding or decoding.
- GSM extension-table septet counting/packing.
- HTTP hex parsing and text validation.
- SMPP PDU encoding, network sessions, billing or routing.
- Multipart reassembly and Redis lifecycle.
- Custom TLVs.
- Removing legacy truncation; policy hardening belongs above this compatibility boundary.

## Differential acceptance

- The capture is generated only inside the frozen baseline container and is byte-for-byte reproducible.
- All 14 fixture IDs are required and no unknown ID can be silently skipped.
- Go output matches every recorded classification, payload chunk, emitted `short_message`, SAR value, UDH byte, consumed byte count and truncation flag.
- Concatenating raw part payloads equals the consumed input prefix; for non-truncated cases it equals the entire input.
- Input and accessor mutation cannot change a stored result.
- Focused rejection tests and fuzzing must not panic or allocate from payloads beyond the cap.

## Publication gate

- Fixture verifier and JSON schema pass.
- `go test -count=20` and `go test -race` pass.
- At least 10 seconds of fuzzing pass.
- Full Go, fixture-integrity and frozen Python regression gates pass.
- Ralph audit reports no unresolved HIGH/CRITICAL finding for the stable candidate.
- GitHub Actions is green for the pushed commit.

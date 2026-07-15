# Phase 2.9 — deterministic route billing

Baseline: `4a7a2bcbaa5bce053f959d28992cae9bfa6f241c`.

## Included
- B-001: Rate calculation for rated/unrated routes and unit rate visibility.
- B-003: Unlimited balance support (`None`/`ND` in Jasmin).
- B-006: Early decrement logic (charge before dispatch).
- B-007: Late decrement logic (charge remainder after successful submission).
- Quota/Balance internal state tracking for users/groups.
- Integration of billing results with routing table selection.

## Excluded
- B-002: Multipart billing (deferred to segmentation phase).
- B-004/B-005: Enforcement/blocking (only tracking for now).
- B-008: Float rounding (corpus-based verification deferred).
- B-009: Persistence/Timer (in-memory only).
- B-010: Redelivery/Duplicates.
- B-011: Parity with HTTP/SMPP protocols.

## Acceptance
Frozen fixtures and no-skip Go replay; reproducible corpus; schema/integrity/coverage; x20/full/race/vet/build/fuzz; frozen regression; Ralph; exact remote SHA and CI 4/4.

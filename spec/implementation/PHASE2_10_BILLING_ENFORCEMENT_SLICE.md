# Phase 2.10 — Billing enforcement and multipart integration

Baseline: `caf6a4014bd64fb075ae19fb097e5bae2105aa35`.

## Included
- B-002: Multipart billing (charge per segment determined by segmentation core).
- B-004: Insufficient balance enforcement (blocking logic in core).
- B-005: Insufficient count enforcement (blocking logic in core).
- Integration: Bridge `internal/core/segmentation` and `internal/core/billing`.
- Validation: Explicit `CanApply(Bill) error` in `User` and `Group`.

## Excluded
- RI-001 to RI-006: Interceptors (deferred to Phase 2.11).
- B-009: Persistence/Timer (deferred).
- B-010: Redelivery/Duplicates.
- B-011: Parity with HTTP/SMPP protocols (integration at transport layer).

## Acceptance
Frozen fixtures and no-skip Go replay; reproducible corpus; schema/integrity/coverage; x20/full/race/vet/build/fuzz; frozen regression; Ralph; exact remote SHA and CI 4/4.

## Production API

### package `internal/core/billing`

- `User.CanApply(Bill) error`
- `Group.CanApply(Bill) error`
- `CalculateBill(routeRate float64, segments int, u *User) Bill`

### Errors
- `ErrInsufficientBalance`
- `ErrInsufficientCount`

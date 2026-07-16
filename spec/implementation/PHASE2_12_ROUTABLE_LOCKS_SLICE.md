# Phase 2.12 — Routable locking and BestQualityMTRoute stub

Baseline: `2d012f7c85a538259c45d545bc1eb42d1d3de66e`.

## Included
- RT-003: Routable field locking (preventing mutation of specific fields).
- RR-010: `BestQualityMTRoute` stub (matches legacy unimplemented state).
- Interceptor integration: Interceptors must honor locked fields during mutation.

## Excluded
- Persistence (JCLI save/load).
- Persistence timer (B-009).
- Redelivery/Duplicates (B-010).

## Production API

### package `internal/core/routingfilter`
- `Routable.Lock(field string)`
- `Routable.IsLocked(field string) bool`
- `Routable.Unlock(field string)` (if needed for compatibility)

### package `internal/core/routingtable`
- `type BestQualityMTRoute struct { ... }` (stub)

## Tasks
1. [x] Implement locking logic in `internal/core/routingfilter/filter.go`.
2. [x] Update `Set*` and `AddTag/RemoveTag` methods in `Routable` to respect locks.
3. [x] Implement `BestQualityMTRoute` stub in `internal/core/routepolicy/policy.go`.
4. [x] Create fixtures for locked field mutation rejection in `compat/fixtures/interceptor/baseline.json`.
5. [x] Update `internal/core/interceptor/runner_python.go` to respect locks.
6. [x] Verification and Ralph audit.

LoopKey: 37a0e1bc787f

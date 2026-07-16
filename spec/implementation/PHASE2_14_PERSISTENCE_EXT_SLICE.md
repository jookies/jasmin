# Phase 2.14 — Persistence of Routes and Interceptors

This phase implements the SQLite persistence for MO/MT Routing tables and Interceptors.

## Included
- P-004: Persistence of MO Routes (SQLite).
- P-005: Persistence of MT Routes (SQLite).
- P-006: Persistence of MO Interceptors (SQLite).
- P-007: Persistence of MT Interceptors (SQLite).
- P-001: Persistence of Connector configurations (basic metadata).

## Excluded
- JCLI interactive commands for persistence management (deferred to Phase 3).
- Legacy Pickle migration (P-008).

## Production API

### package `internal/core/routingtable`
- `type RouteRepository interface { Save(ctx context.Context, order int, r Route) error; LoadAll(ctx context.Context) (map[int]Route, error); Delete(ctx context.Context, order int) error; }`
- `type RouteState struct { ... }` (State Projection)

### package `internal/core/interceptor`
- `type InterceptorRepository interface { Save(ctx context.Context, order int, i Interceptor) error; LoadAll(ctx context.Context) (map[int]Interceptor, error); Delete(ctx context.Context, order int) error; }`
- `type InterceptorState struct { ... }` (State Projection)

### package `internal/infra/storage`
- `type SQLiteRouteRepository struct { ... }`
- `type SQLiteInterceptorRepository struct { ... }`

## Tasks
1. [ ] Define `RouteState` and `FilterState` for persistence.
2. [ ] Define `InterceptorState` and `ScriptState`.
3. [ ] Define repository interfaces.
4. [ ] Implement SQLite schema for MO/MT Routes and Filters.
5. [ ] Implement SQLite schema for Interceptors and Scripts.
6. [ ] Implement `SQLiteRouteRepository` and `SQLiteInterceptorRepository`.
7. [ ] Verification and Ralph audit.

## Verification
- `TestSQLiteRoutePersistence`
- `TestSQLiteInterceptorPersistence`
- Round-trip validation with all filter types.

# Phase 2.11 — Interceptors and sidecar execution

Baseline: `05e193ef974514299b042b4742a781b0a701467a`.

## Included
- RI-001: Interceptor table order (descending, first match).
- RI-002: Script environment (globals: `routable`, `smpp_status`, `http_status`).
- RI-003: Mutation support (PDU fields, tags).
- RI-004: Rejection (status overrides from script).
- RI-005: Failure handling (timeout, syntax, runtime).
- RI-006: Sidecar runner (calling legacy Python for `EvalPy`).

## Excluded
- Persistence (JCLI save/load).
- Advanced interceptor types (if any beyond MO/MT/Static).
- PB integration (real network PB client).

## Production API

### package `internal/core/interceptor`

- `type Table struct { ... }`
- `type Interceptor struct { ... }`
- `type Script struct { ... }`
- `type Runner interface { Run(script Script, context Context) (Result, error) }`
- `type Result struct { Routable routingfilter.Routable; SMPPStatus int; HTTPStatus int; Action Action }`

## Tasks
1. [x] Create `internal/core/interceptor` package and basic structures.
2. [x] Define `InterceptorTable` with matching logic.
3. [x] Implement a Mock Runner for initial testing.
4. [x] Create fixtures for Interceptors (RI-001 to RI-004).
5. [x] Capture Python oracle for Interceptor behavior.
6. [x] Implement a basic Python Sidecar Runner (using `os/exec` or similar).
7. [x] Verification and Ralph audit.

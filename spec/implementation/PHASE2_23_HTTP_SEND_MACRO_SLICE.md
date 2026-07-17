# Phase 2.23 — Macro 1.1: HTTP /send Full Parity

## Goal

Achieve functional and byte-exact parity for the legacy HTTP `/send` endpoint, encompassing all optional parameters, value filters, and quota checks. This slice consolidates multiple micro-rows to accelerate outbound MT flow readiness.

## Included

- **H-003: Optional fields.** Support for `from`, `coding`, `priority`, `sdt`, `validity-period`, `dlr`, `dlr-url`, `dlr-level`, `dlr-method`, `tags`, and `tlv-*`.
- **H-005: Value filters.** Enforce user/group level regex filters for source address, destination address, and content.
- **H-006: Route/Interceptor Logic.** Implement routing selection and interceptor hooks (logic stubs).
- **H-007: Quotas.** Perform synchronous checks for user balance and `submit_sm_count` before accepting the request.
- **H-008: Success format.** Ensure the exact `Success "<uuid>"` body and response headers.
- **Finishing H-001, H-002, H-004, H-009.** Move these from `GO-PARTIAL` to `MATCH`.

## Non-goals

- Live AMQP publication (handled by `A-*` rows).
- Real SMPP connector delivery.
- Asynchronous DLR delivery (handled by Macro 2).

## Acceptance criteria

1. **Oracle Capture:** Capture fixtures covering every optional parameter, filter hit/miss, and quota exhaustion.
2. **Differential Tests:** Go handler must produce identical HTTP status, headers, and body for all captured cases.
3. **Regex Parity:** Go regex engine must match Python `re` behavior for the configured filters.
4. **Quota Integrity:** Verify that a request is rejected with the correct legacy error if balance is insufficient.
5. **Ralph Audit:** Multi-model review of the validation and mapping logic.

## Affected paths

- `internal/transport/httpcompat/handler.go`
- `internal/core/http.go` (updating interfaces/structs)
- `scripts/compat/capture_http_golden.py`
- `compat/fixtures/http/baseline.json`
- `internal/transport/httpcompat/golden_test.go`
- `spec/compatibility/HTTP_MATRIX.md`
- `spec/compatibility/FIXTURE_COVERAGE.csv`

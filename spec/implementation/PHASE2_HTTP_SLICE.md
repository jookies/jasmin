# Phase 2: Legacy HTTP Compatibility Slice

## Goal

Implement the first production Go vertical slice for the contract-first Jasmin rewrite: legacy HTTP ingress behavior covered by all nine committed HTTP golden fixtures, without importing or executing Python.

## Scope

- `GET /ping` exact status/body/header contract.
- `GET /rate` authentication and exact legacy JSON rendering.
- `GET /balance` authentication and exact `ND` quota rendering.
- `POST /send` form and JSON input normalization.
- `/send` mandatory-field validation, authentication failure, and no-live-connector failure.
- Side effects behind typed Go ports.

Not in this slice: successful SMPP submission, routing, billing mutation, Redis persistence, AMQP consumption, DLR/MO callbacks, metrics, or complete HTTP matrix parity.

## Architecture

- `internal/core`: transport-independent errors, values, and ports.
- `internal/transport/httpcompat`: legacy presentation adapter and exact compatibility formatting.
- The HTTP adapter owns legacy status/body/content-type quirks; domain ports do not.
- Differential tests load `compat/fixtures/http/baseline.json` and exercise the real `http.Handler` with fixture-specific deterministic port fakes matching the oracle setup.

## TDD Tasks

1. Create `go.mod` and a differential test that fails because the Go adapter does not exist.
2. Add minimal domain values/errors/ports in `internal/core/http.go`.
3. Implement input normalization and exact response rendering in `internal/transport/httpcompat/handler.go`.
4. Make all nine golden cases pass without fixture-ID branches in production code.
5. Add focused unit tests for validation order, method handling, port call boundaries, and response-header suppression.
6. Run `gofmt`, `go test ./...`, `go test -race ./...`, `go vet ./...`, fixture verification, and `git diff --check`.
7. Run a stable-tree Ralph code audit, resolve reproducible findings, rerun affected gates, then commit/push and verify GitHub Actions.

## Acceptance

- `9 / 9` HTTP fixtures match status, body bytes, and non-empty headers exactly.
- No production code reads fixture IDs or fixture files.
- No Python runtime, pickle, Twisted, Redis, RabbitMQ, or network dependency in Go tests.
- Missing mandatory arguments are rejected before authentication or submit ports are called.
- Authentication and upstream failures map to the exact legacy body/content-type differences per endpoint.
- All local and remote gates pass with a final LoopKey.

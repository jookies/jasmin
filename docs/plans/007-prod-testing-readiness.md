# Production-testing readiness — stand up + shadow-test the outbound MT path

- **Date:** 2026-07-26
- **Status:** done — P0 landed 2026-07-26 (Step 1 #66, Step 2 #68, Steps 3–4 #69; simulator-verified bind + `ESME_ROK`, real-SMSC creds are a deployment-copy action) with en-route fix #67 (silent submit drop on empty byte params — `__builtin__.bytes` allowlist). P1 landed the same day: Step 5 #70 (opt-in `amqp_durable_topology`, broker-restart drill 10/10), Step 6 #71 (`/health`: postgres/amqp/bridge/required-connector checks), Step 7 #72 (inbound HTTPS + SMPPS-over-TLS + `env:`/`file:` secret refs), plus #73 (terminal-reject log lines — the observability gap behind #67). Remaining scope is Macro 2/3 + the formal cutover gate (out of scope here).
- **Summary:** Close the deployability + operational-safety gaps that block running the Go gateway in a prod-like environment and pointing real (or shadow) traffic at its **outbound MT path** — the "legacy-publisher → Go-smppc" bridge edge that the strangler plan cuts over first. Scope is P0 (run it at all) + P1 (shadow safely). Inbound MO, terminal DLR, and filter routing are **out of scope** here (Macro 2/3 — see §Out of scope).
- **Related:** the prod-readiness assessment (memory `prod-testing-readiness`); `internal/app/gateway/runtime.go`, `internal/app/gateway/config.go`, `internal/app/outbound/config.go`, `internal/core/smppc/config.go`; ADR-0001 (leaf-worker-first strangler); `.hermes/plans/…-completion-roadmap.md` (the formal cutover gate, a separate higher bar).

## Context

The outbound MT flow already runs end-to-end and is integration-proven (`TestGatewayHTTPToDurableSMPPResponse`): HTTP `/send` or SMPPs bind → routed → durable Postgres outbox → real `submit_sm` to the SMSC → response, with billing, quota and throughput pacing. What's missing is everything around **deploying and safely operating** it: there is no committed runtime config, no Go image, no orchestration manifest wiring the infra, the AMQP topology is non-durable, there's no real health probe, and inbound listeners + on-disk secrets are plaintext. The gateway also has a hard runtime dependency on a Python subprocess (`scripts/pickle_bridge.py`) that any image must carry.

Desired outcome: a reproducible, documented way to boot the Go gateway against Postgres + RabbitMQ (+ Redis for DLR), configured for one real SMSC connector, safe enough to shadow real traffic and observe parity.

## Out of scope (separate tracks)

- **Inbound MO**, **terminal DLR (L2/L3)**, **filter-based routing** — these are Macro-2/3 feature work (`deliver_sm` ingestion + MO router + `RouteConfig` filter fields), not deployment blockers. Testing here is limited to the outbound direction + level-1 DLR.
- The formal **Core-Cutover gate** (cutover manifest all-MATCH, shadow→canary→rollback runbooks, durable-Postgres control state) — the project's own ~12–18-week bar, tracked in the completion roadmap.
- **Multipart billing** (per-part vs per-message) — an open parity question to resolve before billing claims, flagged in `docs/plans/002`.

## Steps

### P0 — make it deployable (run at all)

**Step 1 — Commit a reference runtime `--config` JSON.**
- **Files:** `configs/gateway.example.json` (new); a short `configs/README.md` note. *(Moved out of the originally-planned `misc/config/` — that whole directory is inside the frozen oracle-tree fingerprint of `scripts/compat/verify_baseline_tree.py`; adding files there fails `fixture-integrity`/`fixture-reproducibility`/`frozen-python-regression`.)*
- **Changes:** a complete, working example: `role:"http+smppc"`, `outbound{ listen_address, amqp_url, postgres_dsn, python_path, users[]( system_id/password_sha256/balance/quota ), routes[]( connector_ids/order/default/rate ) }`, `connectors[]( a fully-specified SMPPClientConfig — host/port/system_id/password/bind type/TON-NPI/submit_sm_throughput/tls )`, and optional `dlr_lookup{redis_url,pid}` + `dlr_thrower` blocks. Derive field names from `internal/app/gateway/config.go`, `internal/app/outbound/config.go`, `internal/core/smppc/config.go` (do not invent keys — `LoadConfig` uses `DisallowUnknownFields`).
- **Verify:** `go run ./cmd/jasmin-go-httpapi --config misc/config/gateway.example.json --check-config` prints `configuration: ok`.

**Step 2 — Go image bundling the Python bridge.**
- **Files:** `docker/Dockerfile.gateway` (new).
- **Changes:** multi-stage — stage 1 `golang:1.26` builds `cmd/jasmin-go-httpapi`; stage 2 `python:3.12-slim` installs the bridge deps (`smpp.pdu3`, `Twisted`, `txredisapi` per `requirements.txt`) + the `jasmin` package, copies the Go binary and `scripts/pickle_bridge.py`, sets a `WORKDIR` from which the bridge script is discoverable (it walks up from cwd — `picklecompat/bridge.go`), and `ENTRYPOINT` the binary. **Gotcha:** the bridge is on the hot path — without importable `smpp.pdu`/`jasmin`, every submit fails at encode.
- **Verify:** `docker build -f docker/Dockerfile.gateway .` succeeds; the container `--check-config`s the example; a manual boot logs the bridge starting.

**Step 3 — Orchestration manifest with the full infra.**
- **Files:** `docker-compose.gateway.yml` (new) — do not touch the Python `docker-compose.yml`.
- **Changes:** services `postgres:16`, `rabbitmq:3`, `redis:7`, and `gateway` (built from Step 2) with the example config mounted, `depends_on` healthchecks. Postgres schema auto-migrates and AMQP topology auto-declares on boot, so no init scripts needed.
- **Verify:** `docker compose -f docker-compose.gateway.yml up` reaches a running gateway; `POST /send` against a **fake SMSC** container reproduces the integration-test flow end-to-end.

**Step 4 — One real bound SMSC connector + TON/NPI.**
- **Files:** the example config's `connectors[0]` (Step 1).
- **Changes:** real SMSC host/creds; set `source_addr_ton/npi` + `dest_addr_ton/npi` explicitly (defaults resolve, but strict SMSCs reject `dest_addr_ton=UNKNOWN(0)` — the full front-door byte-differential is still deferred, `docs/plans/003`).
- **Verify:** connector reaches `StatusBound` against the SMSC (or a real SMPP simulator); a test submit returns `ESME_ROK`.

### P1 — shadow safely (before real traffic)

**Step 5 — Durable AMQP topology.**
- **Files:** `internal/transport/amqpcompat/topology.go` (exchange/queue `durable=false` → `true`); check every `QueueDeclare`/`ExchangeDeclare` (submit queues, DLRLookup queue, billing) + publish `DeliveryMode`.
- **Changes:** declare durable + publish persistent, so a broker restart doesn't drop queued submits. **Gotcha:** durability must be consistent with the legacy topology if the two share a broker during shadow — verify the legacy declares the same, or use a separate vhost.
- **Verify:** a test that enqueues, restarts the broker container, and confirms the message survives + drains.

**Step 6 — Real health/readiness endpoint.**
- **Files:** `internal/transport/httpcompat/handler.go` (+ the runtime exposing dependency state).
- **Changes:** a `/health` (or `/ready`) that checks Postgres ping, AMQP channel, the Python bridge liveness, and required-connector bind state — distinct from the unconditional `/ping`.
- **Verify:** endpoint returns 503 when a dependency is down (kill Redis/PG in compose), 200 when healthy.

**Step 7 — Inbound TLS + secrets injection.**
- **Files:** `cmd/jasmin-go-httpapi/main.go` (HTTP `ListenAndServeTLS` behind a config flag), `internal/app/smppsserver/service.go` (optional TLS listener); config plumbing for cert paths; docs for env/file secret injection.
- **Changes:** either terminate inbound TLS in-process or document a required TLS proxy; stop requiring plaintext creds in the committed config (support env/file references for `postgres_dsn`/`amqp_url`/SMSC passwords).
- **Verify:** HTTPS `/send` works; SMPPs-over-TLS bind works; a config with secret-refs resolves.

## End-to-end verification

`--check-config` on the example; `docker compose -f docker-compose.gateway.yml up` boots clean; the existing `TestGatewayHTTPToDurableSMPPResponse` passes against the composed infra; a manual submit to a real/simulated SMSC returns `ESME_ROK`; broker-restart durability test green. Full `go build/vet ./...` + `go test ./...` clean per change.

## Rollback

Every P0 artifact is additive (new files: example config, Dockerfile, compose) — deleting them reverts to today's state. P1 changes are behind config flags (TLS) or a one-line durability constant + a new endpoint; each is independently revertible. The legacy Python stack remains the rollback target throughout.

## Risks

- **Python-bridge coupling** — the image + runtime must keep `python3` + `jasmin`/`smpp.pdu` + `scripts/pickle_bridge.py` healthy; a bridge crash fails all submits. Monitor its liveness (Step 6).
- **Front-door byte differential** — Go-front-door submits still differ from Jasmin on a few bytes (`docs/plans/003`); a strict SMSC may reject them. Prefer the bridge edge (legacy publisher builds the PDU) for the first shadow, and set TON/NPI explicitly.
- **Non-durable → durable topology** — changing declaration args on an existing broker can error if the queue already exists with different args; plan a clean vhost or a redeclare migration.
- **Shared-broker shadow** — if the Go gateway shares RabbitMQ/Redis/Postgres with the live Python stack, isolate (separate vhost / key prefixes / DB) to avoid cross-contamination.

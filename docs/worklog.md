# Worklog

<!-- Newest entries on top. One entry per significant working session. -->

## 2026-07-26 — docs/plans/007 P0: outbound MT path deployable (4 PRs) + silent-submit-drop fix

### Done

- **#66** — Step 1: reference runtime config `configs/gateway.example.json` (`role: http+smppc`, full outbound block, one fully-specified SMPPClientConfig connector with explicit TON/NPI src 2/1 dst 1/1, `required_connector_ids`, inherited-broker `dlr_lookup`/`dlr_thrower`, `submit_audit_log`) + `configs/README.md`. Verified `--check-config` → `configuration: ok`.
- **#68** — Step 2: `docker/Dockerfile.gateway` — golang:1.26 build stage; python:3.12-slim runtime with the hash-pinned bridge deps (`compat/requirements-pickle-bridge.txt`) + `scripts/pickle_bridge.py` + `jasmin/` source under `WORKDIR /app` (bridge walks up from cwd), baked example config, non-root. In-container `--check-config` + bridge imports verified.
- **#69** — Steps 3–4: `docker-compose.gateway.yml` (postgres:16 + rabbitmq:3-management-alpine + redis:7-alpine + gateway, healthcheck-gated) + hermetic `cmd/jasmin-fake-smsc` / `docker/Dockerfile.fakesmsc` (scratch image on `internal/transport/smppwire`; accepts any bind, ESME_ROKs submits). **E2E proven on the live stack:** bind, `/send` with AND without `from` → `ESME_ROK` at 1 msg/s pacing, SMS-MT audit lines, `submit_attempts` `RESULT_COMMITTED` + `submit_results` `SUCCESS/ESME_ROK` in Postgres.
- **#67** — bug found via that E2E: submits with any EMPTY byte param (e.g. `/send` without `from=`) were **silently dropped** — CPython pickles `b''` at protocol 2 as a `__builtin__.bytes` GLOBAL (non-empty uses `_codecs.encode`), `SubmitSMUnpickler` didn't allowlist `bytes` (DeliverSM's did) → `ErrSubmitSMPoison` → reject-no-requeue, no attempt row, no log. One-line allowlist fix + `TestDecodeSubmitSMEmptyBytes` differential (red on old, green on new; oracle needs only pinned smpp-pdu3).
- `docs/plans/007` → status **active**, P0 marked done; step-1 path note (misc/config → configs).

### Decisions

- **`configs/`, not the planned `misc/config/`** — `misc/config/` is inside the frozen-oracle tree fingerprint (`scripts/compat/verify_baseline_tree.py`: `jasmin/`, `tests/`, `misc/config/`); adding files there broke 3 CI checks (`oracle_tree=changed files=203/201`). Any new Go-side config artifacts must stay out of those trees.
- **Pinned bridge runtime, not full requirements.txt, in the image** — bridge-reachable jasmin modules (`routing.Bills`, `routing.jasminApi`, `routing.Routables`) are stdlib/smpp.pdu-only; CI proves pinned `smpp-pdu3` suffices. Twisted/txredisapi/falcon are legacy-daemon surface; smaller image + supply chain.
- **Own fake SMSC instead of an external simulator image** — the planned `melroselabs/smscsim` doesn't exist on Docker Hub; `cmd/jasmin-fake-smsc` reuses `smppwire` and mirrors the integration test's contract, keeping the stack hermetic.
- **Step 4 "real SMSC creds" = ops action** on a deployment copy of the config; the repo keeps simulator-wired placeholders (secrets injection is P1 Step 7). Simulator-verified bind satisfies the plan's "or a real SMPP simulator" arm.
- Merge cadence held: the 3 fast CI checks gate merges; `frozen-python-regression` (scripts/ change in #67 runs it) left to complete post-merge.

### Next

- **P1 (docs/plans/007 Steps 5–7):** durable AMQP topology (`amqpcompat/topology.go` `durable=false` → true + persistent publishes; broker-restart survival test); real `/health` readiness (PG ping, AMQP channel, bridge liveness, required-connector bind state); inbound TLS + secrets injection (env/file refs for DSN/AMQP/SMSC passwords).
- **Logging follow-up worth pulling forward:** the #67 drop was invisible because submit-lifecycle reject lines aren't implemented yet (O-007 backlog) — a poison-reject should log.
- To point the stack at a real SMSC: copy `configs/gateway.example.json`, set connector host/port/system_id/password + TON/NPI per the SMSC, raise `submit_sm_throughput`; validate with `--check-config`.
- Local repro: `docker compose -f docker-compose.gateway.yml up --build`; send via `curl 'http://localhost:1401/send?username=smppuser&password=password&to=15551234567&content=hi'`.

## 2026-07-26 — O-007 logging rollout + production-testing readiness assessment

### Done

**O-007 logging — 11 PRs merged (#54–#64), all differential-verified against real `jasmin.*` / `smpp.pdu`:**

- **#54** — logging foundation: `internal/core/logging` `slog` handler rendering Jasmin's exact line (`%(asctime)s %(levelname)-8s %(process)d %(message)s`, Python level names, `Redact`).
- **#55** — config `log_*` parsing for the message-path sections (`log_file/rotate/level/format/date_format`) + `LOG_PATH` resolution (double-slash + K8s quirks). New `internal/config/log_config.go`.
- **#56–#59, #61, #62** — the **SMS-MT audit line**, byte-complete: renderer (`internal/core/smppc/mtlog.go`), `smpp-msgid` bytes-repr fix (#57), session emission on final `submit_sm_resp` (#58), runtime activation (#59), populated `tlvs` (#61), multipart reassembly SAR/UDH (#62). Routes to `messages.log`.
- **#60** — `TimedRotatingFileHandler`-compatible file sink (`internal/core/logging/rotating.go`): `midnight`/`W0–W6`, dated suffix, never-deletes; differential-verified across both 2026 US DST transitions.
- **#63** — `#N`-free submit-lifecycle lines: expired-discard (connector readiness) + submit-timeout (`handleTimeout`), byte-exact.
- **#64** — first breadth component: SMPPs `smpp.server` bind/unbind audit lines (`internal/core/smpps/bindlog.go`) + the reusable breadth wiring pattern (`WithLogger` options + gateway `ComponentLogConfig` overlaid from the section's `.Log`).

**Production-testing readiness analysis** (three parallel code surveys — runtime completeness, deployability, spec/roadmap — synthesized). Findings persisted to `docs/plans/007` and the `prod-testing-readiness` memory. Artifacts published: O-007 status board and prod-readiness board.

### Decisions

- **Logging = strict byte-parity** (user choice) over a clean canonical Go format — preserves operator grep/log-shipper tooling across a per-connector cutover; reproduces Python artifacts (`b'…'` bytes-repr, `CommandId.bind_*` / `CommandStatus.*` qualified enums).
- **Deferred, with reasons** (not silently dropped): the `is not bound (#N) … aged …` lifecycle lines need a Go retry-model (per-msgid retry count + delayed requeue) the connector lacks, plus a non-deterministic `msgAge` — blocked, not just logging. The reject/unknown-error lines carry Go error strings ≠ Python.
- **Breadth pattern established** (#64) for the remaining component loggers (throwers, router): core `WithLogger` → app `WithLogger` → gateway `ComponentLogConfig` + `ApplyJasmin` overlay → runtime builds `logging.Logger(name, {Level,File,Rotate})`.
- Merge cadence held: the 3 fast CI checks (fixture-integrity, fixture-reproducibility, go-http-differential); frozen-python-regression skipped when no `jasmin/` or `tests/` files change (none did across #54–#64).

### Next

- **Logging (O-007 breadth):** DLR/MO throwers → their `.log` (needs their config `log_*` parsed first — deferred from #55), then router → `router.log`, then HTTP-access / jCli / interceptor / DLRLookup. Same pattern as #64.
- **Prod-testing readiness (docs/plans/007):** P0 to stand up the outbound path — commit a runtime `--config` JSON example, a Go Dockerfile bundling the Python bridge, and a compose/manifest with Postgres+RabbitMQ(+Redis). P1 to shadow safely — durable AMQP topology, a real health endpoint, inbound TLS + secrets injection.
- **Functional (Macro 2/3, weeks):** MO ingestion (`deliver_sm` read + MO router), terminal DLR L2/L3, filter routing in `RouteConfig`, and the multipart-billing decision (per-part vs per-message — open).

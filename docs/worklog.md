# Worklog

<!-- Newest entries on top. One entry per significant working session. -->

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

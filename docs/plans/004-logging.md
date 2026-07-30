# Logging (O-007) — Jasmin-format-compatible logging foundation + phased rollout

- **Date:** 2026-07-26
- **Status:** active
- **Summary:** Give the Go gateway structured, Jasmin-format-compatible logging (line format, levels, rotation, privacy redaction, per-component loggers), then wire it through the components in phases — MT-path audit is done; this is Macro 4.2 / O-007.
- **Related:** config module (`log_level/log_file/log_rotate/log_format/log_date_format` already parsed per section); roadmap Macro 4.2.

## Context

The Go gateway logs essentially nothing: only `cmd/synevyr-gateway/main.go` has two `log.Printf` calls; every internal component is silent. Jasmin logs richly and to a specific shape:

- **Per-component named loggers** — `smpp.client.<cid>` (SMPPc), `smpp.server.<id>` (SMPPs), `jasmin-sm-listener`, `dlr-thrower`, `deliversm-thrower`, router/interceptor, etc. Names drive per-component **files** and level, not the line text.
- **Line format** — `%(asctime)s %(levelname)-8s %(process)d %(message)s`, date `%Y-%m-%d %H:%M:%S` → `2026-07-26 12:00:00 INFO     12345 SMS-MT [cid:x] [status:ESME_ROK] ...`. Note: logger name is **not** in the line.
- **Rotation** — `TimedRotatingFileHandler(when=log_rotate)` (`midnight`, `W6`, ...).
- **Level** — per-component `log_level` (default INFO).
- **Privacy** — `log_privacy` redacts message content at specific call sites (`** N byte content **`).

Desired outcome: operators' existing log tooling (format, levels, rotation, redaction) keeps working when a connector runs on Go, starting with the highest-value message-path lines.

## Approach

Build a small `internal/core/logging` (jasminlog) package on Go's `log/slog` with a **custom handler** that renders each record in Jasmin's exact line format (date, left-padded 8-char level, pid, message — no logger name, no key=value). Named loggers map to per-component `log_file` + level + rotation, resolved from the already-parsed `log_*` config. Privacy is a call-site concern (a helper that redacts content when `log_privacy`), mirroring Jasmin. Then wire logging through components in phases, most-valuable first, so each phase is a self-contained, verifiable PR rather than one giant change.

Trade-off vs. plain `slog` JSON/text: a custom handler is required for byte-compatible lines (operators grep these); the cost is one handler implementation, well worth format parity.

## Steps

### Phase 1 (this plan's first PR): logging foundation

- **Files:** `internal/core/logging/logging.go` (+ handler), tests.
- **Changes:** A `slog.Handler` that formats `<date> <LEVEL padded-8> <pid> <message>` with the Jasmin date layout; `Logger(name string, cfg LogConfig) *slog.Logger` resolving level (`INFO` default), output (file or stderr), and rotation policy from the parsed config; a `Redact(privacy bool, content []byte) string` helper. Pid captured once at startup (Go has no per-line pid churn).
- **Verify:** golden unit tests asserting the exact line bytes for each level and the redaction helper; `go build/vet`, `-race`.

### Phase 2: config wiring + a rotating file sink

- **Files:** the runtime wiring; a size/time-rotating writer (or `lumberjack`-style, self-contained) matching `TimedRotatingFileHandler` semantics for `midnight`/`W6`.
- **Changes:** Resolve each component's logger from its `log_*` config; default to stderr when no `log_file`.
- **Verify:** unit tests for the rotation trigger; a manual smoke of a rotating file.

### Phase 3: message-path log lines (highest value)

- **Files:** `internal/core/smppc/*` (bind lifecycle, SMS-MT submit/resp, retry/requeue), `internal/core/smpps/*`, the throwers.
- **Changes:** Emit the legacy lines (`SMS-MT [cid:...] [status:...] ...`, `SMPPC [cid:...] is not bound: Requeuing ...`, bind/unbind) at the legacy levels, honoring `log_privacy` for content.
- **Verify:** a differential/golden comparing rendered lines against captured Jasmin output for equivalent events (format + fields; message text is behavioural, compared structurally).

### Phase 4+: breadth

- Router, interceptor, DLRLookup, HTTP API access logs, jCli — remaining loggers and lines, one component per PR, until O-007 line coverage is complete.

## End-to-end verification

Per phase: golden line-format tests (byte-exact for the format), rotation tests, and — for Phase 3+ — a structural differential against captured Jasmin log lines for the same message-path events. Full `go test ./...` clean each PR.

## Rollback

Additive: the logging package is new; component wiring is per-PR and revertible. Absent config keeps stderr defaults.

## Risks

- **Scope** — full O-007 line parity across all components is multi-PR; this plan front-loads the foundation + message path and phases the rest. Each PR must `log()` what it does *not* yet cover so "logging done" is never overclaimed.
- **Rotation semantics** — `TimedRotatingFileHandler` `W6`/`midnight` naming and roll timing must match if operators rotate/ship by filename; verify against a real handler.
- **Privacy** — redaction is per-call-site; a missed site leaks content. Centralize the redaction helper and grep for content-logging sites.
- **Performance** — logging is on the hot path; the handler must be allocation-light and level-gated before formatting.

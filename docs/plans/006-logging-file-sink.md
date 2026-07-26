# Logging file sink + rotation (O-007 Phase 2b) — TimedRotatingFileHandler parity

- **Date:** 2026-07-26
- **Status:** active
- **Summary:** Route the Go loggers to their legacy `log_file` (e.g. `messages.log`) with a writer that reproduces Python's `logging.handlers.TimedRotatingFileHandler` for the `when` values Jasmin uses (`midnight`, `W0`–`W6`), so operators who rotate/ship by filename see identical roll timing and dated suffixes. Currently the SMS-MT line renders to stderr.
- **Related:** `docs/plans/004` (Phase 2), `docs/plans/005` (SMS-MT line), config `LogConfig.File/Rotate` (PR #55), the `jasmin-sm-listener` logger wiring (PR #59).

## Context

Jasmin builds `TimedRotatingFileHandler(filename=log_file, when=log_rotate)` with every other default: `interval=1`, `backupCount=0` (**never deletes** rolled files), local time, `atTime=None`. The `.cfg` only ever produces `when` = `midnight` (smpp-server/sm-listener/dlr) or `W6` (most others). The library's behaviour we must match byte-for-byte:

- **Roll boundary** (`computeRollover`): for `midnight`, the next local midnight (`currentTime + secondsUntilMidnight`, **no DST adjustment**). For `W<n>` (n=0 Mon … 6 Sun), the next occurrence of that weekday at local midnight, **with** a ±3600s DST adjustment when the DST flag differs between now and the roll instant.
- **Dated suffix**: the rolled file is `<base>.%Y-%m-%d`, where the date is `localtime(rolloverAt - interval)` (again ±3600 DST-adjusted vs. now). E.g. `messages.log.2026-07-25`.
- **After roll**: `newRolloverAt = computeRollover(now)`; `while newRolloverAt <= now: += interval`; then the same MIDNIGHT/W DST re-adjust; a stale target file is removed then `base` is renamed onto it and a fresh `base` opened.
- **Weekday convention gotcha**: Python `tm_wday` is Monday=0…Sunday=6; Go `time.Weekday()` is Sunday=0. Convert: `py = (int(goWeekday) + 6) % 7`.

## Approach

Add an allocation-light, mutex-guarded `io.Writer` in `internal/core/logging` that ports `computeRollover`/`doRollover` faithfully (MIDNIGHT + W only — the S/M/H/D forms Jasmin never emits are rejected at construction, caller falls back to stderr). Time is injected (`now func() time.Time`) so rollover is unit-testable without waiting. Then extend `logging.Config` with the file + rotate directives and build the rotating sink when a file is set (nil/empty file keeps the stderr default). Finally thread the sm-listener `log_file`/`log_rotate` (already parsed, PR #55) through the gateway config into the `jasmin-sm-listener` logger.

Trade-off: a faithful port (incl. the DST edge cases and the MIDNIGHT-has-no-adjust asymmetry) over an approximation, because operators key rotation/shipping on the exact filename and roll instant. `backupCount=0` means we never delete — matching the legacy, and avoiding destructive file operations.

## Steps

### Step 1 — rotating writer
- **Files:** `internal/core/logging/rotating.go` (+ `rotating_test.go`).
- **Changes:** `newRotatingFileWriter(baseName, when string, now func() time.Time) (io.WriteCloser, error)`; faithful `computeRollover(unixSecs) int64` and `doRollover(now)`; `Write` rolls when `now >= rolloverAt` then appends. Initial `rolloverAt` seeded from the base file mtime if it exists, else now (as the library does). Invalid `when` → error.
- **Verify:** a differential test generating `computeRollover` results + the dated suffix from the real `TimedRotatingFileHandler` across a matrix of timestamps (each weekday, near-midnight, and around a DST spring-forward/fall-back boundary) for `midnight` + `W0..W6`, asserting the Go port matches; plus an injected-time rollover test asserting the rename to `base.<date>` and a fresh base.

### Step 2 — wire the sink into logging.Config
- **Files:** `internal/core/logging/logging.go`.
- **Changes:** `Config` gains `File string` and `Rotate string`; `NewHandler`/`Logger` build the rotating writer when `File != ""` (else stderr). An unopenable file or invalid rotate falls back to stderr with no fatal error (logging must never crash the process).
- **Verify:** unit test that a Config with a temp File writes the rendered line to that file; empty File still goes to the provided Writer/stderr.

### Step 3 — thread sm-listener file/rotate to the jasmin-sm-listener logger
- **Files:** `internal/app/gateway/config.go` (extend `SubmitAuditLogConfig` with File/Rotate), `internal/app/gateway/jasmin_config.go` (overlay from `jasmin.SMListener.Log.File/Rotate`), `internal/app/gateway/runtime.go` (pass them into `logging.Config`).
- **Verify:** `ApplyJasmin` test asserting File/Rotate overlay; gateway build/tests green.

## End-to-end verification

`go build/vet ./...`, `go test -race ./internal/core/logging/ ./internal/app/gateway/`, the PYTHON_PATH rollover differential, full `go test ./...` clean. Manual smoke: point a logger at a temp file, force `now` across a boundary, confirm `messages.log` + `messages.log.<date>`.

## Rollback

Additive: empty `log_file` keeps the stderr sink (current behaviour), so reverting Step 3 silences file routing with no other change.

## Risks

- **DST + weekday math** — the asymmetry (MIDNIGHT no-adjust, W adjusts) and the Mon=0/Sun=0 convention are the likeliest divergences; the timestamp-matrix differential (incl. DST boundaries) is the guard.
- **Concurrency** — one writer per file, mutex-guarded; multiple loggers must not open the same file with separate writers (share one). For now only `jasmin-sm-listener` is wired.
- **Never crash on logging** — a bad path/permission falls back to stderr, never fatal.
- **Interim scope** — only the sm-listener logger is routed to a file this phase; other components follow as they gain loggers.

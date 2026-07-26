# Logging message-path lines (O-007 Phase 3) — SMS-MT audit + submit lifecycle

- **Date:** 2026-07-25
- **Status:** draft
- **Summary:** Emit the legacy per-message `SMS-MT [cid:…] [status:…]` audit lines (and the submit lifecycle lines around them) from the Go smppc submit-response path, byte-format-compatible with the `jasmin-sm-listener` logger, honoring `log_privacy`. This is Phase 3 of the logging rollout (`docs/plans/004`), the first time a core Go component holds a logger.
- **Related:** `docs/plans/004-logging.md` (umbrella), Phase 1 foundation (`internal/core/logging`, PR #54), Phase 2 config log-fields (`internal/config/log_config.go`, PR #55), GAP 6 `internal/core/smppc/command_status.go` (status-name rendering), the fork-local custom-TLV pipeline (tlvs field).

## Context

The single highest-value operator log in Jasmin is the per-message MT audit line, emitted by `SMPPClientSMListener` (logger name `jasmin-sm-listener`) when a `submit_sm_resp` arrives. Two variants (`jasmin/managers/listeners.py`):

- **Success** (`:361`, INFO): `SMS-MT [cid:%s] [queue-msgid:%s] [smpp-msgid:%s] [status:%s] [prio:%s] [dlr:%s] [validity:%s] [from:%s] [to:%s] [content:%s] [tlvs:%s]`
- **Error/retry** (`:399`, INFO): `SMS-MT [cid:%s] [queue-msgid:%s] [status:ERROR/%s] [retry:%s] [prio:%s] [dlr:%s] [validity:%s] [from:%s] [to:%s] [content:%s] [tlvs:%s]`

The Go rewrite currently emits neither. The submit-response handling lives in `internal/core/smppc` (`session.go` `handleResponse` → `dlr_publish.go`), which today carries only `msgID`, `status`, `smscMessageID` — not the original-request fields (`from/to/content/tlvs/prio/validity`) the audit line needs.

### Field sources and formatting (each MUST be differential-verified)

| Field | Source | Formatting gotcha |
|---|---|---|
| cid | connector config id | plain |
| queue-msgid | durable msgid / part key | plain |
| smpp-msgid | `r.response.params['message_id']` | plain (success only) |
| status | `r.response.status` (`%s` of CommandStatus) | the ESME_ROK-style **name** — GAP 6 `smppStatusName` already produces it |
| retry | `will_be_retried` bool | Python `True`/`False` capitalization |
| prio | AMQP message `priority` | from `EnvelopeSnapshot.Priority` |
| dlr | request `registered_delivery.receipt` (enum) | `%s` of the receipt enum — resolve exact rendering via differential |
| validity | `'none'` or `headers['expiration']` | literal `none` when the header is absent |
| from / to | request `source_addr` / `destination_addr` (**bytes**) | Python `%s` of `b'1111'` renders **with** the `b'…'` prefix |
| content | `'%r' % short_message` (bytes repr) or `** N byte content **` when `log_privacy` | Phase-1 `logging.Redact` covers the privacy branch; non-privacy is Python `repr(bytes)` |
| tlvs | `format_tlvs_for_log(pdu, log_privacy)` | `key:val` joined by `,`, custom TLVs as `0x%04X:%s`, else `none`; reads `custom_tlvs` (fork-local pipeline) |

### Open decision (needs the user) — strict byte-parity vs. clean canonical format

The legacy line embeds Python-runtime-sensitive renderings: `[dlr:%s]` is the enum's default `__str__`, i.e. the **fully-qualified** `RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED` (not just the member); `[from:%s]`/`[to:%s]` are `%s`-of-bytes → `b'1111'` (with the `b'…'`); `[content:%s]` is `repr(bytes)`; `[tlvs:…]` `%s`-renders each decoded optional-param value (more enums). These are byte-exact-reproducible in Go but **brittle** (some enum `__str__` forms shifted across Python 3.11/3.12).

Two directions:
- **A — strict byte-parity** (default of this plan): reproduce every rendering exactly, differential-verified in the CI python. Pro: `grep`/log-shippers see identical bytes across a per-connector cutover. Con: reproduces ugly/brittle Python artifacts (`b'1111'`, `ClassName.MEMBER`) in Go, and pins us to a python-version rendering.
- **B — canonical Go format**: keep the `SMS-MT [cid:…] […]` skeleton + fields but emit clean values (`1111`, `NO_SMSC_DELIVERY_RECEIPT_REQUESTED`, hex content). Pro: cleaner, stable. Con: not byte-identical — breaks exact-line tooling during mixed Python/Go operation.

Recommendation: **A** for the fields operators actually filter on (cid, status, from/to, msgid) to preserve tooling, and accept that once all connectors are on Go the format can be revisited. Confirm before building the renderer, since it sets the rendering contract.

## Approach

Thread a **nil-safe optional `*slog.Logger`** into the smppc submit path (built from the Phase-1 `logging.Logger` using the `sm-listener` `Log.Level`/`LogPrivacy` from Phase-2 config), and thread the **decoded original-request fields + queue properties** from send-time to the response handler so the audit line can be rendered there. Formatting goes through a dedicated, unit- and differential-tested renderer so the byte-exact concerns above are verified in one place.

**Logger-threading decision (the pattern-setting choice — please confirm):** add the logger as a **nil-safe optional dependency**, matching the existing `SubmitServiceDependencies` struct pattern, *not* a positional constructor argument. `Session` already has a 3-constructor explosion (`NewSession` / `NewSessionWithDecoder` / `NewSessionWithDurability`); rather than a 4th, add an unexported `logger *slog.Logger` field defaulting to a no-op (discard) logger, set via a functional option or a dependencies struct. Nil-safe means existing call sites and tests are unaffected and logging is opt-in per wiring. Alternatives considered: (a) positional arg — rejected, worsens the constructor explosion; (b) package-global logger — rejected, not testable/injectable, fights slog idioms.

**Trade-off:** the audit line needs request context at response-time. Rather than re-decode the persisted pickle (Q-009 forbids Go pickle decode; the bridge is heavy), thread the already-decoded PDU fields captured at send-time. Cost: a small per-in-flight-request context struct; benefit: no extra decode on the hot path.

## Steps

### Step 1 — MT-line renderer (pure, no I/O)
- **Files:** `internal/core/smppc/mtlog.go` (+ `mtlog_test.go`).
- **Changes:** a `submitAuditLine(fields)` returning the exact `SMS-MT […]` string for the success and error variants; a `formatAddr(b []byte)` reproducing Python `%s`-of-bytes (`b'…'`), a `formatContent(privacy, short []byte)` (repr vs `Redact`), and `formatTLVsForLog(...)` mirroring `jasmin/tools/tlv.py` (incl. `custom_tlvs` as `0x%04X:%s`, `none` fallback). Status via existing `smppStatusName`.
- **Verify:** golden unit tests for both variants and each formatter (bytes-repr, privacy, tlvs incl. custom, `none`, `ERROR/<name>`).

### Step 2 — nil-safe logger dependency in the submit path
- **Files:** `internal/core/smppc/session.go` (+ wherever the submit request is held in-flight), `connector.go`.
- **Changes:** add an unexported `logger *slog.Logger` (default no-op) set via a functional option / deps struct; NO change to existing constructor signatures. Capture the decoded request fields (`source_addr`, `destination_addr`, `short_message`, tlvs, `registered_delivery.receipt`) + queue props (`priority`, `expiration`) into the in-flight request record keyed by the same correlation the response uses.
- **Verify:** `go build ./...`; existing smppc tests unchanged and green (nil-safe → no behavioural change without wiring).

### Step 3 — emit the lines at the response/retry decision points
- **Files:** `internal/core/smppc/session.go` (`handleResponse`, timeout/requeue), `dlr_publish.go` if the correlation lives there.
- **Changes:** on final `submit_sm_resp`, render + log the success or error/retry variant at INFO, honoring `log_privacy`. Wire the retry variant's `will_be_retried` from the existing retry-policy decision.
- **Verify:** a smppc test asserting an INFO line is emitted with the expected fields for a stubbed ROK and a stubbed error response (captured via a `bytes.Buffer` logger).

### Step 4 — runtime wiring
- **Files:** `internal/app/outbound/runtime.go` and/or `internal/app/gateway/runtime.go`.
- **Changes:** build the `jasmin-sm-listener` logger via `logging.Logger("jasmin-sm-listener", logging.Config{Level: cfg.SMListener.Log.Level})` and inject it into the smppc submit path; pass `LogPrivacy` through. Default (no wiring / nil) stays silent.
- **Verify:** gateway/outbound build + existing runtime tests green; a manual smoke showing the line on a real submit.

### Step 5 — differential verification of the rendered line
- **Files:** `internal/core/smppc/mtlog_differential_test.go` (PYTHON_PATH-gated).
- **Changes:** drive an equivalent submit+resp through the picklecompat bridge / a small Python snippet that calls the real `SMPPClientSMListener` formatting (or reconstructs the exact `%`-format with real PDU objects) and compare the Go renderer's output field-by-field, especially `status`, `from/to`, `content`, `tlvs`.
- **Verify:** differential green under conda jasmin python and CI `go-http-differential`.

## End-to-end verification

`go build/vet ./...`, `go test -race ./internal/core/smppc/ ./internal/core/logging/`, PYTHON_PATH differential for the rendered line, full `go test ./...` clean. Merge on the 3 fast CI checks (no `jasmin/`/`tests/` changes ⇒ frozen-python-regression skippable).

## Rollback

Additive and nil-safe: the renderer is new; the logger defaults to no-op, so reverting the runtime wiring (Step 4) silences the lines with zero behavioural change elsewhere.

## Risks

- **Byte-exactness** — `%s`-of-bytes (`b'…'`), the receipt enum rendering, and `format_tlvs_for_log` ordering are the likeliest divergences; Step 5's differential is the guard. Python dict iteration order for tlvs is insertion order (3.7+) — mirror the PDU param order.
- **Pattern-setting** — Step 2's logger-threading choice is reused by every later component; confirm before building.
- **Correlation** — the in-flight request context must key on the exact same correlation as the response (sequence/durable part key), including the multipart LongSubmitSm case where N PDUs share one attempt; render once per queue message, not per part.
- **Hot path** — render only when the logger is enabled at INFO; gate before formatting.
```

# Spec 002 — An operational event store the console can query

- **Date:** 2026-08-01
- **Status:** draft — revised after adversarial review; the original fingerprinting mechanism was withdrawn
- **Summary:** Errors and significant state changes become deduplicated, queryable rows in PostgreSQL with occurrence counts, surfaced in a console section, so "what is broken and since when" stops being a question only `docker logs` can answer.

## Problem

The gateway logs well and stores nothing. `internal/core/logging` renders
Jasmin-format lines to files with rotation (plans 004–006), which is right for
audit and for parity, and useless for the question an operator asks during an
incident: *what is failing right now, since when, and how often?*

This is not hypothetical. While building the topology map, the dev gateway was
retry-looping on a real defect — the CDR state machine refusing a termination
acceptance event (`cdr_events_kind_check`, the plan 024 defect). It was
invisible on every console surface and had no metric at all. The only way to
find it was to run `docker logs` and read.

Measured on that gateway: 171 log lines, 96 of them ERROR, and those 96 collapse
to **four** distinct problems repeated 21 times each. Errors here are not a
diverse stream to archive; they are a small set of failure modes that repeat.
That ratio is why events carry occurrence counts rather than one row per line.

## Goals

- An operator can answer "what is broken, since when, how often" from the
  console, without shell access to the host.
- A repeating failure occupies one row, so table growth is bounded by distinct
  failure modes rather than by traffic.
- Every event carries enough context to act: component, severity, the identities
  involved, first and last occurrence, and count.
- Retention is enforced automatically, at 90 days by default.
- Writing an event never blocks or fails the message path, and never loses the
  events that describe a database outage.

## Non-goals

- **Not a log sink.** Per-message audit and debug lines stay on files. They are
  high-volume, on the hot path, and the database that would receive them already
  holds CDRs, submit transactions and the message spool. Trading message
  throughput and vacuum headroom for log searchability is a bad trade.
- **Not distributed tracing.** No spans, no propagation.
- Not a replacement for `/metrics/prometheus`. See [spec 003](003-metrics-coverage.md).

## What the review changed

An adversarial review of the first draft found the two load-bearing assumptions
both false against this codebase. They are recorded here because the reasons
constrain the design, not merely the wording.

**Withdrawn: "a fingerprint is a hash of the normalised message."** This
codebase's error strings carry numbers that are *the diagnosis* as often as they
are noise, with no syntactic difference between them:

- `termination/deliver_http.go:358` — `"delivery returned HTTP error status: %d"`.
  Normalise the number and a 404 (partner deleted their webhook; fix config)
  merges with a 503 (partner overloaded; wait). Opposite operator actions, one row.
- `smppc/connector.go:853` — `"bind response: command=%#x status=%#x sequence=%d"`.
  ESME_RINVPASWD (rotate the password) merges with ESME_RTHROTTLED (back off).
- Leave the numbers in, and `httpcompat/handler.go:124` — which carries
  `[bytes:%d] [duration:%s]` — yields a distinct fingerprint per occurrence.
  Zero deduplication on one of the most likely captured lines.

**Withdrawn: "the drop counter will show it before the table does."** The drop
counter detects *rate* explosions, not *cardinality* leaks. One new fingerprint
per second never fills a 5 s buffer, produces no drops and no signal, and writes
roughly 7.8 M rows across a 90-day retention.

**Newly identified, and the reason this is not merely a wording change:**
`httpcompat` logs every HTTP 4xx as a Warn, on the *public* sendsms listener,
with the request path in the message. Any internet scanner walking random paths
would mint unbounded distinct fingerprints — an unauthenticated
write-amplification path into the database. That is a security property, not a
tuning concern.

## Requirements

### Functional

1. **The fingerprint is declared at the emission site, never derived from prose.**
   An event is keyed by `(component, kind, identity...)` where the call site
   states which values are identity and which are payload. A delivery failure
   declares the status code as identity and the duration as payload; a bind
   failure declares the SMPP status as identity and the sequence number as
   payload. Prose hashing survives only as an explicitly-worse fallback for a
   small allowlist of bridged loggers.
2. **Storage errors fingerprint on structured fields, not text.** `errors.As` to
   `*pgconn.PgError` and key on SQLSTATE + `ConstraintName` + `TableName`, which
   pgx already exposes. The rendered message is kept as the sample. Without
   this, the plan 024 `cdr_events_kind_check` violation would share a fingerprint
   with every other check violation reached through the same wrapping chain, and
   requirement 5's most-recent-sample rule would overwrite the evidence of
   whichever fault lost the race.
3. **`logging.Logger` must stop discarding the component name.**
   `internal/core/logging/logging.go:153` takes `name` and does `_ = name`; the
   per-connector loggers built at `gateway/runtime.go:187` carry the connector id
   *only* in that discarded name, and messages like
   `"Connection failed. Reason: %v"` carry no identity of their own. Until the
   name reaches the handler, every connector's reconnect failures are one
   indistinguishable row — and an operator reading it restarts the wrong
   connector. This is a prerequisite, not an enhancement.
4. **Recording is an upsert** keyed by fingerprint: first occurrence inserts with
   `count = 1`; later occurrences bump `last_seen` and `count`.
5. **A bounded sample is retained** — the most recent message and its attributes.
6. **Severity** is `error` or `warn` only.
7. **Correlation fields**, nullable, indexed where queried: `connector_id`,
   `route_id`, `username`, `message_id`.
8. **Episode semantics.** When `now - last_seen` exceeds a gap threshold (hours),
   a recurrence starts a new episode: `episode_started_at` and `episode_count`
   reset and are what the console shows by default, with lifetime totals kept
   separately. Without this, "is this new or has it been happening all week"
   returns *old and frequent* for a fresh incident that happens to share a
   fingerprint with a resolved one — and returns *brand new* for the same fault
   if the gap happened to cross the retention boundary.
9. **Snooze, not a boolean acknowledgement.** Acknowledgement takes an optional
   expiry or a resurface-if-rate-exceeds threshold, plus a permanent mute for
   known-tolerated fingerprints. A bare ack cleared by any recurrence is the
   default but not the only mode: the dominant event class here *recurs* by
   construction, so a partner's nightly maintenance window would un-acknowledge
   itself every night, forever. That is how an alert panel becomes ignored.
10. **One channel per call site.** `termination/manager.go:348` already both
    calls `OnError` *and* logs the same failure, and the gateway's `OnError`
    handler logs it again in a different shape. Bridge plus explicit emission
    would record one fault as two rows with different fingerprints, each
    counting independently. Where an `OnError` seam feeds explicit emission,
    that component's log line must not also be bridged.
11. **A cardinality guard**, since the drop counter cannot serve as one: cap
    distinct fingerprints per component per hour; overflow collapses into a
    single "fingerprint cardinality exceeded" event naming the component. This is
    what protects the unpartitioned-table decision below.
12. **Counts are approximate and the console must say so.** Flushing is
    at-least-once, so a statement timeout on a commit that actually succeeded
    double-counts — and it does so precisely during database degradation, when
    an operator is watching the number to decide whether things are worsening.
    Render as `≥N`. Persist the drop counter as its own event row so a
    post-incident review can see that counting was itself degraded.
13. **Console section** — see user flows.

### Non-functional

- **The write path must never fail a message.** Emission is a non-blocking
  channel send and nothing else: no I/O, no normalisation, no shared lock.
  Fingerprinting happens on the worker goroutine.
- **Two bounds, not one.** The channel bounds burst transport (1024, ~300 KB);
  the dedup map bounds fingerprint cardinality (512 distinct per window).
  The second is what protects memory when identity leaks into a fingerprint —
  the case the withdrawn safety net was supposed to cover.
- **A non-PostgreSQL dead letter is mandatory.** When PostgreSQL fails, the
  leadership monitor marks the lease lost and `cmd/synevyr-gateway/main.go:170`
  exits the process immediately and deliberately without a graceful drain. The
  buffered events — the most diagnostic of the entire incident — are destroyed,
  and unflushable anyway. For a full outage the store would otherwise contain
  *nothing*: no events during it, none leading into it, and post-recovery
  `first_seen` stamps beginning at recovery. Undeliverable events spool to local
  SQLite (already used by the admin plane off the message path, ADR-001) and
  replay on recovery.
- **The recorder's own logger must be excluded from the bridge**, by a marker,
  specified here rather than left to implementation. Otherwise a flush failure
  is logged, bridged, and queued into the buffer it cannot flush.
- **No new secret exposure.** `jcli/session.go:156` logs the *attempted
  username*; a password typed at the username prompt would be persisted into the
  retained sample. That site is excluded. Message content is never an attribute.

### Bridge allowlist, not capture-all

A wrapping handler inserted at `logging.NewHandler` covers 8 component loggers
plus every per-connector logger including admin-provisioned ones — but it must
allowlist loggers in, not capture everything. Excluded, with cause:

| Site | Why |
|---|---|
| `httpcompat/handler.go:134` (4xx→Warn) | Public listener; unbounded scanner-driven cardinality |
| `submit_service.go:560` | "Charging user failed" is the quota-exceeded rejection — an expected billing outcome, one permanent row per out-of-credit customer |
| `submit_service.go:316` | Interceptor policy rejection: the policy working as intended |
| `jcli/session.go:156` | Logs an attempted username that may be a password |
| the recorder's own logger | Feedback loop |

Note also that `slog.SetDefault` is never called, so ~15 sites — including the
admin persisted-state replay failures at `gateway/runtime.go:442-643` ("your
saved routes did not load"), which are exactly what this store is for — bypass a
choke-point bridge entirely and need either `SetDefault` or explicit emission.

### Storage — and why not monthly partitions

The original request was monthly partitions with 2–3 month retention. With
declared fingerprints and the cardinality guard, the table grows with distinct
failure modes, not with traffic, and partitioning is machinery without a payoff
against a schema that has **no partitioned tables today**.

The recommendation is a plain `operational_events` table (not `events`;
`cdr_events` already exists and is where the plan 024 defect lives) with an index
on `last_seen`, pruned by the recorder's own loop.

This holds *only because* of requirement 11. Partitioning does not fix a
cardinality leak — it makes deleting the consequences cheaper. Preventing the
leak is the correct control; partitioning stays available if measurement later
shows it is needed.

**Retention prunes in the recorder's own loop.** The first draft said it would
reuse "the same maintenance loop that already prunes CDRs and the spool". No
such shared loop exists: CDR pruning lives in the outbound runtime
(`outbound/runtime.go:497`) and spool pruning in the termination plane
(`gateway/termination.go:544`), which exists only when termination connectors are
configured — so hooking retention there would silently disable it on any gateway
that does not terminate.

## User flows

**An operator notices something is wrong.**
1. Console problem surfaces (topology rail, dashboard) show a fault.
2. They open the events section: unacknowledged errors, last 24 h, most recent first.
3. Each row: severity, component, message, episode count, first seen, last seen,
   and the connector/route/user it concerns.
4. They open a row for the retained sample.
5. They acknowledge, with a snooze window if it is a known recurring condition.

**Investigating one partner or connector.** Filter by `connector_id` or
`username`, widen the window; episode count and episode start answer "is this
new".

**The plan 024 case, as it would go.** The termination connector fails to record
acceptance; one event appears with component `termination`, `connector_id`
`test-static`, a climbing count, and a fingerprint built from SQLSTATE 23514 +
`cdr_events_kind_check` + `cdr_events` — visible in the console within seconds
instead of requiring someone to think to read container logs.

## Implementation shape

```
internal/core/opevents/recorder.go          core type, Event, worker
internal/infra/storage/postgres_events.go   store, Upsert, Prune
internal/infra/storage/sqlite_events.go     dead letter + replay
```

Worker owns the dedup map; emission is a non-blocking send. Flush every 5 s with
a 3 s timeout so flushes never queue; on failure keep the pending map and let the
next tick retry, matching `QuotaPersister` (`quota_persister.go:185-191`). Prune
hourly using the drain-until-short-batch pattern of `PruneCDRs`
(`postgres_cdr.go:369`). Own PostgreSQL handle with `SetMaxOpenConns(2)` rather
than sharing the repository pool, so a slow flush cannot compete with
`AdmitSubmitWithCDR` for connections.

Start beside the other workers in `NewRuntime`; `Close()` goes immediately after
`workerCancel()` (`gateway/runtime.go:1025`) and before `store.Close()`, joining
the worker then doing one final flush on a fresh 5 s context — the
`shutdownQuotaFlushTimeout` precedent. Never close the channel: late emitters
fall through to `default` and count a drop.

## Open questions

1. **Scope against the feature freeze.** The review turned this from "write logs
   to a table" into declared fingerprints, episode semantics, snooze, a
   cardinality guard and a SQLite dead letter. Each is justified above, and
   together they are substantially more than the original request. *Owner
   decision: full design, or a first cut that ships declared-fingerprint
   explicit emission on the four highest-value paths (termination, DLR/MO
   throwers, storage errors, connector state) and defers bridge, snooze and dead
   letter.*
2. Gap threshold for episode boundaries — hours, but which? 4 h proposed.
3. Whether health-check transitions (`gateway/health.go:59`) should emit. They
   currently reach only the `/health` JSON, so "PostgreSQL is broken" is visible
   to a poller and to nothing else.

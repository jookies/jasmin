# Spec 003 — Metrics coverage: the gaps worth closing before the freeze

- **Date:** 2026-08-01
- **Status:** draft
- **Summary:** An audit of what `/metrics/prometheus` does and does not answer, with four concrete gaps found by building the topology map — including a retry loop that had no metric at all — and a recommendation on which to close before the feature freeze.

## Problem

The production registry (`internal/core/stats/prometheus.go`) is good: submit
lifecycle with latency histograms, DLR outcomes, MO lifecycle, connector state
and uptime, queue depth, throughput rejections, interceptor errors, billing
charges and refusals, termination verdicts and spool census, gateway health. It
renders deterministically and is scrapeable.

What it cannot currently answer came out of building the topology map, where
every number on a card had to be sourced from somewhere. Four gaps surfaced,
each found by trying to draw something true and discovering there was nothing to
draw it from.

## Goals

- Name the gaps precisely, with the incident or observation that exposed each.
- Recommend which to close before the freeze and which to leave documented.
- Change nothing about the existing series: they are byte-stable and scraped.

## Non-goals

- Not a redesign of the registry.
- Not a dashboard specification. Where the numbers are drawn is a separate
  question from whether they exist.
- Not the legacy `/metrics` surface, which is frozen for Jasmin parity
  (KNOWN_QUIRKS Q-011) and must not gain series.

## The gaps

### 1. Retry loops have no metric — the one that matters most

The plan 024 defect (`cdr_events_kind_check` refusing a termination acceptance)
retried indefinitely and incremented **nothing**. It was visible only in logs. A
failure that repeats forever is the single most important thing a metric can
catch, because it is silent by construction: no threshold is crossed, no queue
alarm fires, and the affected messages simply never complete.

Any error path that retries needs a counter with an outcome label. This is a
class of gap, not one series.

*Recommendation: close before the freeze.* Start with the paths already known to
retry: termination acceptance and delivery, DLR lookup, the throwers.

### 2. Everything is since-boot, and there is no time-series store

Every counter resets on restart (Q-011), and `deploy/` contains
`alerts.prometheus.yml` — alert rules for somebody's Prometheus — but no scrape
configuration and no server. If nothing is scraping `/metrics/prometheus`, then
no number in this system has any history at all, and the console's counters look
wrong to anyone who restarts the gateway. That is exactly how they looked during
this work.

*Recommendation: close before the freeze, and it is mostly ops, not code —
confirm something scrapes the endpoint and retains it. This is the highest
value-per-effort item on the list. The console can then link out for trends
instead of pretending to have them.*

### 3. A queue that is not draining is invisible to a depth threshold

The topology map originally flagged backlog at 1000 messages. A consumer that
accepts work and fails to complete it parks at nineteen messages forever — under
any sane threshold — while a healthy burst touches thousands and clears in
seconds. Depth is the wrong variable; the derivative is the right one.

The console now detects this client-side by comparing consecutive polls, which
works but means the signal exists only while somebody has the page open.

*Recommendation: leave documented, close after the freeze.* Server-side
detection needs depth history the registry does not keep, and the client-side
signal covers the operator-present case. Note that `synevyr_queue_depth` is
already scrapeable, so a Prometheus recording rule closes this without any code
here — which is another reason gap 2 outranks it.

### 4. No per-partner error rate

`synevyr_throughput_rejections_total{user}` exists, but nothing answers "this
partner's submits are failing" — the failure counters are labelled by connector,
not by the account that sent them. During an incident the first question is
usually whose traffic is affected.

*Recommendation: close before the freeze if cheap.* The submit path already has
the username; adding it as a label to submit failures is small. Weigh
cardinality: per-user labels grow with the customer count, which is tens here,
not millions.

## Recently closed, for the record

- **Per-route match counters.** A route table said what *would* match and
  nothing said what had, so a route with a wrong filter or one shadowed by a
  higher order was indistinguishable from a working one.
  `synevyr_route_matches_total{route,connector}` and
  `synevyr_route_last_match_seconds{route}` now exist. On the dev stack this
  immediately showed the default route had never fired once, because a
  higher-order catch-all shadowed it.
- **Per-connector churn on the console.** The per-connector registry
  (`stats.SMPPcRegistry`) was populated and never read by any operator surface
  beyond `/api/stats`. Connect/disconnect counts now reach the topology map,
  which surfaced twelve carriers reconnecting in a loop.

## Non-functional

- Label cardinality stays bounded by deployment size (connectors, routes, users
  — tens to hundreds), never by message identity. No message id, no destination
  address, ever, as a label.
- New series go in the modern registry only. The legacy `/metrics` surface is
  frozen.
- `Snapshot()` and `RenderPrometheus()` must stay in agreement; the test in
  `internal/core/stats/snapshot_test.go` fails the build if a family is added to
  one and not the other.

## Open questions

1. Is anything currently scraping `/metrics/prometheus` in production? This
   determines whether gap 2 is an ops task or a project. *Owner decision.*
2. Retention target for scraped metrics — 15 days is the Prometheus default and
   is usually enough for incident review; longer needs remote write.
3. Should submit failures carry both `connector` and `user`? It answers gap 4
   directly at the cost of multiplying that series' cardinality by the customer
   count. *Recommendation: yes at this scale; revisit past a few hundred
   accounts.*

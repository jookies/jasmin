# Monitoring Synevyr

Synevyr exposes two metrics formats. They are not interchangeable. The public
HTTP listener keeps the Jasmin-compatible `/metrics` response byte-stable,
including its unusual `TYPE`-before-`HELP` ordering. The private admin listener
uses `/metrics/prometheus` for new operational series and labels
(`internal/core/stats/stats.go:179`,
`internal/core/stats/prometheus.go:271`). Keeping the second surface separate
also keeps user and connector labels off the public send port
(`internal/app/gateway/runtime.go:470`).

This observability is not production-proven. No 24-hour soak or sustained-load
test has been completed, and the gateway has not run against a real carrier
SMSC (`docs/plans/017-smpp-production-readiness.md:137`). Instrumentation is
also incomplete. Read the live/inert tables below before building a page or an
alert.

## Scraping the two surfaces

The legacy endpoint is always on `outbound.listen_address`:

```console
$ curl -fsS http://127.0.0.1:1401/metrics | sed -n '1,3p'
# TYPE httpapi_request_count counter
# HELP httpapi_request_count Http request count.
httpapi_request_count 0
```

Those are the exact first three lines before any request has incremented
the counter (`internal/core/stats/stats.go:186`). Counters are process-local and
reset at restart (`internal/core/stats/stats.go:1`).

The modern endpoint is installed only if `admin` is enabled **and**
`admin.api_listen_address` is non-empty:

```console
$ curl -fsS http://127.0.0.1:8405/metrics/prometheus | \
    grep -E '^synevyr_gateway_(ready|health)'
synevyr_gateway_ready 0
synevyr_gateway_health{status="starting"} 1
```

Those are the exact initial samples before a health request updates the
registry (`internal/core/stats/prometheus.go:104`,
`internal/core/stats/prometheus.go:434`). If the API address is empty,
`/admin/` moves to the public listener but the Prometheus handler is not
mounted anywhere (`internal/app/gateway/runtime.go:473`). A scrape does not run
health checks; scrape `/ready` as well, or health and connector gauges can be
stale (`internal/app/gateway/health.go:101`,
`internal/app/gateway/health.go:113`).

### Modern series: live versus inert

This table is the incident-time truth. “Inert” means there is no non-test call
site for the recorder. The renderer emits `HELP` and `TYPE`, but no labelled
sample for an empty map (`internal/core/stats/prometheus.go:297`). A dashboard
that turns absent data into zero will therefore say “no failures” when the
gateway did not measure the event.

| Series | State in this build | Meaning |
|---|---|---|
| `synevyr_submit_total{connector,outcome,status}` | **Inert** | Intended submit attempts, successes, and failures by SMPP status. `RecordSubmit` has no production caller (`internal/core/stats/prometheus.go:121`). |
| `synevyr_submit_round_trip_seconds` | **Inert** | Intended write-to-`submit_sm_resp` histogram. It is populated only by the same unused recorder (`internal/core/stats/prometheus.go:136`). |
| `synevyr_dlr_total{final_state,level,outcome}` | **Inert** | Intended DLR outcome and correlation accounting. `RecordDLR` has no production caller (`internal/core/stats/prometheus.go:157`). |
| `synevyr_mo_total{connector,outcome}` | **Live, partial** | Counts only `published` and `publish_failed` at `deliver.sm.<cid>` publication; it does not emit the defined `received`, `routed`, or `dropped` outcomes (`internal/core/smppc/deliver.go:299`, `internal/core/smppc/deliver.go:305`). |
| `synevyr_connector_bound{connector}` | **Live on health probe** | `1` when the most recently probed required connector was `BOUND`, else `0` (`internal/core/stats/prometheus.go:349`). |
| `synevyr_connector_state{connector,state}` | **Live on health probe** | One sample, value `1`, for the most recently observed state. Old state label sets are removed rather than written as zero (`internal/core/stats/prometheus.go:360`). |
| `synevyr_connector_uptime_seconds{connector}` | **Live on health probe** | Time since this registry first observed a transition into `BOUND`; zero when unbound. It is observation time, not the connector's authoritative bind time (`internal/core/stats/prometheus.go:181`, `internal/core/stats/prometheus.go:368`). |
| `synevyr_queue_depth{queue}` | **Inert** | Intended ready-plus-unacknowledged queue depth. `SetQueueDepth` has no production caller (`internal/core/stats/prometheus.go:201`). |
| `synevyr_throughput_rejections_total{user}` | **Live for HTTP only** | HTTP `/send` refusals from the per-user throughput gate. The SMPPS front door has no recorder call (`internal/transport/httpcompat/handler.go:337`). |
| `synevyr_interceptor_errors_total{direction}` | **Inert** | Intended MT/MO script execution failures. `RecordInterceptorError` has no production caller (`internal/core/stats/prometheus.go:222`). |
| `synevyr_billing_charges_total{currency,user}` | **Inert** | Intended charged currency units (`internal/core/stats/prometheus.go:231`). |
| `synevyr_billing_refusals_total{reason,user}` | **Inert** | Intended billing-control refusals (`internal/core/stats/prometheus.go:243`). |
| `synevyr_billing_mismatches_total{kind}` | **Inert** | Intended commercial-ledger reconciliation mismatches (`internal/core/stats/prometheus.go:253`). |
| `synevyr_gateway_ready` | **Live on health probe** | `1` only for overall `ok`; starting, degraded, and broken are `0` (`internal/core/stats/prometheus.go:434`). |
| `synevyr_gateway_health{status}` | **Live on health probe** | One sample, value `1`, for the last overall health state (`internal/core/stats/prometheus.go:442`). |

The source readiness plan records the same missing recorder call sites as a
blocking operability gap (`docs/plans/017-smpp-production-readiness.md:101`).

### Legacy series: live versus inert

All legacy series render a numeric value, so an inert counter really does look
like a healthy zero. HTTP has nine counters:

| `httpapi_...` suffix | State | Meaning |
|---|---|---|
| `request_count` | Live | Accepted GET/POST attempts to `/send`. |
| `auth_error_count` | Live | Authentication and MT-credential failures. |
| `route_error_count` | Live | No route/live connector or quota refusal. |
| `throughput_error_count` | Live | Per-user throughput refusals. |
| `server_error_count` | Live | Other submit errors. |
| `success_count` | Live | `/send` submissions accepted into the pipeline. |
| `interceptor_count` | **Inert** | Intended successful interceptor calls. |
| `interceptor_error_count` | **Inert** | Intended interceptor errors. |
| `charging_error_count` | **Inert** | Intended charging errors. |

The names and legacy descriptions are fixed at
`internal/core/stats/stats.go:20`; the live increments are in
`internal/transport/httpcompat/handler.go:247`.

Every one of the following per-connector `smppc_...{cid}` counters is
**inert**: `connected_count`, `disconnected_count`, `bound_count`,
`submit_sm_request_count`, `submit_sm_count`, `deliver_sm_count`,
`data_sm_count`, `interceptor_count`, `elink_count`,
`throttling_error_count`, `interceptor_error_count`, and
`other_submit_error_count`. The registry is wired into rendering, but no
production code calls its `Inc` method
(`internal/core/stats/stats.go:33`, `internal/core/stats/stats.go:133`,
`internal/app/gateway/runtime.go:183`).

The SMPP server's `smppsapi_...` counters have mixed coverage:

| Suffix | State | Meaning and caveat |
|---|---|---|
| `connect_count`, `connected_count`, `disconnect_count` | Live | TCP session opens and closes. `connected_count` is never decremented, although its HELP text calls it current (`internal/core/smpps/session.go:72`, `internal/core/smpps/session.go:436`). |
| `bind_trx_count`, `bind_rx_count`, `bind_tx_count` | Live | Bind requests by requested role, including rejected requests (`internal/core/smpps/session.go:280`). |
| `bound_trx_count`, `bound_rx_count`, `bound_tx_count` | Live but misleading | Successful binds by role. These are never decremented, although their HELP text calls them current (`internal/core/smpps/session.go:319`). |
| `unbind_count` | Live | Client `unbind` requests (`internal/core/smpps/session.go:202`). |
| `submit_sm_request_count` | Live | Bound `submit_sm` requests reaching the submit handler (`internal/core/smpps/session.go:264`). |
| `submit_sm_count` | Live | Those requests that complete with `ESME_ROK` (`internal/core/smpps/session.go:276`). |
| `deliver_sm_count` | Live | Server-to-ESME `deliver_sm` writes (`internal/core/smpps/session.go:390`). |
| `elink_count` | Live | Client-to-server `enquire_link` requests (`internal/core/smpps/session.go:200`). |
| `interceptor_count`, `data_sm_count`, `throttling_error_count`, `interceptor_error_count`, `other_submit_error_count` | **Inert** | Defined, with no increment call site (`internal/core/stats/stats.go:49`). |

There is also a likely bug: receiving `deliver_sm_resp` increments
`deliver_sm_resp_count`, but that name is absent from the rendered metric list,
so it is invisible (`internal/core/smpps/session.go:224`,
`internal/core/stats/stats.go:49`).

## Health and load-balancer behavior

`/live`, `/health`, and `/ready` share the public HTTP listener
(`internal/app/gateway/runtime.go:342`). Use `/live` for process restart
decisions and `/ready` for traffic admission:

```console
$ curl -fsS http://127.0.0.1:1401/live
{"status":"active","live":true}
```

The active response above is exact and has no trailing newline
(`internal/app/gateway/health.go:193`). In HA standby, `/live` returns 200 and
`/ready` returns 503 with these exact bytes:

```json
{"status":"standby","live":true,"ready":false}
```

The standby handlers are implemented at
`cmd/synevyr-gateway/main.go:194`.

The dependency report checks PostgreSQL and the codec with two-second budgets,
checks whether AMQP is open, then reads every required connector. The whole
request has a five-second budget (`internal/app/gateway/health.go:29`,
`internal/app/gateway/health.go:51`).

| State | Meaning | `/health` | `/ready` | Load-balancer action |
|---|---|---:|---:|---|
| `ok` | PostgreSQL, AMQP, codec, and every required connector are usable/bound. | 200 | 200 | Admit traffic. |
| `starting` | A dependency/status accessor is unavailable, or a required connector is `CONNECTING`. | 503 | 503 | Keep out of rotation; allow startup/reconnect time. |
| `degraded` | Infrastructure works, but a required connector has another non-bound state. | 200 | 503 | Keep the process alive but remove it from traffic. |
| `broken` | PostgreSQL/codec probe failed or timed out, AMQP is closed, or connector status lookup failed. | 503 | 503 | Remove from traffic; restart only if liveness also fails or policy requires it. |

The status severity and connector mapping are defined at
`internal/app/gateway/health.go:35`; HTTP status selection is at
`internal/app/gateway/health.go:165`. A response includes `status`, `ready`, and
a `checks` object. Inspect the named check rather than treating all 503s alike
(`internal/app/gateway/health.go:23`).

## Component logs

Each component uses the Jasmin line shape
`YYYY-MM-DD HH:MM:SS LEVEL PID message`. An empty level means `INFO`
(`internal/core/logging/logging.go:59`, `internal/core/logging/logging.go:75`).
An empty `file` writes to stderr. A file uses append mode `0644` and rotates at
local midnight or `W0` through `W6`; backups are not automatically deleted
(`internal/core/logging/logging.go:124`,
`internal/core/logging/rotating.go:14`,
`internal/core/logging/rotating.go:70`). The production Compose example leaves
component files empty and rotates container stdout/stderr at 10 MiB, retaining
five files (`docker-compose.prod.yml:32`).

| Logger / config | What it records |
|---|---|
| `jasmin-sm-listener` / `submit_audit_log` | Final MT `submit_sm_resp` audit lines and published whole-message MO audit lines. `privacy: true` replaces content with its byte count (`internal/core/smppc/session.go:902`, `internal/core/smppc/deliver.go:343`, `internal/core/logging/logging.go:158`). |
| `smpp.client.<cid>` / connector `log_*` | That connector's connection, bind, loss, retry, and consumer lifecycle (`internal/core/smppc/connector.go:180`, `internal/core/smppc/connector.go:424`). |
| `smpp.server` / `smpp_server_log` | Successful inbound ESME bind/unbind events and active counts by bind type (`internal/core/smpps/bindlog.go:29`, `internal/app/gateway/runtime.go:631`). |
| `jasmin-router` / `router_log` | Routing, interception, charging, durable quota, MO dispatch, and CDR reconciliation failures (`internal/app/gateway/runtime.go:271`, `internal/app/outbound/runtime.go:476`). |
| `jasmin-http-api` / `http_api_log` | HTTP request summaries, at debug/warning/error according to response status (`internal/transport/httpcompat/handler.go:109`). |
| `jasmin-http-access` / `http_access_log` | One info-level access line for every HTTP request (`internal/transport/httpcompat/handler.go:124`). |
| `jasmin-dlr-lookup` / `dlr_log` | DLR worker readiness and per-delivery lookup/correlation failures (`internal/app/gateway/runtime.go:615`). |
| `jasmin-amqp-factory` / `amqp_log` | Outbound topology readiness plus failures from outbound, MO dispatch, lookup, and thrower workers (`internal/app/gateway/runtime.go:278`, `internal/app/gateway/runtime.go:618`). |
| `dlr-thrower` / `dlr_thrower_log` | DLR callback worker readiness and throw failures (`internal/app/gateway/runtime.go:662`). |
| `deliversm-thrower` / `deliver_sm_thrower_log` | MO callback worker readiness and throw failures (`internal/app/gateway/runtime.go:684`). |

If two components name the same file, they share one writer. If they request
different rotations, the first rotation wins and a warning goes to stderr
(`internal/core/logging/logging.go:25`, `internal/core/logging/logging.go:130`).
An invalid rotation or file-open failure also falls back to stderr rather than
stopping the gateway (`internal/core/logging/logging.go:140`).

## Shipped alerts

`deploy/alerts.prometheus.yml` contains nine rules. Several depend on inert
series and cannot protect production yet:

| Alert | Threshold and hold time | Telemetry status |
|---|---|---|
| `JasminConnectorUnbound` | Critical: `connector_bound == 0` for 3m | Live after health probes. |
| `JasminConnectorFlapping` | Warning: at least 4 changes in 15m, for 2m | Live after health probes. |
| `JasminSubmitFailureRateHigh` | Critical: over 5%, with at least 0.1 results/s, for 10m | **Inert submit series.** |
| `JasminDLRCorrelationFailures` | Warning: at least 3 in 10m, for 2m | **Inert DLR series.** |
| `JasminQueueBacklogGrowing` | Critical: depth over 1000 and growth over 1/s across 15m, for 10m | **Inert queue series.** |
| `JasminBillingMismatch` | Critical: any increase in 15m, for 1m | **Inert billing series.** |
| `JasminInterceptorFailures` | Warning: over 0.1 errors/s across 5m, for 5m | **Inert interceptor series.** |
| `JasminThroughputRejectionsSpiking` | Warning: over 1 refusal/s across 5m, for 10m | Live for HTTP only. |
| `JasminGatewayUnready` | Critical: `gateway_ready == 0` for 2m | Live after health probes. |

The exact expressions and severities are at
`deploy/alerts.prometheus.yml:1`. Do not enable paging from the five inert
rules until their recorder call sites are implemented and exercised. Do not
“fix” this by changing their thresholds: the missing measurement is the
problem.

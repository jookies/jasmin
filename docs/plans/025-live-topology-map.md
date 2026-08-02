# Plan 025 — A live topology map for the control room

- **Date:** 2026-07-31
- **Status:** done — shipped; one item deliberately deferred (Step 8a, per-route match counters)
- **Summary:** The running gateway is drawn as one auto-generated graph on a new `/topology` console page, built from a server-owned document that joins configuration to live state, so cross-page faults — a route into an unbound connector, a `dlr-url` with no thrower — become visible for the first time.
- **Related:** [ADR-009](../adr/009-topology-renderer.md), [ADR-002](../adr/002-web-ui-stack.md), [plan 011](011-admin-web-ui.md)

## Context

The gateway's configuration is spread across ~15 console pages (connectors, termination connectors,
MT routes, MO routes, filters, HTTP destinations, users, groups, SMPPs binds, read tokens,
interceptors, billing). Each page is true in isolation, but no surface answers the operator's actual
question: **what goes where, right now, and is it moving?** Today that answer only exists in the
operator's head, reassembled by cross-referencing tables — which is also why misconfigurations that
span two pages (a route pointing at an unbound connector, a `dlr-url` accepted while the thrower is
not running) stay invisible until traffic fails.

This feature renders the running system as one auto-generated, live diagram. Nodes are the real
entities the process holds in memory, edges are the real routing decisions, and the numbers on them
come from the same counters `/metrics` publishes. It is generated from live state, never hand-drawn:
adding a route in the console changes the picture on the next poll.

Placement: the console's landing page is already titled **Control room**
(`web/src/pages/dashboard.tsx:81`) and shows only health checks + quick actions. The map becomes a
full-bleed `/topology` route linked from it (its own route so the graph library is code-split and the
rest of the console pays nothing).

## Decisions taken

| Decision | Choice | Why |
|---|---|---|
| Scope | **Everything** — MT + MO + DLR, queues, termination + spool, billing/latency lenses, infra deps | The cross-page failure modes are the whole point; a partial map recreates the problem it solves |
| Renderer | **`@xyflow/react` v12 (MIT core) + `@dagrejs/dagre` v3** | Layered layout is the expensive, fragile part; dagre is a stable pure function. React Flow nodes are React components, so cards reuse antd + `OperatorUI.tsx` |
| Liveness | **Poll `GET /api/topology` every 3–5 s** | Queue depth is only observed every 15 s upstream (`queuedepth.go:20`), so a stream would not make the scarcest number fresher. No long-lived connection on a privileged listener |
| Control | **Read-only + deep links** | Zero new mutation surface; a misclick on a dense canvas cannot break production. Node inspector links to the page that already edits the entity |

Rejected: hand-rolled canvas 2D (≈1000 lines of layout we own forever, and the exact code that breaks
when a node kind is added); Cytoscape.js (faster past ~5k nodes — we cap at ~400 — but styles through
a non-React selector DSL, so no component reuse and every visual tweak is a second language).

**Architectural rule that keeps this supportable:** the server owns the graph model, the library owns
only pixels. `/api/topology` returns our own `{nodes, edges}` document; a thin adapter maps it to
React Flow types. Replacing the renderer later rewrites the adapter, not the feature.

## What the picture shows

Layered left-to-right pipeline, MO/DLR returning right-to-left in a lower lane:

```
PARTNERS         FRONT DOOR        POLICY             QUEUES            EGRESS            CARRIERS
customer    →   HTTP /send    →   user + group   →   submit.sm.<cid> → smppc conn    →   upstream SMSC
systems     →   SMPPs bind    →   MT filters     →                   → termination   →   local app
                              →   MT route order →                     connector
            ←   MO thrower    ←   MO route       ←   deliver.sm      ←  (same links, inbound)
            ←   DLR thrower   ←   DLR level 1-3  ←   dlr.*
```

### Node kinds (each maps to a real entity)

| Node | Source of truth | Live state on the node |
|---|---|---|
| Customer / group | `outbound.UserConfig`, `GroupConfig` | balance, submit_sm quota left, disabled, throughput rejections |
| SMPP bind account | `smppsserver.UserConfig` + live sessions | bound TRX/RX/TX counts, max bindings |
| HTTP front door | `IngressSnapshot.HTTPBindAddress` (`handlers_integration.go:41`) | request/success/error from `HTTPStats` |
| SMPP server | `IngressSnapshot.SMPPSBindAddress` | connected/bound from `SMPPsStats` |
| MT route | `outbound.RouteConfig` (`internal/app/outbound/config.go:194`) | order, rate, default, filter summary |
| MO route | `modispatch.RouteConfig` (`internal/app/modispatch/service.go:49`) | order, connector filter, destination |
| Filter | `admin.NamedSpecService` + inline specs | type + pattern, referencing route count |
| Interceptor | `admin.InterceptorService` | direction, error count |
| Queue | AMQP, gauged by `queueDepthObserver` | depth at last observation (15 s cadence) |
| SMPP client connector | `smppc.Config` + `ManagedStatus` (`manager.go:22`) | bind state, bound-since, submit/throttle/error counts, latency p95 |
| Termination connector | `termination.ConnectorConfig` + `ManagedStatus` (`manager.go:398`) | consuming state, spool rows, dead-lettered, receipts overdue |
| HTTP destination / throwers | `admin.NamedSpecService`, `Ingress.*ThrowerRunning` | callback URL, retry policy, delivery outcomes |
| Message spool + read tokens | `msgspool.Service`, `admin.MessageConsumerService` | spool depth, revoked state |
| Infra dependency | `/api/health` checks | amqp / postgres / codec |
| Admin plane | adminweb / jCli / REST listeners from config | listening address, TLS |

### Edge kinds

- **Authorization** — user → group, bind account → user. Thin, static, no traffic.
- **Routing** — route → connector candidates, carrying `connector_type` (smppc vs termination); pools
  show every failover candidate with the primary emphasised.
- **Traffic** — front door → queue → connector → carrier. Thickness by observed rate, colour by
  outcome mix.
- **Return** — carrier → `deliver.sm` → MO route → HTTP destination or SMPPs session; DLR levels 1–3
  as a distinct return lane.
- **Degraded ("declared but dead")** — dashed red: a route whose connector is not bound, an
  interceptor with no runner, a `dlr-url` accepted while the thrower is off, a growing
  `submit.sm.<cid>` beside a disconnected connector. **These are the reason the map exists** — no
  table view can show them.

### Lenses (toggles over the same graph)

Health · Throughput · Backlog · Money (balance runway, quota exhaustion, refusals) · Latency
(submit RTT percentile per connector, from the existing histogram buckets).

---

## Implementation

### Step 1 — In-process metrics snapshot

`internal/core/stats/prometheus.go` can only render Prometheus **text** (`RenderPrometheus`,
`Handler`); there is no accessor a JSON API can use. Add `func (registry *PrometheusRegistry)
Snapshot() Snapshot` returning Go maps for the series the map needs: submit counts + latency
histogram by connector/outcome/status, MO by connector/outcome, DLR by level/final-state/outcome,
queue depth by queue, connector state + bound-since, throughput rejections by user, billing charges
and refusals by user, termination verdicts/deliveries/spool census, gateway health.

Reuse the existing label structs (`submitLabels`, `moLabels`, `terminationDeliveryLabels`, …) and the
same `sortedKeys` ordering so `Snapshot()` and `RenderPrometheus()` can never disagree.

*Verify:* new `prometheus_snapshot_test.go` records one of every event, then asserts every value in
`Snapshot()` also appears in `RenderPrometheus()` output — one test that makes drift impossible.

### Step 2 — Graph model

New `internal/app/adminweb/topology.go` holding the pure graph builder — no HTTP, no globals:

```go
type Graph struct {
    Nodes         []Node  `json:"nodes"`
    Edges         []Edge  `json:"edges"`
    StructureHash string  `json:"structure_hash"` // ids+kinds only, NOT metrics
    ObservedAt    string  `json:"observed_at"`
    CountersSince string  `json:"counters_since"` // process start
}
type Node struct {
    ID, Kind, Label, Column string
    Status   string            // ok | degraded | down | idle | readonly
    ManagedBy string           // admin | config  (mirrors the per-page merge)
    Metrics  map[string]float64
    Ref      *Ref              // console deep link: {resource, id}
    Detail   map[string]string // inspector fields, already redacted
}
type Edge struct {
    ID, From, To, Kind string
    Degraded bool
    Reason   string             // why it is dashed-red, in operator language
    Metrics  map[string]float64
}
```

`BuildGraph(ctx, inputs) Graph` takes a plain inputs struct (entity lists, statuses, metrics
snapshot) so it is testable without a running gateway.

**`StructureHash` is load-bearing:** it hashes node ids, edge ids and kinds and excludes every
metric. The client re-runs layout only when it changes; otherwise nodes would jump every poll and the
map would be unusable.

*Verify:* table-driven `topology_test.go` — a fixed inputs fixture produces an expected node/edge set;
a metrics-only change leaves `StructureHash` identical; adding a route changes it.

### Step 3 — Entity merge, factored out

Every list handler already merges admin-owned rows with config-owned ones (`listRoutes` in
`handlers_routes.go:47` is the pattern, repeated per resource). The graph needs all of them at once.
Extract that merge per entity type into small helpers the existing handlers also call, so the map and
the tables can never disagree about what exists.

*Verify:* existing `handlers_test.go` suites must pass unchanged — they are the regression net for
this refactor.

### Step 4 — The endpoint

`internal/app/adminweb/handlers_topology.go`: `GET /api/topology`, registered in
`server.go:routes()` next to `GET /api/stats`, behind `authed(...)` like everything else.

Security: this aggregates the entire configuration into one document. It must reuse existing
per-resource redaction — **no password hashes, no interceptor script bodies, no consumer tokens, no
connector passwords.** Add an explicit test asserting the serialized document contains none of them.

New `Deps` field: `Metrics *stats.PrometheusRegistry`, wired in `internal/app/gateway/runtime.go:658`
as `stats.DefaultPrometheus()` (everything already records into that global). Nil `Metrics` degrades
to structure-only, matching how the other optional deps behave.

*Verify:* `handlers_topology_test.go` — 401 unauthenticated; 200 with a graph for a seeded gateway;
the redaction test above; a config-owned connector appears with `managed_by: "config"`.

### Step 5 — Client data layer

`web/src/topology/types.ts` mirrors the Go document; `useTopology.ts` polls every 4 s via the
existing `httpClient` and Refine's `useCustom`, and exposes `{graph, isStale, error}`.

### Step 6 — Layout + adapter

`web/src/topology/layout.ts` — dagre with `rankdir: LR`, ranks seeded from the server's `Column` so
the six columns stay in the intended order regardless of dagre's ranking. Memoized on
`StructureHash`. `adapter.ts` (~50 lines) maps `Node`/`Edge` → React Flow types; the only file that
knows React Flow's shape exists.

### Step 7 — Page and nodes

`web/src/pages/topology.tsx` (React Flow canvas, lens toolbar, legend, inspector drawer) plus
`web/src/topology/nodes/*.tsx`, one component per node kind, built from
`web/src/components/OperatorUI.tsx` primitives. Register the route and nav entry in `web/src/App.tsx`
(`resources` array ~line 183, `<Route>` block ~line 308) — lazy-imported via `React.lazy` so the
library lands in its own chunk. Deep links reuse the existing paths (`/connectors`, `/routes`,
`/users`, …).

### Step 8 — Visual pass *(reference image received — direction fixed)*

The approved mockup revises three things in this plan, all for the better:

1. **Aggregate nodes, not one node per entity.** The canvas shows *category* cards — "MT routes ·
   142 routes", "SMPP connectors · 6 connectors" — that expand to a few named children plus a
   "+138 more routes" affordance. This is the answer to the 150–400 node scale problem and it
   removes the level-of-detail worry entirely. **Graph document gains `Group`, `ChildCount` and
   `Children`**, and the default document is the collapsed set; expansion is client-side over data
   already delivered.
2. **Three-pane shell, not a bare canvas.** Left explorer rail (search ⌘K, a "Recent problems"
   bucket list — Broken paths / Unbound / Queue backlog with counts — and a collapsible entity tree
   with per-kind counts); centre canvas; right inspector. The problems rail is the map's real entry
   point: it names the degraded edges from Step 2 before the operator has to find them.
3. **Layout is hub-and-spoke, not six rigid columns.** Ingress → gateway core (active sessions,
   submit rate, DLR rate, uptime) → broker queues → fan-out to SMPP connectors / MO routes / HTTP
   destinations / DLR thrower / termination connectors, with infrastructure hanging below the core.
   Seed dagre ranks from this shape rather than the six-column model.

Fixed visual vocabulary: **light theme**, white cards on near-white, 1px grey hairlines. Solid green
= MT flow; **dashed blue = DLR flow**; thin grey = configuration (non-traffic) edges; dashed amber
with an inline reason label ("No active bind") = broken path. Status dots green/amber/red/grey =
healthy/warning/unhealthy/unknown, always beside a text label. Top bar carries LIVE + "Updated 2s ago
· polls every 5s", a FLOW segmented filter (All / MT / MO / DLR), a LENS segmented control (Health /
Traffic / Latency / Billing — four, with backlog folded into Health), search, Refresh and a
**Read only** badge. Bottom-left: minimap, zoom %, Fit view, legend. Inspector ends with "ACTIONS
(READ ONLY)" deep-link buttons and a "DATA AS OF <UTC>" stamp.

### Step 8a — Metrics the mockup implies that do not exist yet

Three inspector fields in the design have no source in the gateway today. Recording this so they are
answered deliberately rather than quietly dropped or fabricated:

- **Per-route match rate and "Last match 2s ago"** — `routingtable.Select` (`atomic.go:31`,
  `table.go:228`) records nothing at all. Cheap fix: one atomic counter + last-match timestamp keyed
  by route order, incremented in the submit path. Worth doing; it powers "which routes are actually
  live" and the Broken-paths detection.
- **"partner-a · 217.123.45.67" on last match** — per-message identity/IP is not something to hold in
  hot-path state. Source it from the CDR service (which already records user and timestamp per
  message) on inspector open, not from the poll.
- **"Billing (MT) $142.36/hr" per route** — billing is recorded per user and currency
  (`RecordBillingCharge`), never per route. Either derive it from CDRs grouped by the connector the
  route selects, or scope the Billing lens to users/connectors and drop the per-route figure. My
  recommendation: scope it, and show "via smsc-primary" instead of a number the system cannot honestly
  attribute.

Also note **rates are derived, not stored**: the registry holds cumulative counters. "428/min" comes
from the delta between two consecutive polls, computed client-side; the first poll after load shows
"—" rather than a number extrapolated from a single sample.

### Step 9 — Docs

`docs/plans/025-live-topology-map.md` (this plan), an ADR for the renderer choice
(`docs/adr/009-topology-renderer.md` — React Flow + dagre, alternatives and why they lost), a
`docs/worklog.md` entry, and a README/`docs/STATUS.md` line for the new console surface.

## Gotchas

1. **Counters are process-local and reset on restart** (KNOWN_QUIRKS Q-011). The UI must label them
   "since <boot time>" rather than imply history — `counters_since` exists for exactly that.
2. **Layout jitter is the failure mode**, not a polish item. Layout only on `StructureHash` change.
3. **Queue depth is 15 s stale by design** (`queuedepth.go:20`). Polling at 4 s must not present it as
   instantaneous; show the observation age on queue nodes.
4. **`dist/` is committed and CI enforces freshness** — `.github/workflows/ci.yml:111` compares
   `scripts/adminweb-source-hash.sh` against `internal/app/adminweb/bundle.sourcehash`. Any `web/src`
   change requires `cd web && npm run build` (which restamps) in the same commit, or CI fails.
5. **Bundle growth**: ≈ +150–200 KB minified on the lazy topology chunk. Measure the real number
   before merging; `dist/` growth is permanent in git history.
6. **MIT core only** — React Flow *Pro* examples/plugins are commercially licensed; do not copy them
   in. `@dagrejs/dagre` is the maintained fork (upstream `dagre` is in maintenance mode).
7. **No outbound requests** — the console ships to air-gapped deployments and Refine telemetry is
   already disabled deliberately (`web/src/App.tsx`). The new page must not fetch fonts, tiles or
   CDN assets.

## Verification (end to end)

1. `go test ./internal/core/stats/... ./internal/app/adminweb/...` — snapshot/render agreement, graph
   builder, endpoint auth, redaction.
2. `cd web && npm run build` — typecheck + bundle + `bundle.sourcehash` restamp. Record the topology
   chunk size from the Vite output.
3. `./scripts/adminweb-source-hash.sh` matches the committed `bundle.sourcehash` (what CI checks).
4. `scripts/dev.sh` to bring up a real gateway with RabbitMQ + Postgres; open the console, log in,
   open `/topology`. Confirm: the six columns render; every configured connector, route and user
   appears; `managed_by: config` entities render read-only.
5. Exercise the failure modes deliberately — stop a connector from `/connectors` and confirm its
   route edge turns dashed-red with a readable reason and its `submit.sm.<cid>` queue node starts
   climbing; restart it and confirm the map recovers within one poll.
6. Send traffic through the partner ESME simulator (`tools/`) and confirm the traffic edges thicken
   and the counters on the connector node move in step with `/metrics/prometheus`.
7. Watch three consecutive polls with the browser idle and confirm **no node moves** — the layout
   stability requirement.

---

## Appendix — image-generation prompt

> A dark-mode network operations console screenshot, ultra-wide 16:9, titled "Control Room — Live
> Topology". Centre: a large canvas showing an auto-generated, left-to-right layered graph of an SMS
> gateway. Six labelled columns: Partners, Front Door, Policy, Queues, Egress, Carriers. Nodes are
> rounded rectangular cards with a small type icon, a bold name, one metric in large type and two
> small stat rows — e.g. a card "carrier-a · SMPP client" showing "BOUND 6h 12m", "1,204 submit/min",
> "0.4% throttled". Edges are smooth bezier curves of varying thickness with small glowing particles
> flowing along them left to right, teal for outbound MT traffic; a second set of violet curves flows
> right to left along a lower lane for inbound MO and delivery receipts. One edge is a dashed red
> curve into a red-outlined node labelled "carrier-b · DISCONNECTED", with an amber queue node
> "submit.sm.carrier-b · depth 12,480" swelling next to it. Queue nodes are cylinders. A right-hand
> inspector panel shows the selected node's details and a small sparkline. A top toolbar has lens
> toggles: Health, Throughput, Backlog, Money, Latency. Bottom-left has a legend and a zoom control.
> Style: precise, technical, high-information-density, thin 1px hairlines, muted slate background
> (#0f1419), teal (#12a594) and violet (#5b5bd6) as the only accent hues, red/amber only for faults,
> monospace numerals, generous whitespace between columns. Looks like a real product screenshot from
> a modern telecom operations tool — not a marketing illustration, no people, no 3D, no glow-heavy
> sci-fi HUD.

Variants worth generating: light theme; a "trace mode" frame with one path highlighted and everything
else dimmed; a zoomed-in detail of three node cards so the card anatomy is legible.

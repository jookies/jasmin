# SMPP production readiness — spec-driven, not parity-driven

- **Date:** 2026-07-29
- **Status:** active
- **Summary:** The release gate becomes SMPP 3.4 correctness plus operational robustness, validated against independent implementations; byte-parity with the frozen Python Jasmin is explicitly descoped except where it is customer-visible.
- **Related:** [015-python-jasmin-deprecation-gate.md](015-python-jasmin-deprecation-gate.md) (superseded as the release gate), [016-release-readiness-and-python-deprecation.md](016-release-readiness-and-python-deprecation.md), [../STATUS.md](../STATUS.md)

## The decision

Jasmin is a production-quality **reference implementation**, not a specification.
The product goal is a correct, robust SMPP gateway with equal or better
functionality — not a byte-identical reimplementation.

This changes what blocks a release. The 205-contract compatibility registry, the
Release A cutover graph and the oracle-differential machinery exist to prove
byte-parity. Most of that is no longer a gate.

### What is descoped

Internal representations, because no external party observes them:

- **AMQP payload parity** (the pickled `Routable*` objects) — 15 of the 50
  unfinished Release A contracts.
- **Redis key layout and TTL parity**.
- **Internal logging line parity** beyond what operators actually grep for.

These stay useful as *regression* tests where they already pass. They stop being
promotion gates.

### What stays in scope

Anything a customer or operator integration parses:

- **HTTP `/send`, `/balance`, `/rate`** request and response shapes, including
  error text — live integrations depend on the exact bytes.
- **DLR callback payloads** — customer webhooks parse them.
- **Delivery-receipt text format** — both SMSCs and customers parse it.
- **jCli command output** — operator muscle memory and scripts.

Everything above already has evidence: jCli is 18/18 `MATCH` with 19 byte-exact
fixtures, and the HTTP matrix is the best-covered of the seven.

### What replaces the gate

SMPP 3.4 conformance and operational robustness, validated where possible
against implementations that share no code with ours.

## Why independent validation is the centre of this plan

Before today every test drove the gateway against a fake SMSC in this repository.
That proves the two halves agree with each other; it cannot catch a shared
misreading of the specification, because the misreading sits on both ends of the
wire. Seven of the fourteen SMPP defects fixed today were invisible to a green
test suite for exactly that reason.

`scripts/interop/esme_probe.py` now removes our code from one end: it binds with
`smpp.twisted`, the independent library the frozen Jasmin runs on. Agreement
there is evidence about the protocol rather than about our assumptions.

## Gates

Each gate is a checklist of executable checks. A gate is green only when every
check runs in CI or has recorded output in `docs/worklog.md`.

### G1 — Protocol correctness (SMPP 3.4)

- [x] Session lifecycle: bind TX/RX/TRX, unbind, enquire_link keepalive plus a
      separate inactivity timeout.
- [x] `deliver_sm` carries a non-zero sequence number from a per-session counter.
- [x] `data_sm` refused the way the reference does, without billing.
- [x] Concatenation both directions: UDH and SAR, honouring
      `long_content_split` / `long_content_max_parts`.
- [x] GSM 03.38 encoding including the replacement map.
- [x] TLVs preserved on the MO path, vendor and standard.
- [ ] Sequence-number wrap at `0x7FFFFFFF` on both client and server.
- [ ] `generic_nack` for an unparseable PDU or unknown `command_id`.
- [ ] Bounded outstanding-request window with a response timeout, both
      directions; a nacked or timed-out `deliver_sm` must be visible, not dropped.
- [ ] Field-by-field `submit_sm` wire audit against the specification, including
      absent-versus-empty for every optional field.

### G2 — Resilience

- [x] Reconnect on connection loss.
- [ ] Exponential backoff with jitter and a cap (the reference has none; hammering
      a recovering SMSC is how a bind gets banned).
- [ ] Backpressure when the window is full, rather than unbounded growth.
- [ ] Graceful drain on shutdown.
- [ ] Chaos drills: kill broker, database and SMSC mid-submit. Each has a
      defined, asserted outcome — no loss, no duplicate, no double charge.

### G3 — Money correctness

- [x] Prepaid and postpaid charging, quotas, early and late billing, durable
      balances.
- [x] `billing_feature` honoured per ingress.
- [ ] Differential prepaid/postpaid/group run across both HTTP and SMPPs ingress.
- [ ] Reconciliation proving no duplicate or missing charge across retry,
      reconnect and process restart.

### G4 — Operability

- [x] `/health`, `/live`, `/ready`; component loggers; legacy `/metrics` surface.
- [x] Modern metrics **registry** implemented (`internal/core/stats/prometheus.go`,
      dependency-free) and served at `/metrics/prometheus` on the admin listener,
      separate from the byte-frozen legacy surface.
- [ ] **Per-connector and per-user observability is non-functional.** Three
      separate families are wired for reading and never written, so they render
      zero regardless of traffic:
      1. `smppc_...{cid}` per-connector counters — the `SMPPcRegistry` is plumbed
         into jCli, adminweb and the outbound runtime, but nothing in
         `internal/core/smppc/` increments it. An operator inspecting a connector
         in the web UI sees zeros while it carries traffic.
      2. jCli `stats --user` — every value is a literal (`managers_stats.go`).
      3. Most modern Prometheus series (below).
      Verified 2026-07-30. `docs/operations/monitoring.md` documents which series
      are live and which are inert; read it before trusting a dashboard.
- [ ] **Prometheus instrumentation is partial.** Wired so far:
      gateway health status, MO published/failed per connector, per-user
      throughput rejections, connector state. Still zero call sites:
      `RecordSubmit` (including the latency histogram), `RecordDLR`,
      `SetQueueDepth`, `RecordInterceptorError`, and all three billing recorders.
      A metric that is defined but never incremented reads as "zero problems"
      rather than "not measured", which is worse than absent — treat this as
      blocking G4.
- [x] Alert rules with defensible thresholds mapped to real failure modes
      (`deploy/alerts.prometheus.yml`, nine rules). Note some fire on series that
      are not yet incremented; they go live with the instrumentation above.
- [x] Runbooks for the eight scenarios (`docs/runbooks/`).

### G5 — Security

- [x] Admin REST API on its own listener, off the public send port.
- [x] `allow_interceptor_editing` defaults false (it is arbitrary Python on the
      gateway host, so with a token it is remote code execution).
- [x] Secrets by `env:` / `file:` reference; TLS for HTTP and SMPP.
- [x] Per-user credential enforcement at both front doors.
- [ ] Rotation procedure documented and drilled.

### G6 — Independent validation

- [x] Third-party ESME binds, submits, receives `deliver_sm`, acks, unbinds.
- [x] A bind with wrong credentials is refused.
- [ ] Our SMPPc client against a third-party SMPP **server**
      (`smpp.twisted.server`) and against SMPPSim.
- [ ] Long-message, DLR and error-status paths through the third-party client.

### G7 — Load and endurance

- [ ] Sustained target TPS with latency recorded and no queue growth.
- [ ] 24-hour soak: no leak, no memory or sequence drift, no unexplained loss.

### G8 — Carrier validation

- [ ] One low-volume real SMSC: real submits, real DLRs, real balances, watched.
- [ ] Rollback rehearsed against that connector.

**G8 cannot be simulated.** It needs operator credentials and is the difference
between "spec-correct in a lab" and "production ready".

## Deliberate divergences from the reference

Recorded here so they are decisions rather than drift. Each should also appear in
`spec/compatibility/DEVIATIONS.md`.

| Divergence | Reason |
|---|---|
| Reconnect backoff instead of a fixed delay | The reference retries at a fixed interval forever, which hammers a recovering SMSC. |
| Bounded outstanding-request window with response timeout | The reference has no server-side window; a silent peer is invisible. |
| MO interception on each segment **and** the whole | The reference intercepts each arriving PDU and never the whole. Intercepting only the whole let a reject be bypassed after segments had shipped; doing both keeps full-text filtering and makes a reject real. |
| `admin.api_listen_address` | The reference has no equivalent; `/admin/` shared the public send port. |
| Modern metrics surface alongside the legacy one | The legacy text format is byte-frozen and cannot carry new series. |

## Cutover hazards

- **Do not point this gateway at the Python deployment's RabbitMQ vhost.** The
  production config enables `amqp_durable_topology`; the reference declares the
  same topology non-durable (`jasmin/queues/factory.py:215` passes no `durable`).
  AMQP answers a mismatched redeclare with `PRECONDITION_FAILED` (406). Use a
  separate vhost, or set the flag false.
- **Python is not fully removable.** The PB compatibility sidecar installs the
  whole legacy tree by design, and interceptors shell to `interceptor_runner.py`.
  Deprecation means retiring the legacy *gateway*, not removing Python.
- **The frozen oracle stays.** It is the rollback target and a differential test
  asset, and descoping parity as a *gate* does not authorize deleting it.

## Estimate

Implementation for G1–G6 is hours to a few days each and largely parallelizable.
G7 has an irreducible 24-hour element. G8 depends on carrier scheduling.

The earlier 12–18 week figure was the cost of *formal Jasmin parity* and no
longer applies.

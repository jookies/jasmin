# Jasmin SMS Gateway → Go Rewrite: Analysis and Full Migration Plan

> **For Hermes:** implementation is intentionally blocked until this plan, compatibility scope, and improvement policy are approved. When approved, execute task-by-task with TDD and a two-stage review.

**Goal:** rewrite Jasmin SMS Gateway 0.12 in Go without losing externally observable behavior, then improve reliability/performance/security behind explicit compatibility boundaries, and only afterward add new concepts.

**Architecture:** use a contract-first strangler migration rather than a big-bang rewrite. Keep upstream Python at commit `0aac58e466d583d0f0436df7b8afa3dc96191263` as the behavioral oracle; build a modular Go runtime with compatibility adapters around a clean domain core; compare both systems using differential protocol tests before any cutover.

**Target stack (provisional until spikes pass):** Go 1.26.x, modular monolith first, RabbitMQ AMQP 0-9-1 via `rabbitmq/amqp091-go`, Redis via `redis/go-redis`, Prometheus/OpenTelemetry, versioned internal envelopes, optional PostgreSQL control-plane store after parity. SMPP library is **not selected yet**: `fiorix/go-smpp` and `linxGnu/gosmpp` require a wire-level client/server capability spike.

---

## 1. Scope and source baseline

### Repository baseline

- Upstream: `https://github.com/jookies/jasmin`
- Fork: `https://github.com/pumpitspace/jasmin`
- Local checkout: `/Users/minibot/Projects/bots-agents/jasmin-go`
- Planning branch: `go-rewrite`
- Behavioral baseline commit: `0aac58e466d583d0f0436df7b8afa3dc96191263`
- Upstream version: `0.12`
- License: Apache-2.0
- Version caveat: `pyproject.toml` declares `0.12`, while `jasmin/__init__.py` reports `0.11.1`; commit SHA plus captured behavior—not a version string alone—is the compatibility identity.
- Trademark caveat: Apache-2.0 permits modification/distribution but grants no trademark rights to the Jasmin name/logo; distribution branding requires a separate decision.
- Current branch is clean; no Go source has been added.

### Quantitative map

| Area | Files | Lines / tests |
|---|---:|---:|
| `jasmin/` | 104 Python files | 18,667 lines; 268 classes; 923 functions |
| `tests/` | 72 Python files | 25,543 lines; 1,039 test methods |
| `misc/doc/sources/` | 81 files | 6,055 RST lines + examples/assets |
| Largest runtime areas | protocols 8,990; routing 3,878; managers 2,394 lines | migration complexity is concentrated in protocol and asynchronous state flows |

The test suite is larger than the runtime. It is valuable as a behavior oracle but cannot be translated mechanically: many tests assert Python object types, pickle payloads, Twisted timing, and implementation details. We must separate **external contracts** from **legacy internals**.

---

## 2. Current system structure

### 2.1 Runtime entrypoints

| Entrypoint | Responsibility |
|---|---|
| `jasmin/bin/jasmind.py` | Main composition root: Redis, RabbitMQ, Router PB, SMPP client manager PB, SMPP server, HTTP API, jCli, optional interceptor/DLR/MO throwers |
| `jasmin/bin/interceptord.py` | Executes Python interceptor scripts through Twisted Perspective Broker |
| `jasmin/bin/dlrd.py` | DLR callback/thrower worker |
| `jasmin/bin/dlrlookupd.py` | Correlates DLRs through Redis and republishes them |
| `jasmin/bin/deliversmd.py` | Delivers MO messages to HTTP or SMPP-server clients |

`jasmind.py` starts components sequentially but generally logs component failures and continues. This creates potentially degraded partial startups. Shutdown is manually ordered in reverse.

### 2.2 Core subsystems

| Subsystem | Source | Current behavior |
|---|---|---|
| SMPP client connectors | `jasmin/managers/clients.py`, `listeners.py`, `protocols/smpp/*` | Connector CRUD/start/stop, reconnect, bind modes, throttling, retries, PDU handling, custom TLVs, multipart messages |
| SMPP server API | `protocols/smpp/factory.py`, `protocol.py`, `pb.py` | Bind receiver/transmitter/transceiver, auth, IP allowlist, max bindings, submit routing, MO/DLR delivery |
| Legacy HTTP API | `protocols/http/*` | `/send`, `/rate`, `/balance`, `/ping`, `/metrics`; GET/POST; exact text/error formats |
| REST API | `protocols/rest/*` | Basic Auth wrapper around legacy HTTP API; single send, batch, scheduling, callbacks, Celery QoS |
| Router | `routing/router.py`, `RoutingTables.py`, `Routes.py`, `Filters.py` | MO/MT tables, descending order, AND filters, default/static/random/failover routes, in-memory users/groups |
| Interceptors | `routing/Interceptors.py`, `interceptor/interceptor.py` | Ordered filters plus arbitrary Python code, remote execution over PB, mutable routable/status result |
| Billing | `routing/Bills.py`, `Routes.py`, `router.py` | Balance and submit count quotas; optional early/late charging tied to `submit_sm_resp`; float amounts |
| Message broker | `queues/*`, manager/router/thrower consumers | RabbitMQ topic exchanges `messaging` and `billing`; manual ACK/reject/requeue |
| DLR state | `managers/dlr.py`, `listeners.py` | Redis maps `dlr:<queue-id>` and `queue-msgid:<smsc-id>`, expiry, HTTP/SMPP receipt routing |
| MO/DLR throwers | `routing/throwers.py` | HTTP GET/POST callbacks with `ACK/Jasmin`; SMPP deliver_sm/data_sm; retry/failover |
| Management | `protocols/cli/*` | Telnet jCli managers for users/groups/connectors/routes/filters/interceptors/stats and profile persistence |
| Persistence | `router.py`, `managers/clients.py`, `tools/migrations/*` | In-memory live config periodically persisted as trusted Python pickle files with version migration |
| Metrics/logging | `tools/stats.py`, protocol stats, `/metrics` | Process-local collectors, Prometheus text endpoint, conventional file logs |

### 2.3 Domain model

`jasmin/routing/jasminApi.py` contains the effective domain model:

- `Group`, `User`;
- `MtMessagingCredential`: authorizations, regex value filters, defaults, quotas;
- `SmppsCredential`: bind authorization, IP/CIDR allowlist, max bindings;
- `CnxStatus` / `UserStats` process-local connection metrics;
- `HttpConnector`, `SmppClientConnector`, `SmppServerSystemIdConnector`.

Important legacy constraints:

- user/group IDs and usernames have narrow regex/length rules;
- passwords are MD5 digests;
- users/groups can be disabled;
- user/group state is held in memory;
- connection status is intentionally not persisted;
- unlimited quota is represented by `None`;
- route prices are Python floats.

### 2.4 Routing semantics

Files: `routing/RoutingTables.py`, `Routes.py`, `Filters.py`, `Routables.py`.

Contracts that must be preserved in compatibility mode:

1. Separate MO and MT routing tables.
2. Higher numeric order wins; order `0` is reserved for `DefaultRoute`.
3. Adding at an existing order replaces the previous route.
4. All filters on a route use logical AND.
5. First matching route wins; no match rejects the message.
6. MT destinations must be SMPP client connectors; MO destinations are HTTP or SMPP-server connectors.
7. Filter set:
   - transparent;
   - source connector (MO);
   - user/group (MT);
   - source/destination regex;
   - short-message regex;
   - date/time interval;
   - Python eval filter;
   - tag.
8. Routes:
   - default;
   - static MO/MT;
   - `RandomRoundrobin` MO/MT — despite the name, implementation uses `random.choice`, not deterministic round-robin;
   - failover MO/MT;
   - `BestQualityMTRoute` exists as an unimplemented/skipped stub and is **not parity scope as working functionality**.

### 2.5 Message flows

#### HTTP/SMPP MT submission

1. Authenticate user and verify enabled user/group.
2. Validate protocol fields and per-user authorizations/value filters.
3. Build `submit_sm`; apply defaults; split long message using SAR or UDH.
4. Apply tags and optional Python MT interceptor.
5. Resolve first matching MT route.
6. For failover, select a bound connector.
7. Compute bill and validate quotas.
8. Persist DLR correlation in Redis when requested.
9. Publish pickled PDU to RabbitMQ `messaging` exchange with key `submit.sm.<CID>`.
10. Connector consumer applies QoS, expiry, retry policy and custom-TLV validation, sends to SMSC.
11. On `submit_sm_resp`, ACK/requeue, publish DLR event and delayed bill request where applicable.

#### MO delivery

1. SMPP client receives `deliver_sm`/`data_sm`.
2. Detect DLR versus MO; handle multipart reassembly rules.
3. Apply optional MO interceptor.
4. Publish MO to `deliver.sm.<CID>`.
5. Router chooses MO route in descending order.
6. Publish to `deliver_sm_thrower.http` or `.smpps`.
7. HTTP destination must return exact body `ACK/Jasmin`; otherwise retry/failover.

#### DLR

1. Submission stores request metadata in `dlr:<queue-msgid>`.
2. `submit_sm_resp` normalizes the SMSC ID (including legacy uppercase/leading-zero and configured decimal/hex conversions) and creates `queue-msgid:<smsc-msgid>` → queue-ID mapping.
3. Incoming receipt is parsed from optional PDU fields and/or receipt text.
4. DLR lookup resolves correlation and routes receipt to HTTP or SMPP origin.
5. DLR level 1/2/3, method, expiry, connector type, receipt PDU type and retry behavior affect observable output.
6. MO multipart reassembly uses Redis key `longDeliverSm:<cid>:<ref>:<destination>` with a legacy 300-second TTL; key shape, serialized parts, expiry and final concatenation are compatibility contracts.

#### Billing

- route rate applies per generated `submit_sm` part;
- `submit_sm_count` decrements for each part even on unrated routes when quota exists;
- without early percentage, full rate is charged at submit;
- with `early_decrement_balance_percent`, part is charged at submit and remainder on successful `submit_sm_resp` processing;
- current mutation is in-memory and async billing travels via `billing` exchange key `bill_request.submit_sm_resp.<UID>`.

### 2.6 External compatibility surfaces

The first parity release must inventory and test all of these:

1. **SMPP 3.4 server wire behavior**
   - bind RX/TX/TRX, auth, IP allowlist, max bindings;
   - enquire_link, timers, session states, status codes;
   - submit_sm/data_sm/deliver_sm;
   - long content, DCS, GSM 03.38/UCS2/binary;
   - standard and vendor TLVs;
   - source/destination TON/NPI;
   - throttling and billing errors;
   - DLR receipt format and message-id correlation.
2. **SMPP client behavior**
   - connector config, bind mode, TLS, reconnect, retries, queue expiry, throughput;
   - error-specific retry map;
   - submit response and DLR handling.
3. **Legacy HTTP API**
   - paths `/send`, `/rate`, `/balance`, `/ping`, `/metrics`;
   - GET/form POST/JSON where supported;
   - field names, defaults, validation order;
   - exact status codes, content type and body strings.
4. **REST API**
   - `/ping`, `/secure/send`, `/secure/sendbatch`, `/secure/balance`, `/secure/rate`;
   - Basic Auth, underscore-to-hyphen mapping;
   - batch scheduling, global parameters, callbacks/errbacks and QoS.
5. **HTTP callbacks**
   - MO and DLR GET/POST payload names;
   - `ACK/Jasmin` acknowledgement contract;
   - retry/failover timing and terminal conditions.
6. **Twisted Perspective Broker programmatic APIs**
   - RouterPB, SMPPClientManagerPB and SMPPServerPB remote methods;
   - public proxy behavior in `jasmin/routing/proxies.py`, `jasmin/managers/proxies.py` and `jasmin/protocols/smpp/proxies.py`;
   - authentication, pickle protocol, return values/errors and connection lifecycle;
   - because Go will not implement Twisted PB natively, compatibility mode requires a Python PB facade translating to the Go control API unless this surface is explicitly approved for removal.
7. **jCli/Telnet management**
   - auth, prompt/output, CRUD commands and profile persist/load behavior.
8. **Configuration**
   - INI sections/defaults and uppercase environment overrides;
   - old persisted profile import, including `PROFILE.smppccs`, `.router-groups`, `.router-users`, `.router-moroutes`, `.router-mtroutes`, `.router-mointerceptors` and `.router-mtinterceptors`;
   - `jcli-prod` autoload behavior, profile headers, configured pickle protocol and quota-triggered persistence timer;
   - process flags and default ports (SMPP 2775, jCli 8990, HTTP 1401, internal PB ports).
9. **AMQP/Redis**
   - topology/routing keys where mixed Python/Go operation is required;
   - DLR key formats and expirations.
10. **Observability/deployment**
   - Prometheus metric names/labels relied upon by users;
   - Docker/Compose/Kubernetes ports, health and configuration mounts.

---

## 3. Main engineering findings

### 3.1 What is good and should be retained

- Clear conceptual split between protocol adapters, routing, broker workers and DLR correlation.
- Strong store-and-forward design with RabbitMQ.
- Broad SMPP behavior and realistic SMSC simulators in tests.
- Explicit routing/filter model that is understandable to operators.
- Separate HTTP and SMPP ingress over one routing/billing core.
- Retry, throughput, DLR and multipart behavior already encoded in tests/docs.
- Apache-2.0 permits a clean Go implementation with attribution.

### 3.2 Risks in a direct line-by-line port

- Twisted Deferred/PB architecture has no useful one-to-one Go mapping.
- Python pickle is both language-specific and unsafe for untrusted data; it cannot be the Go domain or event format.
- Python object identity/type assertions in tests are not product contracts.
- Race conditions hidden by one event loop can appear with Go goroutines.
- Billing and quotas are non-transactional in-memory mutations split across async messages.
- AMQP redelivery can duplicate side effects; explicit idempotency is absent.
- Redis DLR updates are multi-step and can race or become partially applied.
- Timed `basic_reject(requeue=1)` patterns can reorder and churn queues.
- REST is a proxy plus Celery layer over legacy HTTP, not an independent API core.
- Arbitrary Python interceptor execution cannot be natively reproduced in Go without a compatibility sidecar or a new plugin contract.
- Existing “RandomRoundrobin” behavior must not be accidentally corrected during parity.

### 3.3 Security and operability debt

These are findings to design around, not reasons to break compatibility silently:

- MD5 password storage and admin digests;
- trusted pickle loading for profile and PB/event payloads;
- Telnet/Perspective Broker management surfaces;
- arbitrary Python `eval` interceptor execution;
- callback URLs create SSRF risk without egress controls;
- several management listeners default to broad binds;
- component startup may continue after critical dependency failure;
- no typed/versioned event schema;
- no durable idempotency ledger or dead-letter policy as a first-class model;
- process-local metrics/state complicate horizontal scaling;
- float money and split async charges complicate auditability.

---

## 4. Rewrite strategy

### Decision: contract-first strangler, not big bang

A full rewrite with a final “compare at the end” is too risky. We will:

1. freeze one upstream commit as oracle;
2. extract black-box fixtures and protocol captures;
3. define canonical internal domain/events in Go;
4. replace one boundary at a time;
5. run Python and Go against the same SMSC/broker fixtures;
6. shadow traffic without side effects;
7. cut over only when compatibility gates pass.

### Target topology: modular monolith first

Do **not** begin with many microservices. Start with one Go binary that has explicit modules and can later split workers by command/role.

Proposed tree after approval:

```text
go/
  go.mod
  cmd/
    jasmin-go/                 # composition root and role selector
    jasmin-migrate/            # trusted exported-profile importer
    jasmin-compat-probe/       # differential test CLI
  internal/
    domain/                    # Message, PDU-neutral metadata, User, Group, Connector
    routing/                   # filters, routes, ordered tables
    billing/                   # plans, quota reservation/settlement, decimal/fixed-point money
    auth/                      # legacy MD5 verify + modern hash migration
    submission/                # common ingress orchestration
    delivery/                  # MO/DLR egress orchestration
    dlr/                       # state machine and correlation
    connector/                 # connector lifecycle and health
    interceptor/               # compatibility sidecar + future plugin ABI
    envelope/                  # versioned broker schemas
    idempotency/               # inbox/outbox/dedup contracts
    config/                    # legacy INI/env compatibility and typed config
  adapters/
    httpcompat/                # exact legacy HTTP API
    restcompat/                # exact REST API
    smpp/                      # SMPP client/server adapter
    rabbitmq/                  # AMQP topology and confirms
    redis/                     # DLR/cache state
    filestore/                 # legacy-compatible default config store
    postgres/                  # optional production control-plane store
    jcli/                      # Telnet compatibility facade
    adminapi/                  # modern authenticated management API
    callback/                  # HTTP MO/DLR/batch callbacks
    metrics/                   # Prometheus/OpenTelemetry
  migrations/
    schemas/                   # versioned export/import schemas
  tests/
    contract/http/
    contract/rest/
    contract/smpp/
    contract/jcli/
    differential/
    integration/
    soak/
    fixtures/
compat/
  python-exporter/             # isolated trusted pickle → JSON exporter
  python-interceptor-sidecar/  # temporary arbitrary-Python parity boundary
spec/
  compatibility/              # matrices, captured outputs, accepted deviations
  events/                     # JSON Schema or Protobuf definitions
  protocols/                  # endpoint/PDU contracts
```

### Internal architectural rules

1. Protocol adapters never own routing, billing or user logic.
2. Domain logic never imports HTTP/SMPP/AMQP/Redis packages.
3. Every external operation accepts `context.Context` and has a deadline.
4. Every message envelope has schema version, message ID, correlation ID, causation ID, tenant/user, created-at and attempt count.
5. Every side effect is idempotent or protected by an inbox/outbox record.
6. ACK only after durable state/side effect is committed according to the flow.
7. Money is fixed-point decimal internally; legacy float formatting remains at compatibility edges.
8. Configuration is immutable/versioned at runtime; changes are atomically published.
9. Goroutines are bounded; connector and queue concurrency are explicit configuration.
10. Critical dependency failure makes readiness false and fails startup where operation would be unsafe.

---

## 5. What can be improved immediately without breaking parity

These improvements are internal and should exist from the first Go code:

| Improvement | Compatibility technique |
|---|---|
| Typed domain model | Preserve legacy field validation and edge serialization in adapters |
| Fixed-point money | Render legacy HTTP/jCli values exactly; differential billing fixtures prove parity |
| Structured errors | Map typed internal errors to exact legacy HTTP bodies and SMPP statuses |
| Context deadlines/cancellation | Keep legacy timeout values as defaults |
| Structured logs + trace IDs | Keep privacy mode; do not log message bodies by default |
| Prometheus + OpenTelemetry | Export legacy metric names plus new namespaced metrics |
| Bounded concurrency/backpressure | Match legacy throughput externally; expose queue depth and saturation |
| Versioned events | Use a temporary bridge for Python pickle during mixed mode; never unpickle in Go |
| Publisher confirms + consumer idempotency | Preserve at-least-once external behavior while preventing duplicate internal side effects |
| Dead-letter queues | Preserve retry counts/delays; add operator visibility and replay tooling |
| Atomic Redis scripts/transactions | Preserve Redis key names/TTL while making transitions atomic |
| Password migration | Verify legacy MD5, then rehash to Argon2id/bcrypt in modern store; compatibility auth remains |
| Config validation | Accept legacy INI/env names but fail clearly on invalid or conflicting values |
| Readiness/liveness | Add endpoints without changing legacy `/ping` body |
| Reproducible builds/SBOM | No runtime behavior change |
| Race/fuzz testing | No runtime behavior change |

---

## 6. Improvements that must be feature-flagged or postponed until parity

| Existing behavior | Improved concept | Rule |
|---|---|---|
| Random connector choice called “Roundrobin” | deterministic round-robin / weighted routing | legacy mode retains random selection; new route type gets a new name/config |
| Float/in-memory async billing | transactional ledger + reservation/settlement | ship after parity or behind explicit billing engine version |
| Arbitrary Python interceptors | WASM/CEL/Starlark/webhook plugin ABI | Python sidecar remains for migration; no silent semantic translation |
| Telnet jCli + PB | authenticated REST/gRPC admin API | keep jCli compatibility facade until operators migrate |
| Pickled profile files | signed versioned JSON/DB revisions | provide trusted offline exporter/importer and round-trip verification |
| Required RabbitMQ/Redis topology | pluggable queue/state backends | RabbitMQ/Redis remain default through parity |
| REST proxy/Celery batch | native durable batch scheduler | exact old REST response/callback mode remains selectable |
| First-match routes only | health/quality/least-cost policy graph | add new route strategies after baseline route engine is frozen |
| Callback to arbitrary URL | egress allowlists/DNS pinning | strict secure mode opt-in first; document behavior change before defaulting |
| Broad binds/plaintext admin | TLS/mTLS and loopback defaults | compatibility deployment profile retains old defaults; secure profile becomes recommended |

---

## 7. Implementation phases and gates

No phase begins until the previous gate is measurable and green.

### Phase 0 — Baseline freeze and inventory

**Objective:** turn upstream behavior into a versioned specification.

**Create after approval:**

- `spec/compatibility/SURFACES.md`
- `spec/compatibility/HTTP_MATRIX.md`
- `spec/compatibility/SMPP_MATRIX.md`
- `spec/compatibility/JCLI_MATRIX.md`
- `spec/compatibility/PB_API_MATRIX.md`
- `spec/compatibility/AMQP_REDIS_MATRIX.md`
- `spec/compatibility/KNOWN_QUIRKS.md`
- `spec/compatibility/DEVIATIONS.md`

**Tasks:**

1. Tag baseline `upstream-jasmin-0.12-0aac58e` in the fork.
2. Enumerate all HTTP/REST fields, defaults, validation order, status/body/content-type combinations.
3. Enumerate SMPP commands, statuses, timers, TLVs, bind constraints and multipart/DLR behavior.
4. Enumerate jCli commands, prompts, profile formats and config options.
5. Enumerate every remotely callable PB proxy method, auth mode, argument/result serialization and error behavior; decide facade-versus-approved-removal per method.
6. Inventory all AMQP exchanges/queues/routing keys/properties and Redis key schemas/TTL.
7. Classify all 1,039 tests as external contract, domain behavior, infrastructure integration or Python implementation detail.
8. Record unsupported/stub behavior such as `BestQualityMTRoute` separately.

**Gate:** every public surface has an owner, fixture source and parity status; no “same functionality” item is implicit.

### Phase 1 — Differential compatibility harness

**Objective:** compare Python oracle and future Go implementation with identical inputs.

**Create:**

- `go/cmd/jasmin-compat-probe/`
- `go/tests/fixtures/http/`
- `go/tests/fixtures/smpp/`
- `go/tests/fixtures/jcli/`
- `go/tests/differential/`
- `compat/smsc-simulator/`

**Tasks:**

1. Capture golden legacy HTTP/REST requests and byte-exact responses.
2. Reuse/port SMSC simulator scenarios from `tests/protocols/smpp/smsc_simulator.py`.
3. Capture SMPP wire PDUs, statuses and sequence/correlation behavior.
4. Capture callback payloads and `ACK/Jasmin` retry behavior.
5. Add deterministic clocks/random seeds where possible; classify intentionally nondeterministic fields.
6. Build normalization only for approved fields (timestamps, generated UUIDs); never normalize real semantic differences.

**Gate:** the harness can fail on a deliberate oracle deviation and produce a useful protocol-level diff.

### Phase 2 — Go foundation and pure domain model

**Objective:** create a compilable, race-tested modular core without network behavior.

**Create:** `go/go.mod`, `go/internal/domain/*`, `go/internal/config/*`, `go/internal/envelope/*`, CI Go jobs.

**TDD order:** IDs/value objects → users/groups/credentials → connectors → message/routable → typed errors → config parsing.

**Gate:** `go test ./...`, `go test -race ./...`, `go vet ./...`, fuzz seeds and config golden tests pass; no protocol package is imported into domain code.

### Phase 3 — Routing engine parity

**Objective:** reproduce MO/MT route/filter/table behavior as a pure library.

**Create:** `go/internal/routing/` and differential fixtures derived from `tests/routing/*`.

**TDD order:**

1. routable tags/locked fields;
2. filter compatibility matrix and matching;
3. descending routing table and order-0 default;
4. static/default routes;
5. legacy random-choice route with injectable RNG;
6. failover ordering and connector-health predicate;
7. route billing plan generation;
8. interceptor table matching (execution remains a port).

**Gate:** all externally meaningful routing tests are represented; randomized tests prove selection only from configured pool, while seeded differential tests reproduce fixtures.

### Phase 4 — Legacy HTTP API vertical slice

**Objective:** first end-to-end Go path with a fake downstream connector.

**Create:** `go/adapters/httpcompat/`, `go/internal/submission/`, HTTP contract tests.

**Order:** `/ping` → auth → `/balance` → `/rate` → `/send` validation → message construction → route/bill plan → fake enqueue.

**Compatibility requirements:** byte-level bodies/statuses, validation order, GET/form/JSON behavior, DCS, content/hex-content exclusivity, tags, DLR fields, validity/schedule fields, custom TLVs, long-message segment count.

**Gate:** full HTTP matrix matches Python oracle except reviewed entries in `DEVIATIONS.md`.

### Phase 5 — SMPP codec/library spike and decision

**Objective:** prove a library can satisfy Jasmin’s client **and server** contracts before committing.

**Candidates:** `fiorix/go-smpp`, `linxGnu/gosmpp`, or a narrow internal codec/server layer if neither passes.

**Spike cases:** bind RX/TX/TRX, enquire_link/timers, malformed PDU, submit/deliver/data, SAR/UDH, all DCS modes, standard/vendor TLVs, TLS, response status mapping, reconnect and concurrent outstanding requests.

**Decision artifact:** `spec/decisions/ADR-0002-smpp-library.md` with measured gaps and maintenance burden.

**Gate:** chosen approach passes captured wire fixtures and SMSC simulator; no adapter choice based only on stars/activity.

### Phase 6 — Bus-independent SMPP client connector core

**Objective:** implement and verify outbound connector lifecycle and SMPP wire behavior behind in-memory ports; do not claim the RabbitMQ-backed submission path is replaced yet.

**Create:** `go/internal/connector/`, `go/adapters/smpp/client/`, connector contract/integration tests.

**Implement:** connector state machine, CRUD/start/stop, bind modes, reconnect, TLS, status/stats, throughput policy, expiry decisions, error retry map, custom TLV policy, submit response and long-part accounting. Drive all inputs/outputs through fake queue/state ports.

**Gate:** connector wire/soak tests survive disconnect/rebind and SMSC throttling using fake queue ports; parity status/retry tests pass. No RabbitMQ durability or message-loss claim is allowed in this phase.

### Phase 7 — RabbitMQ envelopes and mixed-mode bridge

**Objective:** introduce durable, versioned internal messages while allowing staged migration.

**Create:** `go/adapters/rabbitmq/`, `go/internal/envelope/`, `compat/python-bridge/`, `spec/events/`.

**Rules:**

- Go never decodes arbitrary pickle.
- A tightly scoped trusted Python bridge translates known legacy classes to versioned JSON/Protobuf.
- Preserve legacy exchange/routing key topology during mixed mode.
- Include the optional `publish_submit_sm_resp` contract and exact `submit.sm.resp.<CID>` payload/properties from `jasmin/managers/listeners.py`; do not treat it as an internal implementation detail.
- Use publisher confirms, manual ACK, bounded prefetch, DLQ and retry metadata.
- Add inbox/dedupe keyed by event/message ID.

**Gate:** Python→bridge→Go and Go→bridge→Python fixture round trips preserve all required fields; the Phase 6 connector now passes RabbitMQ pause/reconnect and publisher-confirm tests; optional `submit.sm.resp.<CID>` consumers receive compatible events; duplicate delivery does not double-charge or double-callback.

### Phase 8 — DLR, MO delivery and callbacks

**Objective:** reproduce asynchronous receipt and mobile-originated flows.

**Create:** `go/internal/dlr/`, `go/internal/delivery/`, `go/adapters/redis/`, `go/adapters/callback/`.

**Implement:** Redis compatibility keys/TTL, atomic transitions, exact SMSC-ID normalization (`upper().lstrip('0')` plus configured decimal/hex conversions), submit response mapping, receipt parsing, DLR levels 1/2/3, HTTP/SMPP throwers, MO multipart state under `longDeliverSm:<cid>:<ref>:<destination>` with 300-second legacy TTL, callback GET/POST, `ACK/Jasmin`, retry/failover.

**Gate:** end-to-end tests cover submit → SMSC response → terminal DLR and MO → HTTP/SMPP delivery under duplicate/reordered events and Redis reconnect.

### Phase 9 — SMPP server API

**Objective:** replace external SMPP server while sharing Go submission/routing/billing core.

**Implement:** auth and MD5 migration compatibility, IP CIDR allowlist, max binds, ban/unbind, RX/TX/TRX semantics, per-user throughput, exact SMPP error statuses, deliver_sm/data_sm back to bound clients.

**Gate:** black-box Python client and third-party simulator see contract-equivalent behavior; SMPP fuzz and race tests pass.

### Phase 10 — Billing and quota parity, then ledger mode

**Objective:** first match existing charging, then add an auditable engine.

**Parity engine:** fixed-point implementation is acceptable only if a differential rounding corpus proves identical legacy-visible decisions and representations. The corpus must cover decimal rates not exactly representable by IEEE-754, 1–100% early decrement values, multipart multiplication, balances exactly at/just below charge, repeated charges and HTTP/jCli formatting. If any result differs, compatibility mode must reproduce Python float behavior at the boundary while improved ledger mode uses fixed-point. Preserve per-segment charge, early/late split, unlimited quota and HTTP/SMPP consistency.

**Improved engine (separate version):** append-only double-entry ledger, idempotent holds/reservations, settlement on submit response, release/compensation, immutable audit events.

**Gate:** parity fixtures plus the rounding corpus match Python results byte-for-byte/decision-for-decision; failure injection proves improved mode cannot double-charge and balance reconstructs from ledger.

### Phase 11 — Management and persistence

**Objective:** preserve operator workflows while removing unsafe internals.

**Create:** `go/adapters/jcli/`, `go/adapters/adminapi/`, `go/adapters/filestore/`, `go/adapters/postgres/`, `compat/python-exporter/`.

**Implement:** jCli command/output compatibility; modern API as canonical control plane; versioned config revisions; trusted offline pickle exporter; JSON import validation; atomic activation/rollback; audit log.

**Gate:** representative legacy profiles export/import and produce identical effective users/routes/connectors; rollback restores previous revision; jCli transcripts match golden fixtures.

### Phase 12 — Native REST batch engine

**Objective:** replace Falcon/Celery proxy without changing old REST behavior.

**Implement:** Basic Auth, send/sendbatch, globals, schedule, callback/errback, per-worker QoS semantics in compatibility mode; durable batch state and modern status API in enhanced mode.

**Gate:** response and callback fixtures match; restart during scheduled batch does not lose or duplicate work.

### Phase 13 — Shadowing, performance and cutover

**Objective:** prove production suitability before removing Python.

**Steps:**

1. replay sanitized traffic to both systems with the shadow side strictly side-effect-suppressed: it must not publish a second submission, mutate quota/billing, create DLR state or emit callbacks;
2. compare route, bill, segment count, connector, PDU and callback decisions;
3. cut over leaf workers first where practical (HTTP MO/DLR throwers, DLR lookup after Redis-schema freeze, batch sender, then SMPP clients by CID); router/control-plane/persistence move last;
4. partition ownership by connector/user/CID so exactly one runtime is the side-effecting consumer for each partition—never run competing Python and Go consumers on the same queue;
5. canary one ingress/connector cohort;
6. define rollback based on config/event schema compatibility and ownership handoff;
7. run sustained load, burst, soak and chaos tests;
8. publish parity report and accepted deviations;
9. only then mark Python services deprecated.

**Required SLOs must be agreed before benchmarking:** messages/sec, p95/p99 submit latency, max queue lag, acceptable DLR latency, target connector count, simultaneous SMPP binds, recovery time and message-loss budget.

**Gate:** no high-severity parity difference, error budget met, rollback tested, and all durable events drained/reconciled.

### Phase 14 — New concepts after parity

Candidates, each requiring its own design/ADR:

- true round-robin, weighted, least-cost, quality and health-aware routes;
- multi-tenant isolation and hierarchical quotas;
- provider/SMSC capability profiles and dynamic routing scores;
- transactional billing ledger, credit limits and reconciliation;
- versioned control-plane API/UI;
- WASM/CEL/Starlark/webhook interceptors with resource limits;
- webhook signing, replay protection and egress policy;
- native scheduler/campaign engine;
- delivery analytics and route quality feedback;
- HA control-plane revisions and zero-downtime connector handoff;
- pluggable broker/state adapters only after RabbitMQ/Redis parity is stable.

---

## 8. Test strategy

### Test pyramid

1. **Pure unit tests:** domain, filters, routing, billing math, receipt parsing, config.
2. **Golden contract tests:** byte-exact HTTP/REST/jCli and encoded SMPP frames.
3. **Differential tests:** same input against frozen Python and Go.
4. **Integration tests:** RabbitMQ, Redis, optional PostgreSQL, fake SMSC and callback server.
5. **Property/fuzz tests:** SMPP decoder, DLR parser, long-message segmentation, regex/config boundaries.
6. **Race tests:** connector lifecycle, route revision swaps, quotas and correlation maps.
7. **Failure injection:** duplicate/reordered events, broker disconnect, Redis timeout, callback failure, SMSC throttle, process kill.
8. **Load/soak:** steady-state, burst, many connectors/binds, large DLR backlog.

### Compatibility scorecard

Every contract row gets one status:

- `UNINVENTORIED`
- `FIXTURED`
- `IMPLEMENTED`
- `MATCH`
- `APPROVED_DEVIATION`
- `BLOCKED`

A subsystem is not “done” based on test percentage alone. It is done when every row is `MATCH` or explicitly `APPROVED_DEVIATION`.

---

## 9. CI/CD and quality gates

After implementation starts, CI must run:

```bash
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
go test -run=FuzzSmoke ./...
```

Plus:

- Python oracle suite on Python 3.11/3.12 with RabbitMQ and Redis;
- differential contract suite;
- static analysis (`staticcheck`, `govulncheck`);
- dependency license/SBOM generation;
- container build and Compose smoke;
- protocol fuzz corpus regression;
- no push/merge if compatibility scorecard regresses.

---

## 10. Decisions required before coding

These must be agreed explicitly:

1. **Parity target:** exact baseline commit above, or another Jasmin release/branch?
2. **Compatibility strictness:** byte-exact HTTP/jCli and wire-exact SMPP, or documented semantic compatibility?
3. **Mixed deployment:** must Python and Go components coexist, or is a whole-system maintenance cutover acceptable?
4. **Default persistence:** preserve file-only default through parity, or require PostgreSQL from first Go release?
5. **Interceptor migration:** temporary Python sidecar accepted? Which future plugin model is preferred?
6. **Admin strategy:** how long must Telnet jCli/PB remain supported?
7. **Security profile:** preserve insecure defaults only in compatibility profile and recommend secure defaults?
8. **Performance SLO:** throughput, latency, connector/bind scale, DLR lag and recovery targets.
9. **Platform scope:** Linux containers/Kubernetes only, or native Debian/RPM/systemd parity too?
10. **Brand/versioning:** keep Jasmin-compatible name or publish a distinct distribution name while preserving Apache attribution?

Until these are decided, only Phase 0/1 specification work should proceed.

---

## 11. Recommended immediate next step

Do **not** start porting classes. Approve this plan, answer the ten decisions above, and then execute only **Phase 0: compatibility inventory**. The first implementation PR should contain specifications and fixtures—not production Go routing code. This prevents optimizing the wrong behavior and gives an objective definition of “same functionality.”

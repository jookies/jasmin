# Worklog

<!-- Newest entries on top. One entry per significant working session. -->

## 2026-07-26 — Macro-3 item 4: runtime connector provisioning (admin plane, SQLite)

### Done

- **#78** — authenticated `/admin` HTTP API for SMPP **connectors**: create/update/delete/start/stop, persisted to embedded **SQLite** (pure-Go `modernc.org/sqlite`), applied live through `smppc.Manager`, surviving restart. `internal/app/admin` (store + service + bearer-token handler); gateway `admin` config block (`db_path` + secret-ref `token`) mounted at `/admin`; `LoadAndApply` re-binds persisted connectors at boot.
- **docs/adr/001** records the SQLite choice.
- **Compose E2E:** `POST /admin/connectors` → binds immediately; survives `--force-recreate` (re-bound from SQLite); 401 without token.

### Decisions

- **SQLite via `modernc.org/sqlite` (pure Go), not `mattn/go-sqlite3`:** the gateway image is `CGO_ENABLED=0`; the CGO driver compiles but panics at runtime. Added the pure-Go driver (needed `go mod tidy` for a shifted transitive go.sum). ADR-001 covers node-local-SQLite vs Postgres/Redis.
- **Additive to config, apply-first:** config connectors are reserved (admin owns a separate set); create applies to the manager (validates+binds) before persisting, rolling back on persist failure so store and runtime never diverge.

### Next (finish item 4, then item 5)

- **Routes CRUD:** needs a mutable routing-table holder the submit service reads through (today `routingtable.Table` is built once). Reuse the `FilterConfig` translation from #77.
- **Users CRUD:** needs a mutable billing directory (today built once at boot).
- **Interceptor (item 5):** MO/MT user-script runner — Python subprocess by contract (scripts ARE Python); per-route config; differential vs `interceptord`. **User has not yet confirmed keeping Python here** — surface before building.
- **MO content filters** (deferred from #77): decode deliver_sm content pre-routing in modispatch.

## 2026-07-26 — Macro-3 item 3: filter-based MT routing config

### Done

- **#77** — static MT routes carry `filters []FilterConfig` (all-match, highest order wins; default route rejects filters). Types: `destination_addr`/`source_addr`/`short_message` (regex), `tag`, `user` (username→uid via directory), `date_interval`/`time_interval`. `buildRoutes` gained a username→uid resolver wired from the directory in both runtime and `--check-config`. The `routingfilter` engine already existed — pure config-to-engine wiring + tests.
- Real routing-decision test: French destinations (`^33`) → premium connector, else → default. All rejection paths covered.

### Decisions

- **MT shipped standalone; MO content filters deferred.** MO connector filtering (`filter_connector_id`) already works (#75); MO source/dest/content filters need the dispatcher to decode the deliver_sm content pre-routing (a bridge round-trip before route selection) — a separate slice, not blocking MT.
- **User filter resolves username→uid at build time** via the runtime directory, so a filter referencing an unknown user fails `--check-config` closed rather than silently never matching.

### Next (Macro-3 remaining)

- **MO content filters:** decode source/dest/short_message from the routable in `modispatch` before route selection (add a bridge content-projection or reuse `decode`), then reuse the same `FilterConfig` translation for MO routes.
- **Admin plane (item 4):** ARCHITECTURE DECISION PENDING — routes/users/connectors are frozen at boot today; runtime CRUD needs a mutable store + live-apply into the managers (connector add/remove already supports live via `smppc.Manager`; routes/users need mutable holders). jCli byte-parity console stays deferred.
- **Interceptor (item 5):** MO/MT user-script runner (Python subprocess by contract), per-route config, differential vs `interceptord`.

## 2026-07-26 — Macro-2 finished (plan 008): terminal-DLR loop closed + SMPPS MO proven

### Done

- **#76** — two deliverables. **Terminal DLR (L2/L3) loop closed:** (1) submit-side `dlr:<msgid>` store (`core/dlr.RequestStore`, wired from DLRLookup `redis_url`; new per-connector `dlr_expiry`); (2) **root-cause fix** — the vestigial reply-to `submit.sm.resp.<user>` (nothing consumes it) was published *mandatory* → `NO_ROUTE` → retried forever → the outbox's in-order predecessor gate wedged `dlr.submit_sm_resp` + billing behind it; the dispatcher now drops `NO_ROUTE` on the best-effort `SUBMIT_RESPONSE` event (legacy publishes the reply non-mandatory); (3) DLRLookup `OnError`→slog. **SMPPS MO leg proven:** gated integration test of the full throw chain (`deliver_sm_thrower.smpps` → mothrower → bridge → MOSink → bound receiver ESME). Compose exposes rabbitmq on host 5673 for host-run gated tests.
- **Compose E2E DLR:** submit `dlr-level=2` → `dlr:<msgid>` → `submit_sm_resp` → `queue-msgid:FAKE-N` mapping → injected receipt → correlated → HTTP callback with legacy params.
- **Compose E2E SMPPS MO:** gated test PASS (gateway stopped so it doesn't compete for the shared `deliver_sm_thrower` queue).

### Decisions

- **Best-effort reply-to, not delete-the-event:** keeping the `SUBMIT_RESPONSE` event (dropped only on genuine `NO_ROUTE`) preserves the path for a future reply consumer (e.g. an SMPPs sync waiter) while matching legacy's non-mandatory publish. Every must-route key stays a hard failure.
- **SMPPS MO verified via a gated integration test, not a new compose binary:** the full chain over a real broker to a bound receiver is more durable and CI-runnable than an ad-hoc ESME container. Note the shared-queue gotcha: the running gateway's own mothrower competes, so the local run needs the gateway stopped (CI has no gateway running).

### Next (Macro-3, plan 008 Steps 6–8)

- **Content filters in config (item 3):** `RouteConfig` gains user/group/source/destination/content filter fields (MT + MO); the `routingfilter` engine + all constructors already exist — this is config wiring + golden tests. MO content filters also need the bridge to expose the deliver_sm content pre-routing.
- **Admin plane (item 4):** authenticated HTTP CRUD for users/routes/connectors + durable persistence + live apply; jCli byte-parity console stays deferred.
- **Interceptor (item 5):** MO/MT user-script runner (Python subprocess by contract), per-route config, differential vs `interceptord`.

## 2026-07-26 — Macro-2 slice 1 (plan 008): deliver_sm ingestion — MO + receipt produce sides

### Done

- **docs/plans/008** authored: the five remaining functional gaps (MO, terminal DLR, filter routing, admin, interception) with frozen-contract notes and file-level steps.
- **Inventory finding that reshaped the plan:** the downstream MO/DLR machinery already exists and is idle — DLRLookup fully handles `dlr.deliver_sm` L2/L3 (correlation via `queue-msgid:<smsc-id>` Redis keys, `dlr_thrower.*` publish), mothrower+MOSink deliver HTTP/SMPPS, the router's `deliver.sm.*` consumer runs but dead-ends at a `Reject(true)` stub (`core/router/logic.go:85`), and all routing-filter constructors are implemented. The rewrite gap was the *produce* side.
- **#74** — `deliver_sm` ingestion in the SMPP client (the `deliver_sm_event` port): receipt leg → `dlr.deliver_sm` (ParseReceipt + CodeReceiptID + new `dlr_msg_id_bases` connector key, legacy DLR content); MO leg → `deliver.sm.<cid>` (bridge `encode_routable_deliver_sm` rebuilds the PDU with smpp.pdu from the re-encoded frame and pickles `RoutableDeliverSm` — legacy body by construction) + SMS-MO audit line; ROK/RUNKNOWNERR response contract; SAR/UDH parts follow the legacy redis-less drop branch (critical line) until Step 5. Supporting: smppwire `deliver_sm_resp`, amqpcompat `FieldBool` + `deliver.sm.*` route family, `RouterPB_deliver_sm_all` pre-declared at boot, fake-SMSC `/inject/mo` + `/inject/dlr` triggers (compose :8288).
- **Compose E2E:** injected MO → `ESME_ROK` + SMS-MO line + routable buffered in `RouterPB_deliver_sm_all` (1 msg awaiting the router); injected receipt → `ESME_ROK` + consumed by DLRLookup.

### Decisions

- **Pickle parity by construction, not by mapping**: the bridge decodes the received wire frame with `smpp.pdu` itself before wrapping/pickling — no Go→Python param translation to drift. Differential compares semantically (`RoutableDeliverSm` stamps `datetime.now()`, so byte-compare is impossible).
- Reused the existing `core/dlr` receipt parser/msgid coder instead of a second port (started one, deleted it on discovery).

### Next (in dependency order)

- **Router MO dispatch (plan 008 Step 3):** replace the `logic.go` stub — decode `connector-id` header, route via `routingtable` MO direction (default/static + connector filter first; content filters need bridge decode and come with Step 6), publish `RoutedDeliverSmContent` (needs a bridge `encode_connector_list` action for the pickled dst-connectors header) to `deliver_sm_thrower.*`. **Gotcha discovered:** the router service's `OpenRouterSubscriptions` also consumes the billing queue, which the outbound `lateBillingConsumer` already owns in-gateway — wiring the router in-process must split the deliver consumer from the billing consumer or they'll compete.
- **Terminal-DLR completion (Step 4):** the submit path never writes the DLR request (`dlr:<msgid>`) or the resp-leg `queue-msgid:<smsc-id>` mapping to Redis — that's why the injected receipt correlated to nothing. Add the `/send` dlr-url/dlr-level Redis store + resp-leg mapping write, then the full loop (submit dlr-level=2 → receipt → HTTP callback) closes.
- MO route config (`mo_routes[]` with http/smpps connector defs) in gateway config; then Steps 5–8 per plan 008.

## 2026-07-26 — docs/plans/007 P1: shadow-safe outbound (durable AMQP, /health, TLS+secrets, reject logs)

### Done

- **#70** — Step 5: opt-in durable AMQP via top-level `amqp_durable_topology` threaded through every declaration path (Topology flag + the late-billing consumer's raw declares, which the E2E drill caught 406-crash-looping). Publishes were already persistent. Default stays false = legacy txamqp parity (mismatched redeclare is a 406; keep off on a shared legacy vhost). Broker-restart drill: 10 burst submits, `restart rabbitmq` mid-drain — queue + persistent message survive, consumer re-attaches, 10/10 `ESME_ROK`.
- **#71** — Step 6: `GET /health` (200/503 + per-check JSON): Postgres ping, AMQP connection, pickle-bridge liveness (new `ping` bridge action; probe bounded 3s in a goroutine because the bridge mutex is not ctx-aware), required-connector bind state. Compose healthcheck switched to it. Drill: stop smppsim → 503 `DISCONNECTED`; stop postgres → 503; restore → 200.
- **#72** — Step 7: `env:NAME` / `file:/path` / `literal:` secret refs resolved at LoadConfig (fail-closed `--check-config`) for postgres_dsn/amqp_url/connector passwords/dlr URLs/smpps passwords; top-level `https{cert_file,key_file}` → HTTP API over TLS; `smpps.tls_cert_file/tls_key_file` → SMPPS-over-TLS (TLS 1.2+). E2E: containerized gateway with env-ref config served `https:///send` → `ESME_ROK`; plain HTTP rejected.
- **#73** — terminal-reject visibility: every no-requeue drop (poison decode, custom-TLV reject, malformed part key) now logs an ERROR line with the message id on the sm-listener logger — the silence that hid the #67 empty-bytes drop. Explicitly not a byte-parity line (legacy reject lines stay deferred per O-007).
- `docs/plans/007` → status **done** (P0+P1 landed; Macro 2/3 + cutover gate remain out of scope).

### Decisions

- **Durable default = false (legacy parity), opt-in true for Go-native brokers** — a hard flip would 406 the bridge-edge shadow on the shared legacy vhost; the example config/compose (own broker) enable it.
- **Health bridge probe degrades instead of hanging or killing**: `Bridge.Ping` entered after its deadline returns early without touching the subprocess; only a truly unresponsive bridge (post-send timeout) gets killed by the ctx watch.
- **Secret-ref syntax `env:`/`file:`/`literal:`** over `${VAR}` expansion — no collision with legitimate `$` in passwords, exact fail-closed semantics, greppable.
- **Certs open at boot, not at `--check-config`** — configs must validate on machines without the key material.

### Next

- Ops: point a deployment copy of the config at a real SMSC (creds via `env:`/`file:` refs now), raise `submit_sm_throughput`, run behind the durable vhost; watch `/health`.
- Macro 2/3 (unchanged): MO ingestion, terminal DLR L2/L3, filter routing in `RouteConfig`, multipart-billing decision.
- O-007 logging breadth continues: DLR/MO thrower `.log` files, router, byte-exact legacy reject/requeue lines once the Go retry model exists.

## 2026-07-26 — docs/plans/007 P0: outbound MT path deployable (4 PRs) + silent-submit-drop fix

### Done

- **#66** — Step 1: reference runtime config `configs/gateway.example.json` (`role: http+smppc`, full outbound block, one fully-specified SMPPClientConfig connector with explicit TON/NPI src 2/1 dst 1/1, `required_connector_ids`, inherited-broker `dlr_lookup`/`dlr_thrower`, `submit_audit_log`) + `configs/README.md`. Verified `--check-config` → `configuration: ok`.
- **#68** — Step 2: `docker/Dockerfile.gateway` — golang:1.26 build stage; python:3.12-slim runtime with the hash-pinned bridge deps (`compat/requirements-pickle-bridge.txt`) + `scripts/pickle_bridge.py` + `jasmin/` source under `WORKDIR /app` (bridge walks up from cwd), baked example config, non-root. In-container `--check-config` + bridge imports verified.
- **#69** — Steps 3–4: `docker-compose.gateway.yml` (postgres:16 + rabbitmq:3-management-alpine + redis:7-alpine + gateway, healthcheck-gated) + hermetic `cmd/jasmin-fake-smsc` / `docker/Dockerfile.fakesmsc` (scratch image on `internal/transport/smppwire`; accepts any bind, ESME_ROKs submits). **E2E proven on the live stack:** bind, `/send` with AND without `from` → `ESME_ROK` at 1 msg/s pacing, SMS-MT audit lines, `submit_attempts` `RESULT_COMMITTED` + `submit_results` `SUCCESS/ESME_ROK` in Postgres.
- **#67** — bug found via that E2E: submits with any EMPTY byte param (e.g. `/send` without `from=`) were **silently dropped** — CPython pickles `b''` at protocol 2 as a `__builtin__.bytes` GLOBAL (non-empty uses `_codecs.encode`), `SubmitSMUnpickler` didn't allowlist `bytes` (DeliverSM's did) → `ErrSubmitSMPoison` → reject-no-requeue, no attempt row, no log. One-line allowlist fix + `TestDecodeSubmitSMEmptyBytes` differential (red on old, green on new; oracle needs only pinned smpp-pdu3).
- `docs/plans/007` → status **active**, P0 marked done; step-1 path note (misc/config → configs).

### Decisions

- **`configs/`, not the planned `misc/config/`** — `misc/config/` is inside the frozen-oracle tree fingerprint (`scripts/compat/verify_baseline_tree.py`: `jasmin/`, `tests/`, `misc/config/`); adding files there broke 3 CI checks (`oracle_tree=changed files=203/201`). Any new Go-side config artifacts must stay out of those trees.
- **Pinned bridge runtime, not full requirements.txt, in the image** — bridge-reachable jasmin modules (`routing.Bills`, `routing.jasminApi`, `routing.Routables`) are stdlib/smpp.pdu-only; CI proves pinned `smpp-pdu3` suffices. Twisted/txredisapi/falcon are legacy-daemon surface; smaller image + supply chain.
- **Own fake SMSC instead of an external simulator image** — the planned `melroselabs/smscsim` doesn't exist on Docker Hub; `cmd/jasmin-fake-smsc` reuses `smppwire` and mirrors the integration test's contract, keeping the stack hermetic.
- **Step 4 "real SMSC creds" = ops action** on a deployment copy of the config; the repo keeps simulator-wired placeholders (secrets injection is P1 Step 7). Simulator-verified bind satisfies the plan's "or a real SMPP simulator" arm.
- Merge cadence held: the 3 fast CI checks gate merges; `frozen-python-regression` (scripts/ change in #67 runs it) left to complete post-merge.

### Next

- **P1 (docs/plans/007 Steps 5–7):** durable AMQP topology (`amqpcompat/topology.go` `durable=false` → true + persistent publishes; broker-restart survival test); real `/health` readiness (PG ping, AMQP channel, bridge liveness, required-connector bind state); inbound TLS + secrets injection (env/file refs for DSN/AMQP/SMSC passwords).
- **Logging follow-up worth pulling forward:** the #67 drop was invisible because submit-lifecycle reject lines aren't implemented yet (O-007 backlog) — a poison-reject should log.
- To point the stack at a real SMSC: copy `configs/gateway.example.json`, set connector host/port/system_id/password + TON/NPI per the SMSC, raise `submit_sm_throughput`; validate with `--check-config`.
- Local repro: `docker compose -f docker-compose.gateway.yml up --build`; send via `curl 'http://localhost:1401/send?username=smppuser&password=password&to=15551234567&content=hi'`.

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

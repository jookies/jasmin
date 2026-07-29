# Worklog

<!-- Newest entries on top. One entry per significant working session. -->

## 2026-07-28 — Worktree sync review, and the QoS ceiling that was never enforced

Goal (user): sync the uncommitted admin/UI work made in another session, prove
nothing was broken, merge it, then close Go gaps in the SMPP client, SMPP
server, HTTP front door, interception, ops and billing surfaces.

### Sync review

Five parallel reviewers over the 103-file worktree: new adminweb handlers, Go
core/runtime diffs, the compatibility registry, the frontend, and an adversarial
pass hunting for weakened tests. Everything was verified against the source
before acting on it — two reported findings did not survive that check.

**Confirmed and fixed** (each with a regression test verified to fail without
the fix):

- **Deleting a group re-enabled its suspended users.** `removeGroup` cannot
  reach `userGroup`, so a member kept resolving a gid with no `groupDisabled`
  entry and the zero value read as "not disabled". Authentication now fails
  closed on a dangling group reference, and `DeleteGroup` cascades to the
  group's users the way the oracle does. Worth noting: the reviewer proposed
  refusing the delete while users reference the group — checking
  `perspective_group_remove` (`jasmin/routing/router.py:917`) showed legacy
  *cascades*, so that fix would have been a silent deviation.
- **Saving a user widened its SMPPs bind account.** The mirror rebuilt the
  account wholesale, blanking `ip_whitelist` (which means "any IPv4") and
  resetting `set_source_address`, `set_dlr_level` and `set_priority` to
  permissive defaults. Editing a balance now overlays only the form's fields.
- **Connector reconcile removed without stopping first**, which
  `smppc.Manager` refuses, so a started admin connector silently kept its old
  config or survived its own deletion while stopped ones rebuilt.
- **A failed removal stranded the entity permanently**: the `applied`
  bookkeeping was retained, so the re-add skipped it as "could not be
  refreshed" and the user stayed on 401 until a process restart.
- **J-017 claimed fixture-backed evidence it cannot have** (autoload is boot
  behaviour, not a transcript). The matrix now names its real Go-test evidence
  and records two fixture coverages narrower than the column claims.

**Not defects.** The reported "cleartext password stored for the first time" is
pre-existing and by design — `smppsserver.UserConfig` documents plaintext
because legacy md5s it at bind time. And the registry change is a *repair*, not
a weakening: the validator could not parse `JCLI_MATRIX.md` at all before it
(prose in the status column), so the 18 jCli rows were never actually counted
as MATCH.

### Delivered — the QoS ceiling

`http_throughput` and `smpps_throughput` were fully plumbed through jCli, the
web BFF and the outbound config — and enforced nowhere. `internal/app/outbound/
config.go` said so in a comment ("Not enforced yet"), and the
`throughput_error_count` metric existed with no writer. An operator could
rate-limit a customer, see the limit reported back in `user -s`, and have it do
nothing.

New `internal/core/throughput` implements the ceiling as legacy does — minimum
spacing between *accepted* submits, not a token bucket — and it is checked in
`SubmitService.Submit` after routing and before billing, so a refused submit is
never charged. Five Python behaviours are preserved deliberately and recorded as
Q-022: a quota of 0 (or negative) means unlimited rather than blocked, the first
submit is always exempt, an over-rate submit is rejected rather than delayed, a
rejection does not advance the clock, and the state is per-process.

HTTP answers 403 `Error "User throughput exceeded"` and increments
`throughput_error_count`; SMPPs answers `ESME_RTHROTTLED` without dropping the
bind. Ceilings are tracked per ingress, so HTTP traffic cannot consume a user's
SMPPs allowance.

### Verified

`go build`, `go vet`, `PYTHON_PATH=.venv-oracle go test -count=1 ./...` all
clean; `go test -race` on the changed packages clean; registry validator PASS
(205 contracts, 40 finished); frontend `tsc` and production build clean with
`bundle.sourcehash` reproducing.

Note: without `PYTHON_PATH` pointing at `.venv-oracle`, the picklecompat bridge
test fails on `No module named 'smpp'`. That is an environment gap, not a
product defect — the venv is rebuilt with
`python3 -m venv .venv-oracle && .venv-oracle/bin/pip install -r requirements.txt`.

### Next

Still open on 11-16: the native interceptor runner (Python subprocess remains),
component loggers (router, http-api, dlr, sm-listener, throwers), and CDRs.
The dominant constraint is unchanged and is not code: 8 of 58 Release A
contracts are finished and 29 of 39 macro rows have no executable command.

## 2026-07-28 — Webadmin full-capability and operations pass

Goal (user): implement the useful capabilities already present in the Go API
and management core in the redesigned browser admin, preserve the improved UI,
and make creation-time defaults reflect their effective values.

### Delivered

- Added browser CRUD for groups, saved filters and saved HTTP destinations.
- Expanded user editing to the full MT/SMPPs credential, permission, filter,
  default-source, quota, throughput and bind-policy surface. Username now
  immediately drives the effective external ID during creation; empty
  balance/quota and filter defaults are shown inline.
- Expanded SMPP connector editing to the runtime's addressing, timeout/retry,
  TLS, submit/PDU, DLR/logging/topology and custom-TLV configuration.
- Merged config-owned connectors, MT/MO routes, users, groups and SMPPs bind
  accounts into the web inventory with explicit read-only source badges.
- Added saved-filter insertion to route forms and saved HTTP-destination
  insertion to MO routes. An empty MO source connector now correctly means any
  inbound connector.
- Added SMPPs unbind/ban actions and confirmation-gated flush actions for
  admin-owned MT routes, MO routes and interceptors.
- Added Operations: HTTP/SMPPc/SMPPs counters, exact durable message status,
  balance/rate diagnostics and a confirmation-gated real test submit.
- Added named configuration profile save/restore. Group, user and connector
  services now reconcile previously applied live state during restore instead
  of failing on duplicate resources.
- Kept config passwords write-only and test-submit passwords transient.

### Verification

- Added admin/adminweb tests for advanced users and SMPPs mirroring, group and
  library CRUD, config-owned inventory, session actions, route/interceptor
  flushes, operations tools, profile live restore and error mapping.
- Targeted Go suites pass for admin, adminweb, gateway, outbound and stats.
- `npm run build` passes and refreshes the embedded production bundle.
- Real-browser QA passed at desktop and 390 px mobile widths. Verified
  navigation, operations layout, read-only rows, destructive controls, saved
  filter insertion, responsive drawers and the username/effective-default
  behavior in the create-user form.

### Follow-up

The compose-stack provisioning/restart drill in plan 012 remains formal release
evidence. Commercial/CDR and user/group charge-precedence proof remains plan
015 G2; it is not an admin-UI gap.

## 2026-07-28 — Python deprecation readiness audit and overnight gate

Goal (user): record the current state in the local project knowledge base, give
Claude an executable overnight scope, and determine when the Python Jasmin
deployment can be deprecated.

### Decision

The Go gateway is ready for continued staging and partitioned shadow traffic,
not for a Python-deprecation announcement. The legacy Python implementation can
be feature-frozen now. Deployment deprecation waits for a clean attested
candidate, commercial/CDR correctness, deployed-scope compatibility, operations
coverage, shadow/canary soak and a successful rollback drill. The frozen Python
tree remains the compatibility oracle after deployment cutover.

The planning window remains the project's existing 12–18 week formal-cutover
bar: earliest conditional deprecation 2026-10-20 through 2026-12-01; earliest
end-of-support January–February 2027 after one stable Go release and 30–60 days
of rollback retention. These are gate-dependent windows, not promises.

### Evidence

- Compatibility registry is structurally green: 205 total contracts; 37
  `MATCH`, 3 `GO-COMPLETE`, 60 `GO-PARTIAL`, 105 `INVENTORIED`.
- Release A has 58 unique required contracts: 8 finished, 50 unfinished.
- `GO_MACRO_TESTS.csv` has 39 scope/mode rows; 29 have no executable command or
  mandatory-test list. Only `registry`, `outbound-a`, `outbound-b`, and `dlr`
  currently have executable coverage.
- Full Go suite passes when the full oracle interpreter is explicit:

  ```sh
  PYTHON_PATH="$PWD/.venv-oracle/bin/python" go test -count=1 ./...
  ```

- Running the same suite without `PYTHON_PATH` fails only
  `internal/transport/picklecompat:TestAMQPFixtureDecoding` because system Python
  has no `smpp` module. The package passes with `.venv-oracle`.
- `go vet ./...`, the structural registry validator, JSON-schema validation,
  the signed-evidence validator tests, the focused registry wrapper, and the
  embedded UI source-hash check pass.
- Frontend production build passes. Vite warns that the main chunk is about
  1.52 MB minified, so bundle splitting is a performance follow-up rather than a
  cutover correctness failure.
- The current worktree is large and uncommitted. The candidate-evidence runner
  correctly refuses to attest such a tree.
- Billing already has route/part charging, early/late billing, quota
  enforcement, persistence and group state. CDRs and a release-grade
  HTTP/SMPPs prepaid/postpaid/user-group E2E are genuinely missing.
- The gateway runtime still has Python for `interceptor_runner.py` and the
  Docker healthcheck. The message/pickle hot path is native Go.
- Admin persistence is node-local SQLite; a multi-node target still needs an HA
  control-plane decision.

### Knowledge-base changes

- Added [plan 015](plans/015-python-jasmin-deprecation-gate.md): milestone
  definitions, G0–G5 deprecation gates, timeline, Claude overnight tasks,
  acceptance criteria and guardrails.
- Reconciled `docs/STATUS.md` with the current jCli/group/admin state and added
  the evidence-based deprecation decision.
- Reconciled plan 012: group CRUD/live reconciliation is implemented in the
  current worktree; commercial group-billing proof belongs to plan 015 G2.
- Repaired the jCli compatibility registry so status cells contain exact
  machine-readable `MATCH` values. Removed the 18 finished jCli rows from the
  unfinished ownership ledger and updated validator expectations. Registry and
  compatibility-tool tests pass.

### Local runtime snapshot clarification

Before the user clarified that "local database" meant the documentation
knowledge base, an additive SQLite profile snapshot named
`pre-python-deprecation-audit-2026-07-28` was saved through jCli. It contains a
copy of the current admin configuration and applies no changes. The existing
`known-good` profile was not overwritten. The extra snapshot is retained unless
the maintainer explicitly asks to remove it.

### Claude tonight

The ordered scope is in plan 015: preserve and verify the dirty worktree,
finish knowledge-base reconciliation, make `smpps`/`routing`/`mo`/`control`
macro gates executable, write the first-production CDR contract, and report the
readiness delta. It explicitly forbids removing the Python oracle, fabricating
candidate evidence, implementing the partner-onboarding backend, changing
production exposure/secrets, or discarding user changes.

## 2026-07-28 — jCli console finished against a real recording of the frozen oracle

Goal (user): run the stack, merge the pending UI work, then "finish jCli console, should be ready 100%. If something will block us, unblock in best possible way."

### The blocker was not real

Plan 013 Step 1 was recorded as blocked on the frozen Python stack. It is not: `python3 -m venv .venv-oracle && .venv-oracle/bin/pip install -r requirements.txt` imports the whole stack, every `jasmin.protocols.cli` manager included. Only `compat/requirements-baseline.lock` is broken (`--require-hashes` rejects coveralls' unpinned `coverage[toml]`). The same venv also fixes the `picklecompat` bridge differential, so with `PYTHON_PATH=$(pwd)/.venv-oracle/bin/python` the entire suite is green locally — which supersedes plan 012's "two incompatible groups" guidance.

Capturing usable transcripts then needed three things the plan had not anticipated: an isolated AMQP vhost (a running Go gateway declares `messaging` durable and the frozen stack declares it transient, so connector mutations died mid-transcript), a logging-layer redirect for the `/var/log/jasmin` paths the frozen code opens at mutation time, and a longer settle window so a slow PB reply is not recorded as an empty one. All three failed *silently*, producing fixtures that looked fine and enshrined an environment failure as contract.

### What the recording proved

The console is a Twisted telnet terminal, not a line server. It opens with IAC negotiation and `ESC c` / `ESC [ 4 h`, every line ends with `\r\r\r\n` (four bytes), input is echoed, and `initializeScreen()` suppresses the prompt so **`Username: ` is never emitted on connect**. The existing Go console got all of that wrong while Steps 2–3 were marked done — exactly what "asserted, not proven" was warning about.

### Done

- **18 fixtures captured, all 18 replay byte-for-byte.** 17 of 18 matrix rows are `MATCH`; J-006 (`--smpp-unbind`/`--smpp-ban`) is the only unimplemented command.
- **Every manager**: group, user (+ the whole credential key space), smppccm, filter, httpccm, mtrouter, morouter, mo/mtinterceptor, stats, persist/load, help, completion.
- **Groups became a first-class entity** (plan 012 Step 6, closing that plan). Billing was already group-aware; nothing could create a group.
- **Named filter and httpcc registries** without reversing ADR-003: routes embed a resolved copy, exactly as the oracle pickles the filter object into the route.
- **persist/load are real named snapshots** (`admin_profiles`), which withdrew D-002. An operator rolling back to a known-good profile gets that profile.

### Bugs found outside the console

- **The HTTP front door enforced no user credentials.** Password only: no enabled check anywhere, and `mtcredential` (the tested port of `HttpAPICredentialValidator`) wired only for SMPPs binds. Disabled users and disabled groups are now refused at authentication; the per-authorization/value-filter half is still unwired and tracked.
- **Connector defaults diverged from legacy, on the wire.** Source TON/NPI 0/0 and destination 0/0 instead of NATIONAL/ISDN and INTERNATIONAL/ISDN, plus wrong `trx_to`/`res_to`/`pdu_red_to`, and `enquire_link` running off the PDU read timer.
- **A static MO route demanded a `filter_connector_id`**, making a legal legacy `StaticMORoute` unexpressible.
- **The console listed no config-owned entities**, so the normal config-file deployment looked like an empty gateway.
- **The admin UI bundle CI guard is flaky** — the minifier is not deterministic across runs, so byte-comparing a rebuilt `dist` can fail spuriously.

### Decisions

- **D-003**: `smppccm -s` prints the bind password, matching the oracle. Byte-parity and redaction are mutually exclusive; the console is already an authenticated privilege boundary and scripts read the field. Reversing it is a one-line change that fails J-012 — the deviation cannot be taken silently.
- **Transcript timestamps are normalised** on both sides of the replay. It is the suite's only normalisation; the alternative is a fixture that passes on one machine.
- **`stats` rows with no counter behind them** report 0/ND and are listed exhaustively in `unbackedStatsFields`, because a zero meaning "not counted" is otherwise indistinguishable from "nothing happened".

### Next

- J-006 needs a session-control surface in the Go SMPPs server (drop a bound session on demand).
- Wire `mtcredential.ValidateSend` into the HTTP path, with captured fixtures for its rejection text and ordering; `HTTP_MATRIX` H-004/H-005 claim MATCH and do not hold for those branches.
- Replace the adminweb bundle byte-comparison with a source-hash marker.

## 2026-07-27 — Admin plane coverage: MO routes + interceptors go live-mutable (plan 012 Steps 1–4)

Goal (user): "implement both" — a full jCli alternative **and** the same functionality on the web side. Architecture decision recorded in [plan 012](plans/012-admin-plane-full-coverage.md) + [plan 013](plans/013-jcli-console.md): `internal/app/admin` is the single management core; jCli, the `/admin` JSON API and the web UI are three faces over it, so no business logic may live in a handler or a command.

### Done — runtime first, because the UI was never the blocker

- **MO dispatch is live-swappable** (`modispatch`): routes moved behind `atomic.Pointer[routeTable]`, `prepareTable` extracted, `ApplyRoutes(ctx, adminRoutes)` added with config orders reserved. Proven: swap while dispatching under `-race`, reserved-order rejection, and **no partial apply** (bad regex / bad connector / duplicate order / bridge failure all leave the previous table live).
- **Interceptor tables are live-swappable** (`interceptor.AtomicTable`), both directions. `core.SubmitServiceDependencies.InterceptorTable` became the `InterceptionTable` interface so a fixed or atomic table both satisfy it.
- **MO routes + interceptors are admin entities** end to end: SQLite tables, services, gateway provisioners, `/api/mo-routes` + `/api/interceptors`, and UI pages. The MT/MO/interceptor CRUD shape (order-keyed identity, opaque spec, apply-then-persist) was factored into `admin/ordered_specs.go` rather than written a third time.
- **Verified live** on the compose stack: created an MO route through the API, injected an MO, watched it hit the new destination while unmatched traffic still fell to the config default; restarted and confirmed re-apply from SQLite. Added an MT interceptor at runtime — a matching send flipped from `Success` to `Error "request rejected by filters"` with no restart, survived a restart, and deleting it restored delivery.

### Decisions

- **MO dispatch now starts when the admin plane is enabled even with zero config MO routes**, so routes can be added at runtime. `NewService` accepts an empty set; `ValidateConfig` still requires one (the config-file contract is "if you declare mo_routes, declare at least one"). An MO with no matching route is unroutable — the same outcome as having no dispatcher.
- **Interceptor editing is opt-in and off by default** (`admin.allow_interceptor_editing`). The scripts are arbitrary Python on the gateway host, so enabling it makes an admin session equivalent to shell access. When off, the endpoints 404 and the UI hides the section entirely (the capability is advertised on `/api/session`); when on, the script runner subprocess starts (a table swapped in later needs something to execute it) and every mutation logs at WARN with the username and remote address.
- **Composite id for interceptors** (`mt:10` / `mo:10`): direction+order is the identity, and Refine needs one scalar.

### Also this session — Steps 5, 7, 8 (same branch, PR #102)

- **SMPPs bind users are admin-managed** (Step 5). `smppsserver.Directory` holds an immutable snapshot behind an atomic pointer, swapped by `ApplyUsers`; the server and submit handler keep the same `*Directory`, so nothing downstream changed. Proven against the live stack with a hand-rolled `bind_transceiver`: an account created through `/api/smpps-users` binds (`ESME_ROK`), wrong password and unknown system_id are refused (`ESME_RINVPASWD`), the config-owned account survives the swap, deleting refuses the next bind, and a recreated account still binds after a restart. The example config/compose had no SMPPs *server* block at all, so this was previously unexercisable locally — added one on host loopback.
- **ADR-003 — filters stay inline** (Step 7), rather than porting jCli's named `filter` registry. Inline avoids a second identity space and dangling-reference handling, and nothing in the deployment reuses a filter. Recorded as deviation D-001 with a "revisit when" trigger.
- **jCli matrix now maps to the admin plane** (Step 8). `JCLI_MATRIX.md` gains an "Admin-plane equivalent" column kept deliberately separate from `Status` — capability reachability is a different question from telnet transcript parity. `DEVIATIONS.md` went from "no deviations" to D-001 (inline filters) and D-002 (`persist`/`load` obsolete by design), both pending owner approval.

### Next

- **Plan 012 Step 6 (groups)** is the only open item in that plan, and the riskiest: it adds a new domain concept (config users have no group today, which is why the `group` filter is deferred at `filters.go:64`) and touches billing charge precedence. It must be differential-tested against the frozen oracle — and the local `python3` has no `smpp` module, so the oracle venv (`compat/requirements-pickle-bridge.txt`) has to be installed before that work can start.
- **Plan 013: the jCli console** — transcript-capture harness first, then session/auth, read-only managers, then mutating verbs.
- Remaining admin-plane gaps versus jCli, now explicit in the matrix: J-003 (groups), J-005 (per-authorization user credential fields), J-006 (SMPP session control).

## 2026-07-27 — Admin web UI (#3.3): embedded React SPA over a Go BFF (plan 011)

Goal (user): the next roadmap item after the native pickle codec — a browser UI to set up and manage the gateway. Continued from a half-finished session: config/runtime wiring existed, `internal/app/adminweb` held HTMX-era stubs that did not compile, and `web/` had a Refine SPA scaffold with only a connectors page.

### Done

- **Reversed the stack decision, and rewrote ADR-002 to match.** The draft ADR specified server-rendered `html/template` + vendored HTMX; the started implementation had already moved to a React SPA. Kept the SPA (Refine gives list/form/validation/auth scaffolding that four resources' worth of hand-rolled templates would have to reinvent) and rewrote the ADR to record the real decision, with HTMX as the alternative that lost and the accepted costs stated (Node at build time, a committed `dist/`, ~570 kB gzip).
- **`internal/app/adminweb` is now a JSON BFF + SPA server.** `/api/login|logout|session|health` plus Refine simple-rest resources for connectors, routes and users; unmatched `/api/` paths are JSON 404s, every other GET serves the `go:embed`-ed bundle (index fallback for deep links, `immutable` caching for hashed assets, `no-store` for the shell).
- **Auth reworked from redirects to JSON.** Signed session cookie (HMAC-SHA256, boot-ephemeral key, `HttpOnly`/`SameSite=Strict`/`Secure` under TLS); CSRF moved from a form field to the `X-CSRF-Token` header; `requireSession` answers 401/403 and never redirects, because the SPA owns navigation. Login compares **both** username and password constant-time via SHA-256 digests.
- **Health probe shared.** Extracted `Runtime.healthProbe()` from `healthHandler` so `/health` and the dashboard cannot drift.
- **SPA completed**: MT routes (with a type-dependent filter editor), users, a dashboard over `/api/health`, and connector Start/Stop row actions; built into `internal/app/adminweb/dist`.
- **Build hygiene**: deleted a stale compiled `vite.config.js` that shadowed the `.ts` source (it still wrote `dist/` into `web/`, so nothing would have been embedded), dropped the composite `tsconfig.node.json` that emitted it, and gitignored `node_modules`/`*.tsbuildinfo` while re-including `internal/app/adminweb/dist` past the repo's Python-era `dist` pattern.
- **Verified live** against RabbitMQ + a throwaway Postgres + the repo's fake SMSC: full auth-gate matrix; created a connector, toggled it, deleted it while running; created a user and a filtered route through the UI's API, then submitted through the **public** port as that user and saw the fake SMSC log `submit_sm → ESME_ROK` and the gateway write the `SMS-MT` audit line.

### Decisions

- **BFF, not the existing `/admin` bearer API.** A browser-held admin token is an auth smell, and the bearer API's opaque `spec_json` shapes would push route/user marshalling into client JS. The BFF builds `outbound.RouteConfig`/`UserConfig` server-side from typed fields and maps the services' sentinel errors once.
- **Write-only secrets both directions.** Connector bind passwords are stripped from every response and an empty value on update keeps the stored one; user passwords are hashed server-side into `password_sha256` and never echoed. Partial `PATCH` starts from stored state, so the Start/Stop toggle sending only `desired_started` cannot blank out a config.
- **Delete stops first.** `Service.DeleteConnector` refuses a running connector ("must be stopped before removal") — found live, where the UI's delete button would just 400. The handler now stops it first, since a confirmed UI delete is an explicit act.
- **Order is a route's identity**, so it is locked on edit (renumbering = delete + recreate), matching `RouteService`.

### Local env + in-browser verification (same session, after the user asked to run it)

- **Brought up `docker-compose.gateway.yml`** (postgres/rabbitmq/redis/fake-SMSC/gateway). Two things were broken independently of the UI work: the running gateway image **predated plan 010** and crash-looped on `unknown field "pickle_codec"` (strict decoding), and compose had no `ADMIN_WEB_PASSWORD` for the new config key. Rebuilt the image, added the env var, and published the UI as `127.0.0.1:8404:8404` (host loopback only — the container-side `0.0.0.0` bind is namespace-scoped). Documented the `web_*` keys in `configs/README.md`.
- **Verified the SPA in a real browser** by attaching the Playwright container to `jasmin_default` (detached afterwards): login → dashboard with the live probe; created a connector through the form and watched it reach **BOUND**; deleted it while running from the row action; route form's connector dropdown and type-dependent filter block both work.
- **Bug found and fixed:** the login route used Refine's stock `AuthPage`, which hardcodes an **email** field with email-format validation — the admin username was rejected client-side ("Invalid email address"), and it would have posted `{email, password}` against a BFF expecting `{username, password}`. Replaced with a purpose-built `web/src/pages/login.tsx` on `useLogin`. **Lesson: an embedded-bundle UI needs a real browser pass — the Go tests only prove the shell is served.**

### Next

- **#1 billing** — the last roadmap item before cutover; the submit path already carries `SubmitSmBill` and quota checks, the rating/balance/CDR engine is unbuilt.
- Backlog: CI guard that rebuilds `dist/` and fails on a diff; jCli console; native interceptor runner; logging remainder; formal cutover gate.

## 2026-07-27 — Finish the native pickle codec: retire the Python bridge (plan 010)

Goal (user): "finish #2 to unblock us full" — complete the native Go pickle codec so it is the **default** and the pickle-bridge layer is **retired from the image**. This closes the three deferred codec edges, flips the default, and drops the bridge from the gateway Dockerfile.

### Done

- **schedule/validity absolute-time codec** (`native_time.go`). Encode parses the RFC3339(Nano) schedule_at/validity_until into the tz-aware `datetime.datetime(<10-byte>, timezone(timedelta(...)))` pickle; decode re-encodes it to the SMPP `YYMMDDhhmmss+tenths+quarter-hours+sign` wire form (matching legacy `TimeEncoder().encode()[:-1]`). Differential-proven across UTC / +02:00 / fractional / negative offset (`native_time_differential_test.go`).
- **custom-TLV codec** (`native_custom_tlv.go`). Encode `[]SubmitSMCustomTLV` → `pdu.custom_tlvs` `(tag,length,type,value)` tuples (length/type None when unset); decode → the bridge `[tag,length,type,value]` JSON that `decodeWireCustomTLVs` already consumes (shared submit+deliver). Proven identical `[]tlv.TLV` for str/int/bool/bytes/None values, typed/untyped, null/set length.
- **full DataCoding surface** (`native_datacoding_tables.go`, code-generated from the venv). Ported the two non-default schemes: **RAW** (`schemeData` = the raw int; bytes 0x0b,0x0c,0x0f–0xef) and **GSM_MESSAGE_CLASS** (`DataCodingGsmMsg(msgCoding,msgClass)` NEWOBJ, bytes 0xf0–0xff, lossy 0xf8–0xff→0xf0–0xf7). All 256 bytes now encode+round-trip; cross-checked against the bridge both directions per shape.
- **Default flipped to `native`** (`runtime.go`, `config.go`, `gateway.example.json`); `bridge` is an opt-in fallback. The gateway integration test now runs E2E on the **default** (no `pickle_codec` set) — verified live: HTTP submit → durable AMQP → native decode → SMPP multipart SAR submit → resp, no subprocess.
- **Bridge retired from `docker/Dockerfile.gateway`** — removed `scripts/pickle_bridge.py`, the `smpp-pdu3` pip layer, and the `jasmin/` source tree. Python stays only for the stdlib-only interceptor runner. `*Bridge` code + all differentials kept for regression under `PYTHON_PATH`.

### Decisions

- **Why GSM had to be ported (not left poisoned):** the HTTP front-door `reCoding` regex is exactly the DEFAULT allowlist, but the **SMPPs-inbound** submit path (`smppssubmit/handler.go:81`) passes the ESME's raw `data_coding` byte unconstrained — so flash/message-class codings (0xF0…) are reachable and would have failed once native became the default.
- **Lossy GSM is correct parity:** `DataCodingEncoder` collapses 0xf8–0xff to 0xf0–0xf7; the generated reverse table reproduces this by mapping each `(coding,class)` pair to its canonical wire byte.
- **`more_messages_to_send` on submit stays a defensive poison** — neither encoder emits it (not in the encode request), so a non-None value is genuinely unexpected; the deliver path, which does carry it, decodes it.

### Next

- #3.3 — a basic setup/admin web interface (user's next goal), then #1 billing.
- Optional later: native interceptor runner to remove Python from the image entirely; retire the `pickle_codec: bridge` option once soak confidence is high.

## 2026-07-27 — Core-gateway functional completeness (plan 009)

Goal (user): finish everything for core gateway functionality — the four bounded gaps in [docs/plans/009](plans/009-core-gateway-completeness.md). One PR per step, merged on the 3 fast CI checks.

### Done

- **#87 — front-door full send-path byte-differential** (closes plan 003 Step 5). `submit_encoder_sendpath_differential_test.go` proves the Go front-door submit body equals the legacy `SMPPOperationFactory(config).SubmitSM` + `update_submit_sm_pdu` + `preSubmitSm` + `PDUEncoder` bytes, for the DEFAULT connector (2/1/1/1) and non-default connectors across a shape matrix (service_type, protocol_id, sm_default_msg_id, empty-source fallback, UCS2). Go params derive from `smppc.Config.Validate()+PDUDefaults()` — the resolved defaults, closing the strict-SMSC TON/NPI risk.
- **#88 — MO-direction interception.** Inbound `deliver_sm` can be rejected (drop, ack ESME_ROK) or mutated (source/dest/short_message rewritten before encode+publish). Small consumer-side `smppc.MOInterceptor` interface + in-place hook keeps smppc decoupled; the gateway adapter bridges to `interceptor.Table` + the shared runner. Runs on the whole message and the reassembled whole (long MOs), preserving interceptor-before-routing ordering. Config: `mo_interceptors`; runner now started for MT **or** MO.
- **#89 — MO content filters.** MO routes filter on source/destination/short_message/tag/date/time. The bridge `repickle_routable_pdu` action now returns the decoded routing fields alongside the unchanged pickled PDU (one round-trip); `modispatch` builds an MO routable and matches connector-id AND content filters. Published envelope byte-unchanged.
- **#90 — reconnect soak** (25 bind→drop→rebind cycles, no goroutine leak, `-race`). Audit: reconnection chain sound as-is, no code change (keepalive→`handleControlTimeout`→close→reconnect; inactivity watchdog; fixed ConLoss/ConFail delay kept for parity).

### Decisions

- **MO interception in `deliver.go`, content filters in `modispatch`** — each hook sits where its data naturally is; both reuse already-differential-tested encoders (no new bridge encode action for mutation), and ordering is preserved through the pickle (interception mutates → modispatch decodes the mutated fields).
- **Bridge decode-return over new headers** — folding the field decode into the existing repickle call keeps the `deliver.sm.<cid>`/RoutedDeliverSmContent envelopes byte-identical (no parity risk) with zero extra round-trip.
- **`AddrTon`/`AddrNpi` enums are 1-indexed** (NATIONAL=3) but the encoder maps enum→wire as value−1 (NATIONAL→wire 2); the connector config carries **wire** bytes. First send-path oracle draft used enum-by-value and diverged — fixed by decoding wire bytes like the bridge.
- **Soak gated behind `SMPP_SOAK`** so the goroutine-count assertion never flakes the fast gate; reconnect correctness is already covered by existing tests.
- **No exponential backoff** — kept the fixed reconnect cadence for Jasmin parity.

### Next

- Core-gateway portion complete. Per user sequencing, **billing** is next (long de-prioritized) — confirm timing before starting.
- Backlog (non-blocking): jCli byte-parity console, **native Go pickle codec** to retire the Python bridge (the "actually a Go rewrite" blocker), the formal cutover gate (drive 183 non-MATCH contracts to MATCH + shadow/canary/rollback runbooks; registry 19/205 MATCH).

## 2026-07-27 — Connection types per protocol + receive-side correctness

Goal (user): a gateway that sends/receives over SMPP and HTTP with DLR, modeled as distinct connection types per protocol. Grounded the design in Jasmin's connector model + the SMPP spec rather than an invented one. Billing explicitly de-prioritized (next after this portion).

### Done

- **#82 — SMPP client bind roles.** The standard's connection types are the three bind operations (Jasmin `bindOperation`); the Go client was transceiver-only. Added `transmitter` (send-only), `receiver` (receive-only, excluded from MT routing via `Available`+`CanSubmit`, never consumes the submit queue), `transceiver` (both). `connectAndBind` sends the role's bind PDU; run loop gates the submit consumer on `CanSubmit`. Enables the TX+RX pairing carriers that reject a single transceiver require. Wire-tested + compose (admin-provisioned TX/RX bound `0x2`/`0x1`).
- **#83 — inbound long-MO reassembly.** Multi-part MOs were dropped (`MSG IS LOST`). Now the session accumulates SAR/UDH segments via a `MultipartStore` (gateway adapter over the DLR Redis `longDeliverSm` key) and publishes one whole reassembled MO. **Bug fixed en route:** deliver-upstream wiring was config-connectors-only, so admin-provisioned connectors couldn't receive at all — moved into the manager factory via deferred vars.
- **#85 — inbound `data_sm`.** Was ignored. `data_sm` has a distinct SMPP-3.4 mandatory body (no protocol_id/priority/schedule/validity/replace/sm_default/short_message; content in `message_payload` TLV), so it needed its own `decodeDataSM`/`encodeDataSM`, not a dispatch alias. Byte-parity differential vs `smpp.pdu` (bridge parity); dispatched to `handleDeliver`; answers `data_sm_resp`.
- **#84 — chore:** removed an accidentally-committed `jasmin-fake-smsc` binary + gitignored built binaries.

### Decisions

- **Filter matching is `re.match`-anchored** (`location[0]==0`), faithful to legacy — a `short_message` pattern anchors from the start; use `.*STOP` to block STOP anywhere. (Re-confirmed while drilling; corrected the example config in #81.)
- **`data_sm` proper codec, not a one-line alias** — the different mandatory layout would misparse under `decodeSM`. Honest correction after first assuming it was trivial.
- Chose bind-roles as the concrete meaning of "connection types per protocol" over a config-model rewrite: it's the genuine standard gap (client was transceiver-only), and the `smppc`/`smpps`/`http` connector-type abstraction already exists in routing.

### Next

- **Portion complete** (send/receive over SMPP+HTTP with DLR + connection types). Per user sequencing, **billing** is next (was de-prioritized) — check timing before diving in.
- Outside the portion (non-blocking): MO-direction interception + MO content filters (runner + filter engine already exist), jCli console, native Go pickle codec to retire the bridge, the formal cutover gate.

## 2026-07-26 — Macro-3 items 4–5 finished: full admin plane + MT interception

### Done

- **#79** — MT filter-based routing config (routes carry `filters`), live-swappable table (`routingtable.AtomicTable`).
- **#80** — admin **routes + users** CRUD (SQLite, live, restart-safe), completing the admin plane alongside connectors (#78). Routes swap the atomic table; users mutate the billing directory (new `RemoveUser` + mutation-safe hash map + stable-uid assignment so route user-filters resolve across restarts). Apply-first-then-persist throughout; config entities reserved.
- **#81** — MT interception: `scripts/interceptor_runner.py` + `internal/transport/pyintercept` (Python subprocess, legacy `InterceptorPB` contract) implementing `core.interceptor.Runner`; `outbound.mt_interceptors[]` → `buildInterceptorTable` → submit pipeline; gateway starts/closes the runner; Dockerfile bundles the script.
- **Compose E2E for each:** live route selection by destination (#79/#80), user create→auth→restart→delete (#80), interceptor `.*STOP` → reject http 400 while normal passes (#81).

### Decisions

- **modernc.org/sqlite (pure Go)** for the admin store — the image is `CGO_ENABLED=0`; `mattn` would panic (ADR-001).
- **Filter matching is `re.match`-anchored** (`location[0]==0`), faithful to legacy Python `re.match` — a `short_message` pattern anchors from the start, so use `.*STOP` to block STOP anywhere. Discovered when a `STOP` interceptor filter didn't fire on "please STOP texting"; corrected the example, not a bug.
- **Interceptor keeps Python** (user choice + legacy contract). Boundary: the MT hook is pre-encode, so only source/destination/short_message/tags are surfaced to scripts; documented.

### Next (remaining beyond items 1–5)

- MO **content** filters (source/dest/content on `deliver.sm` routes) — deferred from #77; needs pre-routing content decode in modispatch.
- MO interception (this session did MT); the runner is direction-agnostic, so it's config + a deliver-path hook.
- jCli byte-parity console (admin API covers provisioning; console still deferred).
- The formal cutover gate (contract-matrix all-MATCH, shadow/canary/rollback) — the separate ~12–18wk bar.

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

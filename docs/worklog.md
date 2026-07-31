# Worklog

<!-- Newest entries on top. One entry per significant working session. -->

## 2026-07-30 — MT termination connector, Phase A (plan 021)

- Analysed the two Python services beside the gateway and found they are one
  missing feature split in half: `internal/core/routingtable/table.go:249` lets MT
  reach only an SMPP client connector, so a platform that *terminates* traffic had
  to invent a fake SMSC to route to and then tap the broker to get the content out.
  Plan 021 replaces both with a termination connector; the decisions behind it are
  recorded there, with the questions and answers in `que.md`, `que2.md`, `que3.md`.
- **Phase A built on `feat/mt-termination-connector`:** connector type `term`
  registered for MT; `redis-window` verdict source with a single-flight TTL cache;
  SMSC-leg synthesis that publishes the same two DLR legs a real carrier causes
  (the content constructors in `smppc` were exported rather than duplicated, so the
  partner-visible bytes come from one implementation); the message spool with both
  backends and migration 0006; the signed JSON delivery sink; and the delivery and
  receipt runners. `go build ./...` clean, `go test ./internal/...` green.
- **The receipt is owed by a committed row, not a timer.** Receipts carry a 5–7 s
  parity delay, so a process dying inside that window would otherwise lose one.
  `ClaimDueReceipts` leases exclusively with `SKIP LOCKED`; an expired lease is
  re-claimable through the claim predicate, so no separate recovery sweep exists.
- **The SMSC message id is derived from the queue message id.** A crash between
  publishing the accept leg and committing the spool row replays byte-identical
  events on redelivery; a random id would orphan the receipt already published.
- **Two blockers found by an agent outside its own scope, both fixed with
  regression tests:** `routepolicy` refused non-SMPPC MT pool members, and
  `buildRoutes` hardcoded `SMPPC`, so a persisted `term` route would have returned
  after a restart as an SMPP client route pointed at a connector that does not
  exist. Routes now declare `connector_type`; empty still means `smppc`.
- **The Python decoder destroys OTP digits.** Confirmed by running it, not by
  reading it: `"@>25@>G=K9 :>4 63125"` decodes to `"РОВЕРОЧНЫЙ КОД ЖГБВЕ"` — the
  code `63125` becomes `ЖГБВЕ`. The token-wise restoration beside it already keeps
  digit runs, but its gate needs two characters in `0x70-0x7E` and an all-uppercase
  Cyrillic message has none, so the correct code never runs. `decoder.py:120-135`'s
  own comment says the digits should survive and no Python test asserts either
  value. Fixed in Go behind `msgcontent.Options.PreserveOTPDigits`, on in
  `termination.DefaultDecodeOptions()`, off in `Options{}` so the differential stays
  byte-exact. Recorded as deviation D-004; both behaviours are asserted so neither
  can drift. **The Python service still mangles**, so while both run the same
  message decodes differently depending on which path carried it.
- Also found in the port: the GSM 7-bit branches are dead in every environment
  (`gsm0338` exports no module-level `decode`; the `AttributeError` is swallowed),
  so DCS 0x00 bodies fall through to auto-detection. Exposed as an option, off by
  default — enabling it rewrites `@` before the Cyrillic heuristic sees it.
- `cmd/synevyr-partner-sim` added for development: binds as a partner ESME,
  submits, prints receipts with latency, and flags a receipt whose counters
  contradict its status. Verified against a running local gateway; running it
  immediately exposed a reporting bug of its own (a submit refused with no message
  id never reached the summary), now fixed.
- **Phase A completed later the same day:** the connector manager and AMQP
  consumption, the gateway `termination_connectors` section, the admin plane
  (own table, own service, REST + adminweb + jCli route syntax), and the console
  (route-form connector type, termination connector CRUD). Verified on a real
  broker: one submit → one spool row → acked; a redelivery → still one row; a
  failing spool → never acked.
- **An adversarial review was run over the whole branch and found real defects.**
  Fixed: the SMSC message id was not canonical, so ~1 in 16 terminal receipts
  looked up a correlation key nobody wrote and vanished with no error; a
  redelivery after the receipt went out rewrote the stored verdict, making the
  spool contradict what the partner was actually told; `termination.Message` had
  no redaction, so the first log line would have leaked OTP text; a concatenated
  segment was going to be spooled, announced, receipted and delivered as if it
  were a whole message, once per fragment, and is now refused terminally.
- **The D-004 fix was itself wrong and was re-scoped.** Protecting any four-digit
  run turned ДЕДА into 4540 and БЕДА into 1540 — worse than the defect on
  messages with no code at all. It now protects five or more digits
  unconditionally, or four with a code anchor (КОД, ПАРОЛЬ, PIN, CODE, OTP)
  matched against the restored text. A second defect in the same area: preserved
  digits counted against the token-wise decoder's Cyrillic ratio, so a 16-digit
  code was rejected at 46.7% and destroyed by the fallback anyway, silently,
  with the option on. Both are regression tests now.
- **Contract gaps closed after the runtime landed:** `Message.Partner` was never
  populated — the PDU has no user field, so the submitting user now travels from
  the envelope's `user-id` header through `SubmitMetadata`, which is what makes
  per-partner attribution work at all; the receipt runner built one SMSC leg for
  the whole process and labelled every partner's receipts with one connector id,
  and now builds one per connector from the claimed row; and a pull-only
  connector's rows were scheduled for a push no sink would make, walking them
  through the retry budget to a dead letter.
- **The console was unreachable until the composition root was wired.** The plane
  ran, the admin service existed and the UI was built, but nothing constructed
  `admin.NewTerminationService` or handed it to the REST handler and the web BFF,
  so every console call answered 404 "not enabled on this gateway" — a feature
  present, working and unreachable. Found by the UI agent running the real stack
  in a browser, which is the only way it would have surfaced. Now wired, with a
  test asserting it, and the nil case kept meaningful: no section still answers
  404, because "does not terminate traffic" is a different statement from
  "terminates none right now".
- `scripts/differential/decode_oracle.py` re-checks all 77 vectors against the
  live Python decoder and is the gate before touching the decoder. It passes.
- **Multipart, metrics and the pull API landed after that**, in parallel: UDH and
  SAR reassembly plus the plain-split stitch (step 4), the four safety metrics and
  the decision trail as a query over the spool rather than a second table (step
  11), and the cursor pull API with scoped, revocable, read-only consumer tokens
  (step 10). Scope compiles into SQL rather than filtering afterwards, because a
  post-filter leaks through the cursor and through row counts.
- **The metrics work nearly shipped a permanently-firing alarm** and caught it only
  on the live stack: counting every `ErrDLRMapNotFound` as a correlation failure
  produced 44 from ordinary traffic, because the response path publishes
  `dlr.submit_sm_resp` for every submit and most submits request no receipt.
- **A conformance defect was found by asking the partner question, not by a test.**
  Multipart reassembly initially sent one receipt per assembled message; SMPP 3.4
  makes each segment its own `submit_sm` with its own `message_id`, so a partner
  that requests a receipt per segment is entitled to one per segment — which is
  what the legacy fake SMSC has always sent. Receipts and content are now separate:
  one accept leg and one receipt per segment, one spooled and delivered message.
  The plain-split stitch still has the same defect and is off by default.
- **Next:** the flow differential against the legacy stack (the content half is
  done), then Phase B — multipart stitch, the cursor pull API with scoped
  tokens, metrics, and the Messages console screen.

## 2026-07-30 — Billing console: config, live balances, rated usage (plan 019)

- The web admin now has a Billing section over the durable commercial records
  that no surface reached before: Accounts (granted vs remaining balance and
  quota, group ceiling, derived billing mode), Usage (CDR search by customer and
  window, cursor paging, per-part event timeline, CSV/JSONL download), Statements
  (SQL-side rated aggregate per customer) and Settings (read-only commercial
  configuration, reconcile, confirmation-gated prune).
- Two misleading numbers found and fixed on the way, both of the "present but
  wrong" class this project treats as worse than missing:
  - The user list rendered the **provisioned grant** in a column called
    "balance". A customer down to 3.42 showed 100. Granted and remaining are now
    separate, and an unreadable live value reports the error instead of zero.
  - `runtimeDirectory.Rate` discarded its destination argument. Worse than the
    handbook's note said: `defaultRate` is set by `entry.Order > bestOrder`, so
    it was the **highest-order** route's rate — a customer falling through to a
    cheap order-0 default was quoted the expensive filtered route's price, and it
    never refreshed after a runtime route change. It now resolves the live table.
    `docs/operations/running-the-platform.md` documented this as a known bug and
    told operators not to use the tool; that section is rewritten.
- New core surface: `cdr.Service.Events` (the raw repository method had neither
  authorization nor an audit trail), `cdr.Service.Search`, and
  `cdr.Service.Summarize` over a new `SummarizeCDRs` repository method
  implemented in both PostgreSQL and SQLite, grouped by `(user_id, currency)`.
  Aggregating in SQL rather than paging records into the BFF was the point.
- Charged money is reported as early plus **actually applied** late money;
  quoted-but-pending late money is shown separately. Summing quoted late amounts
  would have booked intent as revenue.
- Every console CDR read passes the session username as `cdr.Principal.Subject`,
  so `cdr_access_audit` gets a real per-actor row. This is audit fidelity, not
  RBAC — every session still has the same power, and roles remain deferred.
- Verified on a running stack (isolated compose project, rated routes, EUR):
  `/rate` quotes 2.0 for the filtered destination and 0.25 for the default one;
  two sends moved the balance 100 → 97.75; the statement's charged total is 2.25,
  equal to the balance delta; export headers and CSV match; prune refuses without
  a typed confirmation and reports disabled retention. The audit table shows
  `admin` for console actions, distinct from `gateway-maintenance`.
- **Same data on the admin REST API** (step 7, added after the "is this web-only?"
  question): `/admin/cdrs`, `/admin/cdrs/{id}`, `/admin/cdrs/{id}/events`,
  `/admin/cdrs/export`, `/admin/billing/summary`, `/admin/billing/accounts`,
  wired through a new `admin.WithBilling` functional option (the
  `restcompat.WithBatchContext` precedent) so `NewHandler`'s signature is intact.
  Absent entirely when the option is not supplied — a deployment with no CDR
  repository answering an empty list would read as "this customer sent nothing".
  Audited as `admin-api`: the plane has one shared token, so there is no
  per-caller identity to record and inventing one would poison the audit table.
- **jCli deliberately excluded**, reasoning recorded in the plan: its only
  distinguishing property is byte-for-byte legacy replay, and verbs with no
  Python oracle would cost that for data reachable by curl or psql.
- **Lookup by gateway message ID** added while correcting the handbook, which
  said no operator-accessible query from message ID to the durable CDR existed.
  It does now — `?message=<id>` on both surfaces, riding the existing
  `cdr_records(message_id, part_number)` index, returning every part. Verified
  live with a two-part send: two rows, each charged 0.25 EUR.
- **Education center filled in.** Nine lessons added — six operator (saved-filter
  and destination libraries, hosting a customer SMPP bind, and the three billing
  screens) and three network (receipt levels in practice, reading an SMPP status,
  how SMS traffic is charged) — plus a third end-to-end journey for the receipt
  path, seven glossary entries and eight FAQ answers. The FAQ now covers the
  inherited behaviours that actually catch people: a filter pattern is anchored
  (`555` means "starts with"), a throughput quota of `0` means unlimited rather
  than blocked, a callback needs HTTP 2xx *and* a body of exactly `ACK/Jasmin`,
  a multi-connector route is ordered failover rather than load balancing, and the
  early charge is not refunded when the SMSC rejects. The interceptor lesson is
  hidden wherever the interceptor screen is, and the "recommended start" count is
  computed from the lesson list so it cannot drift again.
- **Five inline SVG figures** (`web/src/components/EducationDiagrams.tsx`): bind
  modes, route evaluation order, characters per part by encoding, the early/late
  rate split, and what each receipt level delivers. No charting dependency — the
  console must make no outbound request and the bundle is already 1.5 MB. Two
  hues carry one meaning throughout (submit side / return side), validated for
  lightness, chroma, colour-vision separation and contrast; the brand teal was
  rejected as a mark colour because at chroma 0.086 a fill of it reads as gray.
  Every figure also direct-labels its values, so nothing depends on colour alone
  and nothing hides behind a tooltip.
- The figures were server-rendered and rasterised to look at rather than assumed
  correct, which caught a real label collision (`→ carrier-bulk` overlapping the
  `MATCH · stop` badge) and two labels sitting on an edge.
- Not verified: the billing and education pages have not been rendered in a real
  browser (the Chrome extension was not connected). They typecheck, build, their
  code is present in the embedded bundle, and the five figures were inspected as
  images — but no screenshot backs the pages themselves.

## 2026-07-29 — Roadmap #20 PB and #21 active-passive HA completed

- The trusted Twisted sidecar now terminates RouterPB,
  SMPPClientManagerPB, SMPPServerPB and InterceptorPB on their frozen ports
  with digest authentication, Deferred/result shapes and serialized object
  reconstruction. Every normalized call is bearer-authenticated at the
  private Go listener.
- All PB method families are live: groups/users/routes/interceptors, scoped
  profile persist/load with rollback, connector CRUD/lifecycle/stats,
  SMPP-server session operations, manager submit and isolated legacy-script
  execution. Native Go interceptor execution remains intentionally last.
- Objects created by jCli, REST, the web UI or another HA replica are
  deterministically reconstructed for frozen PB list/get calls when no opaque
  legacy pickle exists. A real frozen `RouterPBProxy` connected through the
  sidecar after failover and observed normalized state created before it.
- Manager submit now honors the supplied connector, bill and bill ID, DLR
  connector, queue priority and expiration. Linked `SubmitSM.nextPdu` chains
  cross the normalized boundary as bounded, cycle-checked ordered wire frames;
  Go admits every byte-exact part under one aggregate message ID without
  rerouting, re-intercepting or charging the already-applied early bill again.
- `delQueues=True` returns an explicit failure without stopping or mutating the
  connector. Deleting a connector queue is intentionally unavailable because
  the supported deployment uses shared durable AMQP topology.
- Active-passive HA is complete for the supported topology. A deterministic
  PostgreSQL schema holds shared admin/profile state; exactly one process owns
  the namespace advisory fence while standbys serve `/live` 200 and `/ready`
  503. Fence loss closes public, REST, admin, PB, jCli and SMPP admission before
  release.
- The executable two-gateway HAProxy drill passed: healthy active/standby,
  pre-failover MT send, shared PB-created state, forced active stop, standby
  promotion, post-failover MT send, state retention, and old-node rejoin as an
  unready standby. Multi-active remains explicitly unsupported by ADR-005.
- Focused PB macro: 30 tests, zero skipped/failed. Integrated Go and live
  PostgreSQL suites, affected race suites, the real PB client replay, compose
  validation and HAProxy syntax validation pass. The compatibility registry is
  structurally green at 205 rows: 37 `MATCH`, 17 `GO-COMPLETE`, 60
  `GO-PARTIAL`, 91 `INVENTORIED`.

## 2026-07-29 — Roadmap #19 REST completed

- `/secure/send`, `/secure/sendbatch`, `/secure/balance` and `/secure/rate`
  retain the frozen Basic-auth, JSON mapping, response and error contracts.
  The separate optional REST listener now adds the JSON-wrapped `/ping`;
  combined public `/ping` remains the legacy `Jasmin/PONG` bytes.
- PostgreSQL migration 0005 makes batch admission, scheduled work, terminal
  state and callback/errback delivery durable. Complete destination expansion
  commits before acknowledgement; leased `SKIP LOCKED` claims recover after
  restart and permit safe worker/process concurrency.
- A stable UUID per batch task becomes its trusted internal submit message ID.
  Recovery consults the durable submit ledger before routing/billing, so a
  process crash after admission cannot charge and enqueue that task again.
- Durable tasks store a SHA-256 credential proof, never the password. Dispatch
  rechecks the current digest and user/group enabled state. Callback URLs are
  limited to absolute HTTP(S) URLs without embedded credentials; requests have
  a timeout and bounded retry.
- `rest_api` JSON exposes the daemon address, per-worker throughput, smart QoS,
  backlog ceiling and submit/callback retry policy. A frozen Python config
  differential proves `[rest-api] http_throughput_per_worker` and `smart_qos`
  parsing/defaults.
- Focused REST/HTTP/core/config/storage/outbound suites pass. The PostgreSQL
  lifecycle test passed in the parent integration environment. R-001–R-009
  remain `INVENTORIED`: functionality is complete, but the registry still
  requires committed frozen endpoint macros/fixtures before promotion.

## 2026-07-29 — Roadmap #18 CDR completed

- Correlated final DLRs now append one immutable per-part event and retain
  normalized delivery state plus SMSC/gateway timestamps before Redis cleanup.
- The durable late-billing ledger and CDR projection now settle atomically as
  `APPLIED` (actual amount) or `REJECTED` (zero); contradictory terminal
  transitions are refused.
- `cdr_currency`, retention days/batch and maintenance cadence are
  operator-configurable. The production runtime runs reconciliation alerts and
  bounded pruning; zero retention remains the safe no-delete default.
- Versioned opaque-cursor JSONL/CSV export, role-based read/export/operator
  access and durable fail-closed access auditing are implemented.
- SQLite lifecycle/authorization/export/pruning/reconciliation tests and a
  live PostgreSQL completion test pass. No registry row changed: Python has no
  CDR oracle.

## 2026-07-29 — Roadmap 18–21 started; quota GC and data_sm TLVs closed

Goal (user): use three additional agents to start functional roadmap items
18–21, fix deleted billing-quota row GC and exhaustive `data_sm` optional-TLV
parity, and keep native Go interceptor execution as the final migration step.

### Delivered

- **#18 CDR phase 1 (ADR-006).** Durable content-free per-part projections and
  immutable deduplicated events now commit atomically with submit admission,
  attempt ambiguity/retry and SMSC results in PostgreSQL; SQLite mirrors the
  contract for tests. Final DLR/late-ledger settlement, retention/export,
  reconciliation and RBAC remain.
- **#19 REST first release.** `/secure/send`, `/secure/balance`,
  `/secure/rate`, and `/secure/sendbatch` are authenticated JSON facades over
  the existing HTTP path. Batch globals/overrides, destination expansion,
  scheduling, callbacks and the frozen 8/s smart-QoS default are implemented.
  Scheduled-job recovery across restart remains.
- **#20 PB control seam.** `jasmin.pb-facade.v1` exposes versioned,
  bearer-authenticated normalized calls for live connector/user/group/route/
  interceptor services on a separate private listener. The trusted Twisted
  PB/jelly translator and remaining legacy method families remain.
- **#21 HA phase 1 (ADR-005).** A namespaced PostgreSQL session advisory lock
  is acquired before mutable runtime construction, emits a connection-loss
  fence, stops listener admission on loss, releases only after workers stop,
  and permits takeover. Shared admin state and multi-active atomic billing
  remain.
- **Deleted quota GC.** Admin user/group deletion now prunes durable rows
  after the store commit; boot repeats the pass after complete admin replay.
  The leftover in-memory restore index is also cleared, preventing both
  cross-restart and same-process username reuse from inheriting old spending.
- **`data_sm` optionals.** The Go codec now decodes/encodes the complete frozen
  `DataSM.optionalParams`/`OptionEncoder` intersection in declaration order,
  including network/bearer/telematics and QoS TTL fields. A Python oracle frame
  containing every supported optional round-trips byte-for-byte; known
  command-invalid TLVs are rejected rather than dropped.

Native Go interceptor execution was intentionally not started: roadmap
18–21 still have explicit remaining scope, so it remains the last step.

### Verification

- Integrated targeted Go suite covering admin, PB, REST, outbound, gateway,
  core, storage, SMPP wire and the executable — pass.
- Focused oracle replay for all supported `data_sm` optionals — pass.
- Final integrated `PYTHON_PATH="$PWD/.venv-oracle/bin/python" go test
  -count=1 ./...` — pass; affected admin/PB/REST/outbound/gateway/core/storage/
  SMPP-wire packages under `go test -race -count=1` — pass; `go vet ./...` —
  pass.
- Contract registry — structural PASS, unchanged at 205 total / 165 unfinished
  (`MATCH` 37, `GO-COMPLETE` 3, `GO-PARTIAL` 60, `INVENTORIED` 105).
- `git diff --check` — pass.
- No compatibility registry row was promoted without frozen evidence.

## 2026-07-29 — Functional items 11–17 closed; money edits and raw SMPPs forwarding hardened

Goal (user): fix shared-group quota persistence, admin edits that regrant
money, the admin DLR level, remaining SMPPs PDU fidelity and interceptor crash
recovery; then finish the functional implementation of readiness items 11–17.

### Bugs fixed

- **Shared-group quota snapshot consistency.** A flush could derive group state
  indirectly from multiple dirty users at different moments. It now snapshots
  every dirty user first, then snapshots each affected live group once in
  deterministic key order before the one atomic write.
- **Admin edits regranting money.** User/group replacement now retains the live
  billing object and distinguishes an unchanged provisioned baseline from an
  explicit quota reset. Failed SQLite persistence restores the exact spent
  balance/count, group, credential, provisioning baseline and quota-version
  state while concurrent charges wait on the same lock.
- **Admin diagnostic DLR level.** The diagnostic submit now requests level 3,
  covering both SMSC acknowledgement and terminal receipt correlation.
- **SMPPs PDU fidelity.** The ESME's full mandatory `submit_sm` body, every
  standard optional accepted by the frozen decoder/encoder intersection,
  vendor TLVs, binary coding-zero bytes and pre-segmented UDH/SAR content now
  survive the native AMQP forwarding path. The server rejects known optionals
  belonging to another PDU instead of accepting and dropping them.
- **Interceptor process death.** Cancellation, EOF, broken pipe and a script
  calling `os._exit` invalidate/reap the worker; the next request lazily starts
  a clean child.
- **Component logging.** Named rotating loggers are wired for SMPPc lifecycle,
  SMPPs binds, router, HTTP API/access, DLRLookup, AMQP factory and both
  throwers. Loggers targeting the same file share one synchronized rotating
  sink.
- **Related completion fixes.** HTTP and SMPPs throughput ceilings are enforced;
  persisted user/group filters replay only after their identities exist; group
  filters are provisionable in config/admin/UI; an existing user's external ID
  is immutable in the web edit form.

### Verification

- Affected packages:
  `PYTHON_PATH="$PWD/.venv-oracle/bin/python" go test -count=1
  ./internal/core/billing ./internal/app/admin ./internal/app/outbound
  ./internal/app/gateway ./internal/core ./internal/transport/smppwire
  ./internal/transport/picklecompat ./internal/app/smppssubmit
  ./internal/transport/pyintercept ./internal/core/smppc` — pass.
- The same affected package set under `go test -race -count=1` — pass.
- Reconnect soak:
  `SMPP_SOAK=1 ... go test -race -count=1 -run Soak
  ./internal/core/smppc` — pass, 25 forced reconnect cycles.
- Full repository:
  `PYTHON_PATH="$PWD/.venv-oracle/bin/python" go test -count=1 ./...` —
  pass; `go vet ./...` — pass.
- Frontend: `cd web && npm run build` — pass; embedded source hash
  `9f02697f3afffab9a1909c63ecffaeefae48486aac34cb399cbc3e894bc44f28`
  reproduces.
- Registry validator — pass: 205 total, 165 unfinished; 37 `MATCH`,
  3 `GO-COMPLETE`, 60 `GO-PARTIAL`, 105 `INVENTORIED`. No row was promoted.
- The compatibility-tool unittest initially failed two tests because the oracle
  venv lacked its declared `jsonschema` tool dependency. Installing the pinned,
  hash-checked `compat/requirements-tools.txt` into the ignored venv made both
  failing tests pass; the complete rerun passed all 67 tests in 268.550s.
- `git diff --check` — pass.

### Readiness consequence

Functional implementation for rows 11–17 is complete in this worktree. That
does **not** make the deployment production-ready: oracle evidence remains
40/205 (19.5%), Release A remains 8/58 (13.8%), `SURFACES.md` attestation is
0/21, and 21/39 macro rows still lack executable coverage. The code/evidence
distinction remains the cutover gate.

## 2026-07-28 — Production hazards on the SMPP server, and money that did not survive a restart

Goal (user): implement the next stages of surfaces 11-16, parallelising with
agents. CDRs moved to backlog by decision.

### Delivered

- **QoS throughput ceiling enforced** (`internal/core/throughput`).
  `http_throughput` / `smpps_throughput` were accepted by jCli, stored, and
  reported back in `user -s` while nothing applied them. Five Python quirks
  preserved as Q-022, notably that a quota of 0 means unlimited, not blocked.
- **Four cutover macro gates made executable** (`smpps`, `routing`, `mo`,
  `control`): 10/39 → 18/39. Verified by running the runner, not by asserting.
  The runner rejects any run with skipped != 0, which is why `mothrower` is
  excluded from the `mo` rows.
- **Interceptor subprocess respawn.** A cancelled read killed the Python process
  and nothing restarted it, so one client timeout silently disabled every MT and
  MO interceptor until the gateway restarted.
- **SMPPs DLR registration.** Nothing ever wrote the `sc=smppsapi` record, so
  every receipt for an SMPP-originated message died as `DLRMapNotFound` — 0%
  delivery receipts for wholesale SMPP customers. Also widened the record's
  addresses from int64 to string: the frozen fixture only captured numeric
  MSISDNs, so an alphanumeric sender id was unrepresentable.
- **SMPPs MT credential enforced and provisioned.** Bind users got a synthetic
  permissive credential, so `ValidateSubmit` was a no-op for SMPP traffic and a
  customer fenced to specific prefixes could send anywhere. Needed both halves:
  enforcement in the directory *and* the jCli/web mirrors copying the user's
  `mt_credential` onto the bind account — enforcement nothing provisions is the
  same outcome with more moving parts.
- **Durable prepaid balances** (ADR-004). `NewQuotaPersistenceService` and
  `NewPersistWorker` had zero production callers; every balance reset to its
  provisioned value on each restart.

### Judgement calls worth remembering

- A reviewer proposed refusing group deletion while users reference the group.
  The oracle *cascades* (`jasmin/routing/router.py:917`), so that would have been
  a silent deviation. Cascaded instead.
- A reviewer reported cleartext SMPPs passwords as a new regression. It is
  pre-existing and by design — `smppsserver.UserConfig:19` documents plaintext
  because legacy md5s it at bind time. Not treated as a finding.
- The first version of the interceptor regression test passed against the
  unfixed code, because cancelling before `Run` returns at the entry guard and
  never reaches the kill path. Every regression test this session was checked to
  fail without its fix.

### Corrected

`docs/STATUS.md` claimed billing "persistence" was working. It was not.

### Next

SMPPs submit PDU fidelity (only 6 fields forwarded: ESME multipart loses
`esm_class` so UDH bytes leak into the body, and alphanumeric senders are
replaced by connector defaults), then component loggers. CDRs stay in backlog —
the oracle has no CDR feature, so they are greenfield, and plan 015 G2 permits
excluding them. Unchanged dominant constraint: 8 of 58 Release A contracts
finished.

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

Capturing usable transcripts then needed three things the plan had not anticipated: an isolated AMQP vhost (a running Go gateway declares `messaging` durable and the frozen stack declares it transient, so connector mutations died mid-transcript), a logging-layer redirect for the `/var/log/synevyr` paths the frozen code opens at mutation time, and a longer settle window so a slow PB reply is not recorded as an empty one. All three failed *silently*, producing fixtures that looked fine and enshrined an environment failure as contract.

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
- **#84 — chore:** removed an accidentally-committed `synevyr-fake-smsc` binary + gitignored built binaries.

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
- **#69** — Steps 3–4: `docker-compose.gateway.yml` (postgres:16 + rabbitmq:3-management-alpine + redis:7-alpine + gateway, healthcheck-gated) + hermetic `cmd/synevyr-fake-smsc` / `docker/Dockerfile.fakesmsc` (scratch image on `internal/transport/smppwire`; accepts any bind, ESME_ROKs submits). **E2E proven on the live stack:** bind, `/send` with AND without `from` → `ESME_ROK` at 1 msg/s pacing, SMS-MT audit lines, `submit_attempts` `RESULT_COMMITTED` + `submit_results` `SUCCESS/ESME_ROK` in Postgres.
- **#67** — bug found via that E2E: submits with any EMPTY byte param (e.g. `/send` without `from=`) were **silently dropped** — CPython pickles `b''` at protocol 2 as a `__builtin__.bytes` GLOBAL (non-empty uses `_codecs.encode`), `SubmitSMUnpickler` didn't allowlist `bytes` (DeliverSM's did) → `ErrSubmitSMPoison` → reject-no-requeue, no attempt row, no log. One-line allowlist fix + `TestDecodeSubmitSMEmptyBytes` differential (red on old, green on new; oracle needs only pinned smpp-pdu3).
- `docs/plans/007` → status **active**, P0 marked done; step-1 path note (misc/config → configs).

### Decisions

- **`configs/`, not the planned `misc/config/`** — `misc/config/` is inside the frozen-oracle tree fingerprint (`scripts/compat/verify_baseline_tree.py`: `jasmin/`, `tests/`, `misc/config/`); adding files there broke 3 CI checks (`oracle_tree=changed files=203/201`). Any new Go-side config artifacts must stay out of those trees.
- **Pinned bridge runtime, not full requirements.txt, in the image** — bridge-reachable jasmin modules (`routing.Bills`, `routing.jasminApi`, `routing.Routables`) are stdlib/smpp.pdu-only; CI proves pinned `smpp-pdu3` suffices. Twisted/txredisapi/falcon are legacy-daemon surface; smaller image + supply chain.
- **Own fake SMSC instead of an external simulator image** — the planned `melroselabs/smscsim` doesn't exist on Docker Hub; `cmd/synevyr-fake-smsc` reuses `smppwire` and mirrors the integration test's contract, keeping the stack hermetic.
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

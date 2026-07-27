# Core-gateway functional completeness (MO interception + MO filters + front-door byte-proof + resilience/soak)

- **Date:** 2026-07-27
- **Status:** active
- **Summary:** Close the four bounded gaps that make the core gateway functionally complete: MO-direction interception, MO content filters, the deferred front-door submit byte-differential (plan 003 Step 5), and reconnection/resilience hardening proven by a real soak test.
- **Related:** [008-macro2-mo-dlr-admin.md](008-macro2-mo-dlr-admin.md) (MO/DLR/admin/interceptor), [003-front-door-connector-pdu-config.md](003-front-door-connector-pdu-config.md) (GAP 4 — this plan closes its deferred Step 5). Memory: prod-testing-readiness, mt-path-parity-audit-backlog.

## Context

The send/receive/DLR portion is merged (#66–#86): outbound MT, inbound MO (`deliver_sm`+`data_sm`, long-MO reassembly), terminal DLR, MT routing+filters+interception, admin plane. Four functional gaps remain on the **receive/deliver** side and the **front-door parity** edge, all small and bounded:

1. **MO-direction interception** — the interceptor engine (`internal/core/interceptor`) and the Python runner (`internal/transport/pyintercept`) exist and are direction-agnostic (`rebuildRoutable` preserves `Direction`), and the SQLite `mo_interceptors` table + a direction-parameterized repo already exist. But no MO interceptor is built from config or invoked on the deliver path. Inbound MO cannot be rejected or mutated by a script the way MT can.
2. **MO content filters** — `modispatch` selects an MO route by source connector-id only (the legacy `ConnectorFilter`). MO routes cannot yet filter on `source_addr`/`destination_addr`/`short_message`. The filter engine (`routingfilter`) exists; `modispatch` just lacks a Go-side content view of the MO (it holds only the pickle).
3. **Front-door submit byte-differential** — plan 003 Step 5 is marked *deferred*. In fact `submit_encoder_frontdoor_differential_test.go` already proves the Go body equals the legacy `SMPPOperationFactory(config).SubmitSM(...)` wire body (TON/NPI + `service_type`), so the core parity is closed — but only for one non-default connector and one message shape, and it bypasses the full legacy send path (`update_submit_sm_pdu` + `preSubmitSm`). The residual strict-SMSC risk is an unproven default-connector case and the send-path wrapper.
4. **Reconnection/resilience + soak** — keepalive (`enquire_link` every `PDUTimeout`, `enquire_link_resp` timeout → `handleControlTimeout` → `conn.Close()`), an inactivity watchdog (`PDUTimeout*2`), and a fixed-delay reconnect loop (`ConLossDelay`/`ConFailDelay`, default 10s, faithful to Jasmin) all exist. What is missing is *proof under churn*: a soak test that a connector recovers across repeated forced disconnects with no goroutine/FD leak, no message loss, and no double-settle — plus any small fix that surfaces.

Desired outcome: inbound MO can be intercepted and content-routed with parity-safe encoding; the front-door submit is byte-proven for default and non-default connectors across shapes; and reconnection resilience is demonstrated, not asserted.

## Approach

Four independent PRs, each guarded so an unset config reproduces today's behavior; they compose but create no merge dependency. Merge on the 3 fast CI checks (`go-http-differential`, `fixture-integrity`, `fixture-reproducibility`); never add to `misc/config/`.

- **MO interception → the smppc deliver path (`deliver.go`).** Run the MO interceptor where the fully-decoded `pdu.SM` lives, before publish. Reject drops the MO (ack + `ESME_ROK`, like the legacy internal drop); mutation edits `pdu.SM` and flows through the *already-differential-tested* `EncodeRoutableDeliverSM`, so no new encoder and no new bridge action. Because the (possibly mutated) pickle is what `modispatch` later decodes, the legacy **interceptor-before-routing** ordering is preserved across the two components.
- **MO content filters → `modispatch` route selection.** Fold a decoded content view (`source_addr`/`destination_addr`/`short_message`/`tags`) into the bridge call `modispatch` *already makes per message* (`repickle_routable_pdu`) — zero extra round-trip, and the published `RoutedDeliverSmContent` envelope stays byte-identical (its header set is test-locked). Build a `routingfilter.Routable{MO}` and evaluate per-route filters alongside the existing `ConnectorFilter`.
- **Front-door byte-proof → test-only.** Extend the existing differential to drive the full legacy send path and both default + non-default connectors across a shape matrix; flip plan 003 to `done`.
- **Resilience → audit + soak test.** Keep the reconnect cadence faithful (no non-Jasmin exponential backoff); add a gated soak test over the in-process fake SMSC that forces repeated disconnects and asserts recovery/no-leak/no-loss.

Key trade-off: interception and content-filtering land in two components (smppc session vs `modispatch`) rather than one router. This is deliberate — each hook sits where its data naturally is, both reuse encoders already proven byte-identical, and correctness (ordering) is preserved through the pickle. The alternative (both in `modispatch`) would need a new bridge action to re-encode a mutated deliver PDU, adding parity surface for no behavioral gain.

## Steps

### Step 1: Front-door full send-path byte-differential (closes plan 003 Step 5)

- **Files:** `internal/transport/picklecompat/submit_encoder_frontdoor_differential_test.go` (extend), `docs/plans/003-front-door-connector-pdu-config.md` (status).
- **Changes:** Add table-driven differential cases that (a) cover the **default** connector (`SMPPClientConfig()` with no TON/NPI overrides → 2/1/1/1) as well as the existing non-default one; (b) drive the legacy body through the fuller send path — `SMPPOperationFactory(config).SubmitSM(...)` then, where reachable without a live connector, `jasmin...update_submit_sm_pdu`/`preSubmitSm` (if those require a connector instance, document in a comment that the factory path already applies the same connector param list and assert against it); (c) sweep a small shape matrix: plain, DLR-registered (`registered_delivery`), UDH/SAR multipart part, and custom `service_type`/`protocol_id`. Assert Go wire body (offset 16+) == legacy for every row. Update plan 003 Status to `done` with a one-line pointer here.
- **Verify:** `PYTHON_PATH=<venv> go test ./internal/transport/picklecompat/ -run FrontDoor -count=1 -v` — all rows pass. `go test ./...` shows no new failures.

### Step 2: MO interceptor config + table wiring

- **Files:** `internal/app/outbound/config.go` (add `MOInterceptors []InterceptorConfig`), `internal/app/outbound/filters.go` (reuse `buildInterceptorTable` — direction lives in the Routable, not the interceptor, so no signature change), `internal/app/outbound/runtime.go` (build the MO interceptor table beside the MT one, expose it), `internal/app/gateway/config.go`/`runtime.go` (parse + pass through).
- **Changes:** Parse `mo_interceptors` exactly like `mt_interceptors` (same `InterceptorConfig`: `order`/`py_code`/`filters`). Build a second `*interceptor.Table` from it. No behavior yet — just construct and hold it; empty config → empty table (no-op).
- **Verify:** `go build ./... && go vet ./...`; a unit test that a config with one `mo_interceptors` entry builds a non-empty MO table and rejects duplicate orders; `--check-config` (with `ADMIN_TOKEN=x`) accepts an example with `mo_interceptors`.

### Step 3: MO interception hook on the deliver path

- **Files:** `internal/core/smppc/deliver.go` (hook in `processDeliverMO`), `internal/core/smppc/connector.go` (`SetMOInterceptor` + carry into `connectAndBind`), `internal/core/smppc/session.go` (session fields + `SetMOInterceptor`), `internal/app/gateway/runtime.go` (wire the MO table + shared `pyintercept.Runner` into the connector factory via the existing deferred-vars seam).
- **Changes:** Add `SetMOInterceptor(table *interceptor.Table, runner interceptor.Runner)` (nil table = today's behavior). In `processDeliverMO`, after computing `content` and before the long-part/publish branch, when a table is set: build `routingfilter.Routable{Direction: MO}` from `pdu.SM` (source/dest/short_message/message_payload/tags-empty), call `table.Intercept(ctx, runner, routable)`. On `ActionReject` → log the legacy interceptor-drop line, ack with `ESME_ROK` (return 0), do not publish. On mutation → write `SourceAddr`/`DestinationAddr`/`ShortMessage` back onto a copy of `pdu.SM` and continue through the existing encode+publish (reusing `EncodeRoutableDeliverSM`). Apply the hook to the **reassembled** whole message too (run after reassembly, before `publishMO`), so multipart MO is intercepted once as a whole.
- **Verify:** `go test ./internal/core/smppc/ -run 'Deliver|Intercept' -count=1` — new tests: reject drops (no publish, resp status 0), mutate rewrites published wire (assert via the deliver publisher spy), nil table unchanged. `go vet ./...`.

### Step 4: Bridge decode-return for MO content view

- **Files:** `scripts/pickle_bridge.py` (`repickle_routable_pdu` action), `internal/transport/picklecompat/` (the Go seam that calls it — extend the return type), `internal/app/modispatch/service.go` (`RoutablePDURepickler` interface + call site).
- **Changes:** In `repickle_routable_pdu`, alongside the repickled bare PDU, also return `source_addr`/`destination_addr`/`short_message` (base64) and `tags` (from the routable's `.tags` if present, else `[]`) read off the already-unpickled `routable.pdu.params`. Extend the Go `RepickleRoutablePDU` return to `(pickle []byte, fields RoutableFields, err error)` (or a struct) and update `modispatch`'s interface + the one production call. Keep it one bridge round-trip.
- **Verify:** `PYTHON_PATH=<venv> go test ./internal/transport/picklecompat/ -run Repickle -count=1` — round-trip returns the pickle unchanged (existing assertion) plus the decoded fields matching a known deliver_sm. `go build ./...`.

### Step 5: MO content filters in modispatch route selection

- **Files:** `internal/app/modispatch/service.go` (`RouteConfig.Filters`, `selectRoute` signature + matching, `Handle` builds the Routable), `internal/app/gateway/config.go` (pass `Filters` through — it already threads `MORoutes`).
- **Changes:** Add `Filters []outbound.FilterConfig` (or a local `FilterConfig` mirror to avoid an import cycle — check direction; if cycle, define the filter specs in a shared package) to `RouteConfig`. Build each route's `[]routingfilter.Filter` at `NewService` time (reuse the `routingfilter` constructors; MO permits the `connector` filter and content filters, not `user`). In `Handle`, after the repickle-with-fields, construct `routingfilter.Routable{Direction: MO, ConnectorID: sourceCID, SourceAddr/DestinationAddr/ShortMessage from bridge fields}`; change `selectRoute` to require both the `FilterConnectorID` match (kept) **and** all route `Filters` to match. Default route (order 0) still matches unconditionally as fallback. Unroutable → the existing ack-and-drop.
- **Verify:** `go test ./internal/app/modispatch/ -count=1` — new tests: a content filter selects/rejects a route by `destination_addr`; existing connector-id-only routes still pass (backward compatible); envelope headers unchanged (existing golden assertion). `go vet ./...`.

### Step 6: Reconnection/resilience audit + soak test

- **Files:** `internal/core/smppc/connector_soak_test.go` (new, gated), possibly a small fix in `internal/core/smppc/connector.go`/`session.go` if the soak surfaces one; `cmd/jasmin-fake-smsc/main.go` if the sim needs a force-drop trigger.
- **Changes:** Add a soak test that stands up the in-process fake SMSC (or a minimal in-test SMPP listener), starts a connector, and loops N cycles of: reach `StatusBound` → submit/deliver a message → force-drop the server socket → assert the connector re-binds within `ConLossDelay+ε`. Assert across cycles: `runtime.NumGoroutine()` returns to a stable baseline (no per-cycle leak), no message is lost (durable submit is redelivered post-reconnect) and none double-settled. Audit the enquire_link/inactivity/reconnect chain against the test; keep the fixed-delay cadence (document that exponential backoff is intentionally omitted for Jasmin parity). Fold in any surfaced fix.
- **Verify:** `go test ./internal/core/smppc/ -run Soak -count=1 -timeout 120s` passes; run with `-race`. Goroutine baseline assertion holds. `go test ./...` green.

### Step 7: Docs + worklog

- **Files:** `configs/gateway.example.json` + `configs/README.md` (document `mo_interceptors` and MO route `filters`), `docs/worklog.md` (handoff entry), `docs/plans/009-*.md` status → `active`→`done`.
- **Changes:** Add commented example `mo_interceptors` and an MO route with content `filters`; worklog entry per the handoff skill (Done/Decisions/Next).
- **Verify:** `--check-config` accepts the updated example (`ADMIN_TOKEN=x go run ./cmd/gateway --config configs/gateway.example.json --check-config`); frozen oracle tree still frozen (`python3 scripts/compat/verify_baseline_tree.py` → 201 files, matching sha).

## End-to-end verification

Compose drill (`docker-compose.gateway.yml`): (1) inject an MO whose `short_message` matches an `mo_interceptors` reject rule → assert it is dropped (not delivered) and the interceptor-drop line is logged; (2) inject an MO whose `destination_addr` matches a content-filtered MO route → assert it lands on that route's connector, not the default; (3) inject a matching MO with a mutating interceptor → assert the delivered content is the mutated value; (4) `PYTHON_PATH` front-door differential green for default + non-default across the shape matrix; (5) soak test green under `-race`. Full `go test ./...` and `go vet ./...` clean; frozen oracle tree unchanged.

## Rollback

All four are additive and config-guarded: empty `mo_interceptors`/no route `filters` reproduce today's deliver path; the bridge decode-return is additive (extra JSON keys, existing consumers ignore them); Step 1 and Step 6 are test-only. Revert per-PR branch to undo.

## Risks

- **Bridge decode-return shape** — the extra keys must not change the repickled bytes (`modispatch`'s output contract is test-locked). Mitigation: return the pickle exactly as today; add fields as siblings; the Repickle round-trip test asserts pickle-unchanged.
- **MO interceptor mutation vs long-message reassembly ordering** — must intercept the *reassembled whole*, not each part. Mitigation: hook after reassembly (Step 3 explicitly covers the reassembled path).
- **Import cycle** for the MO `FilterConfig` in `modispatch` (it must not import `outbound`). Mitigation: mirror the small spec struct locally or lift `FilterConfig` to a shared package; decided during Step 5.
- **Front-door send-path reachability** — `update_submit_sm_pdu`/`preSubmitSm` may need a live connector instance. Mitigation: if unreachable in a unit oracle, assert against the factory path (which applies the same connector param list) and document the equivalence; the byte-proof still closes the strict-SMSC TON/NPI risk.
- **Soak flakiness** — timing-based reconnect assertions can flake in CI. Mitigation: gate the soak test (build tag or `SOAK=1`/`PYTHON_PATH`) so it is not in the fast merge gate; use generous, cadence-derived deadlines.

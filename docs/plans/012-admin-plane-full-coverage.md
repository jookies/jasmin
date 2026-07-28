# Admin plane full coverage — manage everything jCli manages, from the web UI

- **Date:** 2026-07-27
- **Status:** active — Steps 1–5, 7, 8 done; **Step 6 (groups) is the only one left**
- **Summary:** Extend the runtime, admin services and web UI to cover the six entity types jCli manages but the Go admin plane does not — MO routes, HTTP connectors, MT/MO interceptors, SMPPs bind users, and groups — so the browser (and the JSON API) becomes a complete management surface.
- **Related:** [plans/011-admin-web-ui.md](011-admin-web-ui.md), [adr/002-web-ui-stack.md](../adr/002-web-ui-stack.md), [adr/001-admin-provisioning-sqlite.md](../adr/001-admin-provisioning-sqlite.md), `spec/compatibility/JCLI_MATRIX.md`

## Context

The Go admin plane (`internal/app/admin` + `internal/app/adminweb`) manages **three** entity types: SMPP client connectors, MT routes, and users. jCli manages **nine**: those three plus groups, `httpccm` (HTTP connectors), `morouter` (MO routes), named filters, `mtinterceptor` and `mointerceptor` — plus `persist`/`load`, which the Go design makes obsolete (admin changes apply live and are written to SQLite in the same operation, surviving restart with no explicit save).

So today, dropping jCli would *lose* capability: MO routes, MO delivery destinations, interceptors, SMPPs bind users and groups are config-file-only and need a restart to change. This plan closes that gap.

**jCli is not being dropped — it is being reimplemented in Go too** ([plan 013](013-jcli-console.md)). The two are one project: `internal/app/admin` is the single management core, and jCli, the `/admin` JSON API and the web UI are three faces over it. That is the whole architectural point — jCli's problem was never telnet, it was that the console *was* the implementation, so automation had to screen-scrape transcripts. Every entity type below must therefore be reachable through a service method that is UI-agnostic; no business logic may live in a handler or a command.

The blocker is not UI work. Two runtime structures are built once at startup and cannot be swapped:

- `modispatch.Service` builds `s.routes` in `NewService` (`internal/app/modispatch/service.go:207-247`) and reads it lock-free in `selectRoute` (`:249`). No atomic holder.
- `interceptor.Table` is immutable after `TableBuilder.Build()` (`internal/core/interceptor/interceptor.go:89`); the MT table is built in `outbound.NewRuntime` (`runtime.go:202`) and the MO table in `gateway.NewRuntime` (`runtime.go:217`).

MT routes already solve exactly this problem, and every step below copies that proven shape.

## Approach

Replicate the MT-route provisioning chain for each new entity type. That chain is:

1. **Live-swappable state** — `routingtable.AtomicTable` (`internal/core/routingtable/atomic.go`), `Store` on write, lock-free `Select` on the hot path.
2. **Apply hook on the owning runtime** — `outbound.Runtime.ApplyAdminRoutes` (`internal/app/outbound/runtime.go:293`): take the mutation lock, reject collisions with config-owned entries (`ErrRouteOrderReserved`), rebuild the combined table from config + admin entries, then atomically `Store`.
3. **Provisioner adapter** in the gateway — `outboundRouteProvisioner` (`internal/app/gateway/dlrstore.go:27`) decoding opaque JSON into the typed config with `DisallowUnknownFields`.
4. **Admin service** — `admin.RouteService` (`internal/app/admin/route_service.go`): mutex-serialised, **apply-first-then-persist**, rolling back the live change if persistence fails, and re-applying at boot via `LoadAndApply`.
5. **BFF resource + UI page** — a flat REST resource in `internal/app/adminweb` and a Refine page in `web/src/pages/`.

Config-owned entities stay **reserved** throughout, exactly as connectors/routes/users already are: config owns its set, admin manages an additive set, and a collision is a `ErrConflict`.

Steps 1–2 (MO routes) and 3–4 (interceptors) are the substance. Steps 5–6 are smaller. Step 7 is a decision, not necessarily code. One PR per step.

## Steps

### Step 1: MO routes — live-swappable dispatch table — DONE

- **Files:** `internal/app/modispatch/service.go` (route storage + `selectRoute` ~:191-260, `NewService` ~:207), new `internal/app/modispatch/atomic_test.go`, `internal/app/gateway/runtime.go` (~:333 where the service is built).
- **Changes:** Move `service.routes []preparedRoute` behind an atomic holder (`atomic.Pointer[[]preparedRoute]`, mirroring `routingtable.AtomicTable`) so `selectRoute` stays allocation- and lock-free on the inbound path. Extract the `NewService` build loop (validate → `preparedRoute` → sort by order desc) into `prepareRoutes(routes []RouteConfig) ([]preparedRoute, error)` used by both construction and swap. Add `func (s *Service) ApplyRoutes(configRoutes, adminRoutes []RouteConfig) error`: reject an admin route whose `order` collides with a config route (new `ErrMORouteOrderReserved`), rebuild from the combined set, `Store` on success — never leaving a partially applied table.
- **Verify:** `go test ./internal/app/modispatch/ -race`. New test: build a service with a default route, swap in a static route, assert `selectRoute` picks the new one for a matching MO and the default otherwise; assert a reserved-order admin route is rejected and the **previous table stays live** (no partial apply); a concurrent `selectRoute`/`ApplyRoutes` loop under `-race` proves the swap is safe.

### Step 2: MO routes — store, service, API, UI — DONE

- **Files:** new `internal/app/admin/mo_route_service.go` + `mo_routes.go` (store table) + tests; `internal/app/admin/store.go` (migration for `admin_mo_routes`); `internal/app/gateway/dlrstore.go` (new `moRouteProvisioner`); `internal/app/gateway/runtime.go` (construct + `LoadAndApply`, pass into `adminweb.Deps`); new `internal/app/adminweb/handlers_mo_routes.go` + tests; `internal/app/adminweb/server.go` (mount `/api/mo-routes`); new `web/src/pages/mo-routes/{list,create,edit,form,index}.tsx`; `web/src/App.tsx` (resource registration).
- **Changes:** Mirror `RouteService` exactly, keyed by `order`. The MO route spec is `modispatch.RouteConfig`: `order`, `default`, `filter_connector_id`, content `filters`, and the destination `connector` (`{"type":"http","cid","url","method"}` or `{"type":"smpps","system_id"}`). The UI form is a destination-type switch (http vs smpps) plus the same filter block the MT route form already uses — extract that block from `web/src/pages/routes/form.tsx` into a shared `web/src/components/FilterList.tsx` rather than copying it, minus the `user` filter type which MO routes do not accept.
- **Verify:** `go test ./internal/app/admin/ ./internal/app/adminweb/ -run MORoute`. End-to-end on the compose stack: create an MO route to an HTTP sink through the UI, trigger an inbound MO via the fake SMSC's `/inject/mo`, and confirm the sink receives it **without a gateway restart** — that is the whole point of Step 1.

### Step 3: Interceptors — live-swappable tables — DONE

- **Files:** `internal/core/interceptor/interceptor.go` (atomic holder alongside `Table`), `internal/app/outbound/runtime.go` (~:202 MT table + the submit-path read), `internal/app/gateway/runtime.go` (~:204-220 MO table + the `smppc.MOInterceptor` adapter), new tests in both packages.
- **Changes:** Add `AtomicTable` to `internal/core/interceptor` (`Store(*Table)` / `Load() *Table`) and have the submit pipeline and the MO deliver hook read through it. Add `outbound.Runtime.ApplyAdminMTInterceptors([]InterceptorConfig)` and a gateway-side equivalent for MO, both rebuilding from config + admin entries with config orders reserved. **Note the runner dependency:** today the interceptor runner is started only when config declares interceptors (`gateway/runtime.go:204`). Once they can be added at runtime, the runner must either start eagerly whenever the admin plane is enabled, or be started lazily on first non-empty apply — pick lazy start, and fail the apply with a clear error if the runner cannot start, so the operator learns immediately rather than at the next message.
- **Verify:** `go test ./internal/core/interceptor/ ./internal/app/outbound/ ./internal/app/gateway/ -race -run Intercept`. Assert: swapping in a rejecting interceptor changes the next submit's outcome; swapping it out restores delivery; an apply with no runner available fails loudly and leaves the previous table live.

### Step 4: Interceptors — store, service, API, UI — DONE

- **Files:** new `internal/app/admin/interceptor_service.go` + store table + tests; gateway provisioner + wiring; new `internal/app/adminweb/handlers_interceptors.go` + tests; `web/src/pages/interceptors/*`; `App.tsx`.
- **Changes:** One service handling both directions, discriminated by a `direction` field (`mt` / `mo`), since the spec shape (`order`, `filters`, `py_code`) is identical. UI form: order, direction, the shared filter block, and a monospace textarea for `py_code` with the contract documented inline (the script receives `routable`, sets `smpp_status`/`http_status` to reject, and mutating `routable.pdu.params` rewrites the message).
- **Security — read before implementing.** Interceptor scripts are **arbitrary Python executed on the gateway host**. Putting them behind the web UI turns any admin-UI session into remote code execution. This is not new in kind (jCli can already set them, and the `/admin` API could be extended the same way), but a browser widens the exposure surface considerably. Mitigations, all of which belong in this step: gate interceptor editing behind an explicit `admin.allow_interceptor_editing` config flag defaulting to **false**; keep the UI listener internal-only (already documented); log every interceptor mutation with the authenticated username at WARN. If the deployment does not need runtime interceptor changes, leaving the flag off costs nothing.
- **Verify:** `go test ./internal/app/adminweb/ -run Interceptor`. Live: with the flag on, add an MT interceptor rejecting `.*STOP` through the UI, submit a matching message, get the rejection; delete it and confirm delivery resumes. With the flag off, the endpoints 404 and the UI hides the section.

### Step 5: SMPPs bind users — DONE

- **Files:** `internal/app/smppsserver/service.go` (`NewDirectory` at `:41`/`:86` → atomic directory + `ApplyUsers`), new `internal/app/admin/smpps_user_service.go` + store table + tests, gateway wiring, `internal/app/adminweb/handlers_smpps_users.go`, `web/src/pages/smpps-users/*`.
- **Changes:** Same chain. The SMPPs directory is currently rebuilt from `config.Users` at construction; put it behind an atomic holder and add an apply hook. Passwords are write-only end to end, exactly as the connector bind password and the HTTP user password already are.
- **Verify:** `go test ./internal/app/smppsserver/ ./internal/app/adminweb/ -run SMPPSUser`. Live: create a bind user in the UI, bind a real SMPP client with those credentials without restarting, then delete the user and confirm a fresh bind is rejected.

### Step 6: Groups

- **Files:** `internal/app/outbound/config.go` (`UserConfig.GroupID` + a `groups[]` block), `internal/app/outbound/runtime.go` (`runtimeDirectory` group installation, mirroring `applyUser`), `internal/core/billing` (wire the existing `billing.Group` — `billing.go:84-163` already implements group balance/quota/`CanApply`), `internal/app/outbound/filters.go` (enable the deferred `group` filter type — see the comment at `filters.go:64`), admin service + store + BFF + UI.
- **Changes:** This is the only step that adds a **new domain concept** rather than exposing an existing one: config users currently have no group, which is why the `group` filter is deferred even though routables carry a `GroupID`. Model groups as first-class (gid, balance, submit quota), let a user reference one, and charge the group per the legacy precedence rules. Verify the charge-order semantics against the frozen oracle before implementing — `spec/compatibility/ROUTING_BILLING_MATRIX.md` is the contract, and getting user-vs-group precedence wrong is a silent money bug.
- **Verify:** `go test ./internal/core/billing/ ./internal/app/outbound/ -run Group`, including a differential against the Python oracle for the charge precedence. Live: create a group, attach a user, submit, confirm the group balance decrements as the oracle says it should.
- **Running oracle differentials locally — read this before setting `PYTHON_PATH`.** The oracle tests fall into two groups with different needs, and satisfying one can break the other:

  1. **Bridge differentials** (`internal/transport/picklecompat`, e.g. `TestAMQPFixtureDecoding`) default to `python3` when `PYTHON_PATH` is unset, so on a machine without `smpp-pdu3` they *fail* rather than skip. A minimal venv fixes them:

     ```sh
     python3 -m venv .venv-oracle
     .venv-oracle/bin/python -m pip install --require-hashes -r compat/requirements-pickle-bridge.txt
     PYTHON_PATH=$(pwd)/.venv-oracle/bin/python go test ./internal/transport/picklecompat/
     ```

  2. **Legacy-import differentials** (`internal/config`, the send-path encoder) **skip** when `PYTHON_PATH` is unset. Setting it switches them on, and they then need the *whole* frozen stack — the `jasmin` package plus `twisted` — importable from the test's working directory. The minimal venv above does not provide that, so pointing `PYTHON_PATH` at it turns green skips into red failures.

  `compat/requirements-baseline.lock` cannot currently be installed as-is (`--require-hashes` rejects it: `coveralls` pulls an unpinned `coverage[toml]`), so there is no one-command local full-oracle setup today. Until that is fixed, either scope `PYTHON_PATH` to the picklecompat package only, or leave it unset and let CI (`go-rewrite-compat.yml`) run the full set. `.venv-oracle/` is gitignored.

  **This matters for Step 6:** the groups charge-precedence differential belongs to group 2, so it needs the full stack — fixing the baseline lock is a prerequisite for doing that work locally rather than blind.

### Step 7: Named filters — decision, then code only if chosen — DONE (inline kept)

- **Files:** decision recorded in `docs/adr/003-filter-model.md`; if adopted, `internal/app/admin/filter_service.go` + a reference-resolution pass in the route builders.
- **Changes:** jCli models filters as **named reusable objects** (`filter -a`, referenced by fid from routes); Go **inlines** them in each route. Inline is simpler, has no dangling-reference problem, and is what every current route/interceptor path already does. Named filters only pay off when the same non-trivial filter is reused across many routes and must be edited in one place. **Recommendation: keep inline, record the deviation, and revisit if operators actually ask.** If adopted instead, the UI would offer "pick a saved filter or define inline", and deleting a referenced filter must be refused.
- **Verify:** N/A if the recommendation stands — the deliverable is the ADR. If adopted: tests that a referenced filter cannot be deleted and that a route resolves its reference after restart.

### Step 8: Parity mapping and docs — DONE

- **Files:** `spec/compatibility/JCLI_MATRIX.md` (mapping column), `spec/compatibility/DEVIATIONS.md`, `configs/README.md`, `docs/STATUS.md`, `docs/worklog.md`.
- **Changes:** Add a column to the jCli matrix mapping every jCli command to its admin-API/UI equivalent, so the "can we retire the telnet console?" question becomes a table lookup instead of an argument. Record deviations explicitly: `persist`/`load` obsolete by design; filter model (per Step 7); anything intentionally not carried over.
- **Verify:** Every jCli command root (`user`, `group`, `smppccm`, `httpccm`, `mtrouter`, `morouter`, `filter`, `mtinterceptor`, `mointerceptor`, `stats`, `persist`, `load`) has a row that is either mapped or an approved deviation.

## End-to-end verification

On the compose stack, with only the config file's reserved entities present at boot, perform a **complete provisioning run entirely through the web UI** — no config edits, no restarts: create an SMPP connector and start it; create a group and a user in it; create an MT route with a filter; create an MO route to an HTTP sink; add an MT interceptor; create an SMPPs bind user. Then submit through the public port and confirm the message routes and is charged; inject an MO and confirm it reaches the sink; bind as the SMPPs user. Finally restart the gateway and confirm every entity is still live — proving apply-first-then-persist plus `LoadAndApply` across all six new types.

## Rollback

Each step is additive and independently revertible: the new config keys default to off, the new store tables are created but unused if the services are not constructed, and every apply hook falls back to config-only behavior when no admin entries exist. The interceptor-editing flag defaults to false, so Step 4 ships inert until deliberately enabled.

## Risks

- **Interceptor editing is remote code execution by design** — the single biggest risk here. Mitigated by the default-off flag, the internal-only listener, and audit logging of every mutation (Step 4).
- **Hot-path regressions from atomic swaps.** MO dispatch and interception sit on the inbound message path. Mitigated by copying the proven `AtomicTable` shape, keeping reads lock-free, and running the new tests under `-race`.
- **Partial application.** A rebuild that fails halfway must leave the previous table live. Every apply hook validates and builds fully before it stores, and each step's tests assert the previous state survives a rejected apply.
- **Billing precedence in Step 6** is a silent-money-bug risk; it must be differential-tested against the frozen oracle, not reasoned about.
- **Scope.** Six entity types is a lot for one branch. One PR per step, each independently shippable, with Steps 1–2 delivering the most operator value first.

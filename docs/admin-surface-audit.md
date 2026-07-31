# Admin surface capability audit

## Scope and method

This is a code audit of the three operator surfaces in this repository:

1. jCli, the telnet-compatible legacy console.
2. The bearer-authenticated REST API mounted below `/admin/`.
3. The React console and its session-authenticated `/api/` BFF.

The last two are different APIs. The `/admin/` API registers only connectors,
MT routes, and users (`internal/app/admin/handler.go:38-51`). The web BFF has a
larger private route set (`internal/app/adminweb/server.go:123-216`). jCli's
complete top-level command set is in
`internal/app/jcli/dispatch.go:14-18` and its dispatch table is in
`internal/app/jcli/dispatch.go:47-83`.

All three call the same in-process admin services. jCli describes that
relationship explicitly (`internal/app/jcli/server.go:1-4`), and the web BFF
receives those services plus live operational readers
(`internal/app/adminweb/server.go:39-73`). A capability marked **full** has the
complete operator action for the model exposed by that surface. **Partial**
means read-only, a material field or action is missing, the operation can leave
related state inconsistent, or the displayed result is not fully measured.
**Absent** means the exhaustive command or route registry has no entry for it.

This audit does not treat a rendered counter as evidence that the event is
measured. The project has not run against a real carrier, its instrumentation
is incomplete, and it has no load or soak evidence
(`docs/architecture.md:180-192`). The production-readiness plan calls
non-functional per-user and per-connector observability a blocking gap
(`docs/plans/017-smpp-production-readiness.md:101-125`).

## Capability matrix

### Access, users, and groups

| Operator capability | jCli | Admin REST API | Web console |
|---|---|---|---|
| Operator authentication | **Full.** Username/password login is constant-time; authentication can be disabled only by configuration (`internal/app/jcli/session.go:132-169`, `internal/app/jcli/server.go:90-101`). | **Full.** Every registered route requires a bearer token (`internal/app/admin/handler.go:54-65`). | **Full.** Session cookies protect reads and CSRF protects mutations (`internal/app/adminweb/auth.go:9-29`). |
| Gateway health and dependency readiness | **Absent.** There is no health command in the exhaustive command list (`internal/app/jcli/dispatch.go:14-18`). | **Absent.** There is no health route in the `/admin/` mux (`internal/app/admin/handler.go:38-51`). | **Full.** The BFF runs the shared probe with a five-second timeout and the dashboard renders its checks (`internal/app/adminweb/handlers_auth.go:66-77`, `web/src/pages/dashboard.tsx:43-74`). |
| Visibility of config-owned resources | **Partial.** It merges config-owned connectors, MT routes, and users, but not groups, MO routes, or standalone SMPPs accounts (`internal/app/jcli/server.go:62-73`, `internal/app/jcli/managers_routes.go:437-458`). | **Absent.** The three services list only persisted admin objects (`internal/app/admin/service.go:211-234`, `internal/app/admin/route_service.go:124-135`, `internal/app/admin/user_service.go:198-209`). | **Full.** All six configured entity families are supplied to the BFF and are returned as read-only resources (`internal/app/adminweb/server.go:59-66`, `internal/app/adminweb/handlers_connectors.go:35-75`, `internal/app/adminweb/handlers_smpps_users.go:31-78`). |
| User list and detail | **Full.** It merges configured and admin users and shows quotas and credentials (`internal/app/jcli/managers.go:278-375`). | **Partial.** List and detail return only `username` and `uid`, deliberately omitting the stored spec (`internal/app/admin/handler.go:240-256`, `internal/app/admin/handler.go:278-292`). | **Full.** The resource projects identity, status, quotas, permissions, filters, throughput, and SMPPs policy (`internal/app/adminweb/handlers_users.go:15-99`). |
| User create and edit | **Full.** Interactive add/update accepts identity, password, MT credentials, and SMPPs credentials (`internal/app/jcli/managers_user.go:26-136`, `internal/app/jcli/managers_user.go:150-191`). | **Full.** POST and PUT accept an opaque complete `outbound.UserConfig` and apply it live (`internal/app/admin/handler.go:240-272`, `internal/app/admin/handler.go:293-312`). | **Full.** Create and update preserve omitted fields and apply the complete web resource (`internal/app/adminweb/handlers_users.go:165-230`, `internal/app/adminweb/handlers_users.go:306-352`). |
| Credential issue and rotation | **Full.** Plaintext input becomes SHA-256 and is also mirrored to the SMPPs account where configured (`internal/app/jcli/managers_user.go:278-306`, `internal/app/jcli/managers_user.go:324-360`). | **Partial.** Raw SHA-256 or legacy MD5 verifier fields can be replaced, but no plaintext-to-hash or SMPPs mirror workflow exists (`internal/app/outbound/config.go:100-108`, `internal/app/admin/handler.go:293-312`). | **Full.** Password is write-only; create requires it and update preserves the old verifier when blank (`internal/app/adminweb/handlers_users.go:301-352`). |
| User balance, message quota, billing split, and throughput | **Full.** The credential editor sets balance, SMS count, early percentage, and both ingress throughput limits (`internal/app/jcli/managers_user.go:244-273`). | **Full.** The raw user shape contains these values (`internal/app/outbound/config.go:100-127`) and POST/PUT apply it (`internal/app/admin/handler.go:257-264`, `internal/app/admin/handler.go:293-304`). | **Full.** The form exposes all five values (`web/src/pages/users/form.tsx:93-135`). |
| User permissions, value filters, and default sender | **Full.** The console maps the full MT credential authorization and value-filter sets (`internal/app/jcli/managers_user_render.go:17-27`, `internal/app/jcli/managers_user_render.go:51-160`). | **Full.** The complete credential is accepted inside raw user JSON (`internal/app/outbound/config.go:145-198`, `internal/app/admin/handler.go:240-264`). | **Full.** The form exposes permissions, regex restrictions, and default source address (`web/src/pages/users/form.tsx:75-90`, `web/src/pages/users/form.tsx:138-190`). |
| Enable or disable a user | **Full.** The flag is applied to the gateway user and mirrored bind account (`internal/app/jcli/managers.go:420-444`). | **Partial.** PUT can set `disabled`, but it updates only `UserService`, not a mirrored SMPPs account (`internal/app/admin/handler.go:293-304`, `internal/app/admin/handler.go:313-318`). | **Full.** The form exposes the flag and the combined-user path copies it to the bind account (`web/src/pages/users/form.tsx:259-266`, `internal/app/adminweb/handlers_users.go:248-283`). |
| Delete a user | **Partial.** It attempts both gateway-user and same-name SMPPs-account deletion, but ignores failure of the latter (`internal/app/jcli/managers.go:400-417`). | **Partial.** It deletes only the gateway user (`internal/app/admin/handler.go:313-318`). | **Partial.** The handler deletes only the gateway user even though create/update can create a mirrored bind account (`internal/app/adminweb/handlers_users.go:232-285`, `internal/app/adminweb/handlers_users.go:355-371`). |
| Group create, edit, and delete | **Partial.** It can add and delete, but add accepts only `gid` and there is no edit verb (`internal/app/jcli/managers_group.go:14-44`, `internal/app/jcli/managers_group.go:47-64`). Group deletion also has the cross-account cascade defect described below. | **Absent.** No group route is registered (`internal/app/admin/handler.go:38-51`). | **Partial.** CRUD includes all group fields (`internal/app/adminweb/handlers_groups.go:99-177`), but deletion can strand members' mirrored SMPPs accounts (`internal/app/admin/group_service.go:158-199`). |
| Group shared balance and message quota | **Absent.** The group editor accepts only `gid` (`internal/app/jcli/managers_group.go:14-44`). | **Absent.** No group route is registered (`internal/app/admin/handler.go:38-51`). | **Full.** Both shared ceilings are editable and visible (`web/src/pages/groups/index.tsx:25-73`, `web/src/pages/groups/index.tsx:95-108`). |
| Enable or disable a group | **Full.** Dedicated verbs rewrite and live-apply the disabled flag (`internal/app/jcli/managers_group.go:47-64`, `internal/app/jcli/managers_group.go:106-135`). | **Absent.** No group route is registered (`internal/app/admin/handler.go:38-51`). | **Full.** Disabled is part of create/edit and status is shown in the table (`web/src/pages/groups/index.tsx:63-70`, `web/src/pages/groups/index.tsx:100-107`). |

### Connectors, routes, and policy libraries

| Operator capability | jCli | Admin REST API | Web console |
|---|---|---|---|
| SMPPc connector create, edit, and delete | **Partial.** CRUD is live, but the frozen field list omits newer reconnect, TLS verification, prefetch, service-type, durable-topology, and window controls (`internal/app/jcli/managers.go:53-82`, `internal/app/jcli/managers_smppccm.go:17-25`). | **Full.** POST/PUT decode the full `smppc.Config` (`internal/app/admin/handler.go:68-100`, `internal/app/admin/handler.go:125-147`). | **Partial.** CRUD is live and the form is broad, but it omits reconnect cap/jitter, window size, and insecure TLS verification (`web/src/pages/connectors/form.tsx:138-210`, `internal/core/smppc/config.go:60-71`, `internal/core/smppc/config.go:107-110`). |
| SMPPc start, stop, and observed status | **Full.** Dedicated start/stop verbs and list status call the live manager (`internal/app/jcli/managers.go:53-77`, `internal/app/jcli/managers.go:85-119`). | **Full.** Start/stop subresources and GET return live status (`internal/app/admin/handler.go:103-164`). | **Full.** Desired and observed states are returned; the list can start and stop (`internal/app/adminweb/handlers_connectors.go:23-32`, `web/src/pages/connectors/list.tsx:28-135`). |
| SMPPc connector traffic statistics | **Partial.** The counters are live as of the fix below; the per-connector clocks (`connected_at`, `bound_at`, `last_*_pdu_at`) are still untracked and render `ND` (`internal/app/jcli/managers_stats.go:146-215`). | **Absent.** No statistics route is registered (`internal/app/admin/handler.go:38-51`). | **Partial.** Same: counters live, clocks absent (`internal/app/adminweb/handlers_operations.go:25-42`, `web/src/pages/operations.tsx:221-263`). |
| Standalone SMPPs bind-account CRUD and policy | **Partial.** Gateway-user provisioning can mirror bind, IP, maximum-bind, and MT policy, but there is no standalone account manager (`internal/app/jcli/managers_user.go:296-360`, `internal/app/jcli/dispatch.go:14-18`). | **Absent.** No SMPPs account route is registered (`internal/app/admin/handler.go:38-51`). | **Full.** It has standalone CRUD for credentials, IP allowlist, binding limits, submit/DLR/source/priority policy, and status (`internal/app/adminweb/server.go:163-170`, `web/src/pages/smpps-users/form.tsx:5-93`). |
| Unbind or ban live SMPPs sessions | **Full.** User verbs revoke bind authorization before unbinding when banning (`internal/app/jcli/managers.go:205-275`). | **Absent.** No SMPPs session route is registered (`internal/app/admin/handler.go:38-51`). | **Full.** Per-system-ID unbind and ban routes disconnect active sessions; ban first disables the account (`internal/app/adminweb/handlers_smpps_users.go:186-238`). |
| MT route CRUD, filters, rates, and connector pools | **Partial.** Create/replace/delete is live, but `FailoverMTRoute` and `RandomRoundrobinMTRoute` collapse to the same connector list; rendering always calls a pool random-round-robin (`internal/app/jcli/managers_routes.go:22-28`, `internal/app/jcli/managers_routes.go:247-265`, `internal/app/jcli/managers_routes.go:404-412`). | **Full.** Raw `RouteConfig` list, create/replace, get, and delete are live (`internal/app/admin/handler.go:166-238`). | **Partial.** CRUD, rates, filters, and pools are present, but the form promises “failover/random pick” while runtime selection is ordered first-available, never random (`web/src/pages/routes/form.tsx:35-50`, `internal/app/outbound/runtime.go:1043-1047`). |
| Flush the admin MT route table | **Full.** `mtrouter --flush` deletes every stored admin route (`internal/app/jcli/managers_routes.go:417-435`, `internal/app/jcli/managers_routes.go:559-571`). | **Absent.** The route collection has only GET/POST and per-order CRUD (`internal/app/admin/handler.go:166-238`). | **Full.** The BFF and page expose a confirmed admin-only flush (`internal/app/adminweb/server.go:147-153`, `web/src/pages/routes/list.tsx:53-63`). |
| MO route CRUD to HTTP or SMPPs | **Partial.** Static/default routes work, but advertised random/failover routes read multiple destinations and silently store only the first (`internal/app/jcli/managers_routes.go:30-35`, `internal/app/jcli/managers_routes.go:289-303`). | **Absent.** No MO route is registered (`internal/app/admin/handler.go:38-51`). | **Full.** CRUD supports the runtime's actual single HTTP or SMPPs destination, connector filter, and content filters (`internal/app/adminweb/server.go:155-161`, `web/src/pages/mo-routes/form.tsx:58-155`). |
| Flush the admin MO route table | **Full.** `morouter --flush` deletes every stored admin MO route (`internal/app/jcli/managers_routes.go:576-593`, `internal/app/jcli/managers_routes.go:684-696`). | **Absent.** No MO route is registered (`internal/app/admin/handler.go:38-51`). | **Full.** A BFF route and confirmed page action clear admin-managed MO routes (`internal/app/adminweb/server.go:155-161`, `web/src/pages/mo-routes/list.tsx:33-63`). |
| Saved filter CRUD | **Partial.** Named entries are list/show/add-upsert/delete, but some types advertised as routable cannot be converted into a route (`internal/app/jcli/managers_filter.go:217-316`, `internal/app/jcli/managers_routes.go:128-190`). | **Absent.** No filter route is registered (`internal/app/admin/handler.go:38-51`). | **Partial.** The library has full CRUD for eleven types, but its route-template converter omits transparent, connector, user, and evaluation filters (`web/src/pages/filters/index.tsx:22-47`, `web/src/components/FilterList.tsx:44-64`). |
| Saved HTTP destination CRUD | **Full.** List/show/add-upsert/delete manages GET/POST URL templates copied into MO routes (`internal/app/jcli/managers_httpccm.go:26-85`, `internal/app/jcli/managers_httpccm.go:88-155`). | **Absent.** No HTTP destination route is registered (`internal/app/admin/handler.go:38-51`). | **Full.** Full CRUD is present and destinations can be copied into MO routes (`web/src/pages/http-connectors/index.tsx:22-113`, `web/src/pages/mo-routes/form.tsx:45-53`). |
| MT/MO interceptor CRUD and flush | **Full when enabled.** Both directions support list/show/add-upsert/delete/flush; disabled deployments return an explicit error (`internal/app/jcli/managers_interceptor.go:166-187`, `internal/app/jcli/managers_interceptor.go:286-323`). | **Absent.** No interceptor route is registered (`internal/app/admin/handler.go:38-51`). | **Full when enabled.** CRUD and directional flush are present and hidden when arbitrary host-code editing is disabled (`internal/app/adminweb/server.go:69-72`, `internal/app/adminweb/server.go:172-178`, `web/src/pages/interceptors/list.tsx:26-131`). |
| Save and restore named profiles | **Partial.** Save snapshots all admin tables, but load never re-applies standalone SMPPs users (`internal/app/admin/profiles.go:28-41`, `internal/app/jcli/managers_persist.go:82-124`). | **Absent.** No profile route is registered (`internal/app/admin/handler.go:38-51`). | **Full.** Save/restore covers all tables and restore re-applies SMPPs accounts as well as the other live services (`internal/app/adminweb/handlers_operations.go:74-129`, `web/src/pages/profiles.tsx:27-48`). |
| List, inspect, or delete saved profiles | **Absent.** Only persist/load are dispatched (`internal/app/jcli/dispatch.go:14-18`). | **Absent.** No profile route is registered (`internal/app/admin/handler.go:38-51`). | **Absent.** The BFF registers save/load only (`internal/app/adminweb/server.go:208-209`). |

### Statistics and live operations

| Operator capability | jCli | Admin REST API | Web console |
|---|---|---|---|
| Global HTTP and SMPPs statistics | **Partial.** It reads real registries, but some HTTP and SMPPs fields are inert and every timestamp except process start is `ND` (`internal/app/jcli/managers_stats.go:217-285`, `docs/operations/monitoring.md:81-130`). | **Absent.** No statistics route is registered (`internal/app/admin/handler.go:38-51`). | **Partial.** It polls the same mixed-coverage registries every five seconds and presents missing values as zero (`internal/app/adminweb/handlers_operations.go:25-42`, `web/src/pages/operations.tsx:79-85`, `docs/operations/monitoring.md:81-130`). |
| Per-user traffic and session statistics | **Partial, fabricated.** Every displayed per-user traffic, bind, error, and activity value is a literal zero, dash, or `ND` (`internal/app/jcli/managers_stats.go:81-143`). | **Absent.** No statistics route is registered (`internal/app/admin/handler.go:38-51`). | **Absent.** `/api/stats` contains only global HTTP, global SMPPs, and per-connector maps (`internal/app/adminweb/handlers_operations.go:13-42`). |
| Exact-message submit-state lookup | **Absent.** There is no message command (`internal/app/jcli/dispatch.go:14-18`). | **Absent.** There is no message route (`internal/app/admin/handler.go:38-51`). | **Partial.** Exact gateway ID lookup reports durable submit-part state only, not terminal DLR or billing outcome (`internal/app/adminweb/handlers_operations.go:45-62`, `internal/core/submittransaction/model.go:95-120`). |
| Send a test message through the live pipeline | **Absent.** There is no submit command (`internal/app/jcli/dispatch.go:14-18`). | **Absent.** There is no submit route (`internal/app/admin/handler.go:38-51`). | **Full.** The tool validates input and invokes the real submitter; the UI warns that it can consume quota and carrier credit (`internal/app/adminweb/handlers_tools.go:72-138`, `web/src/pages/operations.tsx:145-166`). |
| Read effective balance and quote a route rate | **Absent.** No such commands are dispatched (`internal/app/jcli/dispatch.go:14-18`). | **Absent.** No diagnostic routes are registered (`internal/app/admin/handler.go:38-51`). | **Full.** Dedicated tools call the live balance and rate readers (`internal/app/adminweb/handlers_tools.go:11-70`, `web/src/pages/operations.tsx:265-327`). |
| Inspect, drain, replay, or purge RabbitMQ queues | **Absent.** Route/interceptor “flush” commands delete configuration tables, not broker messages (`internal/app/jcli/managers_routes.go:559-571`, `internal/app/jcli/managers_interceptor.go:307-323`). | **Absent.** No queue route is registered (`internal/app/admin/handler.go:38-51`). | **Absent.** No queue BFF route is registered (`internal/app/adminweb/server.go:130-209`). |
| CDR, delivery outcome, billing outcome, or export | **Absent, deliberately.** jCli's value is replaying the legacy console byte-for-byte; billing verbs have no Python oracle. Plan 019 records the reasoning. | **Full** (was Absent; plan 019). `/admin/cdrs`, `/admin/cdrs/{id}`, `/admin/cdrs/{id}/events`, `/admin/cdrs/export`, `/admin/billing/summary` and `/admin/billing/accounts`, registered through `admin.WithBilling` and gated by the existing bearer token (`internal/app/admin/handlers_billing.go`). Audited as subject `admin-api`, distinguishable from console reads. | **Full** (was Absent; plan 019). `/api/billing/cdrs` searches by customer and admitted window with cursor paging, `/{id}` and `/{id}/events` give the rated part and its immutable event timeline, and `/api/billing/export` streams the core's CSV/JSONL bytes with the next cursor in a header (`internal/app/adminweb/handlers_billing.go`). Reads carry the session operator as `cdr.Principal.Subject`, so each one lands in `cdr_access_audit`. |
| Live account balance versus the provisioned grant | **Partial.** The credential editor shows the provisioned values only (`internal/app/jcli/managers_user.go:244-273`). | **Partial.** The raw user shape is the provisioned spec. | **Full** (was misleading; plan 019). `/api/billing/accounts` and the user resource carry granted and remaining balance and message quota side by side, plus the group's shared ceiling. An unreadable live value is reported as an error string, never as zero. |
| Rated usage summary per customer | **Absent.** | **Absent.** | **Full** (was Absent; plan 019). `/api/billing/summary` aggregates in SQL over a required window, grouped by `(user_id, currency)`, separating money actually charged from late money still quoted (`internal/core/cdr/operations.go`, both storage backends). |
| Guided partner onboarding | **Absent.** There is no onboarding command (`internal/app/jcli/dispatch.go:14-18`). | **Absent.** There is no onboarding route (`internal/app/admin/handler.go:38-51`). | **Full** (was a frontend-only prototype; plan 019 step 8). `POST /api/onboarding/partners` provisions the group, gateway user, SMPPs bind account, connector and MT route in one call, undoing everything it created if a later step fails (`internal/app/adminweb/handlers_onboarding.go`). Credentials are generated server-side and displayed once; the bind password is capped at eight characters because SMPP 3.4 caps it there. Onboarding creates only — an existing partner code is refused rather than overwritten. |
| Invoice or customer statement generation | **Absent.** There is no billing-document command (`internal/app/jcli/dispatch.go:14-18`). | **Absent.** There is no billing-document route (`internal/app/admin/handler.go:38-51`). | **Partial** (was Absent; plan 019). Rated usage statements per customer and window exist (`web/src/pages/billing/statements.tsx`), and they reconcile against the export for the same window. An invoice *document* — numbering, tax, payment terms — is still out of scope and belongs in the accounting system. |

## Direct answers

### 1. What can jCli do that the web console cannot?

There is no additional reliable business operation in jCli that the web cannot
perform. The old console's unique value is its byte-stable, scriptable legacy
transcript (`internal/app/jcli/server.go:6-10`). It is also currently the only
surface whose individual user-delete path *attempts* to remove both the gateway
user and the mirrored SMPPs account, but it ignores failure of the latter
(`internal/app/jcli/managers.go:400-417`). That is still unsafe, so it is not a
reliable capability advantage.

jCli nominally offers per-user statistics, random-round-robin and failover MO
routes, and more named filter types. Those must not be counted as advantages:
the per-user data is literal (`internal/app/jcli/managers_stats.go:81-143`), MO
multi-destination routes keep only index zero
(`internal/app/jcli/managers_routes.go:289-303`), and the route converters reject
several types that the filter manager advertises
(`internal/app/jcli/managers_filter.go:47-55`,
`internal/app/jcli/managers_filter.go:105-116`,
`internal/app/jcli/managers_routes.go:128-190`).

### 2. What can the web console do that jCli cannot?

The web is materially broader. It has live readiness, editable group balance and
quota, standalone SMPPs account CRUD, a correct single-destination MO route
editor, an SMPPs-aware profile restore, exact submit-state lookup, live
balance/rate diagnostics, and real test submission. The route registry shows
those operational endpoints together
(`internal/app/adminweb/server.go:130-209`); the matching jCli command registry
does not (`internal/app/jcli/dispatch.go:14-18`). It also shows config-owned
groups, MO routes, and SMPPs accounts read-only, which jCli does not receive
(`internal/app/adminweb/server.go:59-66`,
`internal/app/jcli/server.go:62-73`).

The web is not a complete superset at the field level. Its SMPPc form omits four
`smppc.Config` fields described in the next section, and user/group deletion can
leave bind accounts behind.

### 3. What can the REST API do that neither UI exposes?

Because connector bodies decode the complete `smppc.Config`
(`internal/app/admin/handler.go:79-88`,
`internal/app/admin/handler.go:125-132`), the REST API alone can set
`reconnect_backoff_max`, `reconnect_backoff_jitter`,
`tls_insecure_skip_verify`, and `window_size`
(`internal/core/smppc/config.go:60-71`,
`internal/core/smppc/config.go:107-110`). jCli's exhaustive field list omits all
four (`internal/app/jcli/managers_smppccm.go:17-25`), and the web's connection
and TLS form omits them (`web/src/pages/connectors/form.tsx:138-210`).

The REST user body can also provision the legacy `password_md5` verifier
(`internal/app/outbound/config.go:100-108`). jCli and the web accept plaintext
and create SHA-256 instead (`internal/app/jcli/managers_user.go:278-283`,
`internal/app/adminweb/handlers_users.go:301-304`). This is migration
compatibility, not a reason to prefer MD5 for new credentials.

No other REST-only capability was verified. Its apparent breadth comes from raw
config bodies; its resource families are much narrower than either UI.

### 4. Which UI capabilities show fabricated or inert data?

> **Correction, applied after this audit was written.** The per-connector SMPPc
> counters were inert when this was compiled: `SMPPcRegistry` was handed to jCli,
> adminweb and the outbound runtime as readers and written by nothing, so the web
> console's "Connector counters" panel reported zeros for a connector carrying
> real traffic while captioned "Live process counters". They are now incremented
> by the connector and session (bind, disconnect, submit request/accept/throttle/
> other-failure, inbound `deliver_sm`/`data_sm`, `enquire_link`) and verified on a
> running stack. The per-connector **clocks** remain untracked, and everything
> else below still stands.

This is the highest-risk finding because an unavailable metric would be safer
than a healthy-looking zero.

* jCli's list-level connector start and stop counters are hard-coded zero
  (`internal/app/jcli/managers.go:111-115`).
* Every jCli per-user statistic is fabricated from zero, dash, or `ND`
  (`internal/app/jcli/managers_stats.go:81-143`).
* Every legacy per-SMPPc counter shown by both jCli and the web is inert. The
  registry returns a complete zero map for an unknown/unwritten connector
  (`internal/core/stats/stats.go:166-190`), and there is no production `Inc`
  caller (`docs/operations/monitoring.md:102-110`).
* The web labels those values “Live process counters”
  (`web/src/pages/operations.tsx:221-263`). Its HTTP failure total includes the
  inert `charging_error_count`, while auth, route, and server errors are live
  (`web/src/pages/operations.tsx:182-197`,
  `docs/operations/monitoring.md:81-100`).
* Global SMPPs values are mixed, not wholly fake: current sessions, binds,
  submits, deliveries, and several totals are live, while five defined fields
  are inert (`docs/operations/monitoring.md:112-130`).
* The partner-onboarding page is not backed by anything, but it clearly labels
  itself a frontend-only mock, so it is not deceptive
  (`web/src/pages/partner-onboarding.tsx:210-233`,
  `web/src/pages/partner-onboarding.tsx:259-265`).

> **Second correction, applied 2026-07-30 (plan 019).** Two more values in this
> family were wrong rather than missing, and both are now fixed. The user list's
> "balance" column rendered the **provisioned grant**, not the live balance, so a
> customer down to 3.42 still showed 100 — granted and remaining are now separate
> columns. And the rate quote discarded its destination argument, returning the
> **highest-order** route's rate for every destination and never refreshing after
> a runtime route change; it now resolves the live routing table.

There is a related semantics defect rather than fabricated telemetry: the web
offers “failover/random pick,” but the runtime always chooses the first
available connector in list order (`web/src/pages/routes/form.tsx:39-44`,
`internal/app/outbound/runtime.go:1043-1047`). jCli accepts both class names but
stores the same shape and renders every pool as random-round-robin
(`internal/app/jcli/managers_routes.go:22-28`,
`internal/app/jcli/managers_routes.go:247-265`,
`internal/app/jcli/managers_routes.go:404-412`).

### 5. What is missing from all three for day-to-day SMS operations?

The largest omission is complaint investigation. Durable CDR records already
contain SMSC IDs, submit status, terminal delivery state/error, billing outcome,
and amounts (`internal/core/cdr/model.go:159-179`). The core can read and export
them with access auditing (`internal/core/cdr/operations.go:122-127`,
`internal/core/cdr/operations.go:161-211`), but none of the three surface
registries exposes that service. The web's exact-ID tool stops at the durable
submit transaction, before terminal delivery and billing
(`internal/app/adminweb/handlers_operations.go:45-62`,
`internal/core/submittransaction/model.go:95-120`).

The resulting missing daily workflows are:

* search by customer, destination, time window, gateway ID, or SMSC ID; inspect
  route/connector attempts, terminal DLR, callback result, and charge;
* export customer usage and produce an invoice or statement from rated CDRs;
* inspect queue depth and consumers, then safely drain or replay a selected
  workload without reaching for RabbitMQ administration;
* monitor truthful per-user and per-connector traffic, rejection, latency, DLR,
  and billing data;
* onboard a customer atomically across group, gateway credentials, SMPPs bind
  policy, routes, filters, and a credential handoff;
* suspend or delete a customer across both ingress protocols and active sessions
  as one operation; and
* keep a durable, queryable change audit with separate read-only, support,
  billing, and administrator roles. Today each surface has one shared operator
  credential (`internal/app/jcli/server.go:81-96`,
  `internal/app/admin/handler.go:54-65`,
  `internal/app/adminweb/server.go:73-76`); only interceptor mutations get an
  explicit actor log in the web BFF
  (`internal/app/adminweb/handlers_interceptors.go:68-76`).

Not verified: deployment-specific CRM, invoicing, log aggregation, or RabbitMQ
management tooling outside this repository. Look in the deployment environment
and adjacent business systems before assuming these workflows do not exist
elsewhere.

## Bugs and unsafe inconsistencies found

These are findings, not fixes:

1. **Web and REST user deletion can leave valid SMPPs credentials behind.**
   Both delete only through `UserService`
   (`internal/app/admin/handler.go:313-318`,
   `internal/app/adminweb/handlers_users.go:355-371`), even though web
   create/update can mirror an SMPPs account
   (`internal/app/adminweb/handlers_users.go:232-285`).
2. **Group deletion has the same problem at cascade scale.** It deletes member
   users from the outbound provisioner and store but never calls
   `SMPPsUserService` (`internal/app/admin/group_service.go:158-199`).
3. **jCli profile restore is incomplete.** Profiles snapshot
   `admin_smpps_users` (`internal/app/admin/profiles.go:28-41`), but jCli load
   does not call `SMPPsUsers.LoadAndApply`
   (`internal/app/jcli/managers_persist.go:103-124`). The web load does
   (`internal/app/adminweb/handlers_operations.go:98-123`).
4. **jCli MO failover/random routes silently discard destinations after the
   first.** Multiple values parse successfully
   (`internal/app/jcli/managers_routes.go:192-203`), then only `kinds[0]` and
   `cids[0]` are stored (`internal/app/jcli/managers_routes.go:289-303`).
5. **MT pool labels promise selection modes the data model does not store.**
   Both legacy class names become only `ConnectorIDs`
   (`internal/app/jcli/managers_routes.go:22-28`,
   `internal/app/jcli/managers_routes.go:247-257`), while runtime behavior is
   ordered first-available (`internal/app/outbound/runtime.go:1043-1047`).
6. **Saved-filter libraries advertise unusable route templates.** jCli advertises
   group and evaluation filters for MT but its converter has neither case
   (`internal/app/jcli/managers_filter.go:47-55`,
   `internal/app/jcli/managers_filter.go:105-116`,
   `internal/app/jcli/managers_routes.go:128-158`). The web library creates
   eleven types, but the route-template converter handles only seven
   (`web/src/pages/filters/index.tsx:22-47`,
   `web/src/components/FilterList.tsx:44-64`).
7. ~~**The DLR runbook relies on an inert metric.**~~ **Closed 2026-07-30**
   (plan 021 step 11). `synevyr_dlr_total` now has production recorders on both
   correlation legs, so `docs/runbooks/dlrs-not-arriving.md` reports real
   outcomes. One caveat the runbook's `correlation_failure` wording should be
   read with: a missing DLR map on the `submit_sm_resp` leg is deliberately not
   counted, because the gateway publishes that leg for every submit including
   the majority that requested no receipt
   (`internal/core/dlr/lookup_consumer.go:115`).

## Prioritised recommendations

1. **Stop presenting unmeasured values as zero, then wire the measurements
   (2-4 days).** This is present-but-fake, which is worse than a missing
   feature: an operator can conclude that a busy or failing connector has no
   traffic or errors. Immediately label or hide inert cells, then add production
   writers for SMPPc counters and per-user activity. Include tests that send
   traffic and assert non-zero surface output.

2. **Expose CDR search, detail, events, and export in the BFF/web
   (3-5 days).** This is the shortest path to answering “what happened to my
   message?” and creating usage evidence. Much of the durable model,
   authorization, audit, and CSV/JSONL export already exists
   (`internal/core/cdr/operations.go:122-211`). Start with exact ID and
   customer/time search; add SMSC ID and destination indexes only after checking
   query plans and privacy requirements.

3. **Make customer suspension and deletion cross-service and atomic
   (1-2 days).** Fix the shared workflow once so REST, web, and group cascades
   cannot leave an SMPPs account or live session behind. This directly protects
   the non-payer and credential-compromise workflows. A failure must roll back
   or report a visibly partial result rather than return ordinary success.

4. **Correct route and saved-filter semantics before adding more UI
   (2-3 days).** Either implement and persist an explicit MT pool policy or label
   the existing behavior “ordered failover”; reject unsupported MO multi-route
   classes; and make every advertised saved filter insertable or remove it from
   the chooser. These are present-but-misleading features, again worse than
   absence.

5. **Complete profile restore and add profile discovery
   (1-2 days).** Reapply SMPPs accounts from jCli, list profiles with timestamps
   and scope, and allow guarded deletion. Operators need to know which rollback
   point exists before an incident, not guess a profile name.

6. **Turn onboarding into a real, transactional workflow
   (4-7 days).** Build on the existing prototype, but produce a reviewable plan
   and commit group, gateway user, bind account, route, and filter changes
   together. Generate credentials once, support a secure handoff, and run a
   balance/rate/authentication check before declaring completion.

7. **Add guarded queue operations and truthful depth
   (3-5 days).** First expose queue, ready, unacknowledged, and consumer counts.
   Then add narrowly scoped pause/drain/replay actions with confirmation and an
   audit record. Do not begin with an unrestricted purge; the DLR runbook already
   warns that deleting correlation state during an incident is unsafe
   (`docs/runbooks/dlrs-not-arriving.md:41-45`).

8. **Build rated usage statements, then invoicing integration
   (1-2 weeks).** Treat this as a missing business feature, not a telemetry fix.
   Start from exported CDRs, preserve currency and early/late billing outcomes,
   reconcile totals, and hand a stable statement to the accounting system.
   Tax, payment collection, and jurisdiction-specific invoices should remain
   outside the gateway unless product scope explicitly brings them in.

9. **Add operator roles and a durable change audit
   (3-5 days).** Separate read-only support, traffic operations, billing, and
   configuration administration. Record before/after identity, actor, surface,
   time, and result for every mutation, with special handling for secret fields
   and arbitrary interceptor code.

10. **Bring safe REST and UI field parity after operational correctness
    (1-2 days).** Add reconnect cap/jitter and window controls to the web with
    validation. Keep `tls_insecure_skip_verify` available only behind an explicit
    high-risk acknowledgement. Expanding CRUD before the false-data, complaint,
    and suspension gaps would improve configuration breadth without making the
    gateway safer to operate.

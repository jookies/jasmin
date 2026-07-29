# Perspective Broker compatibility facade

The Go gateway does not accept Twisted PB, jelly references, or Python pickle.
Those formats can construct Python objects and have no safe, byte-compatible
generic Go decoder. The migration architecture in
`spec/compatibility/PB_API_MATRIX.md` therefore keeps a small trusted Python
process at the legacy PB port and gives that process this authenticated,
versioned Go control seam.

The Go implementation is `internal/app/pbfacade`. It calls the same live admin
services as jCli and the web UI; it is not a second configuration store.

## Transport

- Private HTTP listener, with TLS or a loopback/private transport boundary
- `Authorization: Bearer <token>` on every request
- `POST /v1/call`
- `GET /v1/capabilities`
- Maximum call body: 1 MiB
- Exact protocol version: `jasmin.pb-facade.v1`
- JSON decoders reject unknown envelope and parameter fields
- The server never accepts pickle, PB frames, or jelly objects

A request has this envelope:

```json
{
  "version": "jasmin.pb-facade.v1",
  "id": "opaque-facade-call-id",
  "method": "router.group.add",
  "params": {
    "id": "customers",
    "spec": {
      "id": "customers",
      "enabled": true
    }
  }
}
```

Success and failure are explicit:

```json
{"version":"jasmin.pb-facade.v1","id":"opaque-facade-call-id","ok":true,"result":true}
```

```json
{
  "version": "jasmin.pb-facade.v1",
  "id": "opaque-facade-call-id",
  "ok": false,
  "error": {"code": "not_found", "message": "admin: group not found"}
}
```

Stable error codes are `unauthorized`, `bad_request`,
`unsupported_version`, `unknown_method`, `unavailable_method`,
`invalid_params`, `conflict`, `not_found`, and `internal_error`.

## Implemented compatibility projection

`pbfacade/daemon.py` terminates the four frozen PB services and translates
them to the private Go facade. The sidecar lives in the top-level `pbfacade/`
package and *imports* the frozen `jasmin.*` tree as an unmodified dependency; it
must never be moved back inside `jasmin/` or `tests/`, which
`scripts/compat/verify_baseline_tree.py` hashes as the immutable oracle. It retains the historical digest login,
Perspective/Deferred call shape, serialized object results, and default ports.

| Frozen service/capability | Go facade projection | Functional |
|---|---|---:|
| RouterPB digest auth/version | trusted PB listener + `version` | 100% |
| groups CRUD, bulk remove, enable/disable, list | `router.group.*` | 100% |
| users CRUD, bulk remove, auth, quotas, enable/disable, list | `router.user.*` | 100% |
| MO/MT routes CRUD, flush, ordered serialized list | `router.moroute.*`, `router.mtroute.*` | 100% |
| MO/MT interceptors CRUD, flush, ordered serialized list | `router.mointerceptor.*`, `router.mtinterceptor.*` | 100% |
| Router profile persist/load/state | `profile.*` with scoped live reconciliation | 100% |
| connector CRUD/config/list | `client.connector.*` | 100% |
| connector lifecycle/status/session state/counters | `client.connector.*` | 100% |
| connector profile persist/load/state | `profile.*` with connector scope | 100% |
| manager submit (target, bill, DLR, priority, expiry, linked PDUs) | ordered byte-exact `pdu_wires` to `router.submit_sm` | 100% |
| SMPPServerPB list/unbind/ban/deliver | `smpps.*` | 100% |
| InterceptorPB script execution | isolated trusted Python avatar | 100% |

Only the trusted sidecar calls `pickle.loads`. The Go listener accepts
normalized JSON and byte-exact SMPP wire PDUs only. PB-origin objects retain an
opaque legacy pickle for exact list round trips. Objects created through
REST, jCli, or another HA replica do not have that field, so the translator
deterministically reconstructs the corresponding frozen Group, User, Route,
Interceptor, and SMPPClientConfig class. Mixed-origin state is therefore
visible to legacy PB clients after failover.

Named profile loads are transactional at the stored-row boundary and do not
return success until every affected live service has reconciled. A failed
reconcile restores the previous snapshot and live state. Connector lifecycle
counters are maintained by the facade process, matching their historical
process-local lifetime.

The manager-submit projection is explicitly downstream of routing,
interception and early charging, just like the frozen manager. The private Go
request therefore honors the supplied connector and serialized bill instead
of selecting a new route or charging again. It keeps durable admission, late
billing and DLR storage, and preserves a linked multipart `nextPdu` chain as
bounded, cycle-checked, ordered SMPP frames under one aggregate message ID.

`delQueues=True` is deliberately rejected: the Go deployment uses durable
shared AMQP topology and deleting a per-connector queue is not a safe or
equivalent operation. The facade never returns a synthetic success for it.
Native Go interceptor execution remains the final migration step; until then
the compatibility listener executes the frozen script in its isolated Python
process.

## Gateway integration

The gateway constructs `pbfacade.Deps` from the already-created admin services
and serves `Routes()` on the separate
`admin.pb_facade_listen_address`. `admin.pb_facade_token` accepts the normal
`env:` / `file:` secret references. The facade is never mounted on the public
send API, and its listener shares the configured inbound TLS certificate when
TLS is enabled.

```go
handler, err := pbfacade.New(pbfacade.Deps{
    Connectors:   adminService,
    Users:        userService,
    Groups:       groupService,
    MTRoutes:     routeService,
    MORoutes:     moRouteService,
    Interceptors: interceptorService,
    Profiles:     profiles,
    Authenticator: outboundRuntime.Authenticator(),
    Submitter:     outboundRuntime.Submitter(),
    SMPPServer:    pbSMPPServerSlot,
    ScriptRunner:  interceptorRunner,
    Token:        configuredFacadeToken,
})
```

The SMPP server dependency uses a late-binding slot because the private PB
handler is assembled before the public SMPP listener. The slot fails closed
until the live server exists.

## Running the trusted sidecar

Set the same bearer secret configured by
`admin.pb_facade_token`, then point the sidecar at the private listener:

```bash
export JASMIN_PB_FACADE_TOKEN='replace-me'
python -m pbfacade.daemon \
  --go-url http://127.0.0.1:8998 \
  --router-bind 127.0.0.1 \
  --client-bind 127.0.0.1 \
  --smpps-bind 127.0.0.1 \
  --interceptor-bind 127.0.0.1
```

The HA compose deployment builds `docker/Dockerfile.pbfacade`, supplies the
token through a secret environment file, binds the legacy ports to loopback,
and restarts the sidecar independently of the active gateway replica. Use
`--tls-cert` and `--tls-key` together when PB itself crosses a network trust
boundary.

The PB macro runs both the authenticated Twisted listener replay and the
focused Go facade/profile tests. Matrix rows are `GO-COMPLETE`, not `MATCH`:
the compatibility implementation is functionally complete, while a full
frozen-oracle fixture corpus remains a separate evidence task.

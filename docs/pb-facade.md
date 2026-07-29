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

## Implemented method projection

These are facade methods, not claims that a native Go PB listener exists.
The Python side is responsible for translating the named frozen
`perspective_*` call and reconstructing its historical Deferred/result shape.

| Frozen PB operation | Go facade method | Status |
|---|---|---|
| `perspective_version*` | `version` | Implemented |
| `perspective_group_add` | `router.group.add` | Implemented |
| `perspective_group_remove` | `router.group.remove` | Implemented |
| `perspective_group_get_all` | `router.group.list` | Implemented as normalized JSON list |
| facade lookup helper | `router.group.get` | Implemented |
| `perspective_user_add` | `router.user.add` | Implemented |
| `perspective_user_remove` | `router.user.remove` | Implemented |
| `perspective_user_get_all` | `router.user.list` | Implemented as normalized JSON list |
| facade lookup helper | `router.user.get` | Implemented |
| `perspective_mtroute_add/remove/get_all` | `router.mtroute.add/remove/list` | Implemented |
| facade lookup helper | `router.mtroute.get` | Implemented |
| `perspective_moroute_add/remove/get_all` | `router.moroute.add/remove/list` | Implemented |
| facade lookup helper | `router.moroute.get` | Implemented |
| `perspective_mtinterceptor_add/remove/get_all` | `router.mtinterceptor.add/remove/list` | Implemented when interceptor editing is enabled |
| facade lookup helper | `router.mtinterceptor.get` | Implemented when interceptor editing is enabled |
| `perspective_mointerceptor_add/remove/get_all` | `router.mointerceptor.add/remove/list` | Implemented when interceptor editing is enabled |
| facade lookup helper | `router.mointerceptor.get` | Implemented when interceptor editing is enabled |
| `perspective_connector_add` | `client.connector.add` | Implemented |
| `perspective_connector_remove` | `client.connector.remove` | Implemented |
| `perspective_connector_list` | `client.connector.list` | Implemented as normalized JSON list |
| `perspective_connector_details/config` | `client.connector.get` | Implemented as one normalized projection |
| `perspective_connector_start/stop` | `client.connector.start/stop` | Implemented |
| `perspective_service_status` | `client.connector.status` | Implemented as normalized desired/observed state |

Group/user specs, route specs, interceptor specs, and connector configs are
normalized JSON objects. They are not base64-wrapped pickle. List results are
JSON arrays in deterministic service order. Stored specs are returned as JSON
objects rather than JSON strings.

## Remaining PB gaps

The following frozen calls are not advertised by `/v1/capabilities` and return
`unknown_method` if called:

- PB challenge/digest login, PB reconnect behavior, remote references, jelly
  object identities, Deferred error classes, and direct compatibility-client
  connectivity. These belong in the trusted Twisted facade.
- Group/user enable and disable, bulk remove/flush, user authentication, and
  quota mutation operations.
- Router and connector `persist`, `load`, and `is_persisted` behavior. The Go
  admin store is durable after each mutation, while named profile restoration
  must also reapply all live services atomically before it can be exposed.
- Connector session-state distinctions, stop-all, queue-deletion semantics,
  and exact frozen config pickle reconstruction.
- `perspective_submit_sm`; it carries pickled PDU and billing objects and is a
  data-plane operation, not admin CRUD.
- SMPPServerPB bound-session listing, unbind/ban, and deliverer send.
- InterceptorPB `run_script`; the current Go interceptor worker uses its own
  bounded protocol and is not exposed through this management seam.

The facade must not synthesize success for any of these calls. A later
increment must add a normalized Go method, fixture its frozen behavior, and
only then advertise it.

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
    Token:        configuredFacadeToken,
})
```

The snippet above is the implemented gateway composition, not only a future
hook. The trusted Twisted translator itself is still a remaining gap.

This increment does not promote any `PB_API_MATRIX.md` row: promotion still
requires a frozen PB fixture/macro and an actual Python facade replay.

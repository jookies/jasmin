# Security boundaries

Synevyr has several listeners with very different authority. Do not treat “the
gateway port” as one trust boundary. The HTTP send API and SMPP server accept
customer traffic; the admin REST API, browser UI, and jCli can change live
routing, credentials, balances, and connectors
(`internal/app/gateway/runtime.go:480`,
`internal/app/adminweb/server.go:123`).

The shipped production Compose file publishes the data plane on all host
interfaces and publishes management ports on host loopback. PostgreSQL,
RabbitMQ, and Redis have no host ports
(`docker-compose.prod.yml:64`, `docker-compose.prod.yml:171`). Those are
Compose publication defaults, not application defaults: listener addresses
come from JSON, and `outbound.listen_address` is required rather than
defaulted (`internal/app/outbound/runtime.go:922`).

## Listener inventory

| Configured listener | Shipped production exposure | Authority |
|---|---|---|
| `outbound.listen_address` | Public host port 1401 | `/send`, `/rate`, `/balance`, `/ping`, legacy `/metrics`, health endpoints, and `/secure/*`. It admits MT and reveals account rate/balance to an authenticated user (`internal/transport/httpcompat/handler.go:71`, `internal/transport/restcompat/config.go:18`, `internal/app/gateway/runtime.go:346`). |
| `rest_api.listen_address` | Public host port 8080 | A second listener for the same `/secure/*` send, batch, rate, and balance facade. `/secure/*` remains on the public listener too, so closing 8080 does not remove that API (`internal/transport/restcompat/config.go:18`). |
| `smpps.bind_addr` | Public host port 2775 | Customer ESME bind, MT `submit_sm`, and outbound MO/DLR `deliver_sm` (`internal/app/smppsserver/service.go:67`). |
| `admin.api_listen_address` | Loopback host port 8405 | Bearer-token `/admin/` connector, route, and user provisioning plus modern metrics (`internal/app/admin/handler.go:38`, `internal/app/gateway/runtime.go:473`). |
| `admin.web_listen_address` | Loopback host port 8404 | Browser management of connectors, MT/MO routes, users, groups, SMPPS users, filters, HTTP destinations, profiles, tools, and optionally interceptors (`internal/app/adminweb/server.go:130`). |
| `admin.jcli_listen_address` | Loopback host port 8990 | The privileged jCli management console. Empty disables it (`internal/app/gateway/config.go:138`, `internal/app/gateway/runtime.go:554`). |
| `ha.standby_listen_address` | Same in-container 1401 in the examples | While passive, only `/live` and `/ready`; the active listeners replace it after promotion (`cmd/synevyr-gateway/main.go:59`, `cmd/synevyr-gateway/main.go:194`). |

The production JSON binds each enabled in-container listener to `0.0.0.0`;
host publication is what makes admin ports loopback-only
(`configs/gateway.production.example.json:10`,
`configs/gateway.production.example.json:114`,
`configs/gateway.production.example.json:127`). If this binary runs outside
Compose, or with host networking, those management addresses are not
loopback-only.

### The admin REST placement trap

`admin.api_listen_address` gives `/admin/` a separate server. The production
example sets `0.0.0.0:8405`, and Compose publishes it at
`127.0.0.1:8405` (`configs/gateway.production.example.json:114`,
`docker-compose.prod.yml:179`).

If the field is empty, the admin API is mounted on
`outbound.listen_address`. Startup logs a warning, but it remains active
(`internal/app/gateway/runtime.go:473`). This is especially dangerous because
`/admin/` can create users, change balance-bearing user records, create and
start connectors, and replace routes (`internal/app/admin/handler.go:68`,
`internal/app/gateway/runtime.go:480`). Every request still requires the
configured bearer token, compared in constant time
(`internal/app/admin/handler.go:16`, `internal/app/admin/handler.go:54`).

This command is a safe boundary check; the exact body for a missing token is
shown:

```console
$ curl -sS -o - -w '\n%{http_code}\n' \
    http://127.0.0.1:8405/admin/connectors
{"error":"unauthorized"}

401
```

The JSON encoder adds the newline (`internal/app/admin/handler.go:382`).

## Secrets

Secret-bearing JSON fields accept these exact forms:

```json
{
  "amqp_url": "env:AMQP_URL",
  "postgres_dsn": "file:/run/secrets/postgres_dsn"
}
```

`env:NAME` requires a non-empty environment variable. `file:/path` reads the
file and trims trailing CR/LF. `literal:value` escapes a real value beginning
with `env:` or `file:`. Any other string remains literal. Resolution happens
before validation, so `--check-config` fails closed for an absent or empty
reference (`internal/app/gateway/secrets.go:9`).

Only the following fields are resolved:

* `outbound.postgres_dsn` and `outbound.amqp_url`;
* `connectors[].password`;
* `dlr_lookup.amqp_url` and `dlr_lookup.redis_url`;
* `dlr_thrower.amqp_url` and `deliver_sm_thrower.amqp_url`;
* `smpps.users[].password`;
* `admin.token`, `admin.web_password`, and `admin.jcli_password`.

That list is the code's allowlist (`internal/app/gateway/secrets.go:52`).
HTTP users instead store a SHA-256 or legacy MD5 digest, not a secret
reference (`internal/app/outbound/config.go:519`). Do not put live plaintext in
the JSON merely because unlisted string fields accept it.

The two-layer production model is intentional: `.env` supplies the Compose
environment, while `configs/gateway.json` contains `env:` references and
literal non-secret settings (`docker-compose.prod.yml:149`,
`configs/gateway.production.example.json:20`). Environment variables are
still visible to sufficiently privileged container/host inspection. Prefer
`file:` with a read-only secret mount where that threat matters.

## Transport encryption

A top-level `https` block selects TLS for every HTTP server the binary starts:
the public, standalone REST, admin REST, and admin web listeners all use the
same certificate and key (`cmd/synevyr-gateway/main.go:84`,
`cmd/synevyr-gateway/main.go:230`). Both paths are required, but the files are
opened only when the server starts, not by `--check-config`
(`internal/app/gateway/config.go:150`,
`internal/app/gateway/config.go:252`).

For outbound SMPP, `connectors[].tls_enabled` enables TLS 1.2 or newer. The
certificate name defaults to the connector host, system roots are used, and
`tls_ca_file` adds roots. Configuration rejects
`tls_insecure_skip_verify: true`; verification cannot be disabled
(`internal/core/smppc/connector.go:703`,
`internal/core/smppc/config.go:265`).

For inbound SMPP, set both `smpps.tls_cert_file` and
`smpps.tls_key_file`. The listener then requires TLS 1.2 or newer
(`internal/app/smppsserver/service.go:36`,
`internal/app/smppsserver/service.go:132`). There is no code-verified support
for optional client certificates or mutual TLS. Put a controlled network
boundary around the listener if password authentication alone is insufficient.

## Identity and authorization

The HTTP and SMPPS front doors have separate account representations.

HTTP `/send`, `/rate`, and `/balance` accept a user name and plaintext
password, hash it with the user's configured SHA-256 or legacy MD5 algorithm,
and compare in constant time (`internal/app/outbound/config.go:933`,
`internal/transport/httpcompat/handler.go:480`). Disabled users and members of
disabled or missing groups fail authentication
(`internal/app/outbound/config.go:968`). The user's MT credential then controls
which operation and optional fields are allowed, regex value fences, and
per-ingress throughput (`internal/core/mtcredential/credential.go:15`,
`internal/app/outbound/config.go:782`).

Inbound SMPP accounts use `smpps.users[]`. Their configured plaintext password
is converted to an unsalted MD5 digest for compatibility
(`internal/app/smppsserver/directory.go:18`,
`internal/core/smpps/bindauth.go:12`). Bind authorization checks, in order,
password plus user/group state, peer IP whitelist, bind permission, and the
total concurrent binding cap (`internal/core/smpps/bindauth.go:31`). The
surprising default whitelist is `0.0.0.0/0`: any IPv4, but no IPv6
(`internal/core/smpps/ipmatch.go:12`). Omitted bind and submit
authorizations are permissive (`internal/app/smppsserver/directory.go:167`).
Set an explicit customer CIDR, `bind`, `smpps_send`, and `max_bindings`.

Changing or deleting an SMPPS account changes future binds, not sessions that
are already bound. The web API has explicit unbind/ban operations; ordinary
directory replacement does not disconnect sessions
(`internal/app/smppsserver/directory.go:104`,
`internal/app/adminweb/server.go:163`).

The browser UI uses one configured admin username/password. Login comparisons
are constant-time SHA-256 comparisons
(`internal/app/adminweb/handlers_auth.go:35`). A successful login gets a
12-hour HMAC-signed, `HttpOnly`, `SameSite=Strict` cookie; `Secure` is set only
when top-level HTTPS is enabled. Its signing key is generated per process, so a
restart invalidates sessions (`internal/app/adminweb/session.go:13`,
`internal/app/adminweb/server.go:91`). Mutations also require the
`X-CSRF-Token` returned by login/session APIs
(`internal/app/adminweb/auth.go:9`,
`internal/app/adminweb/csrf.go:8`).

## Interceptors are code execution

MT and MO interceptor `py_code` is compiled and evaluated by a Python
subprocess (`scripts/interceptor_runner.py:51`,
`scripts/interceptor_runner.py:62`). It runs as the same operating-system user
as the gateway. The supplied image uses the unprivileged `synevyr` user, but
that user owns the gateway's writable data/log directories and shares its
network reach and environment (`docker/Dockerfile.gateway:29`,
`docker/Dockerfile.gateway:49`).

Consequently, `admin.allow_interceptor_editing: true` turns a compromised
admin web/jCli credential into remote code execution with gateway-user
authority. It defaults false; when false, the service is absent and the web
interceptor endpoints return 404
(`internal/app/gateway/config.go:132`,
`internal/app/gateway/runtime.go:460`,
`internal/app/adminweb/server.go:61`). Config-file interceptors execute even
when admin editing is false, so protect write access to the JSON just as
strictly.

## Pre-exposure checklist

Work through this list before opening any listener:

1. Bind `admin.api_listen_address`, `admin.web_listen_address`, and
   `admin.jcli_listen_address` to a management network or publish them to host
   loopback only. Confirm `/admin/` is 404 on the public port and 401 without a
   token on the admin port (`internal/app/gateway/runtime.go:473`).
2. Replace every production-example placeholder and every bootstrap SMSC
   target. Run `synevyr-gateway --config ... --check-config`; secret resolution
   and strict unknown-field decoding run during that check
   (`internal/app/gateway/config.go:178`).
3. Use distinct, high-entropy admin REST, web, jCli, SMSC, and ESME
   credentials. Do not reuse the legacy MD5-backed ESME/HTTP credentials for
   management (`internal/core/smpps/bindauth.go:12`,
   `internal/app/admin/handler.go:25`).
4. Terminate TLS on every remotely reachable HTTP and SMPP listener, either in
   the gateway as described above or at a deliberately configured trusted
   proxy. Remember that top-level HTTPS also controls the web cookie's
   `Secure` flag (`internal/app/gateway/runtime.go:545`).
5. Replace the SMPPS default `0.0.0.0/0` with explicit source networks, set a
   finite `max_bindings`, and explicitly deny unneeded bind/send capabilities
   (`internal/app/smppsserver/directory.go:151`).
6. Keep `allow_interceptor_editing` false unless administrators are intended to
   have gateway-user shell-equivalent authority. Review all config-file
   `mt_interceptors` and `mo_interceptors`
   (`internal/app/gateway/runtime.go:236`).
7. Keep PostgreSQL, RabbitMQ, and Redis off public interfaces. The shipped
   production stack does so; changing Compose ports changes the boundary
   (`docker-compose.prod.yml:64`, `docker-compose.prod.yml:91`,
   `docker-compose.prod.yml:116`).
8. Preserve `no-new-privileges:true`, the non-root image user, and read-only
   config mounts (`docker-compose.prod.yml:168`,
   `docker-compose.prod.yml:214`).
9. Verify logs do not expose content. Set `submit_audit_log.privacy: true`;
   otherwise MT/MO content is rendered into audit logs
   (`internal/core/logging/logging.go:158`).
10. Rehearse credential rotation and session eviction before cutover. The
    readiness authority records rotation procedure/drill as unfinished
    (`docs/plans/017-smpp-production-readiness.md:120`).

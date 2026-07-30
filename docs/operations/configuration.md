# Configuration reference

Synevyr reads one strict JSON document supplied with `--config`. Unknown fields
and trailing JSON are errors, and secret references are resolved before
validation (`internal/app/gateway/config.go:178`). The existing
[`configs/README.md`](../../configs/README.md) explains the example file,
routing examples, and compatibility background. This page is the exhaustive
field reference and calls out effective defaults that are easy to mistake.

Validate a file without starting listeners or workers:

```console
$ ADMIN_TOKEN=check-only \
    ADMIN_WEB_PASSWORD=check-only \
    JCLI_PASSWORD=check-only \
    GOCACHE="$PWD/.cache/go-build" \
    go run ./cmd/synevyr-gateway \
      --config configs/gateway.example.json --check-config
configuration: ok
```

The `check-only` strings are disposable values for validating the example, not
deployment credentials.

Those exact success bytes come from the command entry point
(`cmd/synevyr-gateway/main.go:48`). Certificate files are opened later, so this
check does not prove that HTTP or SMPPS certificate paths are readable
(`internal/app/gateway/config.go:150`,
`internal/app/smppsserver/service.go:127`).

## The two configuration layers

Keep operational policy literal in `configs/gateway.json`, but refer to secrets
by name. This is a fragment showing that boundary; use the repository's
production example for the required users, routes, and remaining fields:

```json
{
  "role": "http+smppc",
  "outbound": {
    "listen_address": "0.0.0.0:1401",
    "amqp_url": "env:AMQP_URL",
    "postgres_dsn": "env:POSTGRES_DSN"
  },
  "connectors": [{
    "cid": "carrier-a",
    "host": "smsc.example.net",
    "port": 2775,
    "system_id": "account-a",
    "password": "file:/run/secrets/smsc_password",
    "bind": "transceiver"
  }]
}
```

The production Compose file builds `AMQP_URL` and `POSTGRES_DSN` from `.env`
values and passes them to the process; the JSON itself names those environment
variables (`docker-compose.prod.yml:149`). The binary does not parse `.env`.
Outside Compose, export the referenced variables or arrange them with the
service manager.

`env:NAME` requires a non-empty variable. `file:/path` reads the file and trims
trailing CR/LF. `literal:value` escapes a real value beginning with `env:` or
`file:`. An unprefixed value is literal
(`internal/app/gateway/secrets.go:9`). Only the following fields are resolved
this way:

* `outbound.postgres_dsn`, `outbound.amqp_url`;
* `connectors[].password`;
* `dlr_lookup.amqp_url`, `dlr_lookup.redis_url`;
* `dlr_thrower.amqp_url`, `deliver_sm_thrower.amqp_url`;
* `smpps.users[].password`; and
* `admin.token`, `admin.web_password`, `admin.jcli_password`.

That allowlist is implemented explicitly, not inferred from field names
(`internal/app/gateway/secrets.go:52`). A password digest in
`outbound.users[]` is not secret-ref capable.

## Top-level fields

An omitted object or array is disabled/empty unless the table says otherwise.
`required` means configuration validation rejects omission.

| Field | Type | Default / required | Meaning |
|---|---|---|---|
| `role` | string | Required; must be `"http+smppc"` | Selects the single implemented gateway role (`internal/app/gateway/config.go:208`). |
| `outbound` | object | Required | Public HTTP admission, users, billing and MT routing. Its required fields are below (`internal/app/gateway/config.go:31`). |
| `connectors` | array of connector | Required, at least one | Outbound SMPP client connections. `cid` values must be unique (`internal/app/gateway/config.go:356`). |
| `required_connector_ids` | array of string | Every configured connector | The connector binds that gate readiness. Every value must name a configured connector and be unique (`internal/app/gateway/config.go:380`, `internal/app/gateway/config.go:465`). |
| `bind_timeout_seconds` | number | 30 | Startup wait for required binds. Zero means 30, not no timeout (`internal/app/gateway/config.go:451`). |
| `pickle_codec` | string | `""` / `"native"` accepted | `"native"` and `"bridge"` both pass validation. The runtime always constructs the native codec and deliberately ignores the field (`internal/app/gateway/config.go:212`, `internal/app/gateway/runtime.go:123`). |
| `amqp_durable_topology` | boolean | `false` | Propagates one durability choice to every exchange and queue declared by this process. It must match existing objects in the vhost or RabbitMQ returns 406 `PRECONDITION_FAILED` (`internal/app/gateway/config.go:39`, `internal/app/gateway/runtime.go:81`). |
| `dlr_lookup` | object | Omitted: worker disabled | Correlates submit responses and SMSC receipts through Redis (`internal/app/gateway/config.go:46`). |
| `dlr_thrower` | object | Omitted: worker disabled | Delivers DLR callbacks to HTTP or SMPPS (`internal/app/gateway/config.go:49`). |
| `deliver_sm_thrower` | object | Omitted: worker disabled | Delivers routed MOs to HTTP or SMPPS (`internal/app/gateway/config.go:52`). |
| `smpps` | object | Omitted: server disabled | Inbound SMPP server and bind accounts (`internal/app/gateway/config.go:55`). |
| `mo_routes` | array of MO route | Empty | Routes carrier-originated `deliver_sm` messages (`internal/app/gateway/config.go:87`). |
| `rest_api` | object | Effective defaults below | Configures `/secure/*` and an optional standalone REST listener (`internal/transport/restcompat/config.go:18`). |
| `admin` | object | Omitted: all admin planes disabled | Runtime provisioning API and optional browser/jCli listeners (`internal/app/gateway/config.go:106`). |
| `ha` | object | Omitted: single active process | Enables PostgreSQL-fenced active-passive operation (`internal/app/gateway/config.go:83`). |
| `https` | object | Omitted: HTTP plaintext | Terminates TLS for all HTTP listeners created from the top-level handler configuration (`internal/app/gateway/config.go:75`). |
| `submit_audit_log` | log object | INFO to stderr; privacy `false` | Jasmin-compatible final MT audit records (`internal/app/gateway/config.go:167`). |
| `smpp_server_log`, `router_log`, `http_api_log`, `http_access_log`, `dlr_log`, `amqp_log`, `dlr_thrower_log`, `deliver_sm_thrower_log` | component log object | INFO to stderr | Named component streams. See [monitoring](monitoring.md#component-logs) for contents and sinks (`internal/app/gateway/config.go:62`). |

## `outbound`

| Field | Type | Default / required | Meaning |
|---|---|---|---|
| `listen_address` | string | Required | `host:port` for the combined public HTTP API (`internal/app/outbound/runtime.go:927`). |
| `amqp_url` | string, secret-ref | Required | RabbitMQ connection URL (`internal/app/outbound/runtime.go:927`). |
| `postgres_dsn` | string, secret-ref | Required | PostgreSQL connection string for submissions, CDRs and quotas (`internal/app/outbound/runtime.go:927`). |
| `python_path` | string | `python3` when interception starts | Interpreter used only by the optional interceptor runner; no Python is started when no interceptor is configured (`internal/app/gateway/runtime.go:236`, `internal/transport/pyintercept/runner.go:42`). |
| `users` | array of user | Required, at least one | HTTP billing/authentication principals and MT credentials (`internal/app/outbound/runtime.go:930`). |
| `groups` | array of group | Empty | Shared billing ceilings. A user's non-empty `group_id` must resolve here (`internal/app/outbound/config.go:112`, `internal/app/outbound/config.go:707`). |
| `routes` | array of MT route | Required, at least one | Ordered carrier selection and price (`internal/app/outbound/runtime.go:930`). |
| `long_content_split` | string | `"udh"` | Long-message split mode: `"udh"` or `"sar"` (`internal/app/outbound/config.go:51`, `internal/core/submit_service.go:206`). |
| `long_content_max_parts` | integer | 5 | Maximum generated parts; any non-positive value resolves to 5 (`internal/core/submit_service.go:209`). |
| `mt_interceptors` | array of interceptor | Empty | Python scripts applied before MT routing, highest order first (`internal/app/outbound/config.go:56`). |
| `mo_interceptors` | array of interceptor | Empty | Python scripts applied to inbound MO content before routing (`internal/app/outbound/config.go:60`). |
| `quota_persist_interval_seconds` | integer | 10 | Flush cadence for changed user/group quotas. Smaller values reduce crash-time refund exposure but increase PostgreSQL writes (`internal/app/outbound/config.go:65`, `internal/core/billing/quota_persister.go:14`). |
| `cdr_currency` | string | `"XXX"` | Three-uppercase-letter currency written to new CDRs. `XXX` deliberately means unitless (`internal/app/outbound/runtime.go:936`, `internal/core/cdr/model.go:23`). |
| `cdr_retention_days` | integer | 0, retain indefinitely | Positive values enable pruning of terminal CDRs (`internal/app/outbound/config.go:75`). |
| `cdr_retention_batch_size` | integer | 1000 when retention is enabled; otherwise 0 | Rows pruned per maintenance batch; maximum 10,000 (`internal/app/outbound/runtime.go:941`, `internal/app/outbound/runtime.go:960`). |
| `cdr_maintenance_interval_seconds` | integer | 86,400 | Reconciliation/retention cadence (`internal/app/outbound/runtime.go:489`). |
| `amqp_durable_topology` | boolean | `false`, OR top-level value | A section-level `true` is honored, but the top-level switch forces it on. Prefer the top-level switch unless this section uses an isolated vhost (`internal/app/outbound/config.go:45`, `internal/app/gateway/runtime.go:80`). |

### Users and groups

| User field | Type | Default / required | Meaning |
|---|---|---|---|
| `username` | string | Required | Login, matching `[A-Za-z0-9_-]{1,15}` (`internal/app/outbound/config.go:28`, `internal/app/outbound/config.go:484`). |
| `external_id` | string | Required | Stable legacy uid, matching `[A-Za-z0-9_-]{1,16}` (`internal/app/outbound/config.go:29`, `internal/app/outbound/config.go:484`). |
| `password_sha256` | hex string | Exactly one password digest required | 64-character SHA-256 verifier for HTTP basic parameters (`internal/app/outbound/config.go:519`). |
| `password_md5` | hex string | Exactly one password digest required | 32-character compatibility verifier. Prefer SHA-256 for new users (`internal/app/outbound/config.go:519`). |
| `balance` | number or null | `null`, unlimited | Remaining monetary quota. A number must be finite and non-negative (`internal/app/outbound/config.go:491`). |
| `submit_sm_count` | integer or null | `null`, unlimited | Remaining segment count; a number must be non-negative (`internal/app/outbound/config.go:496`). |
| `early_decrement_balance_percent` | integer or null | `null`, 100% charged early | Splits a rated message between admission and `submit_sm_resp` charging. When set it must be 1 through 100; null puts the whole charge on admission (`internal/core/billing/billing.go:558`, `internal/core/billing/billing.go:591`). |
| `group_id` | string | `""`, no group ceiling | Associates the user with a configured group (`internal/app/outbound/config.go:504`). |
| `disabled` | boolean | `false` | Rejects authentication while retaining the record (`internal/app/outbound/config.go:117`). |
| `mt_credential` | object | Permissive defaults below | Per-request authorization, value filters, source default and ingress throughput (`internal/app/outbound/config.go:122`). |
| `smpps_credential` | object | Omitted | Compatibility representation with `bind` (boolean, default true), `ip` (string, default empty), and `max_bindings` (integer/null, default null). Browser/jCli provisioning mirrors this into an SMPPS account (`internal/app/outbound/config.go:129`, `internal/app/adminweb/handlers_users.go:232`). Not verified: no boot-time code reads this field from JSON; configure `smpps.users[]` for a static bind account. |

Each group has `gid` (required string matching
`[A-Za-z0-9_-]{1,16}`), `balance` (number/null, default unlimited),
`submit_sm_count` (integer/null, default unlimited), and `disabled` (boolean,
default false). Negative finite quotas are rejected
(`internal/app/outbound/config.go:30`,
`internal/app/outbound/config.go:292`).

### `mt_credential`

All fields are optional. The following boolean authorizations default to
`true`: `http_send`, `http_balance`, `http_rate`, `smpps_send`,
`http_long_content`, `set_dlr_level`, `http_set_dlr_method`,
`set_source_address`, `set_priority`, `set_validity_period`,
`set_hex_content`, and `set_schedule_delivery_time`. The surprising exception
is `http_bulk`, which defaults to `false`
(`internal/core/mtcredential/credential.go:61`).

Value-filter fields are regex strings:

| Field | Default | Applied to |
|---|---|---|
| `filter_destination_address` | `.*` | Destination address |
| `filter_source_address` | `.*` | Source address |
| `filter_priority` | `^[0-3]$` | SMPP priority |
| `filter_validity_period` | `^\d+$` | Validity-period digits |
| `filter_content` | `.*` | Message content |

These exact defaults are constructed by the credential engine
(`internal/core/mtcredential/credential.go:41`). Regexes use Go RE2, so Python
lookaround and backreferences do not compile
(`internal/core/mtcredential/credential.go:109`).

`default_source_address` is a string or null and defaults to null/unset.
`http_throughput` and `smpps_throughput` are numbers or null and default to
unlimited; zero or a negative value also means unlimited
(`internal/app/outbound/config.go:170`,
`internal/app/outbound/config.go:174`).

### MT routes, route filters and interceptors

| Route field | Type | Default / required | Meaning |
|---|---|---|---|
| `connector_id` | string | One connector source required | Single candidate connector (`internal/app/outbound/config.go:201`). |
| `connector_ids` | array of string | Alternative to `connector_id` | Ordered failover candidates; entries must be non-empty and unique (`internal/app/outbound/runtime.go:987`). |
| `rate` | number | 0 | Non-negative route price (`internal/core/billing/billing.go:591`). |
| `default` | boolean | `false` | Marks the one order-0, filter-free fallback (`internal/app/outbound/runtime.go:1002`). |
| `order` | integer | 0 | Static routes require a positive order; larger orders win (`internal/app/outbound/runtime.go:1003`). |
| `filters` | array of filter | Empty | All filters must match. Forbidden on a default route (`internal/app/outbound/runtime.go:1006`). |

Each filter has a required string `type` and optional `pattern`, `value`,
`username`, `group_id`, `start`, and `end` strings. Supported shapes are:

| `type` | Parameter |
|---|---|
| `destination_addr`, `source_addr`, `short_message` | `pattern` regex |
| `tag`, `eval_py` | `value` |
| `user` | `username` naming a configured user |
| `group` | `group_id` naming a configured group |
| `date_interval` | `start`, `end` in `YYYY-MM-DD` |
| `time_interval` | `start`, `end` in `HH:MM:SS` |

Those are the complete accepted MT cases; `connector` is rejected because it
is MO-only (`internal/app/outbound/filters.go:108`).

An entry in `mt_interceptors` or `mo_interceptors` has `order` (integer,
default 0), `filters` (same array, default empty), and `py_code` (required
non-empty string). Orders must be non-negative and unique. MO interceptors
cannot use a `user` or `group` filter because no resolver exists in that
context (`internal/app/outbound/filters.go:11`,
`internal/app/outbound/filters.go:49`). These scripts are arbitrary Python;
see [security](security.md#interceptors-are-code-execution).

## `connectors[]`

### Identity, bind and addressing

| Field | Type | Default / required | Meaning |
|---|---|---|---|
| `cid` | string | Required | Unique connector identifier (`internal/core/smppc/config.go:164`). |
| `host` | string | Required | SMSC host (`internal/core/smppc/config.go:168`). |
| `port` | integer | Required, 1–65535 | SMSC port (`internal/core/smppc/config.go:171`). |
| `system_id` | string | Required | SMPP bind identity (`internal/core/smppc/config.go:174`). |
| `password` | string, secret-ref | `""` allowed | SMPP bind password (`internal/core/smppc/config.go:25`). |
| `system_type` | string | `""` | Bind `system_type` (`internal/core/smppc/config.go:26`). |
| `bind` | string | `"transceiver"` | `"transceiver"`, `"transmitter"`, or `"receiver"` (`internal/core/smppc/config.go:177`). |
| `addr_ton`, `addr_npi` | integer | 0 | TON/NPI sent in the bind request (`internal/core/smppc/config.go:29`). |
| `address_range` | string | `""` | Address range sent in the bind request (`internal/core/smppc/config.go:32`). |
| `src_ton`, `src_npi` | integer | 2, 1 | Default MT source TON/NPI. JSON zero is treated as unset, so wire value UNKNOWN/0 cannot be selected here (`internal/core/smppc/config.go:218`). |
| `dst_ton`, `dst_npi` | integer | 1, 1 | Default MT destination TON/NPI. The same zero-as-unset caveat applies (`internal/core/smppc/config.go:228`). |
| `service_type`, `source_addr` | string | `""` | Defaults applied when a submit omits the respective PDU field (`internal/core/smppc/config.go:379`). |
| `protocol_id`, `sm_default_msg_id` | integer | 0 | Default byte values, each limited to 0–255 (`internal/core/smppc/config.go:322`). |
| `replace_if_present_flag` | integer | 0 | Default replacement flag, limited to 0 or 1 (`internal/core/smppc/config.go:328`). |

### Timers, recovery and flow control

All timer values are seconds.

| Field | Type | Default | Meaning |
|---|---|---:|---|
| `trx_to` | number | 300 | Session transaction/inactivity timer (`internal/core/smppc/config.go:196`). |
| `res_to` | number | 120 | Wait for an SMPP response (`internal/core/smppc/config.go:203`). |
| `pdu_to` | number | 10 | PDU read timer (`internal/core/smppc/config.go:206`). |
| `elink_interval` | number | 30 | Idle interval before `enquire_link`. Zero selects 30, so it cannot disable this timer (`internal/core/smppc/config.go:209`). |
| `bind_to` | number | 30 | Wait for a bind response (`internal/core/smppc/config.go:212`). |
| `requeue_delay` | number | 120 | Delay before retrying a rejected submit (`internal/core/smppc/config.go:215`). |
| `con_loss_retry`, `con_fail_retry` | boolean | `true` | Retry after established-link loss / initial connection failure (`internal/core/smppc/config.go:252`). |
| `con_loss_delay`, `con_fail_delay` | number | 10 | Initial delay for the respective retry path (`internal/core/smppc/config.go:234`). |
| `reconnect_backoff_max` | number | 60 | Exponential retry-delay cap (`internal/core/smppc/config.go:240`). |
| `reconnect_backoff_jitter` | number | 0.2 | Downward jitter fraction, in `[0,1)` (`internal/core/smppc/config.go:243`). |
| `submit_sm_throughput` | number or null | 1 | Connector submit pace in PDUs/s. An explicit non-positive value disables pacing (`internal/core/smppc/config.go:407`). |
| `prefetch_count` | integer | 1 | RabbitMQ unacknowledged delivery limit, 1–65,535 after defaulting (`internal/core/smppc/config.go:271`). |
| `window_size` | integer | `prefetch_count` | Maximum outstanding `submit_sm` PDUs, 1–65,535 after defaulting (`internal/core/smppc/config.go:274`). |

### TLS, DLRs, logging and vendor data

| Field | Type | Default | Meaning |
|---|---|---|---|
| `tls_enabled` | boolean | `false` | Use TLS to the SMSC (`internal/core/smppc/config.go:67`). |
| `tls_server_name` | string | Connector host | Verification name when TLS is enabled (`internal/core/smppc/connector.go:719`). |
| `tls_ca_file` | string | System roots | Optional PEM CA bundle (`internal/core/smppc/connector.go:724`). |
| `tls_insecure_skip_verify` | boolean | `false`; `true` rejected | Certificate verification cannot be disabled (`internal/core/smppc/config.go:265`). |
| `dlr_msg_id_bases` | integer | 0 | Receipt/submit ID conversion: 0 same base, 1 receipt decimal vs response hex, 2 receipt hex vs response decimal (`internal/core/smppc/config.go:112`). |
| `dlr_expiry` | integer | 86,400 | Redis callback/mapping TTL in seconds (`internal/core/submit_service.go:181`, `internal/core/submit_service.go:502`). |
| `log_file` | string | `""`, stderr | Sink for `smpp.client.<cid>` lifecycle records (`internal/core/smppc/config.go:95`). |
| `log_rotate` | string | `""` | `midnight` or `W0` through `W6` when a file is set (`internal/core/logging/logging.go:38`). |
| `log_level` | string | `"INFO"` | Connector logger threshold (`internal/core/logging/logging.go:26`). |
| `log_privacy` | boolean | `false` | Provisioning field retained for compatibility. Not verified: no production read outside admin/jCli rendering was found. |
| `priority` | integer | 0 | Provisioning field retained for compatibility. Not verified: no connector runtime read was found. |
| `data_coding` | integer | 0 | Described by the config model as a connector PDU default. Not verified: no production runtime read was found (`internal/core/smppc/config.go:87`). |
| `validity_period` | string | `""` | Described by the config model as a connector PDU default. Not verified: no production runtime read was found (`internal/core/smppc/config.go:91`). |
| `amqp_durable_topology` | boolean | `false`, OR top-level value | A connector-level `true` is honored, but mixing durability within one vhost is unsafe because components share `messaging` (`internal/core/smppc/config.go:121`, `internal/app/gateway/runtime.go:156`). |
| `custom_tlvs` | array | Empty | Vendor TLV constraints (`internal/core/smppc/config.go:128`). |

Each `custom_tlvs` item has `tag` (integer, default 0), `type` (required:
`int1`, `int2`, `int4`, `int8`, `octetstring`, or `coctetstring`), `length`
(positive integer or null, default unbounded), and `required` (boolean, default
false). The implementation masks `tag` to 16 bits; validation does not reject a
larger or negative integer (`internal/core/smppc/config.go:142`,
`internal/core/smppc/config.go:332`).

## DLR and MO workers

`amqp_url` in each worker may be empty, in which case it inherits
`outbound.amqp_url` before validation (`internal/app/gateway/config.go:220`).
Each worker object also accepts `amqp_durable_topology` (boolean, default
false). Its effective value is the section value OR the top-level switch. A
section-level `true` is intended only for a worker on its own broker/vhost
(`internal/app/gateway/runtime.go:80`,
`internal/app/gateway/runtime.go:617`).

| Section / field | Type | Default / required | Meaning |
|---|---|---|---|
| `dlr_lookup.amqp_url` | string, secret-ref | Inherit outbound | Broker used by the lookup consumer (`internal/app/dlrlookup/service.go:24`). |
| `dlr_lookup.redis_url` | string, secret-ref | Required | Parseable Redis URL for DLR correlation (`internal/app/dlrlookup/service.go:53`). |
| `dlr_lookup.pid` | string | `"main"` | Suffix in its queue and consumer identity (`internal/app/dlrlookup/service.go:28`). |
| `dlr_lookup.dlr_lookup_max_retries` | integer | 2 | Total delivery attempts, not extra retries (`internal/core/dlr/lookup_consumer.go:26`). |
| `dlr_lookup.dlr_lookup_retry_delay` | number | 10 | Seconds before a delayed lookup requeue (`internal/core/dlr/lookup_consumer.go:32`). |
| `dlr_lookup.smpp_receipt_on_success_submit_sm_resp` | boolean | `false` | Enables the compatibility success receipt behavior (`internal/app/dlrlookup/service.go:35`). |
| `dlr_thrower.amqp_url`, `deliver_sm_thrower.amqp_url` | string, secret-ref | Inherit outbound | Broker used by the corresponding thrower (`internal/app/dlrthrower/service.go:21`, `internal/app/mothrower/service.go:21`). |
| `*.http_timeout` | number | 30 | Per-attempt HTTP client timeout in seconds (`internal/app/dlrthrower/service.go:91`, `internal/app/mothrower/service.go:94`). |
| `*.retry_delay` | number | 30 | Seconds before delayed requeue (`internal/core/dlr/thrower_consumer.go:27`, `internal/core/mo/thrower_consumer.go:41`). |
| `*.max_retries` | integer | 3 | Retry cap; the implementation requeues while the delivery count is at most this value (`internal/core/dlr/thrower_consumer.go:21`, `internal/core/mo/thrower_consumer.go:35`). |

## `smpps`

| Field | Type | Default / required | Meaning |
|---|---|---|---|
| `bind_addr` | string | Required | Inbound SMPP `host:port` listener (`internal/app/smppsserver/service.go:17`). |
| `enquire_link_timeout` | number | 0, disabled | Quiet seconds before sending `enquire_link`. Despite the comment's legacy value of 30, the JSON path applies no 30-second default (`internal/app/smppsserver/service.go:21`, `internal/app/smppsserver/service.go:115`). |
| `inactivity_timeout` | number | 0, disabled | No-traffic seconds before dropping a session (`internal/app/smppsserver/service.go:25`). |
| `deliver_sm_window_size` | integer | 10 | Unacknowledged outbound `deliver_sm` requests per session (`internal/core/smpps/server.go:62`, `internal/core/smpps/server.go:98`). |
| `deliver_sm_response_timeout` | number | 30 | Seconds to wait for `deliver_sm_resp` (`internal/core/smpps/server.go:62`, `internal/core/smpps/server.go:101`). |
| `tls_cert_file`, `tls_key_file` | string | Both omitted, or both required together | SMPPS-over-TLS certificate and key (`internal/app/smppsserver/service.go:36`, `internal/app/smppsserver/service.go:58`). |
| `users` | array of SMPPS user | Empty allowed | Accounts permitted to bind (`internal/app/smppsserver/service.go:34`). |

Each SMPPS user has:

| Field | Type | Default / required | Meaning |
|---|---|---|---|
| `system_id` | string | Required and unique | Bind identity (`internal/app/smppsserver/directory.go:139`). |
| `password` | string, secret-ref | `""` allowed | Plaintext input, MD5-digested for legacy-compatible bind verification (`internal/app/smppsserver/directory.go:18`, `internal/app/smppsserver/directory.go:175`). |
| `disabled`, `group_disabled` | boolean | `false` | Disable the user or its compatibility group (`internal/app/smppsserver/directory.go:175`). |
| `ip_whitelist` | string | `"0.0.0.0/0"` | CIDR allowed to bind (`internal/app/smppsserver/directory.go:151`, `internal/core/smpps/bindauth.go:14`). |
| `max_bindings` | integer or null | `null`, unlimited | Concurrent binding ceiling; zero permits none (`internal/app/smppsserver/directory.go:158`). |
| `bind` | boolean or null | Effective `smpps_send` | Session authorization. When omitted, it inherits the per-submit authorization (`internal/app/smppsserver/directory.go:167`). |
| `smpps_send`, `set_dlr_level`, `set_source_address`, `set_priority` | boolean or null | `true` | Per-submit permissions (`internal/app/smppsserver/directory.go:184`). |
| `filter_destination_address`, `filter_source_address`, `filter_content` | regex string | `.*` | Per-submit value filters (`internal/app/smppsserver/directory.go:196`, `internal/core/mtcredential/credential.go:81`). |
| `filter_priority` | regex string | `^[0-3]$` | Priority filter (`internal/core/mtcredential/credential.go:84`). |
| `filter_validity_period` | regex string | `^\d+$`, but not consulted | Stored for parity; the SMPPS validator deliberately does not read it (`internal/app/smppsserver/directory.go:59`). |
| `default_source_address` | string or null | null/unset | Source substituted for an absent or empty value (`internal/app/smppsserver/directory.go:65`). |

## MO routes

Each top-level `mo_routes[]` item has `order` (integer), `default` (boolean),
`filter_connector_id` (string), `filters` (array), and `connector` (object)
(`internal/app/modispatch/service.go:45`). A default route must use order 0 and
have no filters. A static route must have a positive unique order; its
`filter_connector_id` is optional (`internal/app/modispatch/service.go:129`).

MO filter objects have `type`, `pattern`, `value`, `start`, and `end` strings.
The complete set is `source_addr`, `destination_addr`, `short_message`
(`pattern`); `tag`, `eval_py` (`value`); and `date_interval`, `time_interval`
(`start`, `end`). `user` and an inline `connector` filter are rejected
(`internal/app/modispatch/service.go:86`).

The destination `connector` has these fields:

| Field | Type | Default / required | Meaning |
|---|---|---|---|
| `type` | string | Required | `"http"` or `"smpps"` (`internal/app/modispatch/service.go:160`). |
| `cid` | string | Required for HTTP | HTTP connector identity (`internal/app/modispatch/service.go:162`). |
| `url` | string | Required for HTTP | Webhook destination (`internal/app/modispatch/service.go:162`). |
| `method` | string | `"GET"` effective | HTTP method carried to the legacy connector. The route validator does not constrain it; the native codec replaces an empty value with `"GET"` (`internal/app/modispatch/service.go:34`, `internal/transport/picklecompat/native_codec.go:56`). |
| `system_id` | string | Required for SMPPS | Destination bound ESME (`internal/app/modispatch/service.go:166`). |

## REST, admin, HA, TLS and logs

### `rest_api`

The `/secure/*` routes remain on the public handler even when
`listen_address` is empty; the address only enables the additional standalone
listener (`internal/transport/restcompat/config.go:18`).

| Field | Type | Default | Meaning |
|---|---|---:|---|
| `listen_address` | string | `""`, no standalone listener | Additional REST `host:port` (`internal/app/gateway/config.go:258`). |
| `http_throughput_per_worker` | number or null | 8 | Batch worker tasks/s; explicit zero removes fixed pacing (`internal/transport/restcompat/config.go:71`). |
| `smart_qos` | boolean or null | `true` | Adaptive batch pacing (`internal/transport/restcompat/config.go:78`). |
| `max_pending_tasks` | integer | 10,000 | Admission queue ceiling (`internal/transport/restcompat/config.go:85`). |
| `max_attempts` | integer | 3 | Batch send attempt cap (`internal/transport/restcompat/config.go:92`). |
| `retry_delay_seconds` | number | 1 | Delay between batch attempts (`internal/transport/restcompat/config.go:99`). |
| `callback_max_attempts` | integer | 5 | Batch callback attempt cap (`internal/transport/restcompat/config.go:106`). |
| `callback_retry_delay_seconds` | number | 1 | Delay between callback attempts (`internal/transport/restcompat/config.go:113`). |

### `admin`

| Field | Type | Default / required | Meaning |
|---|---|---|---|
| `db_path` | string | Required without HA; ignored for HA storage | SQLite runtime-provisioning database (`internal/app/gateway/config.go:281`, `internal/app/gateway/runtime.go:359`). |
| `token` | string, secret-ref | Required non-empty | Bearer credential for every `/admin/` API request (`internal/app/gateway/config.go:285`). |
| `api_listen_address` | string | `""`, share public HTTP listener | Dedicated `host:port` for `/admin/`. Empty does not disable the API (`internal/app/gateway/config.go:123`, `internal/app/gateway/runtime.go:473`). |
| `web_listen_address` | string | `""`, UI disabled | Dedicated browser UI listener (`internal/app/gateway/config.go:115`). |
| `web_username`, `web_password` | string | Required when web listener is set | Browser login; password is secret-ref capable (`internal/app/gateway/config.go:293`). |
| `allow_interceptor_editing` | boolean | `false` | Enables admin interceptor CRUD and its Python runner. This grants arbitrary code execution to an admin (`internal/app/gateway/config.go:132`). |
| `jcli_listen_address` | string | `""`, jCli disabled | Dedicated management-console listener (`internal/app/gateway/config.go:138`). |
| `jcli_username`, `jcli_password` | string | Required when jCli listener is set | Console login; password is secret-ref capable (`internal/app/gateway/config.go:301`). |
| `jcli_idle_timeout` | number | 0, disabled | Idle disconnect seconds (`internal/app/gateway/config.go:146`). |

### `ha`

| Field | Type | Default / required | Meaning |
|---|---|---|---|
| `namespace` | string | Required non-blank | Isolates the PostgreSQL advisory lock and shared admin records (`internal/app/gateway/config.go:264`, `internal/app/gateway/runtime.go:359`). |
| `standby_retry_seconds` | number | 2 | Leadership retry interval. Zero means 2 (`internal/app/gateway/config.go:458`). |
| `standby_listen_address` | string | Required | Standby `/live` and `/ready` listener; it may equal the public HTTP address because it closes before promotion (`internal/app/gateway/config.go:273`, `internal/app/gateway/config.go:330`). |

### `https` and logs

`https.cert_file` and `https.key_file` are required non-empty strings when the
object is present (`internal/app/gateway/config.go:252`).

Every component log object has `level` (string, default `"INFO"`), `file`
(string, default `""` meaning stderr), and `rotate` (string, default `""`;
when a file is set, only `midnight` or `W0` through `W6` is valid)
(`internal/core/logging/logging.go:59`,
`internal/core/logging/logging.go:102`,
`internal/core/logging/rotating.go:14`). `submit_audit_log` adds `privacy`
(boolean, default false), which redacts content fields in the final MT line
(`internal/app/gateway/config.go:167`).

## Optional `jasmin.cfg` overlay

This exists to migrate an existing Jasmin deployment without retyping its
infrastructure settings. `configs/jasmin.cfg.example` is a complete file in the
format the parser accepts.

Passing `--legacy-cfg` is an explicit second policy source, not the secret
layer. For the fields it owns, the legacy file wins over JSON
(`internal/app/gateway/jasmin_config.go:9`). It overlays:

* outbound AMQP URL, public HTTP address, long-content split/count, and REST
  batch throughput/smart-QoS;
* enabled DLR lookup Redis/pid/retry/receipt policy;
* enabled DLR and MO thrower timeout/retry policy;
* enabled SMPPS bind address/enquire-link/inactivity timers; and
* all top-level component and submit-audit log settings.

The assignments are exhaustive in `internal/app/gateway/jasmin_config.go:23`.
It never supplies `role`, PostgreSQL, connectors, users, groups, routes, admin,
HA, TLS, or worker enablement. Those remain JSON-owned
(`internal/app/gateway/jasmin_config.go:9`). The base JSON is loaded and
validated before this overlay is applied, then validated again. Consequently,
the JSON must contain valid placeholder values even for fields the legacy file
will replace (`cmd/synevyr-gateway/main.go:32`). The parser's supported legacy
sections and defaults are defined at `internal/config/jasmin.go:48`.

## What can change without a restart

Static JSON remains authoritative and config-owned entity identities are
reserved. The admin plane adds a second set and replays it on boot/promotion
(`internal/app/gateway/runtime.go:350`). Existing SMPPS sessions are not
disconnected merely because their account is edited or removed; new binds see
the changed snapshot (`internal/app/smppsserver/directory.go:104`).

| Change | Live through admin? | Restart boundary |
|---|---|---|
| SMPPc connectors, including start/stop | Yes | JSON connectors cannot be replaced by an admin record (`internal/app/gateway/runtime.go:350`). |
| MT routes, MO routes, users, groups | Yes | JSON-owned orders/names are reserved; admin records are additive (`internal/app/gateway/runtime.go:510`). |
| SMPPS users | Yes | New binds see changes; use the explicit unbind operation to end an existing session (`internal/app/gateway/runtime.go:528`, `internal/app/gateway/runtime.go:535`). |
| Filters and HTTP connectors | Yes, through browser/jCli services | These support the runtime-managed route models (`internal/app/gateway/runtime.go:510`). |
| MT/MO interceptors | Only when `allow_interceptor_editing` is true | Static interceptors still run when configured; admin editing is separately gated (`internal/app/gateway/runtime.go:541`). |
| Balances and submit counts on admin-managed users/groups | Yes | The live objects are updated transactionally with their admin record (`internal/app/outbound/config.go:357`). |
| Listeners, TLS, database/broker URLs, HA, log sinks, REST worker policy, DLR worker policy, static JSON entities | No runtime setter found | Change JSON/environment and restart. |

The token REST API exposes connectors, MT routes, and users
(`internal/app/admin/handler.go:38`). The browser and jCli are wired to the
wider service set shown above (`internal/app/gateway/runtime.go:510`). Do not
assume every browser feature has a token-API equivalent.

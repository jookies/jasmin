# Go gateway runtime configuration

> This directory is intentionally outside `misc/config/` — that directory is part
> of the frozen Python-oracle tree fingerprint (`scripts/compat/verify_baseline_tree.py`)
> and must not change until cutover.

## `gateway.example.json`

Reference `--config` file for the Go gateway (`cmd/jasmin-go-httpapi`) in the
`http+smppc` role: HTTP `/send` front door, durable Postgres outbox, SMPP client
connectors, plus the in-process DLRLookup/DLRThrower workers.

Validate it (prints `configuration: ok`):

```sh
go run ./cmd/jasmin-go-httpapi --config configs/gateway.example.json --check-config
```

Notes:

- **Strict keys.** `LoadConfig` decodes with `DisallowUnknownFields` — a typo'd or
  unknown key fails validation instead of being ignored. Field names come from
  `internal/app/gateway/config.go`, `internal/app/outbound/config.go`,
  `internal/core/smppc/config.go`.
- **Placeholders, not secrets.** Every credential in the example is a placeholder
  wired for `docker-compose.gateway.yml` service names (`rabbitmq`, `postgres`,
  `redis`, `smppsim`). Replace host/creds before pointing at a real SMSC, and do
  not commit real credentials — secret injection lands with the TLS/secrets step
  of `docs/plans/007`.
- **User passwords** are SHA-256 hex digests. The example user `smppuser` uses the
  password `password`:
  `printf '%s' 'password' | shasum -a 256`
- **SMPP bind role** (`bind`): the SMPP-standard connection type — `transceiver`
  (send + receive, default), `transmitter` (send-only: submits MT, gets no MO/DLR),
  or `receiver` (receive-only: gets MO/DLR, cannot submit). A receiver connector
  is automatically excluded from MT routing and never consumes its submit queue;
  pair a `transmitter` + `receiver` to the same SMSC for carriers that reject a
  single transceiver bind.
- **TON/NPI are explicit** (`src_ton 2/src_npi 1`, `dst_ton 1/dst_npi 1` — the
  legacy Jasmin defaults). Strict SMSCs reject `dest_addr_ton=UNKNOWN(0)`; set
  these to what your SMSC expects rather than relying on defaults.
- **Throughput** `submit_sm_throughput: 1.0` is the legacy per-connector default
  pace (1 msg/s). Raise it to your SMSC contract's rate.
- **Routes.** The example has a single default route (`default: true`, `order: 0`).
  Additional static routes need a positive `order` and may use a connector pool:
  `{"connector_ids": ["cid-a", "cid-b"], "order": 10, "rate": 0.0, "default": false}`.
- **Filter-based routing.** A static route may carry `filters` (all must match,
  highest `order` wins); the default route may not. MT filter types:
  `destination_addr`/`source_addr`/`short_message` (`pattern`, regex — Python
  Unicode semantics), `tag` (`value`), `user` (`username`, resolved to its uid),
  `date_interval`/`time_interval` (`start`+`end`, `YYYY-MM-DD` / `HH:MM:SS`). e.g.
  route French traffic to a premium connector:
  `{"connector_id": "premium", "order": 10, "rate": 0.0, "filters": [{"type": "destination_addr", "pattern": "^33"}]}`.
  The `connector` filter is MO-only. MO routes today filter on source connector
  (`filter_connector_id`); MO content filters are a follow-up.
- **DLR workers.** An empty `amqp_url` in `dlr_lookup`/`dlr_thrower` inherits the
  outbound broker. `dlr_lookup.redis_url` must parse as a Redis URL.
- **Terminal DLR (level 2/3).** With `dlr_lookup` (hence a `redis_url`) enabled,
  an HTTP `/send` carrying `dlr-url` + `dlr-level=2|3` persists a `dlr:<msgid>`
  record so the SMSC delivery receipt (`deliver_sm`) correlates back and fires
  the HTTP callback. Per-connector `dlr_msg_id_bases` (0/1/2) codes the receipt
  id to match the submit-response id; `dlr_expiry` (seconds, default 86400) is
  the record TTL. Without `redis_url`, only level-1 (SMSC-accept) callbacks work.
- **MT interception.** `outbound.mt_interceptors` runs user Python scripts
  pre-routing (highest `order` first until one rejects), each gated by the same
  `filters` as routes. A script gets `routable` (with `.pdu.params`
  source_addr/destination_addr/short_message as bytes, and `.tags`),
  `smpp_status`/`http_status` (set both — or one, the other is forced — to
  reject), `extra`, and safe stdlib. Runs in `scripts/interceptor_runner.py`
  (a Python subprocess, like the pickle bridge — interception scripts are
  Python by the legacy contract). Boundary: the MT hook is pre-encode, so PDU
  params beyond the routable fields above aren't surfaced.
- **MO routing.** `mo_routes` runs the MO router dispatch in-process: a default
  route (`order 0`) plus connector-filtered static routes
  (`filter_connector_id` = source SMSC connector, positive `order`, higher
  wins). Destinations: `{"type":"http","cid","url","method"}` (legacy
  HttpConnector URL rules: dotted host / localhost / IP only) or
  `{"type":"smpps","system_id"}`. Enable `deliver_sm_thrower` to actually
  throw routed MOs; the example's sink URL is the compose drill target —
  replace it. Content-based filters land with the filter-routing step.
- **Durable AMQP.** `amqp_durable_topology: true` (top-level) declares every
  exchange/queue durable so queued submits survive a broker restart; publishes
  are always persistent. Durability must be uniform per vhost: AMQP rejects a
  redeclare with different durability (406 PRECONDITION_FAILED). Leave it out
  (legacy default: non-durable) when sharing a vhost with the Python stack —
  e.g. the bridge-edge shadow — or use a fresh vhost to switch modes.
- **TLS to the SMSC**: set `tls_enabled: true` plus optional `tls_server_name` /
  `tls_ca_file` on the connector. Certificate verification cannot be disabled.
- **Secret references.** Credential fields accept `env:NAME`, `file:/path`
  (trailing newline trimmed) or `literal:...` (escape hatch) instead of a
  plaintext value: `outbound.postgres_dsn`, `outbound.amqp_url`,
  `connectors[].password`, `dlr_lookup.amqp_url`/`redis_url`,
  `dlr_thrower.amqp_url`, `deliver_sm_thrower.amqp_url`, `smpps.users[].password`.
  Resolution happens at load, so `--check-config` fails closed on a missing
  secret. Example: `"postgres_dsn": "env:PG_DSN"`.
- **Inbound TLS.** Top-level `"https": {"cert_file": "...", "key_file": "..."}`
  serves the HTTP API over TLS (plain HTTP requests are rejected). For the
  SMPPS server, set `tls_cert_file` + `tls_key_file` inside the `smpps` block
  to terminate SMPPS-over-TLS. Cert files are opened at boot, not at
  `--check-config`, so configs validate on machines without the certs.
- **Vendor TLVs**: per-connector `custom_tlvs` rules
  (`{"tag": ..., "type": "int1|int2|int4|int8|octetstring|coctetstring", "length": null, "required": false}`).
- **Audit log.** `submit_audit_log` emits the Jasmin-format SMS-MT line to stderr
  by default; add `"file": "/var/log/jasmin/messages.log", "rotate": "midnight"`
  (or `W0`..`W6`) for the legacy rotating `messages.log` sink.
- **Admin API.** `admin` (`db_path` + `token`) runs the authenticated runtime
  provisioning plane at `/admin`: SQLite-backed connector CRUD applied live
  through the manager, surviving restart. Every request needs
  `Authorization: Bearer <token>`; `token` takes a secret-ref (`env:ADMIN_TOKEN`).
  Config-defined connectors/routes/users are reserved (admin manages an
  additive set). Endpoints — connectors: `GET/POST /admin/connectors`,
  `GET/PUT/DELETE /admin/connectors/{cid}`,
  `POST /admin/connectors/{cid}/start|stop`; MT routes:
  `GET/POST /admin/routes`, `GET/PUT/DELETE /admin/routes/{order}` (body is a
  `routes[]` entry incl. `filters`); users: `GET/POST /admin/users`,
  `GET/PUT/DELETE /admin/users/{username}` (body is a `users[]` entry;
  admin-assigned uid is stable across restarts so route user-filters resolve).
  All three apply live and survive restart. See docs/adr/001 for the SQLite
  choice; jCli byte-parity console stays deferred.
- **jasmin.cfg overlay.** `--jasmin-cfg /etc/jasmin/jasmin.cfg` optionally overlays
  infrastructure settings (broker, redis, listeners, logging) from a legacy config;
  connectors and routes still come from the JSON.

## Legacy Python daemons

The legacy daemon configs (`jasmind`, `dlrlookupd`, `interceptord`, REST API)
live in `misc/config/*.cfg` — frozen, unrelated to the Go gateway JSON above.

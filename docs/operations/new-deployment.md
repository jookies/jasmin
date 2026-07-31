# Standing up a new Synevyr gateway, from scratch

- **Date:** 2026-07-30
- **Status:** active
- **Summary:** Operator runbook for a brand-new VM with no existing install —
  OS/dependency sizing, a minimal working `gateway.json` for a
  partner-terminates-here deployment, first-boot verification, and what to
  configure before the partner is given credentials.
- **Related:** [running-the-platform.md](running-the-platform.md),
  [configuration.md](configuration.md), [security.md](security.md),
  [scaling.md](scaling.md), [monitoring.md](monitoring.md),
  [termination-safety.md](termination-safety.md),
  [ADR-005](../adr/005-active-passive-postgres-fencing.md),
  [ADR-008](../adr/008-mt-termination-connector.md),
  [deploy/BACKUP.md](../../deploy/BACKUP.md)

## Scope

A fresh VM, no prior Synevyr or Jasmin install. This stands up **one new
gateway beside** the legacy Python stack — it does not touch it, migrate its
traffic, or share its database/broker. The result is an MT **termination**
deployment: the gateway is the destination for a partner's traffic (it rents
numbers and terminates MT locally), not a sender relaying to an upstream SMSC.
See [ADR-008](../adr/008-mt-termination-connector.md) for why that connector
type exists and what it replaces.

Everything below was checked against this repository at
`feat/mt-termination-connector`. Two things were run, not just read:
`go run ./cmd/synevyr-gateway --check-config` against the exact `gateway.json`
in [§4](#4-the-config-file), and `docker build -f docker/Dockerfile.gateway .`
(succeeded, no cache misses of note). Every config key named below was grepped
against `internal/app/gateway/config.go`, `internal/core/termination/config.go`
and `internal/app/outbound/config.go` — see the citations inline. Where
something is inferred from code rather than run, it says so.

## 1. What the VM needs

**OS packages.** The only deployment path this repository documents and tests
is Docker Compose (`docker-compose.prod.yml`, `scripts/deploy/*.sh`). There is
no systemd unit or bare-binary runbook anywhere in the repo — if you want that
path you are hand-rolling it (your own unit file, your own Postgres/RabbitMQ/
Redis install, your own env-var wiring); this document does not attempt it
because nothing here would be verified. For the documented path, install:

```console
# Debian/Ubuntu example — adjust for your distribution
$ curl -fsSL https://get.docker.com | sh          # Docker Engine + Compose v2 plugin
$ sudo apt-get install -y git openssl curl
```

Confirm before continuing:

```console
$ docker compose version   # must succeed — Compose v2, not the old docker-compose v1
$ docker info >/dev/null && echo "daemon reachable"
```

The image is built from source on the VM (`docker-compose.prod.yml` uses
`build:`, not a pulled tag), so `git clone` this repository onto the VM; no
Go toolchain is needed on the host — the multi-stage `docker/Dockerfile.gateway`
compiles it inside the build stage (`golang:1.26` → `python:3.12-slim` runtime;
Python remains only for the optional stdlib-only MT/MO interceptor runner, not
the message path — see the Dockerfile's own header comment).

**CPU/RAM.** `README.md`'s prerequisite is "2 vCPU / 4 GB RAM is enough to
start," and the shipped `docker-compose.prod.yml` resource **reservations**
(what it asks the host to guarantee) sum to well under that: Postgres 0.25
vCPU/256 MB, RabbitMQ 0.25 vCPU/128 MB, Redis 0.1 vCPU/64 MB, gateway 0.5
vCPU/256 MB. The **limits** (ceilings, not simultaneous guarantees) sum higher
— roughly 4.5 vCPU / 2.75 GB across all four services (`.env.example`,
`docker-compose.prod.yml`). None of this is a benchmarked capacity number for
*this* deployment shape: [scaling.md](scaling.md) states plainly that no
sustained-load or 24-hour soak has been run for SMPP passthrough, and the
termination connector's own cost (JSON/PDU decode, ten-plus content encodings,
HMAC signing, an HTTP POST and a spool write per message, a receipt-runner
query every second, a spool census every 10 s) is separate from and untested
beyond that. Start at the README baseline, watch `docker stats` and the
`synevyr_termination_*` gauges (see [§8](#8-what-to-configure-before-traffic)),
and run [scaling.md](scaling.md)'s capacity procedure against your own traffic
before trusting a number.

**Disk.** Two things live here that [scaling.md](scaling.md) does not size,
because they postdate it:

- The **message spool** (`message_spool` table, same `pgdata` volume as
  everything else — see [§2](#2-the-dependencies)). It holds decoded message
  text and raw bytes for `retention_hours` (default 24; see
  `internal/app/gateway/config.go` `TerminationConfig.RetentionHours`). Nothing
  in this repository sizes it, so here is the reasoning from the schema
  (`internal/infra/storage/migrations/0006_message_spool.sql`): one row per
  assembled message (not per segment), with five non-trivial indexes beside
  the primary key. A working planning number is **roughly 0.5–1.5 KB per row
  including index overhead** for a typical short SMS — this is *my own
  estimate from the column list*, not a measured value; treat it as a
  starting point and measure your own once traffic starts. At steady state,
  disk ≈ `arrival_rate (msg/s) × 86,400 × (retention_hours / 24) × row_bytes`:

  | Arrival rate | 24 h retention, ~1 KB/row |
  |---:|---:|
  | 10 msg/s | ~0.9 GB |
  | 50 msg/s | ~4.3 GB |
  | 200 msg/s | ~17 GB |

  [termination-safety.md](termination-safety.md#what-to-watch) independently
  cites "~10 msg/s → ~860 k rows/day; 200 msg/s → ~17 M rows/day" for its
  spool-row-count alarm, which is the same row counts this table uses — the
  byte-per-row figure is the only new estimate here.
- Everything else Postgres already needs (submit transactions, CDRs, billing,
  admin entities, WAL, autovacuum working space) plus RabbitMQ's persisted
  queue data (should stay near-empty in steady state — it holds only
  in-flight messages) plus Docker image layers and container logs. There is
  no repo-provided number for this baseline either; 20 GB free as a floor
  before the spool math above is a conservative starting point for a small
  single-node box, not a derived figure.

## 2. The dependencies

Three infrastructure services, all internal-only (no host ports published —
`docker-compose.prod.yml`), all started by `docker-compose.prod.yml` before
the gateway:

| Service | Image | Used for | Optional? |
|---|---|---|---|
| **PostgreSQL** | `postgres:16-alpine` | Submit transactions, CDRs/billing, HA advisory-lock state, admin-managed entities (SQLite instead, unless `ha` is enabled — [configuration.md](configuration.md#ha)), **and** the termination message spool. All in the *same* database/`pgdata` volume — `internal/app/gateway/termination.go`'s `openMessageSpool` opens the spool on `outbound.postgres_dsn` directly, not a separate DSN. | No. `outbound.postgres_dsn` is a required field (`internal/app/outbound/config.go`). |
| **RabbitMQ** | `rabbitmq:3-management-alpine` | Submit/deliver queueing between the front doors and the connector workers. A termination connector consumes the **same per-connector submit queue** (`submit.sm.<cid>`) an SMPP client connector would (ADR-008) — nothing about queue topology changes for this connector type. | No. `outbound.amqp_url` is required. |
| **Redis** | `redis:7-alpine` | DLR correlation cache and, for termination connectors, the activation-window gate (`dlr:block:<digits>`) and multipart-reassembly part store. | **Not optional for this deployment**, even though `dlr_lookup` is generally optional in the config schema. Verified by running `--check-config` with each piece removed: <br>• no `dlr_lookup` section at all → `termination_connectors requires dlr_lookup: the synthesized submit_sm_resp and receipt legs are unroutable without the DLRLookup queue` <br>• `dlr_lookup` present but `redis_url` empty → `dlr_lookup: dlrlookup: invalid configuration: empty redis_url` <br>• Redis reachable but a `redis-window` verdict source has no resolved URL → rejected by `internal/app/gateway/config.go`'s `validateTerminationConfig` before the process starts. <br>Separately (parity behaviour, not a config-time check): a termination connector with no Redis-backed part store refuses concatenated/multipart submits outright rather than delivering fragments — see [termination-safety.md item 1](termination-safety.md). |

## 3. Database setup and migrations

**Verified from code, not run against a live cluster in this session** (the
`--check-config` runs above don't open a real database): every store's
`Migrate(ctx)` runs automatically inside `gateway.NewRuntime` at every process
start — `internal/app/outbound/runtime.go` for the submit-transaction, billing
quota, CDR and REST-batch stores, `internal/app/gateway/runtime.go` for the
message spool. Each store's schema is a Go `//go:embed`ded `.sql` file under
`internal/infra/storage/migrations/`, applied directly (the message spool file
inspected for this document,
`internal/infra/storage/migrations/0006_message_spool.sql`, is entirely
`CREATE TABLE IF NOT EXISTS` / `CREATE INDEX IF NOT EXISTS`, so re-running it
against an already-migrated database is a no-op).

There is **no separate `migrate` command or step**. On first boot against an
empty Postgres database, the gateway creates every table the enabled config
sections need; on every later boot, the same statements run again and do
nothing. Nothing here needs the operator to run anything beyond starting the
container.

## 4. The config file

This is a **complete, minimal, working `gateway.json`** for a deployment whose
only job is terminating one partner's traffic: an SMPPs front door for the
partner to bind to, one termination connector, one default MT route, one user.
It was validated by actually running:

```console
$ AMQP_URL='amqp://guest:guest@127.0.0.1:5672/' \
  POSTGRES_DSN='postgres://synevyr:x@127.0.0.1:5432/synevyr?sslmode=disable' \
  REDIS_URL='redis://127.0.0.1:6379/0' \
  ADMIN_TOKEN='check-only' ADMIN_WEB_PASSWORD='check-only' \
  JCLI_PASSWORD='check-only' SMPPS_USER_PASSWORD='check-only' \
  GOCACHE="$PWD/.cache/go-build" \
  go run ./cmd/synevyr-gateway --config configs/gateway.json --check-config
configuration: ok
```

```json
{
  "role": "http+smppc",
  "pickle_codec": "native",
  "amqp_durable_topology": true,
  "outbound": {
    "listen_address": "0.0.0.0:1401",
    "amqp_url": "env:AMQP_URL",
    "postgres_dsn": "env:POSTGRES_DSN",
    "users": [
      {
        "username": "acme_smpp",
        "external_id": "acme_smpp",
        "password_sha256": "e95a49243db29eb763ad28f96ca61c9e22908199d1bc6466e43655d269bc68fb",
        "balance": null,
        "submit_sm_count": null,
        "mt_credential": {
          "smpps_send": true,
          "http_send": false,
          "http_balance": false,
          "http_rate": false,
          "http_bulk": false,
          "smpps_throughput": 10
        }
      }
    ],
    "routes": [
      {
        "connector_id": "term-acme",
        "connector_type": "term",
        "rate": 0.0,
        "default": true,
        "order": 0
      }
    ]
  },
  "connectors": [],
  "termination_connectors": {
    "connectors": [
      {
        "cid": "term-acme",
        "verdict": { "source": "redis-window" },
        "delivery": {
          "endpoint": "https://REPLACE_ME_downstream_app.invalid/synevyr/mt",
          "format": "json",
          "secret": "env:DELIVERY_HMAC_SECRET"
        }
      }
    ]
  },
  "bind_timeout_seconds": 30,
  "dlr_lookup": { "redis_url": "env:REDIS_URL", "pid": "main" },
  "dlr_thrower": {},
  "submit_audit_log": { "level": "INFO", "privacy": true },
  "admin": {
    "db_path": "/var/lib/synevyr/admin.db",
    "token": "env:ADMIN_TOKEN",
    "api_listen_address": "0.0.0.0:8405",
    "web_listen_address": "0.0.0.0:8404",
    "web_username": "admin",
    "web_password": "env:ADMIN_WEB_PASSWORD",
    "allow_interceptor_editing": false,
    "jcli_listen_address": "0.0.0.0:8990",
    "jcli_username": "jcliadmin",
    "jcli_password": "env:JCLI_PASSWORD",
    "jcli_idle_timeout": 300
  },
  "smpps": {
    "bind_addr": "0.0.0.0:2775",
    "enquire_link_timeout": 30,
    "users": [
      {
        "system_id": "acme_smpp",
        "password": "env:SMPPS_USER_PASSWORD",
        "ip_whitelist": "203.0.113.10/32",
        "max_bindings": 2,
        "bind": true,
        "smpps_send": true
      }
    ]
  }
}
```

Every key above exists in `internal/app/gateway/config.go`,
`internal/core/termination/config.go`, `internal/app/outbound/config.go` or
`internal/app/smppsserver` — no invented fields. What is non-obvious about
this specific shape, in the order you'll hit it:

- **`"connectors": []` is deliberate and sufficient.** `ValidateConfig` only
  requires *either* an SMPPc connector *or* a termination connector, not both
  (`internal/app/gateway/config.go`: "at least one SMPPc or termination
  connector is required"). A termination-only gateway has zero upstream
  SMSCs. Confirmed by running `--check-config` against this exact file.
- **This means no bind-wait at boot, and no connector gates readiness.**
  `RequiredConnectors()` only ever iterates `config.Connectors` (the SMPPc
  list); with that list empty, `waitRequiredBound` at startup has nothing to
  wait for, and `/health`/`/ready` check no connector bind state at all. A
  termination-only deployment reaches `ok` on Postgres + AMQP + codec alone —
  it never depends on an external SMSC. Do **not** try to add `term-acme` to
  `required_connector_ids`: that list is validated only against configured
  *SMPPc* connectors, and a termination cid there fails validation with
  `required connector "term-acme" is not configured` (verified by reading
  `ValidateConfig`; the `configured` map it checks against is built only from
  `config.Connectors`).
- **`outbound.users[0].password_sha256` is required even though this user
  never logs in over HTTP.** `http_send: false` blocks the HTTP `/send`
  endpoint, but the outbound user record still needs exactly one password
  digest — omitting it fails `--check-config` with `user "acme_smpp" must
  set exactly one of password_sha256 or password_md5` (verified by removing
  it and re-running the check). The value above is the SHA-256 of a
  disposable placeholder string; generate your own and never use it as a
  real credential — it's published in this document.
- **`balance`/`submit_sm_count` are `null` (unlimited) here on purpose, for
  this runbook's own end-to-end test in [§6](#6-first-boot-verification).**
  This is the opposite of the production template's deliberately-zero
  bootstrap (`configs/gateway.production.example.json`), and
  [running-the-platform.md](running-the-platform.md#understand-the-three-kinds-of-quota)
  explains why zero is usually the safer default. Fund/cap this for real
  before handing over credentials — see
  [running-the-platform.md's "Change a rate or fund an account"](running-the-platform.md#change-a-rate-or-fund-an-account).
  Termination traffic is billed exactly like SMPPc traffic (ADR-008: "early
  billing, CDR admission ... unchanged and untouched"), so the route's
  `rate: 0.0` above is also a placeholder, not a statement that termination
  is free.
- **`termination_connectors.redis_url` is absent — leave it that way.**
  `ResolvedRedisURL()` falls back to `dlr_lookup.redis_url` whenever the
  section's own field is empty (`internal/app/gateway/config.go`). If you set
  it explicitly to an `"env:..."` reference instead, **it will not resolve**:
  see the finding immediately below.
- **`dlr_thrower: {}` (empty object, not omitted) is required for the
  partner's SMPP session to ever receive a receipt.** The termination
  connector publishes its synthesized receipt through the same DLR path a
  real carrier uses, but delivery to a bound SMPPS session specifically goes
  through the `DLRThrower` worker's `WithSMPPSReceiptSink` wiring
  (`internal/app/gateway/runtime.go`) — that wiring only happens when
  `config.DLRThrower != nil`. Omit the section and the receipt is computed
  and spooled but never reaches the partner's bind.

### Fixed: termination-connector secrets now support `env:`/`file:`

This runbook's first draft reported that `termination_connectors.redis_url` and
`termination_connectors.connectors[].delivery.secret` were missing from
`resolveSecretRefs`'s allowlist in `internal/app/gateway/secrets.go`, so the only
way to configure a delivery HMAC secret was to write it in clear in
`gateway.json`.

It failed quietly, which is what made it worth fixing rather than documenting: an
`env:` value passed `--check-config` because it was non-empty, and only failed at
real boot when it was parsed as a URL — so the preflight check said the config was
good and the gateway then refused to start.

Both fields are on the allowlist now, and an unresolvable reference fails at config
load rather than at the first delivery attempt hours later. The config block above
uses `"secret": "env:DELIVERY_HMAC_SECRET"`.

Two related points still stand:

- A connector created through the console or the admin API stores its secret in
  the admin database, not in `gateway.json`, and it is redacted on every read.
  Rotate it with `PUT /admin/termination-connectors/{cid}` carrying a new
  `secret`, or `clear_delivery_secret: true` to stop signing.
- `admin_profiles` snapshots hold the delivery secret in clear, exactly as they
  already hold bind passwords. If that table's exposure ever changes, both are in
  scope.

## 5. Firewall and exposure

| Port | What's behind it | Exposure | Why |
|---|---|---|---|
| 2775 | SMPP server — partner binds here | **Public, partner-facing** | The only port this partner actually needs. Restrict with the connector's `ip_whitelist` (set above; the default is `0.0.0.0/0`, any IPv4 — [security.md](security.md#identity-and-authorization)) *and* a host/security-group firewall rule scoped to the partner's real source IP(s). Belt and suspenders: the config-level whitelist is enforced per bind attempt, the firewall rejects before the TCP handshake even reaches it. |
| 1401 | HTTP: `/health`, `/live`, `/ready`, and (only because a termination plane exists) `GET /messages` | **Nobody, by default** — narrower than the shipped `docker-compose.prod.yml`, which publishes it on `0.0.0.0`. This deployment has no HTTP customer (`http_send: false` on the only user), so there is nothing legitimate for the public internet to reach here. If a downstream application will pull via `GET /messages` from off-box, allow only that application's source network — never `0.0.0.0/0`. If nothing external needs `/health`, keep this on loopback or your monitoring network only. |
| 8080 | Legacy REST daemon (`/secure/*`) | **Not started at all** — this config never sets `rest_api.listen_address`, so nothing listens on 8080 inside the container. Comment out (or remove) the `"${GATEWAY_REST_PORT:-8080}:8080"` line in your copy of `docker-compose.prod.yml`; the mapped host port would otherwise sit open with nothing behind it. |
| 8405 | Admin REST API (`/admin/…`) | Loopback only, as shipped | Creates users, starts connectors, reads decoded message content via message-consumer tokens. |
| 8404 | Admin web UI | Loopback only, as shipped | Same privilege boundary. |
| 8990 | jCli console | Loopback only, as shipped | Same privilege boundary. |
| 5432 / 5672 / 6379 | Postgres / RabbitMQ / Redis | Never published — compose default, unchanged | Reachable only on the compose-internal network. |
| (internal) 2775 | `bootstrap-smsc` (unused here) | Never published | See the note in [§6](#6-first-boot-verification) about why this service still exists in your compose file even though nothing routes to it. |

For everything reachable from the internet — in this deployment, that's port
2775 and, if you choose to expose it, 1401 — put TLS in front of it before
real traffic. For SMPP that means a TCP-level TLS terminator (`smpps.
tls_cert_file`/`tls_key_file` in the gateway itself, or stunnel/HAProxy `mode
tcp ssl` in front) — an ordinary HTTP reverse proxy cannot front the SMPP
port. Work through the rest of
[security.md's pre-exposure checklist](security.md#pre-exposure-checklist)
(distinct high-entropy credentials, `allow_interceptor_editing: false` as
shipped above, `submit_audit_log.privacy: true` as set above) before this
deployment carries anything real.

## 6. First-boot verification

**By hand** (mirrors `README.md`'s "prefer to do it by hand" path; do not run
the all-in-one `scripts/deploy/setup.sh` unmodified for this deployment — it
copies `configs/gateway.production.example.json`, which has an SMPPc
connector this deployment does not want, into `configs/gateway.json` on first
run, then immediately starts the stack):

```console
$ cp .env.example .env
# Fill in: POSTGRES_PASSWORD, RABBITMQ_DEFAULT_PASS, ADMIN_TOKEN,
# ADMIN_WEB_PASSWORD, JCLI_PASSWORD, SMPPS_USER_PASSWORD (8 hex chars — SMPP
# 3.4 bind passwords are capped at 8 octets, see running-the-platform.md §3).
# Leave SMSC_PASSWORD and REDIS_URL at their shipped defaults.
$ openssl rand -hex 32   # repeat per secret above
$ openssl rand -hex 4    # for SMPPS_USER_PASSWORD specifically (8 hex chars)

$ $EDITOR configs/gateway.json   # paste the JSON from §4 (configs/ already exists from git clone)

$ docker compose -f docker-compose.prod.yml build gateway bootstrap-smsc
$ docker compose -f docker-compose.prod.yml run --rm --no-deps gateway \
    --config /etc/synevyr/gateway.json --check-config
$ docker compose -f docker-compose.prod.yml up -d
$ curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/health
$ curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
```

`bootstrap-smsc` still has to be built and started even though this
deployment has zero SMPPc connectors: `docker-compose.prod.yml`'s `gateway`
service unconditionally requires `SMSC_PASSWORD` and `depends_on:
bootstrap-smsc`, both written for the common (non-termination-only) case.
Leaving both as shipped costs an idle, unreachable container and an unused
env var — not worth a `docker-compose.prod.yml` edit to remove. Nothing
inside this `gateway.json` ever references `bootstrap-smsc`.

Expect `/health` and `/ready` to report `ok` **without ever binding an SMSC**
— confirmed above, this is the one deployment shape where that's correct
rather than a sign something didn't start.

**Admin console**: `ssh -L 8404:127.0.0.1:8404 you@vm-host`, then
`https://127.0.0.1:8404` (or `http://` — no `https` block is configured
above; add one before this is reachable over an untrusted network per
[security.md](security.md#transport-encryption)), login `admin` /
`$ADMIN_WEB_PASSWORD`. Confirm **Termination Connectors** shows `term-acme`
started, and **MT Routes** shows the order-0 default pointed at it.

**Prove a message goes end to end** with the partner simulator
(`cmd/synevyr-partner-sim` — a dev tool that binds like a real partner ESME,
submits, and prints receipts as they arrive; see its own header comment for
why it exists). From the repo checkout, against the VM:

```console
$ go run ./cmd/synevyr-partner-sim \
    --addr <vm-host>:2775 --allow-remote \
    --system-id acme_smpp --password "$SMPPS_USER_PASSWORD" \
    --destination 15551234567 --text "code 63125" --count 1
```

`--allow-remote` is required for anything other than a loopback/private
address — the tool refuses a public target by default because it submits
real, billed, delivered traffic (`cmd/synevyr-partner-sim/main.go`
`assertDevTarget`). Expect, in order: a `submit_sm_resp status=0x00000000`
line, then — **5 to 7 seconds later**, not immediately; that delay is
deliberate parity with the legacy fake SMSC, see
[termination-safety.md](termination-safety.md#the-one-thing-to-understand-first)
— a `receipt stat=DELIVRD` or `stat=REJECTD` line, depending on whether
`380671234567`-style test digits currently have an open activation window in
Redis. If you haven't pre-seeded an activation window, expect `REJECTD` —
that's the gate correctly saying "no rental window," not a failure. Running
it locally against the dev stack instead of a real VM: `scripts/dev.sh
partner` (see its header comment; it targets `docker-compose.gateway.yml`,
not this production compose file).

If you configured a real `delivery.endpoint`, also confirm the downstream
application actually received the signed POST and that its `X-...` signature
header verifies against the HMAC secret — the partner-sim only proves the
SMPP-facing half.

## 7. Provisioning the partner

For this first partner, **everything is config-owned** — created by writing
it into `gateway.json` in §4 and restarting, not through a live surface. That
table, piece by piece:

| Piece | Created where (this first partner) | Live-editable later? |
|---|---|---|
| Bind credentials (`system_id`/password/IP allowlist) | `smpps.users[0]` in `gateway.json` | Restart to change; a **second** partner's bind account can instead be created live via jCli's `user -a` (mirrors `running-the-platform.md §3`) or the admin console's **SMPPs Binds** page. |
| The termination connector (`term-acme`) | `termination_connectors.connectors[0]` in `gateway.json` | Config-owned connectors can be started/stopped live (`POST /admin/termination-connectors/term-acme/start\|stop`, or the console) but not edited or deleted — same rule as config-owned SMPPc connectors. A **second** termination connector (e.g. for partner two) is created live: `POST /admin/termination-connectors` with `{"config": {...}}`, or the console's **Termination Connectors** page. jCli has **no** create/edit verb for termination connectors at all (`internal/app/jcli/server.go`: "TerminationConnectors has no console verb of its own") — only `load`/`persist` replay config-declared ones. |
| The MT route (order 0, default, `connector_type: "term"`) | `outbound.routes[0]` in `gateway.json` | The order-0 default is config-owned and requires a restart to change. A positive-order route for a second partner can be added live via the console's **MT Routes** page, `POST /admin/routes`, or jCli's `mtrouter` manager using the `term(cid)` connector syntax (`internal/app/jcli/managers_routes.go` — `term` is accepted there specifically so an operator can route to a termination connector without the console). |
| The delivery endpoint + HMAC secret | `termination_connectors.connectors[0].delivery` in `gateway.json`; the secret is `env:`-indirected (see [above](#fixed-termination-connector-secrets-now-support-envfile)) | Same start/stop-only restriction as the connector itself for this config-owned one; a live-created connector's `delivery.secret` can be rotated via `PUT /admin/termination-connectors/{cid}` (send a new `secret`, or `clear_delivery_secret: true` to remove signing). |
| A pull credential for the downstream app, if it will use `GET /messages` instead of (or in addition to) the push | **Admin REST only** — `POST /admin/message-consumers` (see below). There is **no console page for this yet** (`web/src/pages/` has no `message-consumers` directory as of this branch, even though the backend routes exist at both `/admin/message-consumers` and the console's own `/api/message-consumers`) — don't go looking for it in the UI. | N/A — always live; consumers aren't config-owned at all. |

Creating a message-pull consumer, if the downstream app wants to poll instead
of receive pushes (see
[termination-safety.md](termination-safety.md#pulling-messages-instead-of-being-pushed-them)
for the cursor/scope semantics):

```console
$ curl --fail-with-body -sS \
    -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
    --data-binary '{
      "id": "acme-downstream",
      "label": "Acme downstream OTP verifier",
      "scope": {"connectors": ["term-acme"], "include_text": true}
    }' \
    "http://127.0.0.1:8405/admin/message-consumers"
```

The response's `token` field is shown **exactly once** — the gateway stores
only a SHA-256 proof of it, the same as a durable REST batch credential. Copy
it out immediately; if it's lost, revoke the consumer
(`POST /admin/message-consumers/acme-downstream/revoke`) and create another,
per [termination-safety.md item 7](termination-safety.md).

## 8. What to configure before traffic

**The four termination alarms.** None ship in `deploy/alerts.prometheus.yml`
yet — it predates the termination connector's metrics
([monitoring.md](monitoring.md#shipped-alerts): "The termination series have
no shipped rules yet"). Add these before the partner is live; thresholds and
the full reasoning for each are in
[termination-safety.md's "What to watch"](termination-safety.md#what-to-watch)
— reproduced here only as ready-to-paste rules matching the existing file's
format:

```yaml
      - alert: SynevyrTerminationGateBypass
        expr: increase(synevyr_termination_gate_bypass_total[5m]) > 0
        for: 2m
        labels: { severity: critical }
        annotations:
          summary: "Termination gate bypassed on {{ $labels.connector }}"
          description: "Redis is unreachable; every message is being told DELIVRD regardless of rental window. See termination-safety.md item 2."

      - alert: SynevyrTerminationDeadLetterDepth
        expr: synevyr_termination_dead_letter_depth > 0
        for: 15m
        labels: { severity: warning }
        annotations:
          summary: "Dead-lettered termination deliveries on {{ $labels.connector }}"
          description: "Act within 24h — the spool prunes on the retention window and a dead-lettered message older than that is not replayable. See termination-safety.md item 3."

      - alert: SynevyrTerminationSpoolGrowing
        expr: synevyr_termination_spool_rows > <5x your steady-state row count>
        for: 30m
        labels: { severity: warning }
        annotations:
          summary: "Termination spool rows well above steady state on {{ $labels.connector }}"
          description: "There is no absolute threshold — set the multiple against your own steady state, see disk sizing in new-deployment.md §1."

      - alert: SynevyrTerminationReceiptsOverdue
        expr: synevyr_termination_receipts_overdue > 0
        for: 5m
        labels: { severity: critical }
        annotations:
          summary: "Receipts overdue on {{ $labels.connector }}"
          description: "The receipt runner is not draining — partners are waiting and hearing nothing. See termination-safety.md 'What to watch' > Receipts owed but not sent."
```

The `SynevyrTerminationSpoolGrowing` threshold has a placeholder
(`<5x your steady-state row count>`) on purpose — fill it in once you have a
real steady-state number from this deployment; do not copy a number from
another connector's traffic.

**Backups.** `scripts/deploy/backup.sh` already covers this deployment's
message spool without any change: it's a normal table
(`message_spool`, `message_spool_access_audit`, plus the consumer tables in
`0008_message_consumers.sql`) inside the **same** Postgres database
`pg_dump` already backs up (confirmed above — the spool opens on
`outbound.postgres_dsn`, not a separate DSN). [deploy/BACKUP.md](../../deploy/BACKUP.md)'s
"what actually needs backing up" table predates the termination connector and
doesn't name the spool explicitly, which reads as a gap but isn't a
functional one — worth a note in that file, not a reason to add a second
backup step here. Set it up before traffic:

```console
$ scripts/deploy/backup.sh    # writes to $HOME/synevyr-gateway-backups by default
```

Put it on a schedule (cron/systemd timer example in
[deploy/BACKUP.md](../../deploy/BACKUP.md)) pointed at off-host storage —
`BACKUP_DIR=/mnt/backups/... scripts/deploy/backup.sh`. A backup on the same
disk as the thing it backs up is not disaster-recovery coverage.

**Logs and retention**, already reflected in the config in §4 —
cross-referencing rather than repeating why:

- `submit_audit_log.privacy: true` — otherwise MT content (which, for a
  termination connector, is literally the same content stored in the spool)
  is written into the audit log unredacted too
  ([security.md](security.md#pre-exposure-checklist), item 9).
- `termination_connectors.retention_hours` was left at its default (24)
  above. If your contractual/compliance retention window for OTP content is
  shorter, set it explicitly — it directly trades off against the "act within
  24h on a dead-lettered message" guardrail in
  [termination-safety.md item 3](termination-safety.md): a shorter retention
  window narrows how long you have to notice and replay a stuck delivery.
- `cdr_retention_days` (outbound-level, unrelated to the spool) defaults to 0
  — CDRs/billing history are retained indefinitely unless you set this. Not
  termination-specific; see [configuration.md](configuration.md#outbound).

## 9. Rollback

**Nothing to roll back.** Until you hand `acme_smpp` / `$SMPPS_USER_PASSWORD`
to the partner, this gateway carries no traffic — it is a fully running,
fully verified process sitting next to the legacy Python stack with no
inbound bind attempts and no messages in its queues. If §6 or §7 turns up a
problem, fix it and re-verify; there is no cutover step to undo because
nothing was ever pointed at this deployment. The legacy stack is unaffected
throughout: this gateway has its own Postgres, RabbitMQ, and Redis (the
`docker-compose.prod.yml` stack started above), never touches the legacy
stack's brokers or databases, and — per [ADR-005](../adr/005-active-passive-postgres-fencing.md)
— runs as a single active node with no shared state to reconcile if you tear
it down and start over.

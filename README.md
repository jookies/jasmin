# Synevyr

**Synevyr Messaging Platform** — an SMPP 3.4 SMS gateway written in Go.

It terminates SMPP in both directions: outbound to carrier SMSCs (SMPPc) and
inbound from your customers' ESMEs (SMPPs), with an HTTP API in front, durable
routing and billing behind, and an admin plane for running it.

> **Status: early production.** The message path works end to end and is
> exercised against an independent SMPP implementation on every commit. It has
> not yet carried traffic on a real carrier link. Read
> [What is and isn't proven](#what-is-and-isnt-proven) before you point a
> customer at it.

## What it does

| Capability | Detail |
|---|---|
| **Send (MT)** | HTTP `/send` and inbound SMPP → routed → carrier SMSC. GSM7/UCS2/8-bit, long-message concatenation via UDH or SAR, scheduling, validity, priority, custom TLVs. |
| **Receive (MO)** | Carrier `deliver_sm`/`data_sm` → routed to an HTTP webhook or a bound ESME. Long-message reassembly. |
| **Delivery receipts** | Levels 1/2/3, `submit_sm_resp` correlation, terminal-state receipts, HTTP callbacks and SMPP delivery. |
| **Routing & filters** | MT and MO route tables, filters on source, destination, content, tag, date and time. |
| **Billing** | Prepaid and postpaid, per-user and per-group, early and late charging, quotas, durable balances, CDRs in PostgreSQL. |
| **Interception** | Python hooks on the MT and MO paths that can rewrite or reject a message. |
| **Admin plane** | REST API on its own listener, a React web console, and `jCli` — a telnet management console. |
| **Operations** | `/health`, `/live`, `/ready`, legacy and Prometheus metrics, component logs, alert rules, incident runbooks. |
| **HA** | Active-passive fencing through a PostgreSQL advisory lock, drilled behind HAProxy. |

## Architecture

```
   HTTP /send ─┐                                   ┌─→ SMSC A   (SMPPc)
               ├─→ auth ─→ intercept ─→ route ─→ bill ─→ queue ─┤
  ESME (SMPPs)─┘                          │        └─→ SMSC B   (SMPPc)
                                          │
   SMSC deliver_sm ─→ reassemble ─→ intercept ─→ route ─┬─→ HTTP webhook
                                                        └─→ ESME (SMPPs)

   PostgreSQL  durable submits, billing, CDRs, HA fence
   RabbitMQ    submit/deliver/DLR queues
   Redis       DLR correlation, multipart reassembly state
```

The gateway is a single Go binary (`cmd/synevyr-gateway`). Everything above runs
in-process; the three stores are the only required dependencies.

## Quick start

```console
$ scripts/deploy/setup.sh
```

That generates secrets, seeds `configs/gateway.json`, builds the image, starts
the stack, and waits for `/health`. It is idempotent — re-run it any time. Then
send a message:

```console
$ curl "http://127.0.0.1:1401/send?username=USER&password=PASS&to=15551230000&from=1111&content=hello"
Success "f713eaf9-7596-4856-a7e9-a0cc1e9bb409"
```

Details, and what to change before real traffic, are under
[Deployment](#deployment).

## What is and isn't proven

Being straight about this matters more than a feature list.

**Verified on every commit** — build, vet, the full test suite with the race
detector, an end-to-end submit through real RabbitMQ and PostgreSQL to an SMSC,
and **interop against `smpp.twisted`**, an SMPP implementation that shares no
code with this one. That last gate matters: a test suite that drives our server
with our own client cannot catch a misreading of SMPP 3.4 held on both ends of
the wire.

**Not yet proven:**

- **No real carrier link.** Every SMPP peer so far has been a simulator or a
  third-party library. Carrier-specific behaviour — undocumented TLVs, address
  normalisation, throttling thresholds, receipt text quirks — is exactly what
  emulation cannot produce.
- **Metrics are incomplete.** The Prometheus surface exists and is served, but
  several series are defined and never incremented, so they read `0` — which
  looks like "no problems" rather than "not measured". Tracked in
  [`docs/plans/017`](docs/plans/017-smpp-production-readiness.md) as a blocker.
- **No load or soak evidence.** Sustained-throughput and 24-hour endurance runs
  have not been done.
- **Chaos untested.** Behaviour when the broker, database or SMSC dies
  mid-submit is designed for and not yet drilled.

The gates, and what closing each requires, are in
[`docs/plans/017-smpp-production-readiness.md`](docs/plans/017-smpp-production-readiness.md).

## Relationship to Jasmin

Synevyr began as a reimplementation of [Jasmin](https://github.com/jookies/jasmin),
which we ran in production. Jasmin was the reference for behaviour, not a
codebase to port: **no Jasmin code remains here**, and the Python implementation
that served as the comparison oracle has been removed.

Comparing against a working production system was worth it — it surfaced
fourteen real protocol defects that a green test suite had hidden.

Some Jasmin-facing details are kept **on purpose**, because live integrations
parse them, and changing them would break customers rather than modernise
anything:

- `/ping` answers `Jasmin/PONG`.
- MO and DLR webhooks must reply exactly `ACK/Jasmin`.
- AMQP payloads keep the legacy pickle class paths.
- The HTTP API request/response shapes, delivery-receipt text, and jCli output
  are unchanged.
- `--legacy-cfg` still reads a `jasmin.cfg`, so an existing deployment's
  infrastructure settings can be carried over.

Where Synevyr deliberately differs — reconnect backoff, a bounded delivery
window, interception on segments as well as whole messages, tolerance of
off-spec inbound TLVs — each divergence and its reason is recorded in
[`docs/reference/deviations.md`](docs/reference/deviations.md).
[`docs/reference/legacy-behaviours.md`](docs/reference/legacy-behaviours.md)
documents 22 inherited behaviours, several of them bugs we match on purpose.
**Read it before "fixing" anything that looks wrong.**

## Interceptors

MO and MT hooks are Python, so scripts can use the ecosystem — HLR lookups,
number parsing, custom charging. Add packages to
`requirements-interceptors.txt` and rebuild; the file is installed only when it
lists a requirement, so the default costs nothing.

Two things to know. Scripts run **inline on the message path**, so a slow
network call becomes gateway latency. And the pre-imported module list is
convenience, not a sandbox — `import os` works — so
`admin.allow_interceptor_editing` ships `false` and should stay that way unless
the admin API is unreachable from untrusted networks.

## Repository layout

| Path | What's in it |
|---|---|
| `cmd/synevyr-gateway` | The gateway binary. |
| `cmd/synevyr-fake-smsc` | SMSC simulator for tests and first boot. |
| `internal/core` | Protocol and domain logic: SMPP client/server, routing, billing, DLR, segmentation, TLV. |
| `internal/app` | Wiring: gateway runtime, workers, admin, jCli, adminweb. |
| `internal/transport` | Wire and broker codecs: SMPP, AMQP, pickle, Redis, HTTP. |
| `web/` | React admin console, embedded into the binary via `go:embed`. |
| `scripts/interop/` | Third-party SMPP probes and the carrier emulator. |
| `docs/` | Plans, ADRs, runbooks, and the inherited-behaviour reference. |
| `deploy/` | Alert rules and the backup runbook. |

## Development

```console
$ go build ./...
$ go test ./...                                   # 39 packages, no external deps
$ go test -race ./...
```

Integration and interop tests need services and skip cleanly without them:

```console
$ AMQP_URL=amqp://guest:guest@localhost:5672/ \
  TEST_POSTGRES_DSN=postgres://postgres:postgres@localhost:5432/synevyr?sslmode=disable \
  go test -run TestGatewayHTTPToDurableSMPPResponse ./internal/app/gateway/

$ pip install smpp-pdu3 twisted
$ PYTHON_PATH=python go test -run TestThirdPartyESME ./internal/core/smpps/
```

`scripts/interop/smsc_probe.py` doubles as a **carrier emulator** — throttling,
delayed and out-of-order receipts, injected error statuses, mid-session
disconnects — for testing behaviour a well-mannered simulator won't produce.

Editing `web/src` requires rebuilding the embedded bundle (`cd web && npm run
build`); CI fails if it is stale.

## Licence

Proprietary — see [LICENSE](LICENSE). All rights reserved; no licence is granted
without a separate written agreement.

The `LICENSE` file also records this project's provenance relative to Jasmin, and
one thing to settle before any public release: the git history was developed on a
fork and still contains Apache-2.0 licensed source, which carries notice
obligations if that history is published.

## Deployment

This section covers a single-node production deployment with
`docker-compose.prod.yml`. For an active-passive two-node HA deployment behind
HAProxy, see `docker-compose.gateway-ha.yml` (a separate, already-drilled
topology — graduate to it once you need a second node). For local/CI
integration testing with a fake SMSC, see `docker-compose.gateway.yml`; don't
use it for real traffic.

### Prerequisites

- Docker Engine with the Compose v2 plugin (`docker compose version`).
- A Linux server. 2 vCPU / 4 GB RAM is enough to start; the shipped resource
  limits (`.env.example`) assume something in that range — raise them for
  real traffic.
- Your SMSC's connection details (host, port, system_id, bind password) and,
  if you're routing inbound SMS somewhere, that webhook's URL. Not needed to
  boot the stack, but needed before it's useful — see
  [After first boot](#after-first-boot).

### Quick start

```console
$ git clone <this-repo> synevyr-gateway && cd synevyr-gateway
$ scripts/deploy/setup.sh
```

That script is idempotent — re-run it any time. It will:

1. Check for Docker + Compose v2 + a reachable daemon.
2. Create `.env` from `.env.example` on first run, generating strong random
   secrets for every required-but-blank value (admin token, admin/jCli
   passwords, DB/broker passwords, the default SMPPS account password).
3. Create `configs/gateway.json` from `configs/gateway.production.example.json`
   on first run — a template pointed at a bundled fake-SMSC container
   (`bootstrap-smsc`, not a real SMSC) so the stack boots clean and `/health`
   is genuinely `ok` before you've configured a real one. This is
   deliberate, not an oversight: the gateway hard-requires at least one
   connector and crash-loops if it can't bind one — see
   [After first boot](#after-first-boot) before you swap in your real SMSC.
4. Build the gateway image and run `--check-config` as a fail-fast preflight.
5. Start postgres, rabbitmq, redis, then the gateway.
6. Wait for `/health` to report `ok` and print the admin URLs.

Prefer to do it by hand? See what the script automates:

```console
$ cp .env.example .env               # then fill in every REQUIRED value
$ cp configs/gateway.production.example.json configs/gateway.json
$ docker compose -f docker-compose.prod.yml build gateway
$ docker compose -f docker-compose.prod.yml run --rm --no-deps gateway \
    --config /etc/synevyr/gateway.json --check-config
$ docker compose -f docker-compose.prod.yml up -d
$ curl http://127.0.0.1:1401/health
```

### What's exposed, and where

| Port | Service | Default binding | Why |
|---|---|---|---|
| 1401 | HTTP submit/DLR API | `0.0.0.0` (public) | Data plane — this is what senders call. |
| 8405 | Admin REST API (`/admin/`) | `127.0.0.1` (loopback only) | Creates users, changes balances, starts connectors — a privilege boundary. |
| 8080 | Legacy REST daemon | `0.0.0.0` (public) | Duplicate send surface; comment it out in `docker-compose.prod.yml` or firewall it if you don't use it. |
| 2775 | SMPP server (inbound ESME binds) | `0.0.0.0` (public) | Data plane — this is what SMPP clients bind to. |
| 8404 | Admin web UI | `127.0.0.1` (loopback only) | Mints credentials, starts connectors — a privilege boundary. |
| 8990 | jCli console | `127.0.0.1` (loopback only) | Same privilege boundary. |
| 5432, 5672, 6379 | Postgres, RabbitMQ, Redis | not published at all | Internal only, reachable over the compose network. |
| 2775 (internal) | `bootstrap-smsc` fake SMSC | not published at all | Not a real SMSC — see [After first boot](#after-first-boot). |

> **Keep `/admin/` off the public port.** `admin.api_listen_address` gives the
> admin REST API its own listener, and the shipped production config sets it to
> `0.0.0.0:8405`, published on loopback only. If you leave it unset the route
> falls back onto the public sendsms mux alongside `/send`, and the gateway logs
> a warning at startup — it creates users, changes balances and starts
> connectors, protected only by `ADMIN_TOKEN`.
>
> Keep `allow_interceptor_editing` at its shipped `false`: interceptors are
> arbitrary Python run on the gateway host, so enabling it turns that token into
> remote code execution.

**Put a TLS-terminating reverse proxy in front of anything reachable from the
internet.** Nothing here terminates TLS by default. Two ways to add it:

- **At the gateway itself:** set the top-level `"https": {"cert_file": ...,
  "key_file": ...}` in `configs/gateway.json` — it covers the HTTP API, REST
  daemon, and admin web UI listeners at once (they share one server/TLS
  config; see `cmd/synevyr-gateway/main.go`). For the SMPP port, set
  `"smpps": {"tls_cert_file": ..., "tls_key_file": ...}` separately — it's a
  distinct listener with its own TLS config, not something an HTTP reverse
  proxy can front.
- **In front of it:** nginx/Caddy/Traefik terminating TLS for the HTTP ports
  works normally (proxy to the container's plain-HTTP port on the compose
  network). For the SMPP port, you need a TCP-level TLS terminator (stunnel,
  HAProxy in `mode tcp` with `ssl`), not a typical HTTP reverse proxy.

To reach the admin web UI or jCli from off-box, use an SSH tunnel
(`ssh -L 8404:127.0.0.1:8404 you@host`) or put them behind a reverse proxy
that adds its own authentication — do not change their bind address to
`0.0.0.0`.

### Configuration model

Two layers, matching `internal/app/gateway/secrets.go`:

- **Secrets** (`.env`, loaded by `docker-compose.prod.yml`): admin token,
  admin/jCli passwords, DB/broker credentials, SMSC/SMPPS passwords. These are
  referenced from `configs/gateway.json` as `"env:NAME"` and resolved at
  process start — never written to the JSON file directly.
- **Everything else** (`configs/gateway.json`, generated once from
  `configs/gateway.production.example.json`): listener addresses, your real
  SMSC connector (host/port/system_id/bind type), MO routing, the first HTTP
  API user. These aren't secrets, so the config schema requires literal
  values — edit the file directly. See `configs/gateway.production.md` for
  exactly which fields to change and why the template ships the way it does.

After editing `configs/gateway.json`, restart: `docker compose -f
docker-compose.prod.yml restart gateway`. Prefer the admin web UI or jCli over
further hand-edits once the stack is running — both write through the same
live admin store without a restart.

### After first boot

The stack comes up healthy and bound — to a **bundled fake SMSC**
(`bootstrap-smsc`), not a real one. The gateway's role hard-requires at least
one SMPP connector and blocks startup (crash-looping under
`restart: unless-stopped`) until it binds, so shipping no connector, or one
pointed at an address that can never resolve, would not boot clean either —
see `configs/gateway.production.md` for the full mechanics. Before this is
useful for real traffic:

1. **Confirm your SMSC is reachable and your credentials work** before
   editing anything — the gateway won't finish restarting until the
   connector you configure actually binds.
2. Edit `configs/gateway.json`'s `connectors[0]` in place with your real
   SMSC's host/port/system_id/bind settings, your inbound MO webhook (or
   delete that route and wire SMPPS/other routing via the admin UI instead),
   and your first HTTP API sender account.
3. Set `SMSC_PASSWORD` in `.env` to the connector's real bind password
   (replacing the bootstrap-smsc placeholder value).
4. `docker compose -f docker-compose.prod.yml restart gateway`, then watch
   `docker compose -f docker-compose.prod.yml logs -f gateway` for a `BOUND`
   line.
5. Fund the default user's balance (admin web UI or jCli — the shipped
   template ships it at zero on purpose, so nothing can send until you decide
   the limit).
6. `required_connector_ids` already names this connector, so `/health` and
   `/ready` keep reflecting its real bind state — including if it later drops
   its bind. `bootstrap-smsc` is now unused; leave it running (harmless) or
   remove it from `docker-compose.prod.yml`.

### Backup, restore, teardown

See [`deploy/BACKUP.md`](deploy/BACKUP.md). Short version:

```console
$ scripts/deploy/backup.sh                          # postgres + admin.db -> $HOME/synevyr-gateway-backups
$ scripts/deploy/restore.sh <dump.sql.gz> [admin.db] # destructive; asks for confirmation
$ docker compose -f docker-compose.prod.yml down     # stop, keep volumes
$ docker compose -f docker-compose.prod.yml down -v  # stop, DELETE all volumes
```

Postgres is the durability boundary (submit transactions, CDRs/billing) —
back it up on a schedule before you rely on this in production.

### Files this section is about

| Path | Purpose |
|---|---|
| `docker-compose.prod.yml` | Single-node production stack. |
| `.env.example` | Every tunable the compose file consumes; copy to `.env`. |
| `configs/gateway.production.example.json` | Template for `configs/gateway.json`. |
| `configs/gateway.production.md` | Which fields to edit before going live, and why. |
| `scripts/deploy/setup.sh` | Idempotent onboarding: prereqs, secrets, config, build, start, health wait. |
| `scripts/deploy/backup.sh` / `restore.sh` | Postgres + admin.db backup/restore. |
| `deploy/BACKUP.md` | Backup/restore/teardown runbook. |
| `docker/Dockerfile.gateway` | Gateway image (multi-stage Go build, `python3.12-slim` runtime for the interceptor script runner). |
| `docker/Dockerfile.fakesmsc` | The bundled `bootstrap-smsc` simulator — not a production artifact, see [After first boot](#after-first-boot). |

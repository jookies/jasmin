# Jasmin Go Gateway

A Go rewrite of [Jasmin](https://github.com/jookies/jasmin), the open-source
SMPP/SMS gateway. The message path (SMPP/HTTP send and receive, DLRs,
routing/filtering, interception, admin plane, jCli, HA) is functionally
implemented for single-node and active-passive deployments — see
`docs/STATUS.md` for the detailed, evidence-cited breakdown of what's proven
byte-compatible with the legacy Python implementation versus what's
functionally complete but still accumulating compatibility evidence.

Python remains in the runtime only for the MO/MT interceptor script runner
(`scripts/interceptor_runner.py`, stdlib-only — it never unpickles or imports
legacy Jasmin code) and, optionally, a PB/Twisted compatibility sidecar for
legacy PB clients (off by default; see `docs/pb-facade.md`).

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
$ git clone <this-repo> jasmin-gateway && cd jasmin-gateway
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
    --config /etc/jasmin/gateway.json --check-config
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
  config; see `cmd/jasmin-go-httpapi/main.go`). For the SMPP port, set
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
$ scripts/deploy/backup.sh                          # postgres + admin.db -> $HOME/jasmin-gateway-backups
$ scripts/deploy/restore.sh <dump.sql.gz> [admin.db] # destructive; asks for confirmation
$ docker compose -f docker-compose.prod.yml down     # stop, keep volumes
$ docker compose -f docker-compose.prod.yml down -v  # stop, DELETE all volumes
```

Postgres is the durability boundary (submit transactions, CDRs/billing) —
back it up on a schedule before you rely on this in production.

### Optional: PB compatibility sidecar

Off by default. Only turn it on if you have legacy Python-Jasmin PB clients
still talking to this gateway — enabling it means a Python process running
the full legacy `jasmin/` tree runs in production permanently (see
`docs/pb-facade.md`). To enable:

```console
$ # edit configs/gateway.json: add admin.pb_facade_listen_address and
$ # admin.pb_facade_token (the template omits both so the port never opens
$ # unasked); set JASMIN_PB_FACADE_TOKEN in .env
$ docker compose -f docker-compose.prod.yml --profile pb up -d
```

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
| `docker/Dockerfile.pbfacade` | Optional PB compatibility sidecar image. |
| `docker/Dockerfile.fakesmsc` | The bundled `bootstrap-smsc` simulator — not a production artifact, see [After first boot](#after-first-boot). |

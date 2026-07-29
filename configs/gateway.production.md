# `gateway.json` — fields you must edit before going live

`scripts/deploy/setup.sh` copies `configs/gateway.production.example.json` to
`configs/gateway.json` on first run. That copy is what `docker-compose.prod.yml`
mounts into the gateway container. Secrets (tokens, passwords) are already
wired to environment variables via the `env:NAME` indirection (see
`internal/app/gateway/secrets.go`) and come from `.env` — you don't edit those
in the JSON. The fields below are **not** secrets, so the config schema
requires them as literal values; edit `configs/gateway.json` directly.

| Field | Placeholder shipped | What to put there |
|---|---|---|
| `connectors[0].host` | `bootstrap-smsc` (a bundled fake-SMSC container, not a real SMSC — see below) | Your real SMSC hostname/IP |
| `connectors[0].port` | `2775` | Your real SMSC port |
| `connectors[0].system_id` | `bootstrap` | The bind system_id your SMSC issued you |
| `connectors[0].bind` / `*_ton` / `*_npi` | dev defaults | Whatever your SMSC's spec sheet says |
| `outbound.routes[0].connector_id` | `smsc-primary` | Leave as-is unless you also rename `connectors[0].cid` — they must match |
| `mo_routes[0].connector.url` | `http://REPLACE_ME_mo_webhook.invalid/mo` | Your real inbound-SMS webhook, or delete this route and add SMPPS/other routing via the admin UI or jCli instead |
| `outbound.users[0].username` / `external_id` | `changeme_user` | Your first HTTP API sender account. Username must match `^[A-Za-z0-9_-]{1,15}$` (legacy constraint — `internal/app/outbound/config.go`); external_id allows one more character. |
| `outbound.users[0].password_sha256` | sha256 of the literal string `CHANGE_ME` | `printf '%s' 'your-password' \| sha256sum` — replace with the real digest |
| `outbound.users[0].balance` / `submit_sm_count` | `0` / `0` | Deliberately zero so this account cannot send until you fund it (via jCli/admin UI, or edit here) |
| `smpps.users[0].system_id` | `REPLACE_ME_ESME` | The system_id an inbound ESME will bind with |

`connectors[0].password` and `smpps.users[0].password` are already
`env:SMSC_PASSWORD` / `env:SMPPS_USER_PASSWORD` — set those in `.env`, not
here.

## SECURITY — keep the admin REST API off the public port

`admin.api_listen_address` gives `/admin/` its own listener, and the shipped
config sets it to `0.0.0.0:8405`, which `docker-compose.prod.yml` publishes on
loopback only — the same boundary the admin web UI (8404) and jCli (8990)
already had.

If you remove that field, `/admin/` falls back onto the same mux that serves
`/send` on `outbound.listen_address` (`0.0.0.0:1401`), which is published on all
interfaces because it is the legitimate public send port. The gateway logs a
warning when that happens. That API creates users, changes balances, and starts
and stops connectors, guarded by nothing but the `ADMIN_TOKEN` bearer — so if
you do expose it, block `/admin/` at a reverse proxy or firewall the port.

`allow_interceptor_editing` ships as `false` and should stay that way in
production. Interceptor scripts are arbitrary Python executed on the gateway
host, so enabling it turns the admin token into remote code execution.

## Why the shipped connector points at a bundled fake SMSC

This is not cosmetic, and it's not optional to have *some* connector.
`internal/app/gateway/config.go` hard-requires the `"http+smppc"` role to have
at least one entry in `connectors` — an empty list fails config validation
outright. Separately, `internal/app/gateway/runtime.go` waits at startup for
**every** connector in that list to reach `BOUND` state, up to
`bind_timeout_seconds` (default 30s here) — regardless of whether it's also
listed in `required_connector_ids`. If it doesn't bind in time, the whole
process exits; under `restart: unless-stopped` that's a crash-loop, not a
degraded `/health`.

Put those two constraints together: a config with zero connectors doesn't
validate, and a config with one placeholder connector pointed at an
unreachable host (an early draft of this template used `smsc.invalid`)
crash-loops the container forever, every `bind_timeout_seconds`. Neither
gives you a stack that boots clean. So the shipped connector instead points
at `bootstrap-smsc`, a tiny in-repo SMPP simulator
(`docker/Dockerfile.fakesmsc`, already used by `docker-compose.gateway.yml`
for integration testing) that accepts any bind — the gateway genuinely binds
to it, `/health` genuinely reports `ok`, and you get a real, verified,
working stack on first boot. It is **not** a real SMSC and never forwards
anything anywhere; it exists purely so there's something to bind to.

**When you add your real SMSC connector**, it must be reachable and bindable
*at the moment you restart the gateway*, or you'll hit the same crash-loop.
Order of operations:

1. Confirm your SMSC is reachable from the host (test the TCP port, confirm
   credentials with whoever issued them) before touching the config.
2. Edit `connectors[0]` in place: `host`, `port`, `system_id`, and whatever
   `bind`/ton/npi values your SMSC's spec sheet calls for.
3. Set `SMSC_PASSWORD` in `.env` to the real bind password (replacing the
   bootstrap-smsc placeholder value).
4. `docker compose -f docker-compose.prod.yml restart gateway`, then watch
   `docker compose -f docker-compose.prod.yml logs -f gateway` for a `BOUND`
   line. If it doesn't bind within `bind_timeout_seconds`, the container will
   crash-loop — fix connectivity/credentials, it won't fix itself by waiting.
5. `required_connector_ids` already lists `smsc-primary`, so `/health` and
   `/ready` reflect its bind state throughout — including if it later drops
   its bind.
6. `bootstrap-smsc` is now unused. Leave it running (harmless, internal-only,
   idle) or remove its service block and the `depends_on` entry referencing
   it from `docker-compose.prod.yml`.

## Adding more users, routes, or connectors later

Prefer the admin web UI (`https://<host>:8404`, tunnel or reverse-proxy to
reach it — it's loopback-only by default) or the jCli console over hand-editing
this file after first boot; both write through the same admin store and don't
require a restart. Reserve editing `configs/gateway.json` for what only the
static config can express (broker/store DSNs, listener addresses, the config-
owned connector and route set) and restart the `gateway` service after
changing it.

## Don't commit your edited copy

`configs/gateway.json` is not covered by `.gitignore` in this repository (only
files under the explicitly reserved deployment paths were touched to produce
it). Once you've filled in real values, avoid `git add`-ing it — e.g. `git
update-index --skip-worktree configs/gateway.json` in your deployment clone,
or keep the deployment checkout outside of a repo you push from at all.

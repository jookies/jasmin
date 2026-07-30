#!/usr/bin/env bash
# Run and re-run the local Synevyr stack (docker-compose.gateway.yml):
# gateway + postgres + rabbitmq + redis + a fake SMSC. Self-contained — it
# needs no .env and no real SMSC, and it mounts configs/gateway.example.json,
# so it never touches your production configs/gateway.json or .env.
#
# Safe to re-run: `up` and `restart` reconcile whatever is already running and
# rebuild only what changed (Docker layer cache + an input-hash check on the
# embedded admin UI bundle).
#
# Usage: scripts/dev.sh [command]
#
#   up        (default) build changed images, start the stack, wait for /health
#   restart   rebuild + recreate ONLY the gateway (keeps pg/rabbit/redis warm)
#   web       rebuild the embedded admin UI bundle from web/src
#   ui        run the Vite dev server (hot reload) against the running gateway
#   smoke     send one test message through the HTTP API
#   logs [s]  follow logs (all services, or one: gateway, smppsim, postgres...)
#   ps        show container status
#   health    print the gateway /health payload
#   stop      stop containers, keep them and all data
#   down      remove containers + network, KEEP volumes (data survives)
#   reset     remove containers + network + VOLUMES (destroys all local data)
#
# Env knobs: HEALTH_TIMEOUT (seconds, default 180), SKIP_WEB_BUILD=1.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"

COMPOSE_FILE="docker-compose.gateway.yml"
# Own compose project, deliberately not the default ("jasmin", the directory
# name). docker-compose.prod.yml declares volumes with the SAME names (pgdata,
# admindata), so sharing the default project would let a dev `reset` wipe the
# data of a prod-compose stack started from this checkout. Cost of isolating:
# this stack starts with empty volumes; pre-existing jasmin_* volumes are left
# alone (`docker volume ls`).
PROJECT_NAME="synevyr-dev"

# Ports as published by docker-compose.gateway.yml.
HTTP_PORT=1401     # public: /send, /health, /secure/*
REST_PORT=8080     # loopback: legacy REST daemon
WEB_PORT=8404      # loopback: admin web UI  (privilege boundary)
SMPP_PORT=2775     # loopback: SMPPs server (inbound ESME binds)
JCLI_PORT=8990     # loopback: jCli console (privilege boundary)
SMSC_INJECT_PORT=8288  # fake SMSC: POST /inject/mo, /inject/dlr

HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-180}"

# ----------------------------------------------------------------------------
# Output helpers (same vocabulary as scripts/deploy/setup.sh)
# ----------------------------------------------------------------------------
info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$1" >&2; }
fail() { printf '\033[1;31mERROR:\033[0m %s\n' "$1" >&2; exit 1; }

DC() { docker compose -p "${PROJECT_NAME}" -f "${COMPOSE_FILE}" "$@"; }

require_docker() {
  command -v docker >/dev/null 2>&1 ||
    fail "docker not found in PATH. Install Docker Desktop / Engine first."
  docker compose version >/dev/null 2>&1 ||
    fail "'docker compose' (v2 plugin) not found. Install the Compose plugin."
  docker info >/dev/null 2>&1 ||
    fail "cannot reach the Docker daemon — is Docker running?"
}

# ----------------------------------------------------------------------------
# Admin UI bundle
#
# internal/app/adminweb/dist is committed and embedded into the binary
# (go:embed), so a stale bundle means the container serves yesterday's UI.
# bundle.sourcehash records the inputs the committed dist was built from; if
# web/src has moved on, rebuild before baking the image.
# ----------------------------------------------------------------------------
web_is_stale() {
  local recorded current
  recorded="$(cat internal/app/adminweb/bundle.sourcehash 2>/dev/null || echo none)"
  current="$(scripts/adminweb-source-hash.sh)"
  [ "${recorded}" != "${current}" ]
}

build_web() {
  command -v npm >/dev/null 2>&1 ||
    fail "npm not found — needed to rebuild the admin UI bundle. Install Node 20+, or set SKIP_WEB_BUILD=1 to run with the committed bundle."
  if [ ! -d web/node_modules ]; then
    info "Installing web dependencies (npm ci)..."
    (cd web && npm ci)
  fi
  info "Building the admin UI bundle (web/src -> internal/app/adminweb/dist)..."
  (cd web && npm run build)
  info "Bundle rebuilt. It is committed to git — include dist/ in your commit."
}

ensure_web() {
  if [ "${SKIP_WEB_BUILD:-0}" = "1" ]; then
    info "SKIP_WEB_BUILD=1 — using the committed admin UI bundle as-is."
    return 0
  fi
  if web_is_stale; then
    warn "Admin UI bundle is stale relative to web/src — rebuilding."
    build_web
  else
    info "Admin UI bundle is up to date."
  fi
}

# ----------------------------------------------------------------------------
# Health
# ----------------------------------------------------------------------------
health_url() { printf 'http://127.0.0.1:%s/health' "${HTTP_PORT}"; }

http_ok() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsS -o /dev/null "$1" 2>/dev/null
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O /dev/null "$1" 2>/dev/null
  else
    return 2
  fi
}

wait_healthy() {
  local url rc deadline_steps=$((HEALTH_TIMEOUT / 3)) i=0
  url="$(health_url)"
  info "Waiting for the gateway at ${url} (up to ${HEALTH_TIMEOUT}s)..."
  while [ "${i}" -lt "${deadline_steps}" ]; do
    rc=0
    http_ok "${url}" || rc=$?
    if [ "${rc}" -eq 0 ]; then
      info "Gateway is healthy."
      return 0
    fi
    # 2 = no HTTP client available; anything else is "not ready yet".
    if [ "${rc}" -eq 2 ]; then
      warn "Neither curl nor wget available — skipping the health wait."
      return 0
    fi
    i=$((i + 1))
    sleep 3
  done
  warn "Gateway did not report healthy within ${HEALTH_TIMEOUT}s. Recent logs:"
  DC logs --tail=60 gateway || true
  warn "The stack is still up — inspect with 'scripts/dev.sh logs gateway'."
  return 1
}

print_endpoints() {
  cat <<EOF

  HTTP API      http://127.0.0.1:${HTTP_PORT}      user smppuser / password
  REST daemon   http://127.0.0.1:${REST_PORT}
  Admin web UI  http://127.0.0.1:${WEB_PORT}      admin / dev-admin-password
  jCli console  nc 127.0.0.1 ${JCLI_PORT}         jcliadmin / dev-jcli-password
  SMPPs bind    127.0.0.1:${SMPP_PORT}            shortcode-app / shortcodepw
  Fake SMSC     http://127.0.0.1:${SMSC_INJECT_PORT}/inject/mo  (and /inject/dlr)

  Send one:   scripts/dev.sh smoke
  Iterate:    scripts/dev.sh restart      # rebuild gateway only
  Logs:       scripts/dev.sh logs gateway

EOF
}

# ----------------------------------------------------------------------------
# Commands
# ----------------------------------------------------------------------------
cmd_up() {
  require_docker
  ensure_web
  info "Building and starting the stack (${COMPOSE_FILE})..."
  DC up -d --build
  wait_healthy || true
  DC ps
  print_endpoints
}

cmd_restart() {
  require_docker
  ensure_web
  info "Rebuilding and recreating the gateway (dependencies stay up)..."
  # --force-recreate so a configs/gateway.example.json edit is picked up too:
  # that file is a bind mount, so it leaves the image digest unchanged and
  # compose would otherwise consider the running container up to date.
  DC up -d --build --force-recreate gateway
  wait_healthy || true
}

cmd_ui() {
  command -v npm >/dev/null 2>&1 || fail "npm not found — needed for the Vite dev server."
  [ -d web/node_modules ] || (cd web && npm ci)
  http_ok "$(health_url)" ||
    warn "Gateway does not answer on ${HTTP_PORT} — start it first ('scripts/dev.sh up'), or the UI's /api proxy will 502."
  info "Vite dev server on http://127.0.0.1:5273 (proxies /api -> 127.0.0.1:${WEB_PORT})."
  info "Hot reload only — the embedded bundle is unchanged until 'scripts/dev.sh web'."
  (cd web && npm run dev)
}

cmd_smoke() {
  command -v curl >/dev/null 2>&1 || fail "curl not found."
  local url="http://127.0.0.1:${HTTP_PORT}/send?username=smppuser&password=password&to=15551234567&from=1111&content=hello"
  info "GET ${url}"
  curl -fsS "${url}" && echo
}

cmd_reset() {
  require_docker
  warn "This deletes the '${PROJECT_NAME}' volumes: Postgres (submits, CDRs) and"
  warn "the admin SQLite DB (users, connectors, routes created in the UI/jCli)."
  printf 'Type "yes" to destroy local data: '
  local answer
  read -r answer
  [ "${answer}" = "yes" ] || fail "Aborted — nothing was removed."
  DC down -v
  info "Stack and volumes removed. 'scripts/dev.sh up' starts from scratch."
}

main() {
  case "${1:-up}" in
    up) cmd_up ;;
    restart) cmd_restart ;;
    web) build_web ;;
    ui) cmd_ui ;;
    smoke) cmd_smoke ;;
    logs) require_docker; shift || true; DC logs -f --tail=100 "$@" ;;
    ps) require_docker; DC ps ;;
    health)
      command -v curl >/dev/null 2>&1 || fail "curl not found."
      curl -fsS "$(health_url)" && echo
      ;;
    stop) require_docker; DC stop ;;
    down) require_docker; DC down; info "Containers removed; volumes kept." ;;
    reset) cmd_reset ;;
    # Print the header comment block (line 2 up to the first non-comment line).
    -h | --help | help)
      awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "${BASH_SOURCE[0]}"
      ;;
    *) fail "unknown command '${1}'. Run 'scripts/dev.sh --help'." ;;
  esac
}

main "$@"

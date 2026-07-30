#!/usr/bin/env bash
# Onboarding script for the Go Jasmin gateway production stack.
#
# Idempotent and safe to re-run: it never overwrites an existing .env or
# configs/gateway.json, only fills in secrets that are still blank, and
# `docker compose up -d` naturally reconciles an already-running stack.
#
# What it does:
#   1. Checks for docker + docker compose v2 + a reachable daemon.
#   2. Creates .env from .env.example on first run, generating strong random
#      secrets for anything required-but-blank.
#   3. Creates configs/gateway.json from the production template on first run.
#   4. Builds the gateway image and runs --check-config as a fail-fast
#      preflight, before touching postgres/rabbitmq/redis.
#   5. Pulls postgres/rabbitmq/redis and starts the stack.
#   6. Waits for /health to report ok and prints next steps.
#
# Usage: scripts/deploy/setup.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${REPO_ROOT}"

COMPOSE_FILE="docker-compose.prod.yml"
ENV_FILE=".env"
ENV_EXAMPLE=".env.example"
CONFIG_FILE="configs/gateway.json"
CONFIG_TEMPLATE="configs/gateway.production.example.json"

# ----------------------------------------------------------------------------
# Output helpers
# ----------------------------------------------------------------------------
info()  { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn()  { printf '\033[1;33m!!\033[0m %s\n' "$1" >&2; }
fail()  { printf '\033[1;31mERROR:\033[0m %s\n' "$1" >&2; exit 1; }

# ----------------------------------------------------------------------------
# 1. Prerequisites
# ----------------------------------------------------------------------------
info "Checking prerequisites..."

command -v docker >/dev/null 2>&1 || fail "docker not found in PATH. Install Docker first: https://docs.docker.com/engine/install/"

if ! docker compose version >/dev/null 2>&1; then
  fail "'docker compose' (v2 plugin) not found. Install the Compose plugin: https://docs.docker.com/compose/install/"
fi

if ! docker info >/dev/null 2>&1; then
  fail "cannot reach the Docker daemon. Is it running, and does this user have permission (docker group membership, or run with sudo)?"
fi

DC() { docker compose -f "${COMPOSE_FILE}" "$@"; }

info "Prerequisites OK: $(docker --version), $(docker compose version --short 2>/dev/null || echo 'compose v2')"

# ----------------------------------------------------------------------------
# 2. .env
# ----------------------------------------------------------------------------
generate_secret() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

# SMPP 3.4 declares bind password a COctetString of at most 9 octets -- 8
# characters plus the null terminator. A 64-character secret cannot be encoded by
# a conformant ESME at all, so a generated one would make the SMPP server
# unbindable rather than merely hard to guess. 8 hex characters is 32 bits, which
# is weak on its own: the SMPP listener must be reachable only by known peers
# (IP allow-listing or a private network), never exposed to the internet on the
# strength of this password.
generate_smpp_password() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 4
  else
    head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n'
  fi
}

# set_secret_if_blank NAME FILE: leaves an already-non-empty NAME=... line
# alone; fills in a generated value if the line is missing or empty.
set_secret_if_blank() {
  local name="$1" file="$2" value
  if grep -qE "^${name}=.+" "${file}"; then
    return 0
  fi
  case "${name}" in
    SMPPS_USER_PASSWORD|SMSC_PASSWORD) value="$(generate_smpp_password)" ;;
    *) value="$(generate_secret)" ;;
  esac
  if grep -qE "^${name}=" "${file}"; then
    sed -i.bak "s|^${name}=.*|${name}=${value}|" "${file}" && rm -f "${file}.bak"
  else
    printf '%s=%s\n' "${name}" "${value}" >> "${file}"
  fi
  info "  generated ${name}"
}

if [ ! -f "${ENV_FILE}" ]; then
  [ -f "${ENV_EXAMPLE}" ] || fail "${ENV_EXAMPLE} is missing from the repo — cannot bootstrap ${ENV_FILE}."
  cp "${ENV_EXAMPLE}" "${ENV_FILE}"
  info "Created ${ENV_FILE} from ${ENV_EXAMPLE}."
else
  info "${ENV_FILE} already exists — leaving existing values untouched."
fi

info "Filling in any blank required secrets in ${ENV_FILE}..."
for secret in POSTGRES_PASSWORD RABBITMQ_DEFAULT_PASS ADMIN_TOKEN ADMIN_WEB_PASSWORD JCLI_PASSWORD SMPPS_USER_PASSWORD; do
  set_secret_if_blank "${secret}" "${ENV_FILE}"
done

if ! grep -qE '^SMSC_PASSWORD=.+' "${ENV_FILE}"; then
  fail "SMSC_PASSWORD is blank in ${ENV_FILE}. It's required (the gateway always needs at least one SMPPc connector) — .env.example ships a working default for the bundled bootstrap-smsc simulator; restore it, or set your real SMSC's bind password if you've already edited ${CONFIG_FILE}."
fi

# Load .env into this script's environment for the health-check wait below.
set -a
# shellcheck disable=SC1090
source "${ENV_FILE}"
set +a

# ----------------------------------------------------------------------------
# 3. configs/gateway.json
# ----------------------------------------------------------------------------
if [ ! -f "${CONFIG_FILE}" ]; then
  [ -f "${CONFIG_TEMPLATE}" ] || fail "${CONFIG_TEMPLATE} is missing from the repo — cannot bootstrap ${CONFIG_FILE}."
  cp "${CONFIG_TEMPLATE}" "${CONFIG_FILE}"
  info "Created ${CONFIG_FILE} from ${CONFIG_TEMPLATE}."
  warn "${CONFIG_FILE} points at the bundled bootstrap-smsc simulator, not a real SMSC, and has placeholder users."
  warn "Read configs/gateway.production.md and edit ${CONFIG_FILE} before relying on this for real traffic."
else
  info "${CONFIG_FILE} already exists — leaving it untouched."
fi

# ----------------------------------------------------------------------------
# 4. Build + fail-fast config validation (no dependencies required yet)
# ----------------------------------------------------------------------------
info "Building the gateway and bootstrap-smsc images..."
DC build gateway bootstrap-smsc

info "Validating configuration (--check-config)..."
if ! DC run --rm --no-deps -T gateway --config /etc/jasmin/gateway.json --check-config; then
  fail "configuration failed validation. Check ${CONFIG_FILE} and ${ENV_FILE}, then re-run this script."
fi
info "Configuration OK."

# ----------------------------------------------------------------------------
# 5. Pull infra images and start the stack
# ----------------------------------------------------------------------------
info "Pulling postgres/rabbitmq/redis images..."
DC pull postgres rabbitmq redis

info "Starting the stack..."
DC up -d

# ----------------------------------------------------------------------------
# 6. Wait for health
# ----------------------------------------------------------------------------
HEALTH_URL="http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/health"
info "Waiting for the gateway to report healthy at ${HEALTH_URL} ..."

http_get_ok() {
  # Errors during the polling loop are expected (the connector is still
  # binding) and would otherwise print a spurious "curl: (22) ..." line per
  # attempt; only the final timeout/logs message should alarm anyone watching.
  if command -v curl >/dev/null 2>&1; then
    curl -fsS -o /dev/null "$1" 2>/dev/null
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O /dev/null "$1" 2>/dev/null
  else
    return 2
  fi
}

attempts=0
max_attempts=36 # 36 * 5s = 180s, generous for a first-run image pull/migrate
healthy=0
while [ "${attempts}" -lt "${max_attempts}" ]; do
  if http_get_ok "${HEALTH_URL}"; then
    healthy=1
    break
  fi
  rc=$?
  if [ "${rc}" -eq 2 ]; then
    warn "Neither curl nor wget is available — skipping the automated health wait."
    warn "Check manually: docker compose -f ${COMPOSE_FILE} ps, then curl ${HEALTH_URL}"
    break
  fi
  attempts=$((attempts + 1))
  sleep 5
done

if [ "${healthy}" -eq 1 ]; then
  info "Gateway is healthy."
elif [ "${attempts}" -ge "${max_attempts}" ]; then
  warn "Gateway did not report healthy within $((max_attempts * 5))s. Recent logs:"
  DC logs --tail=80 gateway || true
  warn "The stack is still running — inspect with 'docker compose -f ${COMPOSE_FILE} ps' and 'docker compose -f ${COMPOSE_FILE} logs gateway'."
fi

# ----------------------------------------------------------------------------
# Summary
# ----------------------------------------------------------------------------
echo
info "docker compose -f ${COMPOSE_FILE} ps:"
DC ps
echo
info "Next steps:"
cat <<EOF
  1. Edit ${CONFIG_FILE} with your real SMSC connector, MO webhook, and first
     HTTP API user (see configs/gateway.production.md), then:
       docker compose -f ${COMPOSE_FILE} restart gateway
  2. Admin web UI (loopback-only, tunnel or reverse-proxy to reach it):
       http://127.0.0.1:${GATEWAY_ADMIN_WEB_PORT:-8404}  (user: admin)
  3. jCli console (loopback-only):
       nc 127.0.0.1 ${GATEWAY_JCLI_PORT:-8990}  (user: jcliadmin)
  4. Public data plane:
       HTTP API   http://<this-host>:${GATEWAY_HTTP_PORT:-1401}
       REST daemon http://<this-host>:${GATEWAY_REST_PORT:-8080}
       SMPP server <this-host>:${GATEWAY_SMPP_PORT:-2775}
     Put a TLS-terminating reverse proxy in front of anything reachable from
     the internet — see README.md's Deployment section.
  5. Set up backups: deploy/BACKUP.md (Postgres holds your CDR/billing data).
  6. Re-run this script any time — it is idempotent.
EOF

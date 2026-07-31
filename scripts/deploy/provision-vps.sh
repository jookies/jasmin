#!/usr/bin/env bash
# Provision a bare VPS into a running Synevyr gateway, from your laptop.
#
#   scripts/deploy/provision-vps.sh --host 203.0.113.10 --port 22 --user root
#
# Unlike scripts/deploy/setup.sh (which runs ON the box, from a source checkout,
# and builds locally) this drives a REMOTE machine over SSH and runs the
# published image. The VPS needs nothing but a fresh Debian/Ubuntu and your SSH
# key; Docker is installed if missing.
#
# Idempotent. Re-running reconciles: it never regenerates secrets that already
# exist, never overwrites a config you edited, and `compose up -d` settles an
# already-running stack.
#
# ----------------------------------------------------------------------------
# SECURITY POSTURE, and why it is what it is
# ----------------------------------------------------------------------------
# Everything is closed by default. You open exactly what you need, explicitly.
#
# The admin console (8404), admin API (8405) and jCli (8990) are NEVER published
# to the internet by this script, and there is no flag to do so. They mint
# credentials and start connectors. Reach them over an SSH tunnel; the command is
# printed at the end.
#
# The data plane (1401 HTTP, 8080 REST, 2775 SMPP) is ALSO closed by default,
# bound to the VPS's loopback. Open a port with --open-http / --open-rest /
# --open-smpp, ideally alongside --allow-ip so only your carrier or partner can
# reach it.
#
# THE FOOT-GUN THIS SCRIPT EXISTS TO AVOID: Docker publishes ports by writing
# iptables rules in the DOCKER chain, which is evaluated BEFORE ufw's. So
# `ufw deny 1401` does NOT block a container published on 0.0.0.0:1401 -- the
# firewall looks correct, `ufw status` agrees with you, and the port is open to
# the internet anyway. This script therefore closes ports at the BIND ADDRESS
# (127.0.0.1:1401:1401) rather than trusting the firewall, and uses ufw only for
# things Docker does not publish, such as SSH.
#
# ----------------------------------------------------------------------------
# Options
# ----------------------------------------------------------------------------
#   --host <ip>           REQUIRED. VPS address.
#   --port <n>            SSH port (default 22).
#   --user <name>         SSH user (default root).
#   --tag <version>       Image tag to deploy (default 0.1.0).
#   --open-http           Publish 1401 (submit/DLR API) on all interfaces.
#   --open-rest           Publish 8080 (legacy REST + message pull API).
#   --open-smpp           Publish 2775 (inbound ESME binds).
#   --open-admin          Publish 8404/8405 (admin console + admin API).
#                         DANGEROUS: there is no TLS, so the admin password and
#                         session cookie cross the network in cleartext, and the
#                         console mints credentials and reads message content.
#                         Use an SSH tunnel unless you have put TLS in front.
#   --open-jcli           Publish 8990 (jCli). Same warning; jCli is plaintext
#                         telnet-style, so the password is readable on the wire.
#   --allow-ip <cidr>     Restrict opened data-plane ports to this source.
#                         Repeatable. Applied in Docker's own DOCKER-USER chain,
#                         which is the only place a rule survives Docker's
#                         iptables management.
#   --dry-run             Print what would run remotely; change nothing.
set -euo pipefail

HOST=""; SSH_PORT=22; SSH_USER=root; TAG="0.1.0"
OPEN_HTTP=0; OPEN_REST=0; OPEN_SMPP=0; OPEN_ADMIN=0; OPEN_JCLI=0; DRY_RUN=0
ALLOW_IPS=()
REMOTE_DIR=/opt/synevyr
IMAGE_REPO="aroksetx/synevyr-messaging-platform"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$1" >&2; }
fail() { printf '\033[1;31mERROR:\033[0m %s\n' "$1" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --host)      shift; HOST="${1:?--host needs a value}" ;;
    --port)      shift; SSH_PORT="${1:?--port needs a value}" ;;
    --user)      shift; SSH_USER="${1:?--user needs a value}" ;;
    --tag)       shift; TAG="${1:?--tag needs a value}" ;;
    --allow-ip)  shift; ALLOW_IPS+=("${1:?--allow-ip needs a value}") ;;
    --open-http) OPEN_HTTP=1 ;;
    --open-rest) OPEN_REST=1 ;;
    --open-smpp) OPEN_SMPP=1 ;;
    --open-admin) OPEN_ADMIN=1 ;;
    --open-jcli) OPEN_JCLI=1 ;;
    --dry-run)   DRY_RUN=1 ;;
    # Print the header comment block: line 2 up to the first non-comment line.
    -h|--help)   awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "${BASH_SOURCE[0]}"; exit 0 ;;
    *)           fail "unknown option '$1'" ;;
  esac
  shift
done

[ -n "${HOST}" ] || fail "usage: $0 --host <ip> [--port 22] [--user root] [--open-http] [--allow-ip CIDR]"
command -v ssh >/dev/null 2>&1 || fail "ssh not found."
command -v scp >/dev/null 2>&1 || fail "scp not found."

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"
[ -f docker-compose.release.yml ] || fail "docker-compose.release.yml not found; run from the repository."

SSH=(ssh -p "${SSH_PORT}" -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new "${SSH_USER}@${HOST}")
remote() { "${SSH[@]}" "$@"; }

# Everything privileged runs through this. A cloud image's default login
# (ubuntu, debian, ec2-user) is not root, and Docker group membership does not
# take effect until a NEW session -- so even after installing Docker, this
# session still needs sudo for `docker`. Using it uniformly avoids a class of
# "works on the second run" bugs.
SUDO=""
if [ "${SSH_USER}" != "root" ]; then SUDO="sudo "; fi
rsudo() { remote "${SUDO}$1"; }

# Bind addresses. A closed port is bound to the VPS's loopback rather than left
# unpublished, so `docker compose ps` still shows the mapping and an operator can
# curl it over an SSH tunnel while the internet cannot reach it.
bind_for() { if [ "$1" -eq 1 ]; then printf '%s' "$2"; else printf '127.0.0.1:%s' "$2"; fi; }
HTTP_BIND="$(bind_for "${OPEN_HTTP}" 1401)"
ADMIN_WEB_BIND="$(bind_for "${OPEN_ADMIN}" 8404)"
ADMIN_API_BIND="$(bind_for "${OPEN_ADMIN}" 8405)"
JCLI_BIND="$(bind_for "${OPEN_JCLI}" 8990)"
REST_BIND="$(bind_for "${OPEN_REST}" 8080)"
SMPP_BIND="$(bind_for "${OPEN_SMPP}" 2775)"

info "target      ${SSH_USER}@${HOST}:${SSH_PORT}"
info "image       ${IMAGE_REPO}:${TAG}"
info "data plane  http=${HTTP_BIND}  rest=${REST_BIND}  smpp=${SMPP_BIND}"
if [ "${OPEN_ADMIN}" -eq 1 ] || [ "${OPEN_JCLI}" -eq 1 ]; then
  warn "admin plane PUBLISHED: web=${ADMIN_WEB_BIND} api=${ADMIN_API_BIND} jcli=${JCLI_BIND}"
  warn "There is no TLS. The admin password and session cookie cross the network"
  warn "in cleartext, and this console mints credentials and reads message text."
else
  info "admin plane loopback only (SSH tunnel)"
fi
if [ ${#ALLOW_IPS[@]} -gt 0 ]; then info "allow-list  ${ALLOW_IPS[*]}"; fi

if [ "${DRY_RUN}" -eq 1 ]; then
  info "--dry-run: nothing will be changed on the remote host."
fi

# ----------------------------------------------------------------------------
# 1. Reachability and OS
# ----------------------------------------------------------------------------
info "Checking SSH reachability..."
remote true 2>/dev/null || fail "cannot SSH to ${SSH_USER}@${HOST}:${SSH_PORT}. Check the address, port, and that your key is authorized."

OS_ID="$(remote '. /etc/os-release 2>/dev/null && echo "${ID:-unknown}"' || echo unknown)"
info "remote OS: ${OS_ID}"
case "${OS_ID}" in
  ubuntu|debian) ;;
  *) warn "only ubuntu/debian are handled; Docker installation may fail on '${OS_ID}'." ;;
esac

[ "${DRY_RUN}" -eq 1 ] && { info "Dry run complete — reachable, OS ${OS_ID}."; exit 0; }

# ----------------------------------------------------------------------------
# 2. Docker
# ----------------------------------------------------------------------------
if rsudo 'docker compose version >/dev/null 2>&1'; then
  info "Docker and the Compose plugin are already installed."
else
  info "Installing Docker (official convenience script)..."
  rsudo 'sh -c "set -e
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq ca-certificates curl ufw >/dev/null
    curl -fsSL https://get.docker.com | sh >/dev/null
    systemctl enable --now docker"' ||
    fail "Docker installation failed. Install it manually and re-run."
  rsudo 'docker compose version >/dev/null 2>&1' ||
    fail "Docker installed but the Compose v2 plugin is missing."
fi

# ----------------------------------------------------------------------------
# 3. Firewall
#
# ufw governs only what Docker does not publish. SSH must be allowed BEFORE the
# default-deny, or enabling ufw over SSH locks you out of your own machine.
# ----------------------------------------------------------------------------
info "Configuring the host firewall (ufw)..."
rsudo "sh -c \"set -e
  ufw allow ${SSH_PORT}/tcp
  ufw --force default deny incoming
  ufw --force default allow outgoing
  ufw --force enable\"" >/dev/null || warn "ufw configuration failed; continuing."

# Source allow-listing has to live in DOCKER-USER: it is the one chain Docker
# consults and does not rewrite, so a rule here survives container restarts and
# daemon reloads. Rules in ufw's chains do not apply to published ports at all.
if [ ${#ALLOW_IPS[@]} -gt 0 ] && { [ "${OPEN_HTTP}" -eq 1 ] || [ "${OPEN_REST}" -eq 1 ] || [ "${OPEN_SMPP}" -eq 1 ]; }; then
  info "Restricting opened data-plane ports to the allow-list..."
  PORTS=""
  [ "${OPEN_HTTP}" -eq 1 ] && PORTS="${PORTS} 1401"
  [ "${OPEN_REST}" -eq 1 ] && PORTS="${PORTS} 8080"
  [ "${OPEN_SMPP}" -eq 1 ] && PORTS="${PORTS} 2775"
  ALLOW_LIST="${ALLOW_IPS[*]}"
  rsudo "sh -c \"set -e
    for port in ${PORTS}; do
      # Flush this port's previous rules so re-running does not stack duplicates.
      while iptables -C DOCKER-USER -p tcp --dport \$port -j DROP 2>/dev/null; do
        iptables -D DOCKER-USER -p tcp --dport \$port -j DROP
      done
      for cidr in ${ALLOW_LIST}; do
        while iptables -C DOCKER-USER -p tcp --dport \$port -s \$cidr -j RETURN 2>/dev/null; do
          iptables -D DOCKER-USER -p tcp --dport \$port -s \$cidr -j RETURN
        done
        iptables -I DOCKER-USER 1 -p tcp --dport \$port -s \$cidr -j RETURN
      done
      iptables -A DOCKER-USER -p tcp --dport \$port -j DROP
    done\"" || warn "allow-list rules failed to apply; opened ports are reachable from anywhere."
  warn "iptables rules are NOT persisted across reboot. Install iptables-persistent, or re-run this script after a reboot."
fi

# ----------------------------------------------------------------------------
# 4. Registry credentials
#
# The image repository is private. The token is read interactively and piped
# straight into `docker login --password-stdin` over the SSH channel: it never
# appears in argv (visible to every user via `ps`), in your shell history, or in
# this repository.
# ----------------------------------------------------------------------------
if rsudo "docker image inspect ${IMAGE_REPO}:${TAG} >/dev/null 2>&1 || ${SUDO}docker pull -q ${IMAGE_REPO}:${TAG} >/dev/null 2>&1"; then
  info "Image is already pullable on the remote host."
else
  info "The image repository is private and the VPS cannot pull it yet."
  printf 'Docker Hub username [aroksetx]: '; read -r DH_USER; DH_USER="${DH_USER:-aroksetx}"
  printf 'Docker Hub ACCESS TOKEN (not your password; input hidden): '
  read -rs DH_TOKEN; printf '\n'
  [ -n "${DH_TOKEN}" ] || fail "no token given."
  printf '%s' "${DH_TOKEN}" | remote "${SUDO}docker login --username '${DH_USER}' --password-stdin" >/dev/null ||
    fail "docker login failed on the remote host."
  unset DH_TOKEN
  info "Registry login stored in ${SSH_USER}'s ~/.docker/config.json on the VPS."
fi

# ----------------------------------------------------------------------------
# 5. Deploy directory, compose file, secrets
# ----------------------------------------------------------------------------
info "Creating ${REMOTE_DIR} and copying the compose file..."
rsudo "mkdir -p ${REMOTE_DIR} && ${SUDO}chmod 750 ${REMOTE_DIR}"
scp -q -P "${SSH_PORT}" docker-compose.release.yml "${SSH_USER}@${HOST}:/tmp/synevyr-compose.yml"
rsudo "mv /tmp/synevyr-compose.yml ${REMOTE_DIR}/docker-compose.yml"

# Secrets are generated ON the VPS so they never traverse your terminal, your
# scrollback, or this machine's disk. Re-running preserves an existing .env:
# regenerating secrets would invalidate the running Postgres password and the
# credentials any partner already holds.
info "Generating secrets (only those not already present)..."
rsudo "sh -c \"set -e
  cd ${REMOTE_DIR}
  if [ ! -f .env ]; then
    umask 077
    {
      echo 'SYNEVYR_IMAGE=${IMAGE_REPO}'
      echo 'SYNEVYR_VERSION=${TAG}'
      echo 'POSTGRES_USER=synevyr'
      echo \"POSTGRES_PASSWORD=\$(openssl rand -hex 24)\"
      echo 'POSTGRES_DB=synevyr'
      echo 'RABBITMQ_DEFAULT_USER=jasmin'
      echo \"RABBITMQ_DEFAULT_PASS=\$(openssl rand -hex 24)\"
      echo 'REDIS_URL=redis://redis:6379/0'
      echo \"ADMIN_TOKEN=\$(openssl rand -hex 32)\"
      echo \"ADMIN_WEB_PASSWORD=\$(openssl rand -hex 16)\"
      echo \"JCLI_PASSWORD=\$(openssl rand -hex 16)\"
      echo 'SMSC_PASSWORD=bootstrap-smsc-accepts-any-password'
      # SMPP 3.4 caps the bind password at 8 characters (COctetString of 9
      # octets including the terminator). A longer value cannot be encoded by a
      # conformant ESME, so the bind fails before authentication -- with an
      # error that never mentions length. 8 hex chars is weak on its own, which
      # is why 2775 is closed unless you open it, ideally allow-listed.
      echo \"SMPPS_USER_PASSWORD=\$(openssl rand -hex 4)\"
    } > .env
    chmod 600 .env
    echo CREATED
  else
    echo KEPT
  fi\"" | while read -r state; do
    case "${state}" in
      CREATED) info "Wrote ${REMOTE_DIR}/.env (0600), secrets generated on the VPS." ;;
      KEPT)    info "Existing ${REMOTE_DIR}/.env kept — secrets not regenerated." ;;
    esac
  done

# Port bindings are rewritten on every run, so --open-* / --allow-ip changes take
# effect without hand-editing .env on the box.
info "Applying port bindings..."
rsudo "sh -c \"set -e
  cd ${REMOTE_DIR}
  # Rewritten on every run, not just at creation. These are the values --tag and
  # the --open-* flags control, and an existing .env is otherwise kept verbatim
  # to preserve secrets -- so without this, re-running with a new --tag silently
  # redeployed the OLD image and reported success. A flag that does nothing is
  # worse than one that errors.
  sed -i '/^SYNEVYR_VERSION=/d;/^SYNEVYR_IMAGE=/d' .env
  sed -i '/^GATEWAY_HTTP_PORT=/d;/^GATEWAY_REST_PORT=/d;/^GATEWAY_SMPP_PORT=/d' .env
  sed -i '/^GATEWAY_ADMIN_WEB_PORT=/d;/^GATEWAY_ADMIN_API_PORT=/d;/^GATEWAY_JCLI_PORT=/d' .env
  {
    echo 'SYNEVYR_IMAGE=${IMAGE_REPO}'
    echo 'SYNEVYR_VERSION=${TAG}'
    echo 'GATEWAY_HTTP_PORT=${HTTP_BIND}'
    echo 'GATEWAY_REST_PORT=${REST_BIND}'
    echo 'GATEWAY_SMPP_PORT=${SMPP_BIND}'
    echo 'GATEWAY_ADMIN_WEB_PORT=${ADMIN_WEB_BIND}'
    echo 'GATEWAY_ADMIN_API_PORT=${ADMIN_API_BIND}'
    echo 'GATEWAY_JCLI_PORT=${JCLI_BIND}'
  } >> .env\""

# ----------------------------------------------------------------------------
# 6. Start
# ----------------------------------------------------------------------------
info "Pulling images and starting the stack..."
rsudo "sh -c \"cd ${REMOTE_DIR} && docker compose pull -q && docker compose up -d\"" ||
  fail "compose up failed. Inspect with: ssh -p ${SSH_PORT} ${SSH_USER}@${HOST} 'cd ${REMOTE_DIR} && docker compose logs gateway'"

info "Waiting for /health (up to 180s)..."
HEALTHY=0
for _ in $(seq 1 36); do
  # curl runs ON the VPS: the port is loopback-bound unless you opened it, so
  # health cannot be checked from here.
  if remote "curl -fsS -o /dev/null http://127.0.0.1:1401/health" 2>/dev/null; then HEALTHY=1; break; fi
  sleep 5
done

if [ "${HEALTHY}" -eq 1 ]; then
  info "Gateway is healthy."
  remote "curl -fsS http://127.0.0.1:1401/health" || true
  printf '\n'
else
  warn "Gateway did not report healthy in 180s. Recent logs:"
  rsudo "sh -c \"cd ${REMOTE_DIR} && docker compose logs --tail=40 gateway\"" || true
  fail "Deployment finished but the gateway is not ready."
fi

# ----------------------------------------------------------------------------
# 7. What to do next
# ----------------------------------------------------------------------------
ADMIN_PW="$(rsudo "grep '^ADMIN_WEB_PASSWORD=' ${REMOTE_DIR}/.env" | cut -d= -f2)"
JCLI_PW="$(rsudo "grep '^JCLI_PASSWORD=' ${REMOTE_DIR}/.env" | cut -d= -f2)"

cat <<EOF

  ── Deployed ────────────────────────────────────────────────────────────────

  Admin console   ssh -p ${SSH_PORT} -L 8404:127.0.0.1:8404 ${SSH_USER}@${HOST}
                  then open http://127.0.0.1:8404
                  admin / ${ADMIN_PW}

  jCli console    ssh -p ${SSH_PORT} -L 8990:127.0.0.1:8990 ${SSH_USER}@${HOST}
                  then: nc 127.0.0.1 8990
                  jcliadmin / ${JCLI_PW}

  Admin API       ssh -p ${SSH_PORT} -L 8405:127.0.0.1:8405 ${SSH_USER}@${HOST}
                  token is ADMIN_TOKEN in ${REMOTE_DIR}/.env

  Data plane      http=${HTTP_BIND}  rest=${REST_BIND}  smpp=${SMPP_BIND}
$( [ "${OPEN_HTTP}${OPEN_REST}${OPEN_SMPP}" = "000" ] && echo "                  all closed to the internet — open with --open-http / --open-rest / --open-smpp" )

  Logs            ssh -p ${SSH_PORT} ${SSH_USER}@${HOST} 'cd ${REMOTE_DIR} && docker compose logs -f gateway'
  Stop            ssh -p ${SSH_PORT} ${SSH_USER}@${HOST} 'cd ${REMOTE_DIR} && docker compose down'

  ── Before real traffic ─────────────────────────────────────────────────────

  1. No users exist yet, so /send answers 403. Create one in the console.
  2. The stack is bound to the BUNDLED FAKE SMSC, which accepts every bind and
     ESME_ROKs every submit — it looks perfectly healthy while delivering to
     nobody. Point the connector at your real carrier before you trust it:
     edit the connector in the console, or mount a real gateway.json.
  3. Back up the Postgres volume before it holds anything you need
     (deploy/BACKUP.md). Nothing here does that for you.
  4. This is a DEMO deployment: no TLS anywhere, and the data plane is plain
     HTTP/TCP. Put a reverse proxy with TLS in front of anything you expose.

EOF

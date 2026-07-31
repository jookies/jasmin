#!/usr/bin/env bash
# Seed a running gateway with a complete, testable demo configuration.
#
#   scripts/deploy/demo-setup.sh --host 3.120.178.111 --webhook https://webhook.site/<your-uuid>
#
# Creates, through the admin console API, everything needed to exercise BOTH
# directions end to end:
#
#   gateway user       demo / demo1234        submits over HTTP
#   SMPPs bind         demoesme / demo1234    a partner binds in (transceiver)
#   termination conn.  demo-term              MT that STOPS here -> pushed to you
#   MT route  (10)     -> demo-term           so submits terminate instead of
#                                             going to the fake SMSC
#   MO webhook         demo-hook              inbound destination library entry
#   MO route  (20)     -> demo-hook           inbound -> your endpoint
#
# Idempotent: an object that already exists is left alone rather than failing
# the run, so this is safe to re-run after editing something by hand.
#
# ----------------------------------------------------------------------------
# THE ONE THING THAT WILL BITE YOU
# ----------------------------------------------------------------------------
# The two directions demand DIFFERENT things of your endpoint:
#
#   termination push -> any 2xx is success (json format)
#   MO webhook       -> 2xx AND a body that is exactly "ACK/Jasmin"
#
# webhook.site answers 2xx with its own placeholder text, so out of the box the
# termination half works and the MO half fails four times and drops the message.
# That is not a bug: the ACK contract is inherited Jasmin behaviour.
#
# FIX: open your webhook.site inbox, click Edit (top right), set the response
# body to exactly ACK/Jasmin, save. Then both directions work.
set -euo pipefail

HOST=""; WEB_PORT=8404; HTTP_PORT=1401; WEBHOOK=""
ADMIN_USER="admin"; ADMIN_PASS=""
SSH_USER="ubuntu"

DEMO_USER="demo"; DEMO_PASS="demo1234"
ESME_ID="demoesme"; ESME_PASS="demo1234"   # <=8 chars: SMPP 3.4 hard limit
TERM_CID="demo-term"; HOOK_CID="demo-hook"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$1" >&2; }
fail() { printf '\033[1;31mERROR:\033[0m %s\n' "$1" >&2; exit 1; }
ok()   { printf '   \033[1;32m✓\033[0m %s\n' "$1"; }
skip() { printf '   \033[1;33m·\033[0m %s\n' "$1"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --host)     shift; HOST="${1:?}" ;;
    --webhook)  shift; WEBHOOK="${1:?}" ;;
    --password) shift; ADMIN_PASS="${1:?}" ;;
    --ssh-user) shift; SSH_USER="${1:?}" ;;
    --web-port) shift; WEB_PORT="${1:?}" ;;
    -h|--help)  awk 'NR>1 && /^#/ { sub(/^# ?/, ""); print; next } NR>1 { exit }' "${BASH_SOURCE[0]}"; exit 0 ;;
    *)          fail "unknown option '$1'" ;;
  esac
  shift
done

[ -n "${HOST}" ]    || fail "usage: $0 --host <ip> --webhook <url> [--password <admin pw>]"
[ -n "${WEBHOOK}" ] || fail "--webhook is required (your endpoint; e.g. a webhook.site URL)"
command -v curl >/dev/null 2>&1 || fail "curl not found."
command -v python3 >/dev/null 2>&1 || fail "python3 not found."

# The admin password is read off the box rather than pasted on a command line,
# where `ps` would expose it to every other user on this machine.
if [ -z "${ADMIN_PASS}" ]; then
  info "Reading the admin password from ${SSH_USER}@${HOST}:/opt/synevyr/.env ..."
  ADMIN_PASS="$(ssh -o ConnectTimeout=10 "${SSH_USER}@${HOST}" \
    'sudo grep "^ADMIN_WEB_PASSWORD=" /opt/synevyr/.env' 2>/dev/null | cut -d= -f2)" ||
    fail "could not read the admin password; pass it with --password"
  [ -n "${ADMIN_PASS}" ] || fail "no ADMIN_WEB_PASSWORD found on the host; pass --password"
fi

API="http://${HOST}:${WEB_PORT}/api"
JAR="$(mktemp)"; trap 'rm -f "${JAR}"' EXIT

info "Logging in to ${API} ..."
CSRF="$(curl -fsS -c "${JAR}" -X POST "${API}/login" -H 'Content-Type: application/json' \
  -d "$(python3 -c 'import json,sys; print(json.dumps({"username":sys.argv[1],"password":sys.argv[2]}))' "${ADMIN_USER}" "${ADMIN_PASS}")" |
  python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))')" ||
  fail "login failed. Is the console reachable at ${API%/api} and the password right?"
[ -n "${CSRF}" ] || fail "login returned no CSRF token."
ok "authenticated as ${ADMIN_USER}"

# post <path> <json> <label>
# 2xx is created; 409/400-already-exists is treated as "already there" so the
# script stays re-runnable. Anything else is a real failure and is printed.
post() {
  local path="$1" body="$2" label="$3" out code
  out="$(curl -sS -b "${JAR}" -X POST "${API}${path}" \
        -H 'Content-Type: application/json' -H "X-CSRF-Token: ${CSRF}" \
        -d "${body}" -w $'\n%{http_code}')"
  code="$(printf '%s' "${out}" | tail -n1)"
  body="$(printf '%s' "${out}" | sed '$d')"
  case "${code}" in
    2*)   ok "${label}" ;;
    409)  skip "${label} — already exists" ;;
    400)  if printf '%s' "${body}" | grep -qi "exist\|duplicate\|already"; then
            skip "${label} — already exists"
          else
            warn "${label} → HTTP ${code}: ${body}"; return 1
          fi ;;
    *)    warn "${label} → HTTP ${code}: ${body}"; return 1 ;;
  esac
}

info "Creating the demo configuration ..."

# 1. A gateway user: who may submit over HTTP, and what billing applies.
#    The CONSOLE api takes a PLAINTEXT password and hashes it server-side; the
#    admin REST api on 8405 takes password_sha256 instead. Two surfaces, two
#    shapes -- sending the wrong one returns "password is required".
post /users "$(python3 - "$DEMO_USER" "$DEMO_PASS" <<'PYEOF'
import json,sys
print(json.dumps({"username":sys.argv[1],"password":sys.argv[2]}))
PYEOF
)" "gateway user ${DEMO_USER}" || true

# 2. An SMPPs bind account: a partner connecting IN as a transceiver.
#    ip_whitelist is left at the default here because this is a demo; on
#    anything real, set it to the partner's source address — an 8-character
#    password (the SMPP 3.4 ceiling) is thin protection on a public port.
post /smpps-users "$(python3 - "$ESME_ID" "$ESME_PASS" <<'PY'
import json,sys
print(json.dumps({"system_id":sys.argv[1],"password":sys.argv[2]}))
PY
)" "SMPPs bind ${ESME_ID} (transceiver)" || true

# 3. The termination connector: MT traffic that stops here and is pushed to you.
#    verdict static accepts everything, which is what you want while testing;
#    redis-window is the production gate.
post /termination-connectors "$(python3 - "$TERM_CID" "$WEBHOOK" <<'PY'
import json,sys
print(json.dumps({"cid":sys.argv[1],
 "verdict":{"source":"static"},
 "delivery":{"endpoint":sys.argv[2],"format":"json","max_attempts":3,
             "backoff":10000000000,"backoff_cap":60000000000},
 "receipt_delay":1000000000}))
PY
)" "termination connector ${TERM_CID} -> webhook" || true

# Creating a connector does NOT start it: the admin plane separates "this
# object exists" from "this process is consuming its queue", so that a
# half-configured connector cannot start taking traffic the moment it is saved.
# A created-but-stopped connector is invisible to routing, and a submit routed
# at one fails with "Cannot send submit_sm" rather than anything that names the
# actual cause -- so start it explicitly.
info "Starting ${TERM_CID} ..."
if curl -sS -b "${JAR}" -X POST "${API}/termination-connectors/${TERM_CID}/start" \
     -H "X-CSRF-Token: ${CSRF}" -o /dev/null -w '%{http_code}' | grep -q '^2'; then
  ok "termination connector ${TERM_CID} started"
else
  warn "could not start ${TERM_CID}; start it from the console (Termination -> Termination connectors)"
fi

# 4. MT route. Order 10 beats the config-owned default (order 0) that sends
#    everything to the bundled fake SMSC, so submits now terminate here instead.
post /routes "$(python3 - "$TERM_CID" <<'PY'
import json,sys
print(json.dumps({"order":10,"default":False,"rate":0.0,
 "connector_id":sys.argv[1],"connector_type":"term"}))
PY
)" "MT route order 10 -> ${TERM_CID}" || true

# 5. The MO webhook library entry. On its own this does nothing — a library is
#    a named registry, and the MO route below is what actually uses it.
post /http-connectors "$(python3 - "$HOOK_CID" "$WEBHOOK" <<'PY'
import json,sys
print(json.dumps({"cid":sys.argv[1],"baseurl":sys.argv[2],"method":"POST"}))
PY
)" "MO webhook ${HOOK_CID}" || true

# 6. MO route. Order 20 beats the config-owned default that points at
#    host.docker.internal, which does not exist on a VPS.
post /mo-routes "$(python3 - "$HOOK_CID" "$WEBHOOK" <<'PY'
import json,sys
print(json.dumps({"order":20,"default":False,
 "connector":{"type":"http","cid":sys.argv[1],"url":sys.argv[2],"method":"POST"}}))
PY
)" "MO route order 20 -> ${HOOK_CID}" || true

cat <<EOF

  ── Ready ───────────────────────────────────────────────────────────────────

  Send an MT that terminates here and is pushed to your webhook:

    curl "http://${HOST}:${HTTP_PORT}/send?username=${DEMO_USER}&password=${DEMO_PASS}&to=15551234567&from=DEMO&content=hello"

  Bind as the partner (transceiver) and submit:

    host ${HOST}   port 2775
    system_id ${ESME_ID}   password ${ESME_PASS}

  Read the spool back the way your application would (pull path):

    console -> Termination -> Read tokens -> create one, then
    curl -H "Authorization: Bearer <token>" "http://${HOST}:8080/messages?limit=20"

  Console: http://${HOST}:${WEB_PORT}   ${ADMIN_USER} / ${ADMIN_PASS}

  ── Before the MO half will work ────────────────────────────────────────────

  Your endpoint must answer MO callbacks with HTTP 2xx AND a body of exactly
  ACK/Jasmin. webhook.site replies with its own placeholder text, so MO will
  fail four times and drop the message while termination push works fine.

    webhook.site -> Edit (top right) -> response body: ACK/Jasmin -> save

  Termination push needs no such change: any 2xx is success.

EOF

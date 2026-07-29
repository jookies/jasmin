#!/usr/bin/env bash
# Restores a Postgres dump produced by scripts/deploy/backup.sh, and
# optionally an admin.db snapshot alongside it.
#
# DESTRUCTIVE: this drops and recreates the target database. It stops the
# gateway first so nothing writes to Postgres mid-restore. Requires explicit
# confirmation unless --yes-i-am-sure is passed (for scripted/DR use).
#
# Usage:
#   scripts/deploy/restore.sh <postgres-dump.sql.gz> [admin.db] [--yes-i-am-sure]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${REPO_ROOT}"

COMPOSE_FILE="docker-compose.prod.yml"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$1" >&2; }
fail() { printf '\033[1;31mERROR:\033[0m %s\n' "$1" >&2; exit 1; }

PG_DUMP_FILE=""
ADMIN_DB_FILE=""
CONFIRMED=0
for arg in "$@"; do
  case "${arg}" in
    --yes-i-am-sure) CONFIRMED=1 ;;
    *)
      if [ -z "${PG_DUMP_FILE}" ]; then PG_DUMP_FILE="${arg}";
      elif [ -z "${ADMIN_DB_FILE}" ]; then ADMIN_DB_FILE="${arg}";
      else fail "unexpected extra argument: ${arg}"; fi
      ;;
  esac
done

[ -n "${PG_DUMP_FILE}" ] || fail "usage: $0 <postgres-dump.sql.gz> [admin.db] [--yes-i-am-sure]"
[ -f "${PG_DUMP_FILE}" ] || fail "dump file not found: ${PG_DUMP_FILE}"
if [ -n "${ADMIN_DB_FILE}" ]; then
  [ -f "${ADMIN_DB_FILE}" ] || fail "admin.db file not found: ${ADMIN_DB_FILE}"
fi

[ -f ".env" ] || fail ".env not found in ${REPO_ROOT}."
set -a
# shellcheck disable=SC1091
source .env
set +a

DC() { docker compose -f "${COMPOSE_FILE}" "$@"; }

DB_NAME="${POSTGRES_DB:-jasmin}"
DB_USER="${POSTGRES_USER:-jasmin}"

warn "This will STOP the gateway, DROP the '${DB_NAME}' database and replace it with the contents of ${PG_DUMP_FILE}."
warn "Everything currently in Postgres (submit transactions, CDRs/billing since the backup) will be lost."
if [ -n "${ADMIN_DB_FILE}" ]; then
  warn "admin.db will also be overwritten from ${ADMIN_DB_FILE}."
fi

if [ "${CONFIRMED}" -ne 1 ]; then
  printf 'Type "restore" to continue: '
  read -r reply
  [ "${reply}" = "restore" ] || fail "aborted."
fi

info "Stopping gateway..."
DC stop gateway

info "Dropping and recreating database '${DB_NAME}'..."
DC exec -T postgres psql -U "${DB_USER}" -d postgres -c "DROP DATABASE IF EXISTS \"${DB_NAME}\";"
DC exec -T postgres psql -U "${DB_USER}" -d postgres -c "CREATE DATABASE \"${DB_NAME}\" OWNER \"${DB_USER}\";"

info "Restoring from ${PG_DUMP_FILE}..."
gunzip -c "${PG_DUMP_FILE}" | DC exec -T postgres psql -U "${DB_USER}" -d "${DB_NAME}"

if [ -n "${ADMIN_DB_FILE}" ]; then
  info "Restoring admin.db from ${ADMIN_DB_FILE}..."
  DC cp "${ADMIN_DB_FILE}" gateway:/var/lib/jasmin/admin.db
fi

info "Starting gateway..."
DC up -d gateway

info "Restore complete. Verify with: curl http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/health"

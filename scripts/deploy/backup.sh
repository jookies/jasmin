#!/usr/bin/env bash
# Backs up the two pieces of durable gateway state:
#   1. Postgres — submit transactions, CDRs/billing, HA fence. The primary
#      durability boundary; see deploy/BACKUP.md.
#   2. admin.db (SQLite, in the admindata volume) — admin-plane connectors,
#      routes and users managed through the web UI / jCli.
#
# Writes timestamped, gzip-compressed backups to $BACKUP_DIR, which defaults
# to a directory OUTSIDE this repo checkout on purpose — backups should not
# live inside a directory you might `git add`.
#
# Usage: scripts/deploy/backup.sh [BACKUP_DIR]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${REPO_ROOT}"

COMPOSE_FILE="docker-compose.prod.yml"
BACKUP_DIR="${1:-${BACKUP_DIR:-${HOME}/synevyr-gateway-backups}}"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
fail() { printf '\033[1;31mERROR:\033[0m %s\n' "$1" >&2; exit 1; }

[ -f ".env" ] || fail ".env not found in ${REPO_ROOT} — nothing to back up (has the stack been set up yet?)."
set -a
# shellcheck disable=SC1091
source .env
set +a

DC() { docker compose -f "${COMPOSE_FILE}" "$@"; }

mkdir -p "${BACKUP_DIR}"

info "Backing up postgres (database: ${POSTGRES_DB:-jasmin})..."
PG_DUMP_FILE="${BACKUP_DIR}/postgres-${TIMESTAMP}.sql.gz"
if ! DC exec -T postgres pg_dump -U "${POSTGRES_USER:-jasmin}" -d "${POSTGRES_DB:-jasmin}" | gzip > "${PG_DUMP_FILE}"; then
  rm -f "${PG_DUMP_FILE}"
  fail "pg_dump failed — is the postgres service running? (docker compose -f ${COMPOSE_FILE} ps postgres)"
fi
info "  -> ${PG_DUMP_FILE} ($(du -h "${PG_DUMP_FILE}" | cut -f1))"

info "Backing up admin.db..."
ADMIN_DB_FILE="${BACKUP_DIR}/admin-${TIMESTAMP}.db"
if DC cp gateway:/var/lib/synevyr/admin.db "${ADMIN_DB_FILE}" 2>/dev/null; then
  info "  -> ${ADMIN_DB_FILE}"
else
  info "  skipped (gateway container not running, or admin.db not yet created — fine on a fresh install)."
fi

echo
info "Backup complete. Restore with: scripts/deploy/restore.sh ${PG_DUMP_FILE} [${ADMIN_DB_FILE}]"

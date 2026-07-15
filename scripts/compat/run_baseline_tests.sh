#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
python3 "$ROOT/scripts/compat/verify_baseline_tree.py"
PROJECT=${JASMIN_COMPAT_PROJECT:-jasmin-go-compat-$$}
COMPOSE=(docker compose -p "$PROJECT" -f "$ROOT/compat/compose.yaml")

cleanup() {
  status=$?
  trap - EXIT
  if [[ "${KEEP_COMPAT_SERVICES:-0}" != "1" ]]; then
    if ! "${COMPOSE[@]}" down --volumes --remove-orphans; then
      printf 'compat cleanup failed for project %s\n' "$PROJECT" >&2
      status=1
    fi
  fi
  exit "$status"
}
trap cleanup EXIT

"${COMPOSE[@]}" up -d --wait redis rabbitmq
"${COMPOSE[@]}" build baseline-tests

if [[ $# -gt 0 ]]; then
  "${COMPOSE[@]}" run --rm baseline-tests "$@"
else
  "${COMPOSE[@]}" run --rm baseline-tests \
    coverage run --source=jasmin -m twisted.trial \
    --temp-directory=/tmp/jasmin-trial tests
fi

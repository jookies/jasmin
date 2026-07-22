#!/usr/bin/env bash
set -u
set -o pipefail

usage() {
  echo "usage: $0 <registry|outbound-a|outbound-b|control|dlr|mo|routing|smpps|pb|jcli|rest|core-ops|full> <focused|candidate|release>" >&2
}

if [ "$#" -ne 2 ]; then usage; exit 64; fi
scope=$1; mode=$2
case "$scope" in registry|outbound-a|outbound-b|control|dlr|mo|routing|smpps|pb|jcli|rest|core-ops|full) ;; *) usage; exit 64;; esac
case "$mode" in focused|candidate|release) ;; *) usage; exit 64;; esac

repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd -P) || exit 1
cd "$repo" || exit 1
matrix=${GO_MACRO_TEST_MATRIX:-spec/compatibility/GO_MACRO_TESTS.csv}
python_bin=${GO_MACRO_TEST_PYTHON:-python3}
go_bin=${GO_MACRO_TEST_GO_BIN:-go}
compose_bin=${GO_MACRO_TEST_DOCKER_BIN:-docker}
compose_file=${GO_MACRO_TEST_COMPOSE_FILE:-compat/compose.yaml}

if [ "${GO_MACRO_EVIDENCE_DIR+x}" = x ]; then
  echo "FAIL: the candidate repository runner cannot publish closure evidence; use an operator-controlled immutable/read-only executor outside the candidate trust boundary" >&2
  exit 65
fi

if [ -n "${PYTHON_PATH:-}" ] && { [ ! -f "$PYTHON_PATH" ] || [ ! -x "$PYTHON_PATH" ]; }; then
  echo "FAIL: PYTHON_PATH must name an executable Python interpreter: $PYTHON_PATH" >&2
  exit 65
fi

policy=$($python_bin - "$matrix" "$scope" "$mode" <<'PY'
import csv,json,sys
path,scope,mode=sys.argv[1:]
with open(path,newline='',encoding='utf-8') as f:
    rows=list(csv.DictReader(f))
match=[r for r in rows if r.get('scope')==scope and r.get('mode')==mode]
if len(match)!=1:
    raise SystemExit('policy row missing or duplicated')
r=match[0]
print(json.dumps({k:r.get(k,'') for k in ('requires_services','commands','required_tests','cross_macro_gates')},separators=(',',':')))
PY
) || { echo "FAIL: cannot read macro test policy for $scope/$mode" >&2; exit 65; }

field() { "$python_bin" -c 'import json,sys; print(json.loads(sys.argv[1])[sys.argv[2]])' "$policy" "$1"; }
services=$(field requires_services) || exit 65
commands=$(field commands) || exit 65
required_tests=$(field required_tests) || exit 65
cross_macro_gates=$(field cross_macro_gates) || exit 65

if [ -z "$commands" ] || [ -z "$required_tests" ]; then
  echo "FAIL: $scope/$mode is not yet configured; no production suite mapping exists" >&2
  if [ "$services" = "postgres-rabbitmq-redis-smsc" ]; then
    echo "Required PostgreSQL and SMSC simulator services are absent from compat/compose.yaml; add them in a later wave before enabling this mode." >&2
  fi
  exit 78
fi
if [ "$scope" != registry ] && { [ -z "${PYTHON_PATH:-}" ] || [ ! -x "$PYTHON_PATH" ]; }; then
  echo "FAIL: configured non-registry scopes require executable PYTHON_PATH" >&2
  exit 65
fi

project="jasmin-go-${scope//[^a-zA-Z0-9]/-}-${mode}-$$"
started=0
active_pid=""
active_pgid=""
in_cleanup=0
output=$(mktemp "${TMPDIR:-/tmp}/jasmin-go-macro.XXXXXX") || exit 1
terminate_active() {
  signal=$1
  if [ -z "$active_pid" ]; then
    return 0
  fi
  target=${active_pgid:-$active_pid}
  kill -s "$signal" -- "-$target" 2>/dev/null || kill -s "$signal" "$active_pid" 2>/dev/null || true
  attempts=0
  while [ "$attempts" -lt 20 ] && kill -0 -- "-$target" 2>/dev/null; do
    sleep 0.1
    attempts=$((attempts + 1))
  done
  if kill -0 -- "-$target" 2>/dev/null; then
    kill -KILL -- "-$target" 2>/dev/null || kill -KILL "$active_pid" 2>/dev/null || true
  fi
  wait "$active_pid" 2>/dev/null || true
  active_pid=""
  active_pgid=""
}
run_tracked() {
  "$@" &
  active_pid=$!
  active_pgid=$(ps -o pgid= -p "$active_pid" | tr -d ' ')
  [ -n "$active_pgid" ] || active_pgid=$active_pid
  wait "$active_pid"
  tracked_rc=$?
  active_pid=""
  active_pgid=""
  return "$tracked_rc"
}
cleanup() {
  exit_status=$?
  trap - EXIT
  in_cleanup=1
  terminate_active TERM
  if [ "$started" -eq 1 ]; then
    run_tracked "$compose_bin" compose -p "$project" -f "$compose_file" down --volumes --remove-orphans >/dev/null 2>&1 || {
      cleanup_rc=$?
      [ "$exit_status" -ne 0 ] || exit_status=$cleanup_rc
    }
  fi
  rm -f "$output"
  exit "$exit_status"
}
on_signal() {
  signal=$1; code=$2
  trap - "$signal"
  terminate_active "$signal"
  if [ "$in_cleanup" -eq 1 ]; then
    trap - EXIT
    rm -f "$output"
  fi
  exit "$code"
}
trap cleanup EXIT
trap 'on_signal INT 130' INT
trap 'on_signal TERM 143' TERM

set -m

if [ "$services" = "rabbitmq-redis" ] || [ "$services" = "postgres-rabbitmq-redis-smsc" ]; then
  run_tracked "$compose_bin" compose -p "$project" -f "$compose_file" config --quiet || exit $?
  started=1
  if [ "$services" = "postgres-rabbitmq-redis-smsc" ]; then
    run_tracked "$compose_bin" compose -p "$project" -f "$compose_file" up -d --wait --wait-timeout 120 rabbitmq redis postgres || exit $?
  else
    run_tracked "$compose_bin" compose -p "$project" -f "$compose_file" up -d --wait --wait-timeout 120 rabbitmq redis || exit $?
  fi
  rabbit_port=$($compose_bin compose -p "$project" -f "$compose_file" port rabbitmq 5672 | "$python_bin" -c 'import sys; print(sys.stdin.read().strip().rsplit(":",1)[-1])') || exit 1
  redis_port=$($compose_bin compose -p "$project" -f "$compose_file" port rabbitmq 6379 | "$python_bin" -c 'import sys; print(sys.stdin.read().strip().rsplit(":",1)[-1])') || exit 1
  export AMQP_URL="amqp://guest:guest@127.0.0.1:$rabbit_port/"
  export REDIS_URL="redis://127.0.0.1:$redis_port/0"
  if [ "$services" = "postgres-rabbitmq-redis-smsc" ]; then
    postgres_port=$($compose_bin compose -p "$project" -f "$compose_file" port rabbitmq 5432 | "$python_bin" -c 'import sys; print(sys.stdin.read().strip().rsplit(":",1)[-1])') || exit 1
    export TEST_POSTGRES_DSN="postgres://jasmin:jasmin@127.0.0.1:$postgres_port/jasmin?sslmode=disable"
  fi
elif [ "$services" != "none" ]; then
  echo "FAIL: unsupported or unavailable service set '$services'" >&2
  exit 78
fi

# The command language is closed: CSV can select only these exact tools/prefixes.
run_gate() {
  gate=$1
  case "$gate" in
    "python3 scripts/compat/validate_contract_registry.py") "$python_bin" scripts/compat/validate_contract_registry.py ;;
    "python3 scripts/compat/run_python_unittest_json.py scripts.compat.test_validate_contract_registry") "$python_bin" scripts/compat/run_python_unittest_json.py scripts.compat.test_validate_contract_registry ;;
    "python3 scripts/build_test_manifest.py --check") "$python_bin" scripts/build_test_manifest.py --check ;;
    "python3 scripts/compat/verify_fixtures.py") "$python_bin" scripts/compat/verify_fixtures.py ;;
    "go test -json "*)
      args=${gate#go test -json }
      old_ifs=$IFS; IFS=' '; set -- $args; IFS=$old_ifs
      "$go_bin" test -json "$@"
      ;;
    *) echo "FAIL: policy contains unallowlisted command: $gate" >&2; return 78 ;;
  esac
}

old_ifs=$IFS; IFS=';'; set -- $commands; IFS=$old_ifs
for gate in "$@"; do
  echo "+ $gate" | tee -a "$output"
  (
    set -o pipefail
    run_gate "$gate" 2>&1 | tee -a "$output"
  ) &
  active_pid=$!
  active_pgid=$(ps -o pgid= -p "$active_pid" | tr -d ' ')
  [ -n "$active_pgid" ] || active_pgid=$active_pid
  wait "$active_pid"
  rc=$?
  active_pid=""
  active_pgid=""
  [ "$rc" -eq 0 ] || exit "$rc"
done

results=$($python_bin - "$output" "$required_tests" <<'PY'
import json,sys
from pathlib import Path
from scripts.compat.contract_registry import parse_framework_output
result=parse_framework_output(Path(sys.argv[1]).read_bytes(), [x for x in sys.argv[2].split(';') if x])
print(json.dumps(result,separators=(',',':'),sort_keys=True))
PY
) || { echo "FAIL: test output is not valid successful framework evidence" >&2; exit 1; }
tests=$("$python_bin" -c 'import json,sys; print(json.loads(sys.argv[1])["tests"])' "$results") || exit 1
skipped=$("$python_bin" -c 'import json,sys; print(json.loads(sys.argv[1])["skipped"])' "$results") || exit 1
failed=$("$python_bin" -c 'import json,sys; print(json.loads(sys.argv[1])["failed"])' "$results") || exit 1
if [ "$tests" -le 0 ] || [ "$skipped" -ne 0 ] || [ "$failed" -ne 0 ]; then
  echo "FAIL: framework results require positive tests and zero skipped/failed" >&2
  exit 1
fi
digest=$(shasum -a 256 "$output" | cut -d ' ' -f 1)

printf '{"scope":"%s","mode":"%s","tests":%d,"skipped":%d,"failed":%d,"output_sha256":"%s"}\n' "$scope" "$mode" "$tests" "$skipped" "$failed" "$digest"

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
evidence_target=${GO_MACRO_EVIDENCE_DIR:-}
attest_private=${GO_MACRO_ATTEST_PRIVATE_KEY:-}
attest_public=${GO_MACRO_ATTEST_PUBLIC_KEY:-}

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

if [ -n "$evidence_target" ]; then
  evidence_target=$($python_bin -c 'import os,sys; print(os.path.abspath(sys.argv[1]))' "$evidence_target") || exit 65
  case "$evidence_target" in "$repo"|"$repo"/*) echo "FAIL: evidence directory must be outside repository" >&2; exit 65;; esac
  if [ -e "$evidence_target" ]; then
    echo "FAIL: evidence output directory must not already exist" >&2
    exit 65
  fi
  if [ -z "$attest_private" ] || [ ! -f "$attest_private" ] || [ -L "$attest_private" ] \
      || [ -z "$attest_public" ] || [ ! -f "$attest_public" ] || [ -L "$attest_public" ]; then
    echo "FAIL: GO_MACRO_ATTEST_PRIVATE_KEY and GO_MACRO_ATTEST_PUBLIC_KEY must name trusted regular files" >&2
    exit 65
  fi
  derived_public=$(mktemp "${TMPDIR:-/tmp}/jasmin-go-attestor.XXXXXX") || exit 1
  if ! openssl pkey -in "$attest_private" -pubout -out "$derived_public" >/dev/null 2>&1 \
      || ! cmp -s "$derived_public" "$attest_public"; then
    rm -f "$derived_public"
    echo "FAIL: attestation private key does not match externally supplied trust anchor" >&2
    exit 65
  fi
  rm -f "$derived_public"
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

if [ "$services" = "rabbitmq-redis" ]; then
  run_tracked "$compose_bin" compose -p "$project" -f "$compose_file" config --quiet || exit $?
  started=1
  run_tracked "$compose_bin" compose -p "$project" -f "$compose_file" up -d rabbitmq redis || exit $?
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
set +m

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

if [ -n "$evidence_target" ]; then
  parent=$(dirname "$evidence_target")
  mkdir -p "$parent" || exit 1
  stage=$(mktemp -d "$parent/.jasmin-go-evidence.XXXXXX") || exit 1
  out_name="$scope-$mode.out"
  json_name="$scope-$mode.json"
  cp "$output" "$stage/$out_name" || { rm -rf "$stage"; exit 1; }
  : > "$stage/.runner-origin"
  head=$(git rev-parse HEAD) || { rm -rf "$stage"; exit 1; }
  tree=$(git write-tree) || { rm -rf "$stage"; exit 1; }
  "$python_bin" - "$stage/$json_name" "$head" "$tree" "$scope" "$mode" "$commands" "$required_tests" "$cross_macro_gates" "$results" "$out_name" "$digest" <<'PY'
import json,sys
path,head,tree,scope,mode,commands,required,gates,results,out_name,digest=sys.argv[1:]
data={"schema_version":"candidate-evidence-v1","commit_sha":head,"tree_hash":tree,"scope":scope,"mode":mode,
      "commands":commands.split(';'),"required_tests":required.split(';'),"cross_macro_gates":[x for x in gates.split(';') if x],
      "results":json.loads(results),"output_file":out_name,"output_sha256":digest}
with open(path,'x',encoding='utf-8',newline='\n') as f: json.dump(data,f,sort_keys=True,separators=(',',':')); f.write('\n')
PY
  [ "$?" -eq 0 ] || { rm -rf "$stage"; exit 1; }
  openssl dgst -sha256 -sign "$attest_private" -out "$stage/$json_name.sig" "$stage/$json_name" \
    || { rm -rf "$stage"; exit 1; }
  "$python_bin" scripts/compat/validate_contract_registry.py \
    --evidence-dir "$stage" --scope "$scope" --mode "$mode" \
    --trusted-public-key "$attest_public" >/dev/null \
    || { rm -rf "$stage"; exit 1; }
  mv "$stage" "$evidence_target" || { rm -rf "$stage"; exit 1; }
fi

printf '{"scope":"%s","mode":"%s","tests":%d,"skipped":%d,"failed":%d,"output_sha256":"%s"}\n' "$scope" "$mode" "$tests" "$skipped" "$failed" "$digest"

#!/usr/bin/env bash
# Remove the Python / Jasmin-parity surface, leaving a pure Go SMPP gateway.
#
# Recovery: `git tag pre-python-purge` is pushed. To undo everything:
#   git reset --hard pre-python-purge
#
# Run from the repo root on branch chore/python-purge:
#   bash scripts/purge-python.sh
#
# It only stages deletions. It does NOT commit, and it does NOT push, so you can
# inspect `git status` and `go build ./...` before deciding.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

if [ "$(git rev-parse --abbrev-ref HEAD)" = "master" ]; then
  echo "refusing to run on master; switch to chore/python-purge" >&2
  exit 1
fi

echo "== 1. The frozen Jasmin oracle and its test suite =="
# 201 hash-frozen files plus the upstream Twisted test suite. Deleting these is
# what makes the 108 differential test functions unrunnable (see step 5).
git rm -rq --ignore-unmatch jasmin tests

echo "== 2. The PB compatibility surface =="
# The Python/Twisted sidecar imports 8 modules from jasmin/, so it cannot outlive
# step 1. The Go seam it fronts goes with it. PB was already opt-in and off by
# default in the production config, and contributes zero Release A contracts.
git rm -rq --ignore-unmatch pbfacade internal/app/pbfacade
git rm -q --ignore-unmatch docker/Dockerfile.pbfacade

echo "== 3. Python packaging and the Python-era docs =="
# pyproject.toml/setup.py/requirements*.txt exist to build and install the Python
# distribution. README.rst and misc/doc are the Sphinx docs for it, and
# .readthedocs.yml publishes them.
git rm -q --ignore-unmatch setup.py pyproject.toml requirements.txt \
  requirements-test.txt README.rst .readthedocs.yml
git rm -rq --ignore-unmatch misc/doc misc/scripts

echo "== 4. Oracle tooling and the parity registry =="
# scripts/compat/* captures fixtures from, and validates parity against, the tree
# deleted in step 1. compat/ is the container that ran the upstream suite.
# spec/compatibility is the 205-contract parity registry, already descoped as a
# release gate by docs/plans/017. scripts/pickle_bridge.py is the opt-in Python
# pickle path; the native Go codec has been the default since plan 010.
git rm -rq --ignore-unmatch scripts/compat compat spec
git rm -q --ignore-unmatch scripts/pickle_bridge.py

echo "== 5. Go tests that shell out to the deleted oracle =="
# 108 of 1013 test functions (11%). Every one already t.Skip()s when PYTHON_PATH
# is unset, so they were opt-in rather than part of the default gate. They are
# differential-vs-Jasmin evidence, which docs/plans/017 replaced with SMPP 3.4
# conformance plus independent interop (scripts/interop/, which needs only the
# smpp-pdu3 pip package and therefore survives).
mapfile -t oracle_tests < <(grep -rl "PYTHON_PATH\|pythonPath" --include='*_test.go' internal/ || true)
if [ "${#oracle_tests[@]}" -gt 0 ]; then
  printf '%s\n' "${oracle_tests[@]}"
  git rm -q --ignore-unmatch "${oracle_tests[@]}"
fi

echo
echo "== staged deletions =="
git diff --cached --stat | tail -3
echo
echo "== remaining Python outside .venv-oracle =="
find . -name '*.py' -not -path './.git/*' -not -path './.venv-oracle/*' \
  -not -path './web/node_modules/*' 2>/dev/null || true
echo
echo "Next: fix the build (the pbfacade import in internal/app/gateway/runtime.go"
echo "and any picklecompat bridge references), then:"
echo "  go build ./... && go test -count=1 ./..."
echo "Nothing is committed yet. To undo: git reset --hard pre-python-purge"

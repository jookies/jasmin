# Release readiness and Python deprecation — corrected roadmap

- **Date:** 2026-07-29
- **Status:** active
- **Summary:** CI integrity is restored in Phase 0; the remaining work to a defensible cutover is four operator-facing code gaps plus frozen-oracle differential evidence for 50 Release A contracts — not gateway features.
- **Related:** [015-python-jasmin-deprecation-gate.md](015-python-jasmin-deprecation-gate.md), [../pb-facade.md](../pb-facade.md), [../STATUS.md](../STATUS.md), `spec/compatibility/`

## Context

An external audit of `go-rewrite` at commit `51a98278` reported "implementation is
strong, but the pushed branch is not currently releasable", with 1 of 5 CI jobs
passing and a set of headline readiness percentages. Every number in that audit
was independently recomputed here and confirmed. Three of its named bugs were
also confirmed with primary evidence.

Two of its conclusions needed correction, and both change the plan:

1. **The Go code was never the blocker.** The full suite passes locally —
   `PYTHON_PATH="$PWD/.venv-oracle/bin/python" go test -count=1 ./...` → exit 0,
   44/44 packages `ok`; `go build ./...` and `go vet ./internal/...` clean. All
   four CI failures reduced to **three mechanical causes**, none a product
   defect.
2. **"8 of 58 Release A" measures attestation throughput, not completeness.** Of
   54 finished contracts, only 8 are Release A. Spot-checking `INVENTORIED` rows
   (nominally "no Go work claimed") found working implementations for every one
   checked. The gap is missing frozen-oracle differentials, not missing code.

A third finding is new and constrains the deprecation goal itself: the PB
compatibility surface (roadmap #20) is served by a Python/Twisted sidecar that
installs the entire legacy `jasmin/` tree as a deployed service
(`docker/Dockerfile.pbfacade`, ports 8987/8988/8989/14000). Per
`docs/pb-facade.md`, pickle/jelly has "no safe, byte-compatible generic Go
decoder". **If PB compatibility is offered, Python runs in production
permanently, by design** — so porting the interceptor does not, on its own,
remove Python from the runtime.

Crucially, PB has **zero rows in Release A** (verified by joining
`CUTOVER_GRAPH.csv` against the matrices), so this costs nothing at cutover. It
only invalidates the claim in `docs/STATUS.md` that the interceptor decision
"would remove Python from the runtime image".

### Verified baseline (2026-07-29)

| Measure | Value | Source |
|---|---|---|
| Registry total / finished / unfinished | 205 / 54 / 151 | `validate_contract_registry.py` |
| By status | MATCH 37, GO-COMPLETE 17, GO-PARTIAL 60, INVENTORIED 91 | same |
| Release A required / finished | 58 / 8 | `CUTOVER_GRAPH.csv` join, zero drift vs matrices |
| Macro scope/mode rows executable | 21 / 39 | `GO_MACRO_TESTS.csv` |
| Local Go suite | 44/44 pass | `go test ./...` with oracle interpreter |

Release A composition — **PB and jCli contribute nothing**:
SMPP 19, AMQP/Redis 15, HTTP 14, Routing/Billing 10.
`CONFIG_OBSERVABILITY_DEPLOY` (0/33 finished) is entirely outside Release A.

## Approach

Four phases, ordered so that each unblocks the next and no phase depends on an
unmade decision.

Phase 0 restores CI integrity without laundering the frozen-oracle violation —
the baseline hash is **not** updated; the offending files are relocated instead.
Phase 1 closes the small set of genuinely missing code, all of it operator-facing
rather than message-path. Phase 2 is the long pole: producing differential
evidence for the 50 unfinished Release A contracts. Phase 3 is the G3/G4
operability and migration proof already specified in plan 015.

Key trade-off: Phase 2 is deliberately **not** front-loaded with the cheapest
wins. Filling the blank `jcli` macro rows is nearly free (19 fixtures already
replay byte-for-byte) but moves the cutover number by zero, because jCli has no
Release A rows. Effort goes to SMPP, AMQP/Redis, HTTP and Routing/Billing
differentials instead.

## Steps

### Step 1: Restore frozen-oracle integrity — DONE

- **Files:** `jasmin/pbfacade/*` → `pbfacade/*`; `jasmin/bin/pbfacaded.py` →
  `pbfacade/daemon.py`; `tests/pbfacade/test_*.py` → `pbfacade/tests/`;
  `pyproject.toml`; `docker/Dockerfile.pbfacade`;
  `scripts/compat/run_go_macro_tests.sh`; `spec/compatibility/GO_MACRO_TESTS.csv`;
  `docs/pb-facade.md`
- **Changes:** Commit `51a98278` added 6 files inside the hashed oracle tree
  (`jasmin/`, `tests/`) plus a `pyproject.toml` script entry, so
  `verify_baseline_tree.py` reported `files=207/201` and exited 1. That guard runs
  at `run_baseline_tests.sh:5` under `set -e`, and `capture_all.sh` calls that
  runner 18 times — one violation therefore failed two CI jobs. The sidecar only
  *imports* `jasmin.*`, so it relocates to a top-level `pbfacade/` package with
  `from jasmin.pbfacade.X` → `from pbfacade.X`. `pyproject.toml` restored from the
  baseline commit. Image entrypoint becomes `python -m pbfacade.daemon`. Macro
  module path `tests.pbfacade.*` → `pbfacade.tests.*` in both the closed command
  language and the CSV that must match it byte-for-byte.
- **Verify:** `python3 scripts/compat/verify_baseline_tree.py` →
  `oracle_tree=frozen files=201 sha256=8e7c1439…`, exit 0, baseline hash
  unchanged. `docker build -f docker/Dockerfile.pbfacade .` succeeds and
  `docker run --rm <img> --help` resolves all imports. PB macro green:
  `scripts/compat/run_go_macro_tests.sh pb focused` →
  `{"scope":"pb","mode":"focused","tests":30,"skipped":0,"failed":0}`.

Side benefit: because the sidecar tests left `tests/`, the frozen regression job
(`twisted.trial tests`) no longer runs our own code as if it were oracle tests.

### Step 2: Correct the stale registry assertions — DONE

- **Files:** `scripts/compat/test_validate_contract_registry.py`
- **Changes:** Three constants pinned pre-PB-promotion values: `:134` 165→151,
  `:135` 40→54, `:272` 165→151. These are an intentional drift tripwire, so they
  stay hardcoded rather than derived — only the pinned values move.
- **Verify:** `python3 -m unittest discover -s scripts/compat -p 'test_*.py'`
  passes (previously `FAILED (failures=3)`).

### Step 3: Make the jCli replay hermetic — DONE

- **Files:** `internal/app/jcli/transcript_test.go`
- **Changes:** Fixtures J-010/J-011 record
  `script python3(/tmp/jasmin-oracle-interceptor.py)`, and the frozen manager
  compiles that path at add time. The file existed only as a side effect of a
  local capture run, so the replay passed on a machine that had captured before
  and failed on every clean runner. `writeOracleInterceptorScript` now
  materializes it byte-identically to
  `capture_jcli_transcript.py::_write_interceptor_script`.
- **Verify:** `rm -f /tmp/jasmin-oracle-interceptor.py && go test
  ./internal/app/jcli/ -run TestOracleTranscripts -count=1` → ok. Deleting first
  is the point: it proves hermeticity rather than passing on a leftover.

### Step 4: Make CDR operator-reachable

- **Files:** `internal/app/outbound/runtime.go` (`CDRService()` at :469);
  new admin transport in `internal/app/admin` + route in `internal/app/adminweb`
- **Changes:** `CDRService()` has **zero callers repo-wide, including tests**;
  `cdr.Service.Get`/`.Export` are called only from
  `internal/infra/storage/sqlite_cdr_completion_test.go`. Records land in
  Postgres and can only be retrieved with direct SQL. Add an authenticated
  read/export path reusing the existing session-auth BFF and the versioned
  cursor JSONL/CSV export already implemented in `internal/core/cdr/operations.go`.
- **Verify:** New integration test asserting an admitted submit produces a CDR
  retrievable over the authenticated route, and that an unauthenticated request
  is refused.

### Step 5: Wire the CDR reconciliation alert

- **Files:** `internal/app/outbound/runtime.go` (:140, :493);
  `internal/app/gateway/runtime.go` (:273)
- **Changes:** `CDRAlert` is declared and consumed but set in neither
  `RuntimeDependencies` literal, so it is always nil and mismatches produce only
  `logger.Error`. Supply it in the production gateway. Note `/metrics` is the
  frozen legacy-parity surface (`internal/core/stats/stats.go` pins exact metric
  names), so a CDR counter cannot be added there without a registry deviation —
  route the alert to a component logger and, if a metrics sink is wanted, raise a
  DEVIATIONS.md entry first.
- **Verify:** Test asserting an injected reconciliation mismatch invokes the
  supplied alert in the gateway-constructed runtime.

### Step 6: Replace fabricated jCli statistics

- **Files:** `internal/app/jcli/managers_stats.go`;
  `spec/compatibility/JCLI_MATRIX.md`
- **Changes:** `stats --user` returns literals for every value (`:123-141`);
  `bound_connections_count` is a hardcoded zero map (`:120-121`) even though
  `smpps.BindManager.CountByType` (`internal/core/smpps/bindmanager.go:59`) and
  `smpps.Server.BoundSystemIDs()` (`server.go:268`) already track it and the PB
  facade already serves it (`internal/app/pbfacade/handler.go:705-712`). Wire the
  available data first; leave genuinely unavailable clocks as `ND`. Then demote
  `J-014`, which is `MATCH` on transcript-shape evidence while its values are
  synthetic — the one row in the registry where `MATCH` does not mean
  behaviourally equivalent.
- **Verify:** Test binding a session and asserting `stats --user` reports the
  real bind count; registry validator passes after the status change.

### Step 7: Delete the two dead packages

- **Files:** `internal/core/router/`, `internal/core/routepolicy/`
- **Changes:** `router/logic.go:85` carries `@TODO: Implement Macro 2 dispatching`
  and rejects everything; `routepolicy/policy.go:40` returns `ErrNotImplemented`.
  Neither is in the binary graph (`go list -deps ./cmd/jasmin-go-httpapi`), live
  dispatch is `internal/app/modispatch` + `internal/core/dlr/*`, and
  `routepolicy` is *correct parity* anyway — legacy
  `jasmin/routing/Routes.py:371` itself raises `NotImplementedError`. They are the
  only things in `internal/` that read as unfinished features and they are what
  makes audits report false gaps.
- **Verify:** `go build ./... && go test -count=1 ./...` unaffected.

### Step 8: Produce Release A differential evidence

- **Files:** `spec/compatibility/fixtures/*`, the four Release A matrices,
  `GO_MACRO_TESTS.csv`
- **Changes:** The 50 unfinished contracts, by flow: outbound-mt 23, dlr 12,
  mo 7, smpps-submit 8. Promotion requires registered fixture/macro evidence —
  never code inspection or a passing unit test (plan 015 G1). The macro runner
  **already works** in focused+candidate mode for `outbound-a`, `outbound-b`,
  `dlr`, `mo`, `routing` and `smpps`, so the blocker is fixture capture, not
  runner plumbing. Blank rows that matter are the `release` modes, which depend on
  the G0 clean-candidate runner.
- **Verify:** Each promoted row runs at least one real framework test and rejects
  skips/zero-test output; registry validator passes after every edit.

### Step 9: Reconcile the knowledge base

- **Files:** `docs/STATUS.md`, `docs/plans/015-python-jasmin-deprecation-gate.md`
- **Changes:** Plan 015 is stale — it claims `3 GO-COMPLETE, 105 INVENTORIED`
  (pre-PB-promotion), inverts the macro numbers ("18 are executable and 21 have
  no command"; actually 21 executable, 18 blank), and states "CDRs are absent",
  which the code contradicts. `STATUS.md` "Left" item 3 must stop claiming the
  interceptor port removes Python from the runtime image, and record the PB
  sidecar as a permanent Python dependency for as long as PB is offered.
- **Verify:** Numbers in both documents match `validate_contract_registry.py`
  output on the day they are edited.

## End-to-end verification

Phase 0 is proven when all five CI jobs pass on a pushed branch. Locally the
equivalent set is:

```sh
python3 scripts/compat/verify_baseline_tree.py
python3 scripts/compat/validate_contract_registry.py --repo .
python3 scripts/compat/verify_fixtures.py
python3 scripts/build_test_manifest.py --check
python3 -m unittest discover -s scripts/compat -p 'test_*.py'
PYTHON_PATH="$PWD/.venv-oracle/bin/python" go test -count=1 ./...
PYTHON_PATH="$PWD/.venv-oracle/bin/python" scripts/compat/run_go_macro_tests.sh pb focused
```

The full plan is complete only when plan 015's G0–G5 are satisfied and the
maintainer records a dated deprecation decision. Phases 0–1 do not authorize any
deprecation claim.

## Rollback

Phase 0 is a pure relocation plus three assertion/test edits; `git revert` of the
Phase 0 commit restores the previous state exactly, and the frozen baseline hash
was never modified, so no oracle trust is lost either way. Phases 4–7 are additive
and independently revertible per step. Step 7 deletes only packages absent from
the binary graph, so reverting is a file restore with no behavioural coupling.

## Risks

- **Re-contamination of the frozen tree.** Anything added under `jasmin/`,
  `tests/` or `misc/config/`, or any edit to `requirements*.txt`/`pyproject.toml`,
  fails `verify_baseline_tree.py` and takes two CI jobs with it. Mitigation: the
  guard already runs first in `run_baseline_tests.sh`; `docs/pb-facade.md` now
  states the constraint explicitly. Never "fix" this by updating the baseline hash.
- **Evidence work is the schedule.** Phase 2 is 50 contracts of differential
  capture. The existing 12–18 week estimate for the formal cutover bar stands;
  nothing found here shortens it, and Phase 0 taking hours does not change that.
- **`J-014` demotion reduces a headline number.** jCli drops from 18/18 `MATCH`.
  This is correct — the current status overstates behaviour — but it will look
  like a regression in any dashboard that tracks MATCH counts.
- **PB scope is now explicit.** Keeping PB means Python in production forever.
  That is acceptable and costs nothing at cutover (PB has no Release A rows), but
  it must be stated in operator-facing material rather than discovered later.

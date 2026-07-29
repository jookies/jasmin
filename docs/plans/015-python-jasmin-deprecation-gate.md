# Python Jasmin deprecation gate and Claude overnight scope

- **Date:** 2026-07-29
- **Status:** active — **NOT READY to deprecate the Python deployment**
- **Decision owner:** project maintainer; compatibility status may change only with executable evidence
- **Related:** [007-prod-testing-readiness.md](007-prod-testing-readiness.md), [010-native-pickle-codec.md](010-native-pickle-codec.md), [014-partner-onboarding.md](014-partner-onboarding.md), `spec/compatibility/`

## Decision in one paragraph

The Go gateway is functionally strong enough for continued staging and
partitioned shadow traffic, and the Python implementation can be feature-frozen
now. It is not yet safe to tell operators that the Python deployment is
deprecated. The repository has no clean, signed release-candidate evidence;
only 8 of 58 unique Release A cutover contracts are finished, 21 of 39 macro
test rows have no executable command, CDRs are absent, formal operability
attestation is incomplete, and the current control plane is single-node SQLite.
The frozen Python tree must remain the compatibility oracle and rollback target
until the gates below pass.

## What "deprecate Python Jasmin" means

These are separate milestones and must not be collapsed:

1. **Feature freeze — allowed now.** Do not add product features to the legacy
   Python implementation. Security and critical rollback fixes remain allowed.
2. **Deployment deprecation — blocked.** New deployments default to Go and
   operators are given a dated migration notice, but the Python deployment is
   still supported as the rollback target.
3. **End of support — blocked.** Production traffic has completed the
   shadow/canary/full-cutover sequence and the agreed rollback-retention window.
4. **Source/oracle removal — explicitly out of scope.** The frozen Python source
   and fixtures are test assets. Removing Python from the runtime image is a
   separate interceptor decision and does not authorize deleting the oracle.

## Audit snapshot

Evidence collected from the current `go-rewrite` working tree on 2026-07-29:

| Area | State | Evidence / blocker |
|---|---|---|
| Core MT/MO/DLR/routing | Green for staging | Core paths and package tests pass; native pickle is the default hot path. |
| jCli | Green | 19 fixtures replay byte-for-byte; all 18 matrix rows are `MATCH`. |
| Admin API/web | Yellow-green | Broad entity coverage and a successful production frontend build, but the current implementation is an uncommitted, large working-tree change and therefore is not a candidate. |
| Billing | Yellow-red | Route/part charging, early/late billing, quota enforcement and persistence exist. Shared-group snapshots are consistent, and online admin edits preserve or exactly roll back spent state. CDRs and a release-grade prepaid/postpaid/group oracle E2E do not. |
| Compatibility registry | Red for cutover | Structural validation passes: 205 total contracts; 37 `MATCH`, 3 `GO-COMPLETE`, 60 `GO-PARTIAL`, 105 `INVENTORIED`. |
| Release A graph | Red | 58 unique required contracts: 8 finished and 50 unfinished (32 `GO-PARTIAL`, 18 `INVENTORIED`). |
| Executable macro gate | Red | 39 scope/mode rows; 18 are executable and 21 have no command or mandatory-test list. Executable coverage now includes `registry`, `outbound-a`, `outbound-b`, `control`, `dlr`, `mo`, `routing`, and `smpps` in the modes recorded by `GO_MACRO_TESTS.csv`. |
| Candidate evidence | Red | The attested runner requires a clean commit/tree and an external signing key. The current worktree is intentionally dirty and has no candidate evidence. |
| Runtime Python dependency | Yellow | The message hot path is Go-only. `docker/Dockerfile.gateway` still uses `python:3.12-slim` for `interceptor_runner.py` and its healthcheck. |
| Operations | Yellow | Health/TLS/secrets/durable AMQP and the named SMPPc/SMPPs/router/HTTP/DLR/AMQP/thrower component loggers exist. Strict log-oracle promotion, metrics/alerts/runbooks, and formal shadow/canary/rollback evidence remain incomplete. |
| HA/control plane | Red if HA is required | Admin state is node-local SQLite by ADR-001. A multi-node production target needs a separate approved design. |
| Partner onboarding | Backlog only | Frontend mock is allowed; backend provisioning, secrets, saga, RBAC and audit remain deliberately frozen in plan 014. |

The local full Go suite passes when the documented full oracle interpreter is
selected:

```sh
PYTHON_PATH="$PWD/.venv-oracle/bin/python" go test -count=1 ./...
```

Without `PYTHON_PATH`, the only observed failure is the pickle compatibility
test attempting to import `smpp` from the system Python. This is a test
environment failure, not a Go runtime failure, but the release runner must make
the interpreter explicit.

## Deprecation gates

Python deployment deprecation is authorized only when every applicable gate is
green or has a named, approved deviation with a rollback invariant.

### G0 — Reproducible candidate

- Candidate is one clean commit and tree; no untracked files.
- `go test -count=1 ./...`, race tests selected by policy, `go vet ./...`,
  frontend production build, source-hash guard, schema validation and
  compatibility-tool unit tests pass.
- Test commands select the full oracle interpreter explicitly.
- Candidate evidence is generated outside the repository and verified with an
  external trusted public key.

### G1 — Deployed-scope compatibility

- Every Release A row required by `CUTOVER_GRAPH.csv` is `MATCH`,
  `GO-COMPLETE`, or `APPROVED_DEVIATION`.
- A status is never promoted from code inspection or a passing unit test alone;
  it needs the fixture/macro evidence required by the registry.
- The currently blank `GO_MACRO_TESTS.csv` scope/mode rows are executable or
  explicitly removed from Release A through an approved scope change.

### G2 — Commercial correctness

- CDR ownership, fields, durability, deduplication, retention and export
  contract are approved and implemented, or CDRs are explicitly excluded from
  the first commercial scope.
- Prepaid/postpaid, user/group precedence, multipart charging, early decrement,
  late billing, insufficient balance and redelivery are differential-tested
  end to end through both HTTP and SMPPs ingress.
- Reconciliation proves no duplicate or missing charge across retries,
  reconnects and process restarts.

### G3 — Operability and security

- Required component logs, metrics and alerts exist for submit, MO, DLR,
  billing, connector/bind state and queue backlog.
- On-call runbooks cover broker/database/SMSC failure, stuck queues, billing
  mismatch and credential compromise.
- Production secrets, TLS verification, network exposure, backup/restore and
  access boundaries are reviewed.
- The team either approves single-node SQLite for the target or replaces it
  with an HA-capable control-plane design.

### G4 — Migration proof

- Side-by-side shadow run uses isolated broker/state namespaces and compares
  results without double-sending or double-charging.
- Canary ownership is partitioned by connector/user/CID so exactly one runtime
  owns side effects.
- The agreed soak window has no unexplained message loss, duplicate delivery,
  billing mismatch or unreconciled DLR.
- A rollback drill restores Python ownership and the known-good configuration
  within the agreed recovery objective.

### G5 — Deprecation and removal policy

- Migration guide, known deviations, support window and final Python-supported
  version are published.
- Python remains available as rollback for at least 30 days after full cutover
  and for at least one stable Go release.
- The Python oracle is retained even after runtime end-of-support unless a
  separate test-asset retirement decision is approved.

## Earliest responsible timeline

The existing project estimate for the formal cutover bar is **12–18 weeks**.
Given the current evidence inventory, that remains the defensible planning
range, not a promise:

- **Feature freeze:** 2026-07-28.
- **Earliest deployment deprecation window:** 2026-10-20 through 2026-12-01,
  only if G0–G4 are green.
- **Earliest end-of-support/removal window:** no earlier than January–February
  2027, after at least one stable Go release and 30–60 days of rollback
  retention.

A smaller single-node, non-commercial scope could be faster only by explicitly
changing Release A and approving deviations. It must not silently inherit the
full-replacement claim.

## Claude overnight scope

The goal for tonight is to make the cutover bar executable and leave the
existing product work safer. It is not to declare deprecation complete.

### Task 1 — Preserve and verify the current worktree

1. Read `git status --short` and treat every existing change as user-owned.
2. Do not reset, stash, clean, rewrite or discard the UI/admin work.
3. Verify:

   ```sh
   PYTHON_PATH="$PWD/.venv-oracle/bin/python" go test -count=1 ./...
   go test -race -count=1 ./internal/app/admin ./internal/app/adminweb ./internal/app/jcli
   python3 scripts/compat/validate_contract_registry.py --repo .
   python3 -m unittest discover -s scripts/compat -p 'test_*.py'
   cd web && npm run build
   ```

4. Record exact pass/fail output in the newest `docs/worklog.md` entry. Do not
   turn an environment error into a product defect.

**Acceptance:** all current user changes remain present; every failure has a
reproducible command and owner.

### Task 2 — Finish knowledge-base reconciliation

1. Reconcile plan 012 and `docs/STATUS.md` with the actual group/admin state.
2. Keep jCli rows machine-readable (`MATCH` in the status column; fixture detail
   belongs in prose/coverage, not in the status cell).
3. Run the registry validator after every matrix/ownership edit.
4. Do not promote any non-jCli compatibility row without new executable
   evidence.

**Acceptance:** registry validation and compatibility-tool tests pass; no stale
claim says jCli or groups are still unimplemented.

### Task 3 — Make the next cutover macros executable

Populate focused and candidate commands plus package-qualified mandatory tests
for the highest-risk currently blank scopes, in this order:

1. `smpps`
2. `routing`
3. `mo`
4. `control`

Use existing tests first. Add only the smallest missing integration test needed
to prove an edge. Commands must run through
`scripts/compat/run_go_macro_tests.sh`; candidate evidence must not be
fabricated on a dirty tree.

**Acceptance:** each completed row runs at least one real framework test,
rejects skips/zero-test output, and keeps cross-macro prerequisites intact.

### Task 4 — Close the commercial decision, not the whole billing system

Write an ADR/spec for the first production CDR contract:

- event identity and dedupe key;
- submit, SMSC response, DLR and terminal-failure states;
- user/group/route/connector/message/part identifiers;
- amount/currency/rate and prepaid/postpaid semantics;
- storage, retry, retention, export and reconciliation;
- privacy/redaction rules.

Map the spec to B-001…B-011 and name the minimum oracle fixtures. Implement code
only if the contract is unambiguous and the change is bounded; otherwise stop
at an approved decision-ready document.

**Acceptance:** no commercial cutover can occur without either the implemented
CDR contract or a named approved exclusion.

### Task 5 — Produce the next readiness delta

Add a concise worklog section with:

- commands run and results;
- registry and Release A counts before/after;
- newly executable macro scopes;
- remaining blockers ordered by cutover risk;
- whether the 12–18 week estimate changed, with evidence.

## Overnight guardrails

- Do not delete or "clean up" the frozen Python source, fixtures, bridge
  differential code or oracle environment.
- Do not claim runtime Python-free status while Python interceptors are
  supported.
- Do not implement the partner-onboarding backend; plan 014 permits the
  frontend mock only.
- Do not change public port exposure, real partner/SMSC credentials, production
  secrets or deployment ownership.
- Do not add a distributed database migration without an approved HA ADR.
- Do not mark a compatibility row finished because the implementation looks
  complete; produce the registered evidence.
- Do not sign candidate evidence with a key stored in the repository.

## Exit criteria for this plan

This plan is complete only when G0–G5 are satisfied and the maintainer records a
dated deprecation decision. Completing tonight's tasks advances the gate; it
does not complete it.

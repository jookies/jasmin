# Contract ID decisions — Wave 0

## B-008 — Python float operation order and visible amounts

`B-008` is a distinct public billing contract, not an alias of a historical
`Q-008`. It is inserted immediately after `B-007` in
`ROUTING_BILLING_MATRIX.md`, owned by MS-1 / Task 1.4, and starts as
`INVENTORIED`.

The required future fixture must cover exact IEEE-754 result bits, externally
visible values, split early/late arithmetic, equality boundaries, and adjacent
ULP quota boundaries. Wave 0 does **not** claim fixture coverage for `B-008`.
The registry therefore has 205 dynamic rows, 184 unfinished rows, and an MS-1
primary partition of 39.

## Coverage-based status corrections

The following rows had `GO-PARTIAL` labels but no executable mapping in
`FIXTURE_COVERAGE.csv`: `A-010`, `H-010`, `H-011`, `RT-003`, `RR-010`, and
`RI-001` through `RI-006`. They are downgraded to `INVENTORIED`, exactly and
only, because prose, helper code, or an isolated implementation is not an
executable differential assertion for the full row contract. Existing code is
not removed and no regression claim is made. Promotion requires the normal
fixture/schema/coverage/production-path evidence.

## Evidence admission

A normal invocation of `validate_contract_registry.py` is structural-only. It
proves matrix, ownership, fixture registry, macro test policy, and cutover
manifest consistency, but does not claim any macro is closed.

Supplying `--evidence-dir` opts into candidate-evidence validation against the
exact committed SHA and tree. Evidence must conform to
`CANDIDATE_EVIDENCE.schema.json`, have non-empty commands and mandatory tests,
positive test count, zero skipped/failed tests, and an output file whose SHA-256
matches the record. Every evidence JSON must have a detached RSA/SHA-256
signature verifiable by an operator/CI-supplied public key outside the candidate
repository. `CANDIDATE_EVIDENCE_ATTESTOR.pem` is a documented reference key,
not a security trust anchor: candidate evidence validation fails closed unless
`--trusted-public-key` / `GO_MACRO_ATTEST_PUBLIC_KEY` names an external regular
file. `run_go_macro_tests.sh` also refuses an unsigned run or a private key that
does not match that external trust anchor, validates the staged evidence, and
only then publishes the directory atomically. `CLOSE_MACROS` and
its detached signature are also required together. Closure requires candidate
evidence for every scope of the primary macro (MS-1 requires both `outbound-a`
and `outbound-b`) and every transitive cross-macro dependency. Stale, forged,
skipped, zero-test, package-failed, missing-output, and missing-gate evidence
fails closed.

`CONTRACT_ID_BASELINE.json` and `CUTOVER_EDGE_BASELINE.json` are append-only.
Validation compares the worktree form with tracked `HEAD` and `HEAD^` history,
so deleting a row from both its source manifest and its current baseline cannot
silently shrink the contract universe or the Release A cutover topology. A new
contract or manifest version is additive; retirement requires an explicit
future tombstone/approval mechanism rather than deletion.

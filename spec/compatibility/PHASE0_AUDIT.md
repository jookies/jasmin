# Phase 0 audit report

Date: 2026-07-14  
Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`  
Scope: compatibility inventory only; no Go implementation.

## Deterministic verification

- Test manifest regeneration: PASS.
- Test methods inventoried: 1,039 across 55 files containing tests.
- Test surfaces: 13.
- Matrix row IDs: globally unique.
- Matrix rows have reached `INVENTORIED`; none are claimed `FIXTURED` or `MATCH`.
- Go source files: 0.
- `git diff --check`: PASS.

## Independent local-model review

Two independent local critics reviewed separate evidence packets:

1. protocol/routing/billing critic (`qwen2.5-coder:7b`);
2. PB/jCli/AMQP/Redis/persistence critic (`qwen3:8b`).

The first returned a summary without falsifiable findings and was not used as evidence.

The second produced findings that were filtered against source and project decisions.

### Accepted findings

- Mixed-mode access to pickled Redis multipart state needed an explicit rule. `RD-003` and project decisions now require Python partition ownership or an allowlisted trusted Python bridge; Go never decodes pickle.

### Orchestrator-discovered gaps during review

- Added `ROUTING_BILLING_MATRIX.md` for routables, filters, routes, interceptors, billing and quota.
- Added `CONFIG_OBSERVABILITY_DEPLOY_MATRIX.md` for INI/env configuration, runtime processes, ports, metrics and deployment.

### Rejected findings

- “Add TLS to legacy jCli”: no cited source contract and no evidence that legacy jCli offers TLS.
- “Implement pickle decoding in Go”: conflicts with the accepted security boundary and is unnecessary with partition ownership/bridges.
- “Differential tests are absent”: Phase 0 inventories contracts; Phase 1 explicitly creates fixtures and differential execution.
- “MD5 migration is unspecified”: `PROJECT_DECISIONS.md` specifies legacy verification followed by Argon2id migration in modern storage.

## Residual risks carried into Phase 1

- Exact HTTP response bytes/headers and validation order still require executable fixtures.
- SMPP library viability remains unproven until wire-level spike/tests.
- PB remote method signatures and pickle class allowlists require generated method/schema manifests.
- AMQP declaration properties and failure-by-failure ACK/requeue semantics require live broker fixtures.
- Python float billing requires a dedicated differential rounding corpus.
- Existing Python integration tests have not yet been executed in this environment with RabbitMQ/Redis.

No S0/S1 issue blocks completion of the Phase 0 inventory. These residual risks block parity claims and implementation cutover, not Phase 0 documentation.

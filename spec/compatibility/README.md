# Jasmin Go compatibility specification

This directory defines the frozen behavior that the Go rewrite must preserve before optimization.

## Baseline

- Upstream: `jookies/jasmin`
- Commit: `0aac58e466d583d0f0436df7b8afa3dc96191263`
- Tag: `upstream-jasmin-0.12-0aac58e`
- License: Apache-2.0

## Documents

- `SURFACES.md` — top-level parity registry and status model.
- `HTTP_MATRIX.md` — legacy HTTP, REST, encoding and callback contracts.
- `SMPP_MATRIX.md` — SMPP client/server wire and lifecycle contracts.
- `JCLI_MATRIX.md` — Telnet jCli transcript and mutation contracts.
- `PB_API_MATRIX.md` — Twisted Perspective Broker compatibility facade scope.
- `AMQP_REDIS_MATRIX.md` — broker, transient state and profile persistence contracts.
- `ROUTING_BILLING_MATRIX.md` — routables, filters, routes, interceptors, billing and quota contracts.
- `CONFIG_OBSERVABILITY_DEPLOY_MATRIX.md` — INI/env, processes, ports, metrics and deployment contracts.
- `KNOWN_QUIRKS.md` — legacy behavior that cannot be silently fixed.
- `DEVIATIONS.md` — owner-approved deviations; currently none.
- `PHASE0_AUDIT.md` — deterministic checks, independent criticism and residual risks.
- `TEST_MANIFEST.csv` — deterministic inventory of 1,039 existing `test*` methods.
- `FIXTURE_COVERAGE.csv` — exact mapping from executable oracle cases to contract rows, with honest partial/full coverage.

## Current phase status

Phase 0 inventories the surfaces. The matrices remain the authoritative parity registry.

Phase 1 provides the frozen Python oracle harness, 27 executable golden cases
across HTTP/SMPP/AMQP/Redis, schema and semantic validation, reproducibility
checks, and an exact differential comparer. `FIXTURE_COVERAGE.csv` distinguishes
partial from full fixture coverage. These artifacts do not mark any Go surface
as `MATCH`; that requires a future Go implementation to emit identical results.

## Regenerate test inventory

```bash
python3 scripts/build_test_manifest.py
python3 scripts/build_test_manifest.py --check
```

Expected baseline result:

```text
tests=1039 files=55 surfaces=13 status=ok
```

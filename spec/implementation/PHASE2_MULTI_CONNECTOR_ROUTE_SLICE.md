# Phase 2.8 — multi-connector route policies

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.

## Included
- RandomRoundrobin MO/MT eligible connector pool and deterministic injected-index selection.
- Failover MO/MT ordered iteration, exhaustion and per-match reset.
- Empty/invalid connector rejection and Failover MO mixed-type rejection.
- Filter compatibility and MT rate; MO routes remain unrated.
- Immutable connector pools and per-selection failover cursors for race-free use.

## Excluded
Billing/quotas, connector availability probing, jCli persistence, interceptors and BestQualityMTRoute.

## Acceptance
Frozen fixtures and no-skip Go replay; reproducible corpus; schema/integrity/coverage; x20/full/race/vet/build/fuzz; frozen regression; Ralph; exact remote SHA and CI 4/4.

# Phase 2.7 — deterministic static routing tables

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.

## Goal

Port deterministic routing-table ordering and static/default route selection behind a Go domain boundary using the Phase 2.6 filter core.

## Included

- MT and MO tables.
- Descending order and first-match selection.
- Replacement at an existing order.
- Remove, flush and immutable table snapshots.
- Order 0 default-route rule and no-match behavior.
- AND composition of deterministic filters.
- Static MO/MT routes, connector type restrictions and visible MT rate.

## Excluded

- Random round-robin, failover and connector availability.
- Billing and quota mutation.
- EvalPy/interceptors.
- HTTP/SMPP protocol error mapping.
- Persistence and jCli mutation contracts.

## Design

A mutable builder mirrors legacy table mutation. `Build` emits an immutable, concurrency-safe table snapshot. Routes and connectors are immutable values; filters are the immutable Phase 2.6 implementations.

## Caps

- At most 10,000 routes per builder.
- Order must be in `[0, 2^31-1]`.
- At most 64 filters per route.
- Connector IDs retain the 1 KiB compatibility cap.
- Rates must be finite, non-negative float64 values.

## Gates

Frozen operational fixtures; generic no-skip Go replay; schema/integrity/coverage/reproducibility; x20/full/race/vet/build/fuzz; frozen upstream suite; Ralph; exact push SHA and CI 4/4.

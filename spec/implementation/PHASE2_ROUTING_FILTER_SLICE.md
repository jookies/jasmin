# Phase 2.6 — deterministic routing filter compatibility slice

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Oracle: `jasmin.routing.Filters` and `jasmin.routing.Routables`.

## Decision

Implement the deterministic filter truth table as an isolated Go domain package. This slice does not execute Python, select routes, bill messages, persist objects, or perform network I/O.

## Included

- Transparent, connector, user and group identity filters.
- Source, destination and short-message regex filters.
- Python `re.match` start-position behavior over UTF-8 replacement-decoded bytes for a documented RE2-compatible pattern subset.
- `short_message` precedence over `message_payload`; missing content returns false.
- Inclusive date and time intervals.
- Legacy reversed date/time intervals matching nothing; no midnight wrap.
- String-normalized tag membership.
- MO/MT filter compatibility metadata.
- Explicit missing-field errors where the Python oracle raises `KeyError`.

## Excluded

- EvalPy execution (`RF-011`).
- Full Python `re` syntax/semantics (look-around, backreferences, conditional groups and Python-specific anchors).
- Routable mutation/locks (`RT-002`, `RT-003`) beyond read-only fixture inputs.
- Route table ordering/selection (`RR-*`).
- Interceptors, billing, persistence and protocol error mapping.

## Frozen cases

The capture corpus must include:

1. Transparent matches for MO and MT.
2. Connector, user and group identity match/miss pairs.
3. Source/destination anchored match and miss, invalid UTF-8 replacement match and missing-field `KeyError`.
4. Short-message direct match, short-message precedence, payload fallback, missing-content false and invalid UTF-8 replacement.
5. Date/time before, inclusive boundaries, inside, after and reversed ranges.
6. Tag int/string normalization, duplicate membership and miss.
7. Exact `usedFor` compatibility list for every included filter class.

## Go API rules

- Constructors validate IDs, intervals and regex patterns before use.
- Regex evaluation is start-position-only and never panics.
- Input byte slices and tag slices are defensively copied by the immutable routable constructor.
- Missing source/destination fields return stable sentinel errors.
- Missing short-message and payload fields return `(false, nil)`.
- Match methods are deterministic and safe for concurrent callers.

## Resource caps

- Regex pattern: 4 KiB UTF-8 bytes.
- Each routable byte field: 1 MiB.
- Tags: at most 256 entries; each at most 1 KiB.
- IDs: at most 1 KiB.

## Status discipline

`RF-001` through `RF-010` and `RF-012` may move only to `GO-PARTIAL`. `RF-011`, routes, billing and mutation remain `INVENTORIED`.

## Gates

- Frozen capture reproducible byte-for-byte.
- Every fixture case executed without allowlist skips.
- Exact match/error outcome and compatibility map compared.
- Negative integrity tests reject self-consistent corpus edits.
- Package x20, full Go, race, vet, build and fuzz.
- Frozen upstream regression and all fixture gates.
- Ralph audit with no unresolved HIGH/CRITICAL finding.
- Commit/push exact SHA and GitHub CI 4/4.

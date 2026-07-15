# Phase 2.4 — Redis DLR state compatibility slice

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Fixture: `compat/fixtures/redis/baseline.json` (`4` live Redis captures).

## Decision

Implement a zero-dependency Go state boundary in `internal/state/rediscompat`. It builds and validates the three fixture-proven typed DLR/correlation hash records and can inspect the one legacy multipart pickle field as opaque metadata without storing, returning, decoding or constructing its payload.

This is a state codec and key boundary, not a live Redis adapter.

## In scope

- Immutable Redis key values and strict builders/parser for:
  - `dlr:<queue-message-id>`;
  - `queue-msgid:<normalized-smpp-message-id>`;
  - `longDeliverSm:<connector-id>:<reference>:<destination>`.
- Immutable typed hash fields represented by the fixtures: string and signed integer.
- Named constructors for:
  - HTTP DLR request state;
  - SMPP-server DLR request state;
  - normalized SMSC-ID to queue-message-ID correlation.
- Explicit positive TTL seconds and exact fixture field names/types/values.
- Legacy multipart metadata inspection: key, segment sequence, TTL, byte length, SHA-256 and passive `0x80 <protocol>` marker only.
- The multipart inspector checks a `1 MiB` cap before hashing and does not retain or expose payload bytes.
- Redis key cap `512` bytes and aggregate typed field bytes cap `64 KiB` before record construction.
- Differential tests over every committed Redis fixture plus defensive-copy, malformed-input and fuzz tests.

## Non-goals

- Redis connection pools, commands, transactions, Lua, retries or network integration.
- TTL countdown, missing/expired behavior, deletion or duplicate/reordered state transitions.
- DLR receipt-ID normalization; the queue correlation constructor accepts an already-normalized SMSC ID.
- Multipart assembly, part persistence, `HSET` ownership or Redis hash enumeration.
- Decoding, validating, constructing, translating or re-emitting Python pickle.
- SMPP PDU interpretation, DLR routing, callback delivery or billing.
- Claiming complete Redis matrix-row parity.

## Proposed API contract

- `BuildDLRKey(queueMessageID string) (Key, error)`.
- `BuildQueueMessageKey(normalizedSMSCID string) (Key, error)`.
- `BuildLegacyMultipartKey(connectorID string, reference uint32, destination string) (Key, error)`.
- `ParseKey(string) (Key, error)`.
- `NewHTTPDLRRecord(key Key, request HTTPDLRRequest) (HashRecord, error)`.
- `NewSMPPSDLRRecord(key Key, request SMPPSDLRRequest) (HashRecord, error)`.
- `NewQueueMessageCorrelation(key Key, correlation QueueMessageCorrelation) (HashRecord, error)`.
- `InspectLegacyMultipartPart(key Key, segmentSequence uint32, ttlSeconds int64, payload []byte) (LegacyMultipartMetadata, error)`.
- Mutable accessors return copies. Stable sentinels identify invalid keys, records, TTLs and oversized payloads.

## Differential acceptance

For all `4 / 4` fixture cases:

1. key kind/pattern and Redis type match the capture;
2. TTL seconds match exactly;
3. all typed records project exactly the captured field names, kinds and values;
4. no fixture can be silently skipped;
5. multipart metadata matches segment sequence, payload length, SHA-256 and pickle protocol 2;
6. multipart payload bytes are never exposed or retained;
7. mutating constructor inputs or accessor results cannot mutate stored records.

Focused rejection tests cover empty/oversized/malformed keys, colon-bearing dynamic components, wrong key kind per constructor, non-positive TTL, invalid request fields, oversized typed field payloads and multipart payloads. Fuzzing arbitrary keys and payload bytes must never panic.

## Compatibility status on completion

- RD-001: `GO-PARTIAL` for fixture-proven HTTP/SMPPS DLR hash construction only.
- RD-002: `GO-PARTIAL` for fixture-proven correlation hash construction only.
- RD-003: `GO-PARTIAL` for opaque metadata inspection only; legacy multipart ownership remains Python/bridge-only.
- RD-004 through RD-006 remain `INVENTORIED`.

## Verification gates

- RED before the production package exists.
- `go test -count=20 ./internal/state/rediscompat`.
- `go test -race ./...`, `go vet ./...`, `go build ./...`.
- At least 10 seconds of Go fuzzing.
- Frozen-oracle and fixture verification unchanged.
- Ralph audit on a stable candidate before commit.
- GitHub Actions green on the pushed commit.

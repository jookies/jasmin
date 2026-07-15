# Phase 2.3 — opaque AMQP envelope compatibility slice

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Fixture: `compat/fixtures/amqp/baseline.json` (`7` RabbitMQ publish/consume captures).

## Decision

Implement a zero-dependency Go ingress boundary in `internal/transport/amqpcompat` that receives an already-delivered AMQP routing key, properties and body; classifies the captured routing-key families; preserves properties and body bytes without aliasing; fingerprints the body; and identifies the pickle protocol marker without deserializing pickle.

This is an envelope boundary, not a RabbitMQ client or AMQP frame codec.

## In scope

- Immutable `Envelope` construction from routing key, typed properties and opaque body bytes.
- Typed fixture-proven AMQP field values: string, signed integer and byte string.
- Optional `reply-to` and priority plus required `message-id` and headers.
- Routing-key parsing for:
  - `submit.sm.<CID>`;
  - `submit.sm.resp.<UID>`;
  - `dlr.submit_sm_resp`;
  - `dlr_thrower.http` and `dlr_thrower.smpps`;
  - `bill_request.submit_sm_resp.<UID>`;
  - `deliver_sm_thrower.http` and `deliver_sm_thrower.smpps`.
- SHA-256 of the exact body bytes.
- A `1 MiB` envelope body cap checked before defensive copying.
- Header count (`128`) and aggregate header bytes (`64 KiB`) checked before the property-table copy.
- Passive legacy pickle marker detection from `0x80 <protocol>` only. Detection is metadata, never validation or deserialization.
- Differential projection of every committed AMQP fixture.
- Defensive copy tests and malformed-input/fuzz coverage.

## Non-goals

- RabbitMQ connection, exchange/queue declaration, publish or consume loops.
- AMQP wire-frame serialization or a third-party AMQP dependency.
- ACK, reject, requeue, retry, QoS, expiry, reconnect or in-flight recovery.
- Decoding, validating, constructing or translating Python pickle.
- Interpreting SMPP PDUs carried inside opaque bodies.
- Business routing, billing, DLR state transitions or HTTP/SMPP thrower behavior.
- AMQP table value kinds not represented by the seven fixtures.
- Claiming full coverage of any AMQP matrix row.

## Proposed API contract

- `NewEnvelope(routingKey string, properties Properties, body []byte) (Envelope, error)`.
- `ParseRoutingKey(string) (Route, error)`.
- `Envelope` accessors return copies for mutable values.
- `Envelope.BodySHA256() [32]byte` fingerprints the exact stored bytes.
- `Envelope.LegacyPickleProtocol() (uint8, bool)` only inspects the two-byte marker.
- `Field` has explicit string, integer and bytes constructors/accessors; callers cannot create an invalid kind/value combination.
- Errors expose stable sentinels for malformed routing keys and invalid properties.

## Differential acceptance

For all `7 / 7` fixture cases:

1. routing key parses to the expected route kind and target;
2. body bytes decoded by the test harness from `wire_base64` are preserved exactly;
3. computed SHA-256 equals fixture `wire_sha256`;
4. passive pickle protocol detection equals fixture `pickle_protocol`;
5. normalized properties project exactly into typed Go values;
6. mutating constructor inputs or accessor outputs cannot mutate the envelope.

Focused rejection tests cover empty/oversized/unknown routing keys, empty dynamic targets, oversized bodies, missing message IDs, empty header names and invalid property construction. Fuzzing arbitrary routing keys and bodies must never panic.

## Compatibility status on completion

Mark only fixture-proven portions of A-002, A-003, A-005, A-006, A-007 and A-008 as `GO-PARTIAL`. A-013 (`pickle bridge`) remains `INVENTORIED`: opaque preservation and marker detection are not a bridge.

## Verification gates

- RED before production package exists.
- `go test -count=20 ./internal/transport/amqpcompat`.
- `go test -race ./...`, `go vet ./...`, `go build ./...`.
- At least 10 seconds of Go fuzzing.
- Frozen-oracle and fixture verification unchanged.
- Ralph audit on a stable candidate before commit.
- GitHub Actions green on the pushed commit.

# Phase 2.16 — Pickle Bridge and Legacy Class Decoding

This phase implements a trusted Python sidecar bridge for decoding and encoding Python pickles (A-013, RD-003) to ensure full fidelity with legacy Jasmin data structures.

## Included
- A-013: Trusted Python sidecar for pickle decoding/encoding.
- `internal/transport/picklecompat` package for Go-side interaction.
- JSON-based bridge protocol over `stdin`/`stdout`.
- Allowlisted classes: `jasmin.routing.Routable.Routable`, `jasmin.routing.Content.Content`.
- Handling of nested PDU structures.

## Excluded
- Native Go pickle implementation.
- Support for non-allowlisted Python classes.
- High-performance binary bridge (JSON is sufficient for now).

## Production API

### package `internal/transport/picklecompat`
- `type Bridge struct { ... }`
- `func (b *Bridge) Decode(ctx context.Context, data []byte) (any, error)`
- `func (b *Bridge) Encode(ctx context.Context, obj any) ([]byte, error)`

## Tasks
1. [x] Create `scripts/pickle_bridge.py` sidecar.
2. [x] Implement Go `Bridge` client in `internal/transport/picklecompat`.
3. [x] Define Go structures matching allowlisted Python classes.
4. [x] Verification with existing AMQP fixtures.
5. [ ] Ralph audit.

## Verification
- `TestPickleBridgeRoundTrip`
- `TestAMQPFixtureDecoding`

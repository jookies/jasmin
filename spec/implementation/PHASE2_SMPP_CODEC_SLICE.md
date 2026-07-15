# Phase 2.2: SMPP PDU Wire Codec Slice

## Goal

Implement a production Go SMPP 3.4 wire-codec subset that exactly matches the seven committed SMPP golden cases without importing Python or depending on an external Go SMPP library.

This is a transport adapter. Domain packages must not depend on its wire types.

## Why this slice

- Seven executable wire fixtures already exist.
- It unlocks both future SMPP client and server paths.
- It is deterministic and can be tested without sockets, RabbitMQ, Redis, or Python.
- Extending HTTP now would require a new fixture-capture phase first.
- AMQP implementation is deferred because legacy pickle must remain behind a trusted Python bridge.

## Scope

Package: `internal/transport/smppwire`.

### Framing

- Parse and emit the 16-byte SMPP header: command length, command ID, command status, sequence number.
- Read exactly one frame from `io.Reader`, including partial reads, while leaving a following coalesced frame unread.
- Reject lengths below 16, lengths above a configured maximum, truncation, and header/body length mismatch.

### Supported PDUs

- `bind_transceiver`
- `submit_sm`
- `submit_sm_resp`
- `deliver_sm`

The codec preserves raw protocol values needed for wire fidelity: TON, NPI, `esm_class`, protocol ID, priority, delivery flags, DCS, validity/schedule C-octets, addresses, message bytes, and sequence/status values.

### Known optional parameters

- `sar_msg_ref_num` (`0x020c`)
- `sar_total_segments` (`0x020e`)
- `sar_segment_seqnum` (`0x020f`)
- `message_payload` (`0x0424`)
- `receipted_message_id` (`0x001e`)
- `message_state` (`0x0427`)

TLV lengths and fixed-width integer values are validated before access.

### Legacy compatibility quirks

No deviation is approved, so the codec must reproduce the observable golden behavior:

1. The DLR `message_payload` fixture decodes successfully but re-encoding returns a typed legacy compatibility error corresponding to the Python oracle's size mismatch.
2. Unknown vendor TLV `0x1403` is accepted during decode and omitted during re-encode, producing the committed shorter `roundtrip_wire_hex`.

## Non-goals

- SMPP session state, bind authentication, timers, TLS, reconnect, enquire-link, unbind, windowing, or socket server/client.
- Routing, billing, segmentation generation/reassembly, DLR correlation, MO dispatch, AMQP, or Redis.
- Full SMPP command/status/TLV registry.
- Preserving unknown TLVs; the frozen oracle currently drops the captured unknown TLV.
- Correcting either legacy quirk without an approved compatibility deviation.

## TDD stages

1. RED: differential tests load all seven committed SMPP cases and fail because `smppwire` does not exist.
2. GREEN framing: header/frame reader plus malformed/truncated/coalesced tests.
3. GREEN mandatory bodies: bind, submit/deliver shared body, submit response.
4. GREEN TLVs and quirks: SAR, DLR payload/receipt/state, unknown TLV drop, typed legacy error.
5. Refactor only while all differential tests remain green.
6. Verify with `go test`, repeated tests, race, vet, build, frozen-oracle guards, and immutable CI.
7. Ralph audit a stable candidate, remediate concrete findings, then commit/push and require all GitHub jobs green.

## Acceptance

- All `7 / 7` golden cases decode to the fixture's command ID/status/sequence and relevant normalized parameters.
- All five encode-direction cases produce byte-for-byte `wire_hex` and exact normal round-trip bytes.
- The DLR `message_payload` case produces the expected typed round-trip failure.
- The unknown vendor TLV case re-encodes to its committed shorter `roundtrip_wire_hex`.
- Focused tests cover partial reads, two coalesced frames, invalid short/oversized lengths, truncated frames, unterminated C-octets, malformed TLVs, and fixed-width TLV length mismatch.
- Fuzz smoke tests never panic on arbitrary frame bytes or TLV sections.
- Production code contains no fixture IDs, fixture paths, Python execution, unsafe deserialization, or external Go modules.
- The SMPP matrix is updated only for the exact implemented subset, using `GO-PARTIAL` rather than claiming full row parity.
- Local and GitHub Go gates pass, with a final post-CI LoopKey.

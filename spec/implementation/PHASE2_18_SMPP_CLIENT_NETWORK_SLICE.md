# Phase 2.18 — SMPP Client Network Connectivity and Reconnect

This phase implements real TCP connectivity for SMPP connectors, including the state machine integration with `internal/transport/smppwire` and reconnection logic (SC-003).

## Included
- SC-003: Reconnect logic (initial delay, reconnect delay, max retries/infinite).
- S-001: Framing and network integration for `internal/core/smppc`.
- Real TCP connection management in `internal/core/smppc/connector.go`.
- Automated bind on connection (Transceiver/Transmitter/Receiver).

## Excluded
- SC-004: Throughput/Pacing (deferred to Phase 2.19).
- SC-006: Error retry based on SMPP status codes (deferred).
- Persistence of active state across restarts (deferred).

## Production API
- `Connector.Start()`: Now initiates a background connection loop.
- `Connector.Stop()`: Gracefully unbinds and closes the connection.

## Tasks
1. [ ] Implement TCP connection loop in `Connector`.
2. [ ] Integrate `smppwire` for BIND PDU exchange.
3. [ ] Implement exponential backoff / linear retry for reconnection.
4. [ ] Unit tests with a mock/real SMPP server (simulated).
5. [ ] Ralph audit.

## Verification
- `TestConnectorConnectionSuccess`
- `TestConnectorReconnectLoop`
- `TestConnectorBindFailureRetry`

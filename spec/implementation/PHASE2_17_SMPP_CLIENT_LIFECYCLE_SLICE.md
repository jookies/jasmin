# Phase 2.17 — SMPP Client Foundations and Lifecycle

This phase implements the foundational management and configuration for outbound SMPP connectors (SC-001, SC-002), providing parity with legacy Jasmin `smppccm` management.

## Included
- SC-001: Configuration mapping (host, port, system_id, password, bind_type, etc).
- SC-002: Manager lifecycle (Add, Remove, List, Start, Stop).
- SC-003: Connector state machine (DISCONNECTED, CONNECTING, BOUND, UNBINDING).
- `internal/core/smppc` package.
- Unit tests for configuration and state transitions.

## Excluded
- Real network connectivity (deferred to Phase 2.18).
- Reconnect logic (SC-003) - deferred to Phase 2.18.
- Throughput/Pacing (SC-004) - deferred to Phase 2.19.
- Persistence (already handled in Phase 2.13, will be extended).

## Production API

### package `internal/core/smppc`

- `type Config struct { ... }`
- `type Manager struct { ... }`
- `type Connector struct { ... }`
- `func (m *Manager) Add(cid string, cfg Config) error`
- `func (m *Manager) Remove(cid string) error`
- `func (m *Manager) Start(cid string) error`
- `func (m *Manager) Stop(cid string) error`

## Tasks
1. [ ] Define `Config` and `Connector` structures in `internal/core/smppc`.
2. [ ] Implement `Manager` for connector registry.
3. [ ] Implement state machine transitions for `Connector`.
4. [ ] Verification with unit tests.
5. [ ] Ralph audit.

## Verification
- `TestConnectorConfigValidation`
- `TestManagerLifecycle`
- `TestConnectorStateTransitions`

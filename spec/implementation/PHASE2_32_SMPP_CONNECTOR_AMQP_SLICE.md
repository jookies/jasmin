# Phase 2.32 — SMPP Connector AMQP Consumer and Readiness

## Goal

Connect the `smppc.Connector` to the live AMQP `messaging` exchange and integrate the `ReadinessPolicy`. This slice bridges the gap between the internal connector state and the message stream, implementing the foundational logic for `SC-005` (Readiness).

## Scope

- **AMQP Consumer**: Each `Connector` instance starts a consumer for its specific `submit.sm.<CID>` routing key on the `messaging` exchange.
- **Readiness Integration**: Apply `ReadinessPolicy.Decide` to every consumed message.
- **Action Execution**:
  - `Proceed`: Placeholder for PDU submission (implemented in Phase 2.33). For now, it will log and ACK.
  - `Requeue`: Reject with `requeue=true`.
  - `Discard`: Reject with `requeue=false` (A-011/SC-005 expiration).
- **Lifecycle Wiring**: The AMQP consumer starts when the connector transitions to `BOUND` and stops on `UNBINDING` or `DISCONNECTED`.
- **Timeouts**: Implement `PDUTimeout` for the initial BIND operation to prevent hanging on silent servers.
- **Configuration**: Support `con_fail_delay` vs `con_loss_delay` distinction.

## Non-goals

- Full SMPP session management (PDU correlation, EnquireLink, etc. - Phase 2.33).
- Real PDU submission to the wire (Phase 2.33).
- Throughput Pacing (SC-004 - Phase 2.34).
- Error Retry logic (SC-006 - Phase 2.35).
- Failover or Router-side availability reporting.

## Legacy Contract (SC-005)

- **Behavior**: If the connector is not BOUND, messages are requeued until `max_age` is reached.
- **Expiration**: If `now - created_at > max_age`, the message is discarded.
- **Requeue Delay**: Requeued messages should ideally have a delay (using AMQP `x-delay` or simple requeue). Legacy uses a fixed `retry_delay`.

## Acceptance Criteria

1. `Connector` correctly starts an AMQP consumer upon reaching `StatusBound`.
2. Messages received while `StatusBound` is active are evaluated by `ReadinessPolicy`.
3. Expired messages (SC-005/A-011) are rejected without requeue.
4. Messages received while the connector is NOT ready (e.g. lost connection shortly after) are requeued.
5. `PDUTimeout` correctly cancels a BIND attempt if the server does not respond within `pdu_to` seconds.
6. Integration tests prove the consumer starts/stops correctly across connector restarts.
7. Ralph audit confirms no races in the new consumer/readiness logic.

## Affected Paths

- `internal/core/smppc/connector.go`
- `internal/core/smppc/manager.go`
- `internal/core/smppc/connector_test.go`
- `spec/compatibility/SMPP_MATRIX.md` (Update SC-005 status)

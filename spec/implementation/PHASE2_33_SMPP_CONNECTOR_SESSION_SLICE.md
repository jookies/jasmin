# Phase 2.33 — SMPP Connector Session Management and PDU Submission

## Goal

Implement the core SMPP session management within `smppc.Connector`. This includes handling the full PDU lifecycle: submitting `submit_sm` PDUs to the wire, correlating `submit_sm_resp` responses, and managing `enquire_link` keep-alives. This phase brings the connector from a simple AMQP consumer to a functional SMPP client.

## Scope

- **PDU Submission**: Send encoded `submit_sm` PDUs to the established TCP/SSL connection.
- **Response Correlation**: Maintain a window of pending requests and correlate incoming `submit_sm_resp` PDUs by `sequence_number`.
- **Enquire Link**: Implement the `enquire_link` heartbeat mechanism using `enquireLinkTimerSecs`.
- **Inactivity Handling**: Monitor connection inactivity and trigger reconnection if `inactivityTimerSecs` is exceeded.
- **Response Timeouts**: Implement `responseTimerSecs` for pending PDUs. If a response is not received, the message is requeued and the session may be reset.
- **AMQP Settlement Integration**: Connect SMPP response outcomes (success, throttled, error) to AMQP `Ack`/`Reject` side effects, integrating with the Phase 2.21 `ErrorRetryPolicy`.

## Non-goals

- Throughput Pacing (SC-004 - Phase 2.34).
- Advanced TLV injection (Macro 4).
- Failover or Router-side availability reporting (SC-007).

## Legacy Contract (S-005, SP-002, SC-006)

- **Enquire Link**: Sent every `enquireLinkTimerSecs`. Failure to receive response within `responseTimerSecs` results in connection drop.
- **Inactivity**: Any PDU received/sent resets the inactivity timer.
- **Status Codes**: Map SMPP status codes to `ErrorRetryPolicy` decisions (e.g., `ESME_RTHROTTLED` -> Requeue with delay).

## Acceptance Criteria

1. `Connector` sends `submit_sm` PDUs for messages consumed from AMQP.
2. `submit_sm_resp` PDUs are correctly correlated to the original request.
3. `enquire_link` PDUs are sent at configured intervals.
4. Session is terminated and reconnected if `inactivityTimerSecs` or `responseTimerSecs` are exceeded.
5. SMPP errors are handled according to the `ErrorRetryPolicy` (ACK/Requeue/Discard).
6. Integration tests prove successful message flow to a simulated SMPP server.
7. Ralph audit confirms correct correlation and timer management.

## Affected Paths

- `internal/core/smppc/connector.go`
- `internal/core/smppc/connector_test.go`
- `internal/core/smppc/session.go` (new)
- `spec/compatibility/SMPP_MATRIX.md` (Update S-005, SP-002 status)

LoopKey: 4ef5f977293e
Verification: 26 tests passed in internal/core/smppc (correlation, timers, settlement).
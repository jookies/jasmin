# SC-001–SC-007 Quick Reference

## SC-001: Configuration

**Mandatory:**
- `id` (3–25 alphanumeric, `-`, `_`)
- `host` (default: `127.0.0.1`)
- `port` (default: `2775`)
- `username` (max 15 chars, default: `smppclient`)
- `password` (max 16 chars, default: `password`)

**Require Restart:**
- `host`, `port`, `username`, `password`, `systemType`

**Timers (all in seconds):**
- `sessionInitTimerSecs`: 30 (bind response timeout)
- `enquireLinkTimerSecs`: 30 (ping interval)
- `inactivityTimerSecs`: 300 (max inactivity)
- `responseTimerSecs`: 120 (PDU response timeout)
- `pduReadTimerSecs`: 10 (read timeout)

**Reconnection:**
- `reconnectOnConnectionLoss`: True (default)
- `reconnectOnConnectionFailure`: True (default)
- `reconnectOnConnectionLossDelay`: 10 sec (default)
- `reconnectOnConnectionFailureDelay`: 10 sec (default)

**QoS:**
- `requeue_delay`: 120 sec (default)
- `submit_sm_throughput`: 1 per sec (default)
- `dlr_expiry`: 86400 sec (default)

---

## SC-002: Lifecycle

| Operation | State Before | State After | Side Effects |
|-----------|--------------|------------|--------------|
| **Add** | N/A | UNBOUND, service=stopped | AMQP queue created, service instantiated (not started) |
| **Start** | UNBOUND | BOUND_TRX/RX/TX, service=running | TCP/SSL connection, bind, consumer attached (prefetch=1) |
| **Stop** | BOUND_*, service=running | UNBOUND, service=stopped | Consumer detached, pending messages requeued, service stopped |
| **Remove** | Any | (deleted) | Service stopped, consumer cancelled, connector removed |
| **List** | (running/stopped) | (no change) | Returns: id, session_state, service_status, start/stop counts |

**Session States:**
- `NONE`: Not initialized
- `OPEN`: Connected, waiting for bind response
- `BOUND_TRX`: Bound as transceiver
- `BOUND_RX`: Bound as receiver
- `BOUND_TX`: Bound as transmitter
- `UNBOUND`: After unbind
- `UNBIND_PENDING`: Unbind sent

**Start Preconditions:**
- Session state must be in: [NONE, UNBOUND]
- AMQP broker connected
- Service not already running

**Stop Behavior:**
- Requeues pending messages with active timers
- Clears all listener timers
- Sets connectionRetry=False in factory

---

## SC-003: Reconnect Logic

**Triggers:**
1. Connection failure (TCP/SSL handshake fails) → `reconnectOnConnectionFailure`
2. Connection lost (socket closed) → `reconnectOnConnectionLoss`

**Delay:**
- Fixed (no exponential backoff)
- `reconnectOnConnectionFailureDelay` (default 10 sec)
- `reconnectOnConnectionLossDelay` (default 10 sec)

**Retry Strategy:**
- **No retry limit** — continues forever unless:
  - `reconnectOn*` flag set to False
  - `stopConnectionRetrying()` called (e.g., on stop)

**State During Reconnect:**
- Session state: `OPEN` (connecting) or `UNBOUND` (waiting)
- Can query via: `factory.getSessionState()`

**Stop Cancels Reconnect:**
- `perspective_connector_stop()` → `stopConnectionRetrying()` → cancels reconnect timer

---

## SC-007: Failover

**Route Types:**
- `StaticRoute`: Single connector
- `RandomRoundrobinRoute`: Random connector per message
- `FailoverRoute`: Sequential connectors, failover if unavailable

**Failover Algorithm:**
```
Router calls: route.matchFilters(msg)
  → For FailoverRoute: seq reset to -1

First thrower call: getConnector() → seq++, return connector[seq=0]
Second thrower call: getConnector() → seq++, return connector[seq=1]
...

For NEXT message: matchFilters() resets seq=-1
  → First call: getConnector() → seq++, return connector[seq=0]  (starts over!)
```

**Key: Sequence resets per MESSAGE, not per attempt.**

**Failover in Thrower:**
```
for each connector in connectors:
    if connector.getAvailableSessionCount() > 0:
        try deliver
        if success: break
    else:
        continue
if all fail: reject and requeue
```

**Availability Check:**
- `session count > 0`
- Ignores: OPEN (binding), UNBOUND (disconnected)

**Constraints:**
- FailoverMORoute: Cannot mix connector types (must be homogeneous http or smpps)
- FailoverMTRoute: All smppc (no constraint on homogeneity)
- **Billing: All connectors share same rate**

**Quirks:**
- No connector health check beyond session count
- No weight/priority ordering
- Sequence resets per message (NOT round-robin across messages)
- Session count race: could be lost between check and delivery

---

## Critical Implementation Gaps (Not in Legacy)

1. ✗ No exponential backoff (always fixed delay)
2. ✗ No retry limit (forever unless stopped)
3. ✗ No connector health monitoring
4. ✗ No weighted/prioritized failover
5. ✗ No round-robin across messages (resets per message)

---

## Testing Priorities

### High Priority (Must Match Legacy)
- Config persist/load with restart requirement enforcement
- Lifecycle state machine: Add → Start → Stop → Remove
- Reconnect: Fixed delay, no limit, cancels on stop
- Failover: Sequence reset per message, session count availability

### Medium Priority (Common Edge Cases)
- Start from UNBOUND only (reject other states)
- Requeue pending messages on stop
- Duplicate connector ID rejection on add
- Mixed connector types rejection in MO failover

### Low Priority (Nice-to-Have Validation)
- Session state enum completeness
- TLV validation rule parsing (tag, type, length, required)
- Prefetch=1 enforcement

---

## Source Code Locations (Go Rewrite Reference)

| What | Legacy File | Lines | Go Rewrite |
|------|-------------|-------|-----------|
| Config fields | `configs.py` | 32–223 | Map 1:1 to struct |
| Restart keys | `smppccm.py` | 36 | Enforce in CLI |
| Add connector | `clients.py` | 224–287 | Manager.AddConnector |
| Start connector | `clients.py` | 336–407 | Manager.StartConnector |
| Stop connector | `clients.py` | 410–466 | Manager.StopConnector |
| Reconnect | `factory.py` | 102–129 | SMPPClientFactory |
| Route selection | `RoutingTables.py` | 79–91 | RoutingTable.GetRoute |
| Failover | `Routes.py` | 262–369 | FailoverRoute |
| Thrower | `throwers.py` | 420–540 | Deliver thrower |


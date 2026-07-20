# SC-001–SC-007 Implementation Gotchas & Validation Tests

## Gotchas

### SC-001: Configuration

1. **Restart Requirement Silent Flag**
   - When user updates `host`, `port`, `username`, `password`, or `systemType` via CLI, set `PendingRestart=True` on config object
   - If connector is running, CLI must stop and restart it automatically
   - No warning if restart fails (just logs error)

2. **TLV Custom Rules Per Connector**
   - TLV validation is **per-connector**, not global
   - Each connector can have different TLV rules
   - `custom_tlvs` is empty list by default (no rules = no validation)
   - Rules don't set TLV values — they only validate; values come from HTTP/REST per-message

3. **String vs Number Keys**
   - Config keys like `host`, `username`, `password`, `addressRange`, `useSSL`, `source_addr`, `custom_tlvs` must stay as strings
   - Other keys are parsed to int/float/enum
   - CLI input parsing: call `str2num()` first, then `castInputToBuiltInType()`

4. **Data Coding Enum**
   - Valid values: 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 13, 14 only
   - NOT all 0–15; missing 11, 12, 15
   - Reject with: `UnknownValue('Invalid data_coding: %s')`

5. **Default Null Handling**
   - `None` (null) means unlimited/not set, not zero
   - E.g., `balance = None` → unlimited credit
   - E.g., `protocol_id = None` → don't include in PDU
   - Parse CLI "none" string to Python None if value is string type

6. **Bind Operation Enum**
   - Exactly three values: `transceiver`, `transmitter`, `receiver`
   - Case-sensitive (lowercase)
   - Reject anything else

7. **Session Init vs Inactivity Timers**
   - `sessionInitTimerSecs`: Timeout for BIND response (initial handshake)
   - `inactivityTimerSecs`: Max idle time after bind (keepalive timeout)
   - These are different purposes; don't confuse them

---

### SC-002: Lifecycle

1. **Add Doesn't Start**
   - `perspective_connector_add()` creates connector but does NOT start service
   - Must call `perspective_connector_start()` separately
   - Service is in `running=0` state after add

2. **Service Running Flag is 0 or 1, Not Boolean**
   - Check `if connector['service'].running == 1:` (not `== True`)
   - State is int from Twisted service framework

3. **AMQP Broker Must Be Ready**
   - Both add and start check: `if amqpBroker.connected == False: return False`
   - If broker is not connected, add/start fail silently
   - No retry; user must retry manually

4. **Prefetch Must Be 1**
   - Line 245 in clients.py: `yield self.amqpBroker.chan.basic_qos(prefetch_count=1)`
   - This must be set BEFORE any consumer attach
   - Critical for per-message throughput control

5. **Consumer Tag Reuse Prevents Duplicates**
   - Using the same consumer tag prevents duplicate consumers (issue #234)
   - On restart, first cancel old consumer tag, then create new one with same tag
   - Guarantees only one consumer per queue

6. **Stop Requeues Not Acks**
   - Pending messages with active timers are **requeued** (not ack'd)
   - This is critical for no message loss on stop
   - Requeue happens via: `basic_reject(requeue=1)` called on each pending timer

7. **Session State Enum Export**
   - Never export raw enum object (Python pickle security issue)
   - Always convert to string via `.name` attribute
   - Line 519: `return session_state.name` (not `return session_state`)

8. **Start Requires Specific Session States**
   - Line 355: `acceptedStartStates = [None, SMPPSessionStates.NONE, SMPPSessionStates.UNBOUND]`
   - Only these three states allow start
   - Reject start from any other state (OPEN, BOUND_*, UNBIND_PENDING)

9. **Remove Calls Stop Internally**
   - Line 305 in clients.py: `yield self.perspective_connector_stop(cid)` inside remove
   - But also stops service again (line 301)
   - Stopping twice is OK; second stop returns False but doesn't crash

10. **Consumer Tag Initialization**
    - After add, `consumer_tag` and `submit_sm_q` are `None`
    - Only set after start succeeds (lines 401–402)
    - Always check `if consumer_tag is not None` before canceling

---

### SC-003: Reconnect Logic

1. **No Exponential Backoff**
   - Delay is always constant: 10 sec every retry
   - Not: 10, 20, 40, 80, ...
   - This is intentional in legacy; preserve it

2. **No Retry Limit**
   - No max retry count; retries forever
   - Only stops when:
     - `reconnectOn* = False` (disabled)
     - `stopConnectionRetrying()` called (e.g., on stop)
     - Factory destroyed
   - Can lead to infinite retry if configured wrong

3. **Two Failure Types, Same Delay**
   - `connectionFailed`: TCP handshake error (e.g., host down)
   - `connectionLost`: Socket closed unexpectedly (e.g., server killed connection)
   - Both use same delay field: `reconnectOnConnectionFailureDelay` and `reconnectOnConnectionLossDelay`
   - Different delays OK; commonly both set to 10

4. **Deferred Reset on Reconnect**
   - If `connectDeferred.called == True` (previous attempt already fired), create new Deferred
   - Line 136–138 in factory.py: `if self.connectDeferred.called is True: self.connectDeferred = defer.Deferred()`
   - Allows multiple reconnect cycles in same factory instance

5. **Connect vs Bind**
   - Connection failure = TCP/SSL handshake fails
   - Bind failure = TCP OK but SMPP bind PDU rejected
   - Both trigger `reconnectOnConnectionFailure` retry
   - No distinction between the two in legacy

6. **Session State Not Guaranteed During Reconnect**
   - Between "connection lost" callback and "reconnect scheduled", state is undefined
   - Race window: socket closed → factory notified → state = ? → reactor scheduled
   - Don't rely on session state during reconnect window

7. **Reconnect Timer Cancellation**
   - Line 177–179: Check `if self.reconnectTimer and self.reconnectTimer.active():`
   - Must check both conditions: timer exists AND still active
   - If timer fired already, it's not active and can't be canceled

8. **Connection Lost vs Exit**
   - If `reconnectOnConnectionLoss = False`, connection lost → exit mode
   - Exit callback: `self.exitDeferred.callback(None)`
   - After this, factory is dead; no reconnect possible without new factory

---

### SC-007: Failover

1. **Sequence Resets Per Message, Not Per Attempt**
   - `matchFilters()` resets `seq = -1` when route is matched
   - This happens for EACH new message, not each reconnect attempt
   - **Result:** Every message tries connector[0] first (not round-robin across messages)
   - Example: Message 1 tries [0,1,2], Message 2 tries [0,1,2] (not [1,2,0])

2. **No Connector Type Mixing in MO Failover**
   - FailoverMORoute: `cannot mix http and smpps`
   - Lines 318–325 in Routes.py: Check all connectors same type
   - FailoverMTRoute: No such constraint (all smppc anyway)
   - Reject with: `InvalidRouteParameterError('FailoverMORoute cannot have mixed connector types')`

3. **Router Passes All Connectors to Thrower**
   - `route.getConnectors()` returns entire list for FailoverRoute
   - `route.getConnector()` returns single connector for StaticRoute
   - Thrower receives `routed_content.connectors` array
   - Thrower iterates, not router

4. **Availability = Session Count > 0**
   - Connector is "available" if it has at least one bound session
   - Doesn't check SMPP state machine details (OPEN, UNBOUND, etc.)
   - Session lost → immediately unavailable
   - Session created on bind complete

5. **No Failover Across Route Types**
   - If primary route is StaticRoute, secondary route (if not matched) is tried next
   - But failover within StaticRoute is NOT possible (single connector)
   - Failover only works within FailoverRoute
   - Cross-route failover is router's responsibility (route matching order)

6. **Billing Shared Across Failover**
   - All connectors in FailoverRoute share same `rate`
   - User charged once, regardless of which connector delivers
   - No per-connector override

7. **No Session State Check in Failover Decision**
   - Thrower only checks `session_count > 0`
   - Doesn't care about SMPP state (OPEN, UNBOUND, etc.)
   - Connector in OPEN state (binding) with 0 sessions → skipped
   - Could be silent failure if binding takes time

8. **Failover Iteration Stops on First Success**
   - Thrower breaks loop on successful delivery
   - Doesn't try redundant delivery to multiple connectors
   - One delivery per message through first available connector

9. **No Connector Health Monitoring**
   - No heartbeat/ping check
   - No latency-based selection
   - Only availability = session count > 0
   - Could deliver to slow/dead connector if session still exists

10. **Message Requeue on All Failover Exhaustion**
    - If all connectors in failover unavailable → reject message, requeue
    - Message goes back to queue for retry
    - No immediate re-attempt within same failover call

---

## Validation Test Cases

### SC-001 Configuration Tests

```python
# Test 1: Mandatory ID validation
assert config.id matches regex ^[A-Za-z0-9_-]{3,25}$
config.id = 'ab'  # Too short → ConfigInvalidIdError
config.id = 'a' * 30  # Too long → ConfigInvalidIdError
config.id = 'test@123'  # Invalid char → ConfigInvalidIdError
config.id = 'valid-id_123'  # OK

# Test 2: Port is integer
config.port = 2775  # OK
config.port = '2775'  # TypeError: port must be an integer
config.port = 2775.5  # TypeError: port must be an integer

# Test 3: Username/password length limits
config.username = 'x' * 15  # OK
config.username = 'x' * 16  # TypeError: username longer than allowed size (15)
config.password = 'x' * 16  # OK
config.password = 'x' * 17  # TypeError: password longer than allowed size (16)

# Test 4: Timers accept int or float
config.sessionInitTimerSecs = 30  # OK
config.sessionInitTimerSecs = 30.5  # OK
config.sessionInitTimerSecs = '30'  # TypeError: must be int or float

# Test 5: Data coding validation
config.data_coding = 0  # OK
config.data_coding = 13  # OK
config.data_coding = 12  # UnknownValue: Invalid data_coding
config.data_coding = 15  # UnknownValue: Invalid data_coding

# Test 6: Bind operation validation
config.bindOperation = 'transceiver'  # OK
config.bindOperation = 'transmitter'  # OK
config.bindOperation = 'receiver'  # OK
config.bindOperation = 'Transceiver'  # UnknownValue: invalid (case mismatch)

# Test 7: TLV custom rules validation
config.custom_tlvs = []  # OK (empty = no rules)
config.custom_tlvs = [{'tag': 0x1400, 'type': 'Int2', 'length': 4, 'required': False}]  # OK
config.custom_tlvs = [{'tag': 0x1400, 'type': 'Invalid', 'length': 4}]  # UnknownValue
config.custom_tlvs = [{'tag': 0x1400, 'type': 'Int2', 'length': -1}]  # TypeError: length must be positive
config.custom_tlvs = [{'tag': 0x1400, 'type': 'Int2', 'length': None}]  # OK (unlimited)
```

### SC-002 Lifecycle Tests

```python
# Test 1: Add creates connector in UNBOUND state
manager.perspective_connector_add(config)
connector = manager.getConnector('test_cid')
assert connector['service'].running == 0  # Not running
assert connector['consumer_tag'] is None  # No consumer yet

# Test 2: Add duplicate ID fails
manager.perspective_connector_add(config_1)
result = manager.perspective_connector_add(config_1)  # Same ID
assert result == False

# Test 3: Start from UNBOUND → BOUND_TRX
manager.perspective_connector_start('test_cid')
state = connector['service'].SMPPClientFactory.getSessionState()
assert state in [SMPPSessionStates.OPEN, SMPPSessionStates.BOUND_TRX]

# Test 4: Start from BOUND_TRX fails
manager.perspective_connector_start('test_cid')  # Already started
result = manager.perspective_connector_start('test_cid')
assert result == False  # Already running

# Test 5: Stop cancels consumer
manager.perspective_connector_stop('test_cid')
assert connector['consumer_tag'] is None
assert connector['submit_sm_q'] is None

# Test 6: Stop requeues pending messages
# (Inject timer in sm_listener.rejectTimers before stop)
manager.perspective_connector_stop('test_cid')
# Assert: messages with timers were rejected with requeue=1

# Test 7: List shows correct state
connectors = manager.perspective_connector_list()
assert len(connectors) >= 1
assert connectors[0]['id'] == 'test_cid'
assert connectors[0]['service_status'] in [0, 1]
assert connectors[0]['session_state'] in ['BOUND_TRX', 'UNBOUND', 'NONE', ...]

# Test 8: Start requires correct session state
# Set session state to UNBIND_PENDING
result = manager.perspective_connector_start('test_cid')  # Invalid state
assert result == False

# Test 9: Remove stops and deletes
manager.perspective_connector_remove('test_cid')
connector = manager.getConnector('test_cid')
assert connector is None

# Test 10: Session state export as string, not enum
state_str = manager.perspective_session_state('test_cid')
assert isinstance(state_str, str)  # Not SMPPSessionStates enum
assert state_str in ['BOUND_TRX', 'UNBOUND', 'NONE', ...]
```

### SC-003 Reconnect Tests

```python
# Test 1: Connection failure triggers reconnect
config.reconnectOnConnectionFailure = True
config.reconnectOnConnectionFailureDelay = 2
# Simulate connection failure (network unreachable)
# Assert: reconnectTimer scheduled for 2 seconds

# Test 2: Connection loss triggers reconnect
config.reconnectOnConnectionLoss = True
config.reconnectOnConnectionLossDelay = 3
# Simulate connection lost (socket closed)
# Assert: reconnectTimer scheduled for 3 seconds

# Test 3: Disabled flags prevent reconnect
config.reconnectOnConnectionFailure = False
config.reconnectOnConnectionLoss = False
# Simulate connection failure
# Assert: exitDeferred called, no reconnect

# Test 4: Stop cancels reconnect timer
config.reconnectOnConnectionFailure = True
config.reconnectOnConnectionFailureDelay = 10
# Simulate connection failure → timer scheduled
manager.perspective_connector_stop('test_cid')
# Assert: reconnectTimer canceled, connectionRetry=False

# Test 5: Session state during reconnect
# Start connector, trigger connection loss
# Assert: state transitions from BOUND_TRX → OPEN (reconnecting) → BOUND_TRX

# Test 6: No exponential backoff
config.reconnectOnConnectionFailureDelay = 5
# Simulate 3 consecutive failures
# Assert: delays are always 5 sec (not 5, 10, 20, ...)

# Test 7: Deferred reset on reconnect cycle
# Simulate failure 1: connectDeferred fired
# Simulate failure 2: new connectDeferred created before retry
# Assert: factory can handle multiple reconnect cycles
```

### SC-007 Failover Tests

```python
# Test 1: FailoverRoute sequence resets per message
route = FailoverMTRoute([filter1], [connector_a, connector_b, connector_c], rate=1.0)
route.matchFilters(message_1)  # seq = -1
c1_1 = route.getConnector()  # seq=0, returns connector_a
c1_2 = route.getConnector()  # seq=1, returns connector_b
c1_3 = route.getConnector()  # seq=2, returns connector_c
c1_4 = route.getConnector()  # seq=3, returns None (end of list)

route.matchFilters(message_2)  # seq reset = -1 (new message!)
c2_1 = route.getConnector()  # seq=0, returns connector_a (starts over)

# Test 2: Router detects failover vs static route
route_static = StaticMTRoute([filter1], connector_a, rate=1.0)
route_failover = FailoverMTRoute([filter1], [connector_a, connector_b], rate=1.0)
assert repr(route_static) == 'StaticMTRoute'
assert repr(route_failover) == 'FailoverMTRoute'

# Test 3: Router passes connectors to thrower
router.getMTRoutingTable().add(route_failover, 1)
routable = RoutableSubmitSm(pdu)
route = router.getMTRoutingTable().getRouteFor(routable)
if repr(route) == 'FailoverMTRoute':
    connectors = route.getConnectors()  # All connectors
    assert len(connectors) == 2  # [a, b]
else:
    connectors = [route.getConnector()]  # Single connector

# Test 4: Thrower iterates in order, skips unavailable
# Session counts: connector_a=0, connector_b=1, connector_c=2
# Expected: Thrower skips connector_a, tries connector_b first

# Test 5: MO failover rejects mixed types
mo_route_mixed = FailoverMORoute([filter1], [http_connector, smpps_connector])
# Assert: InvalidRouteParameterError('FailoverMORoute cannot have mixed connector types')

mo_route_homogeneous = FailoverMORoute([filter1], [http_1, http_2])
# Assert: OK (both http)

# Test 6: Billing applies to all failover connectors
route = FailoverMTRoute([filter1], [connector_a, connector_b], rate=0.50)
# User charged 0.50 regardless of whether connector_a or connector_b delivers
assert route.getRate() == 0.50

# Test 7: Thrower stops on first success
# Connector_a available and delivers successfully
# Assert: Connector_b NOT attempted (breaks after success)

# Test 8: Thrower requeues on all exhausted
# All connectors unavailable (session_count=0)
# Assert: Message rejected, requeue=1
```

---

## Edge Case Scenarios

### Restart Requirement Enforcement

**Scenario:** User updates `host` field while connector is running.
1. CLI detects `host` in `RequireRestartKeys`
2. Sets `config.PendingRestart = True`
3. Checks `connectorDetails['service_status'] == 1`
4. Calls `perspective_connector_stop(cid)` → service stops
5. Calls `perspective_connector_start(cid)` → service starts with new host
6. If start fails after 5 sec, retry once

**Go Rewrite Must Match:** Automatic restart on restart-required key update.

---

### Message Loss Prevention (Requeue on Stop)

**Scenario:** Connector has 10 pending messages with active timers when stop is called.
1. Loop through `sm_listener.rejectTimers` dictionary
2. For each msgid with active timer:
   - Cancel timer
   - Call timer's function (typically `basic_reject(requeue=1)`)
   - Remove from dict
3. Messages return to queue

**Go Rewrite Must Match:** No messages lost on stop.

---

### Reconnect Loop Prevention

**Scenario:** Network down for 1 hour, connector keeps retrying every 10 sec (360 retries).
- **Legacy behavior:** Retries forever, no backoff, no limit
- **Acceptable:** Logs every retry (can be noisy)
- **Go Rewrite:** Must preserve forever-retry semantics (user can disable via flag)

---

### Failover Session Count Race

**Scenario:** Thrower checks connector_a.session_count == 1, then attempts delivery. Meanwhile, connector_a session lost.
- **Legacy behavior:** Delivery fails, thrower moves to connector_b
- **Go Rewrite:** Must handle this gracefully (retry on next connector)


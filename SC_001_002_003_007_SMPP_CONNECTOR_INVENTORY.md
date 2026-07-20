# Legacy Jasmin SMPP Connector Contract Inventory
## SC-001, SC-002, SC-003, SC-007

**Task:** Finalize Macro 1.4 (SMPP Outbound Stability)

**Baseline Commit:** Latest (go-rewrite branch)

**Scope:**
- SC-001: SMPP Connector configuration — mandatory fields, defaults, restart requirements
- SC-002: Lifecycle operations — Add, Remove, List, Start, Stop side effects
- SC-003: Reconnect logic — delay calculation, retry limits, state reporting
- SC-007: Failover — router selection between multiple connectors for same route

---

## SC-001: SMPP Connector Configuration

### Source Files
- `jasmin/protocols/smpp/configs.py` (SMPPClientConfig class, lines 32–223)
- `jasmin/protocols/cli/smppccm.py` (CLI config mapping, lines 15–36)

### Mandatory Configuration Fields

| Field | Type | Default | Require Restart? | Source |
|-------|------|---------|------------------|--------|
| `id` (cid) | string (3–25 chars: alphanumeric, `-`, `_`) | REQUIRED | N/A | config.py:40–42 |
| `host` | string | `127.0.0.1` | **YES** | config.py:98–100 |
| `port` | integer | `2775` | **YES** | config.py:46–48 |
| `username` | string (max 15 chars) | `smppclient` | **YES** | config.py:101–103 |
| `password` | string (max 16 chars) | `password` | **YES** | config.py:104–106 |

### Optional Configuration Fields (Non-Restart)

| Field | Type | Default | Restart Required? | Description |
|-------|------|---------|-------------------|-------------|
| `systemType` | string | empty string | **YES** | SMPP system type parameter |
| `bindOperation` | enum: `transceiver`, `transmitter`, `receiver` | `transceiver` | NO | SMPP bind mode |
| `addressTon` | AddrTon enum | `UNKNOWN` | NO | TON for bind operation |
| `addressNpi` | AddrNpi enum | `UNKNOWN` | NO | NPI for bind operation |
| `source_addr_ton` | AddrTon enum | `NATIONAL` | NO | Source address TON in submit_sm |
| `source_addr_npi` | AddrNpi enum | `ISDN` | NO | Source address NPI in submit_sm |
| `dest_addr_ton` | AddrTon enum | `INTERNATIONAL` | NO | Destination address TON in submit_sm |
| `dest_addr_npi` | AddrNpi enum | `ISDN` | NO | Destination address NPI in submit_sm |
| `addressRange` | string | `None` | NO | Address range for bind |
| `source_addr` | string | `None` | NO | Default source address for submit_sm |
| `esm_class` | EsmClass | `STORE_AND_FORWARD, DEFAULT` | NO | ESM class for PDU |
| `protocol_id` | int | `None` | NO | Protocol ID for PDU |
| `priority_flag` | PriorityFlag | `LEVEL_0` | NO | Priority level |
| `validity_period` | string | `None` | NO | Validity period for PDU |
| `registered_delivery` | RegisteredDelivery | `NO_SMSC_DELIVERY_RECEIPT_REQUESTED` | NO | DLR request mode |
| `replace_if_present_flag` | ReplaceIfPresentFlag | `DO_NOT_REPLACE` | NO | Message replacement flag |
| `sm_default_msg_id` | integer | `0` | NO | Default message ID |
| `data_coding` | integer (0-14, valid: 0,1,2,3,4,5,6,7,8,9,10,13,14) | `0` (SMSC_DEFAULT_ALPHABET) | NO | Data coding scheme |

### Timer Configuration (Non-Restart)

| Field | Type | Default | Unit | Description |
|-------|------|---------|------|-------------|
| `sessionInitTimerSecs` | int/float | `30` | seconds | Timeout for bind response |
| `enquireLinkTimerSecs` | int/float | `30` | seconds | Enquire link ping interval |
| `inactivityTimerSecs` | int/float | `300` | seconds | Max inactivity before reconnect |
| `responseTimerSecs` | int/float | `120` | seconds | Timeout for any PDU response |
| `pduReadTimerSecs` | int/float | `10` | seconds | Timeout for reading single PDU |

### Reconnection Configuration (Some Require Restart)

| Field | Type | Default | Restart Required? | Description |
|-------|------|---------|-------------------|-------------|
| `reconnectOnConnectionLoss` | bool | `True` | NO | Reconnect if connection lost |
| `reconnectOnConnectionFailure` | bool | `True` | NO | Reconnect if connection fails |
| `reconnectOnConnectionLossDelay` | int/float | `10` | NO | Delay before reconnecting on loss (seconds) |
| `reconnectOnConnectionFailureDelay` | int/float | `10` | NO | Delay before reconnecting on failure (seconds) |
| `useSSL` | bool | `False` | NO | Enable SSL/TLS |
| `SSLCertificateFile` | string | `None` | NO | Path to SSL certificate file |

### QoS & DLR Configuration (Non-Restart)

| Field | Type | Default | Restart Required? | Description |
|-------|------|---------|-------------------|-------------|
| `requeue_delay` | int/float | `120` | NO | Delay before requeuing rejected message (seconds) |
| `submit_sm_throughput` | int/float | `1` | NO | Max submit_sm per second |
| `dlr_expiry` | int/float | `86400` | NO | How long DLR kept in Redis (seconds) |
| `dlr_msg_id_bases` | enum: 0, 1, 2 | `0` | NO | Message ID base conversion (0=same, 1=resp_hex/deliver_dec, 2=resp_dec/deliver_hex) |

### Custom TLV Configuration (Non-Restart)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `custom_tlvs` | list of dicts | `[]` | Per-connector TLV validation rules (no value injection) |

Each TLV entry contains:
```python
{
    'tag': int,                      # TLV tag (0x0000–0xFFFF)
    'type': str,                     # Int1, Int2, Int4, Int8, OctetString, COctetString
    'length': int or None,           # Max byte length of encoded value (None = unlimited)
    'required': bool                 # True = reject submit if missing this tag
}
```

### Logging Configuration (Non-Restart)

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `log_file` | string | `{LOG_PATH}/default-{id}.log` | Log file path |
| `log_level` | logging level | `logging.INFO` | Log level (10=DEBUG, 20=INFO, 30=WARNING, 40=ERROR, 50=CRITICAL) |
| `log_rotate` | string | `midnight` | Log rotation interval |
| `log_format` | string | `%(asctime)s %(levelname)-8s %(process)d %(message)s` | Log format |
| `log_privacy` | bool | `False` | Mask sensitive data in logs |

### Restart Requirement Enforcement

**RequireRestartKeys** (line 36, smppccm.py):
```python
RequireRestartKeys = ['host', 'port', 'username', 'password', 'systemType']
```

When any of these keys are updated, the connector's `PendingRestart` flag is set to `True`. If the connector is running, update CLI automatically stops and restarts it (smppccm.py:382–402).

### Key Validation Constraints

1. **id**: Regex `^[A-Za-z0-9_-]{3,25}$` (3–25 alphanumeric, dash, underscore)
2. **port**: Must be integer
3. **username**: Max 15 characters
4. **password**: Max 16 characters
5. **Timers**: Must be int or float, not negative
6. **data_coding**: Only 0,1,2,3,4,5,6,7,8,9,10,13,14 allowed
7. **dlr_msg_id_bases**: Only 0, 1, or 2
8. **bindOperation**: Must be `transceiver`, `transmitter`, or `receiver`
9. **TLV types**: Int1, Int2, Int4, Int8, OctetString, COctetString only
10. **TLV max_length**: Must be positive integer or `-` (unlimited)

---

## SC-002: Lifecycle Operations

### Source Files
- `jasmin/managers/clients.py` (SMPPClientManagerPB class, lines 31–650)
- `jasmin/protocols/cli/smppccm.py` (CLI management, lines 304–450)
- `jasmin/protocols/smpp/services.py` (SMPPClientService, lines 11–54)

### Operation 1: Add Connector

**Method:** `perspective_connector_add(ClientConfig)`
**Source:** clients.py:224–287
**Side Effects:**

1. **Validation** (lines 234–242):
   - Check connector ID doesn't already exist
   - Validate AMQP broker is attached
   - Check AMQP channel is connected

2. **AMQP Queue Setup** (lines 247–257):
   - Declare messaging exchange (type `topic`, no durability set)
   - Create queue `submit.sm.{cid}`
   - Bind queue to routing key `submit.sm.{cid}`

3. **Service Creation** (lines 260–269):
   - Instantiate `SMPPClientService` (factory + listener setup)
   - Instantiate `SMPPClientSMListener` with:
     - AMQP broker reference
     - Redis client reference
     - Router PB reference
     - Interceptor client reference

4. **Connector Storage** (lines 274–280):
   - Append to `self.connectors` list with:
     - `id`, `config`, `service`, `consumer_tag`, `submit_sm_q`, `sm_listener`
   - All queue refs initialized to `None`

5. **State Change** (line 285):
   - Set `self.persisted = False` (pending persistence)

6. **Return:** `True` on success, `False` on failure

**Quirks:**
- Service is NOT started automatically; must call `perspective_connector_start` separately
- Connector is created in `UNBOUND` state (not yet connected)

---

### Operation 2: Remove Connector

**Method:** `perspective_connector_remove(cid)`
**Source:** clients.py:290–318
**Side Effects:**

1. **Validation** (lines 295–298):
   - Check connector exists

2. **Stop Service** (lines 299–301):
   - If service is running, call `stopService()` immediately

3. **Stop Queue Consumer** (line 305):
   - Call `perspective_connector_stop(cid)` to cancel AMQP consumer

4. **Delete from List** (lines 307–314):
   - Remove connector from `self.connectors`

5. **State Change** (line 310):
   - Set `self.persisted = False`

6. **Return:** `True` on success, `False` on failure

**Quirks:**
- Calls `perspective_connector_stop` internally (which handles queue cleanup)
- Pending messages in rejectTimers are requeued (lines 440–451 in stop)
- Does NOT delete AMQP queue by default (only cancel consumer)

---

### Operation 3: List Connectors

**Method:** `perspective_connector_list()`
**Source:** clients.py:320–333
**Return Value:**

Array of connector details:
```python
[
    {
        'id': str,                    # Connector ID
        'session_state': str,         # SMPPSessionStates.name (e.g., 'BOUND_TRX')
        'service_status': int,        # 1=running, 0=stopped
        'start_count': int,           # Number of times started
        'stop_count': int             # Number of times stopped
    },
    ...
]
```

**Source of Details:**
- `id`: Direct
- `session_state`: `SMPPClientFactory.getSessionState().name` (factory.py:200–204)
- `service_status`: `service.running` (Twisted service flag)
- `start_count`: `service.startCounter` (service.py:13)
- `stop_count`: `service.stopCounter` (service.py:14)

**Quirks:**
- Returns only current runtime state (not persisted config)
- Session state is enum name, not numeric value (for security, line 519)

---

### Operation 4: Start Connector

**Method:** `perspective_connector_start(cid)`
**Source:** clients.py:336–407
**Side Effects:**

1. **Pre-flight Checks** (lines 342–361):
   - Connector exists
   - AMQP broker attached
   - AMQP channel connected
   - Service not already running
   - Session state in: `[None, NONE, UNBOUND]` (line 355)

2. **Start Service** (line 363):
   - Call `service.startService()`
   - This calls `SMPPClientFactory.connectAndBind()` (service.py:42)
   - Factory establishes TCP/SSL connection and binds (factory.py:157–161)

3. **Stop Existing Consumer** (lines 378–380):
   - If consumer tag already set, cancel it (handles restart edge case)

4. **Create AMQP Consumer** (lines 383–390):
   - Call `basic_consume` on queue `submit.sm.{cid}` with consumer tag `SMPPClientFactory-{cid}`
   - No-ack mode: `False` (manual acknowledgment)
   - Prefetch: 1 per consumer (line 245, set during add)

5. **Attach Callback Chain** (lines 393–395):
   - Wire up: `submit_sm_q.get() → sm_listener.submit_sm_callback → requeue/ack/reject`

6. **Store References** (lines 401–402):
   - Store consumer tag
   - Store submit_sm_q object for later iteration

7. **State Change** (line 405):
   - Set `self.persisted = False`

8. **Return:** `True` on success, `False` on failure

**Quirks:**
- Service is started BEFORE consumer is attached (line 363 before line 383)
- If start fails, consumer is not created (leftover messages remain in queue)
- Using same consumer tag prevents duplicate consumers (issue #234)
- Acceptable start states: service can be started from `NONE` or `UNBOUND` only

---

### Operation 5: Stop Connector

**Method:** `perspective_connector_stop(cid, delQueues=False)`
**Source:** clients.py:410–466
**Side Effects:**

1. **Validation** (lines 416–419):
   - Connector exists

2. **Cancel AMQP Consumer** (lines 422–429):
   - Cancel by consumer tag
   - Clear `consumer_tag` and `submit_sm_q` references

3. **Check Not Already Stopped** (lines 431–433):
   - Error if already stopped

4. **Optional Queue Deletion** (lines 435–438):
   - If `delQueues=True`, delete the `submit.sm.{cid}` queue from broker

5. **Reject & Requeue Pending Messages** (lines 442–451):
   - For each timer in `sm_listener.rejectTimers`:
     - Cancel timer
     - Call its function (typically `basic_reject(requeue=1)`)
     - This prevents message loss when connector shuts down

6. **Clear Listener Timers** (lines 454–456):
   - Call `sm_listener.clearAllTimers()`
   - Clear `submit_sm_q` reference in listener

7. **Stop Service** (line 459):
   - Call `service.stopService()`
   - Factory sets `connectionRetry = False` and cancels reconnect timer

8. **State Change** (line 464):
   - Set `self.persisted = False`

9. **Return:** `True` on success, `False` on failure

**Quirks:**
- Pending messages with active timers are requeued (not ack'd or permanently rejected)
- After stop, session state goes to `UNBOUND`
- Stop can only be called on running service (returns False if already stopped)
- `delQueues=False` by default — queue remains on broker (messages re-consumed if restarted)

---

### Operation Timing & Sequencing

**Add → Start → Stop → Remove Sequence:**
1. `Add` creates connector in `UNBOUND` state, no service running
2. `Start` connects to SMSC and binds, attaches message consumer
3. `Stop` disconnects, clears pending messages, detaches consumer
4. `Remove` deletes connector instance

**State Transitions:**
```
After Add:   UNBOUND, service.running=0
↓ Start
After Start: BOUND_TRX/RX/TX, service.running=1, consumer attached
↓ Stop
After Stop:  UNBOUND, service.running=0, consumer detached
↓ Start (again)
After Start: BOUND_TRX/RX/TX, service.running=1
↓ Remove
(Connector deleted)
```

---

## SC-003: Reconnect Logic

### Source Files
- `jasmin/protocols/smpp/factory.py` (SMPPClientFactory, lines 44–204)
- `jasmin/protocols/smpp/configs.py` (Config fields, lines 109–123)

### Reconnect Triggers

**Trigger 1: Connection Failure (TCP/SSL handshake fails)**
- **Method:** `clientConnectionFailed(connector, reason)`
- **Source:** factory.py:102–115
- **Condition:** `self.config.reconnectOnConnectionFailure and self.connectionRetry`

**Trigger 2: Connection Lost (TCP/SSL socket closed unexpectedly)**
- **Method:** `clientConnectionLost(connector, reason)`
- **Source:** factory.py:117–129
- **Condition:** `self.config.reconnectOnConnectionLoss and self.connectionRetry`

### Delay Calculation

| Trigger | Config Field | Default | Source | Unit |
|---------|--------------|---------|--------|------|
| Failure | `reconnectOnConnectionFailureDelay` | `10` | configs.py:120 | seconds |
| Loss | `reconnectOnConnectionLossDelay` | `10` | configs.py:116 | seconds |

**Delay Application:**
```python
reactor.callLater(delay_seconds, self.reConnect, connector)
```
Uses Twisted reactor's callLater (fired once, no repeat).

### Retry Limits

**There are NO explicit retry limits in the legacy code.**

Behavior:
- If `reconnectOnConnectionFailure = True`, retries forever with fixed `reconnectOnConnectionFailureDelay`
- If `reconnectOnConnectionLoss = True`, retries forever with fixed `reconnectOnConnectionLossDelay`
- **Only stops retrying when:**
  - `perspective_connector_stop()` calls `stopConnectionRetrying()` (sets `connectionRetry = False`)
  - `perspective_connector_remove()` calls `perspective_connector_stop()`
  - Config `reconnectOn*` flags are set to `False` (no retry)

### Reconnect State Flow

**Attempt Logic:**
1. Connection attempt made via `_connect()` (line 143)
2. If fails: callback `clientConnectionFailed` (line 102)
3. If lost: callback `clientConnectionLost` (line 117)
4. In either callback, check `self.config.reconnectOn*` and `self.connectionRetry`
5. If both True: schedule reconnect with `reactor.callLater(delay, self.reConnect, connector)`
6. If False: errback connectDeferred and callback exitDeferred (exit mode)

**Reconnect Attempt:**
```python
def reConnect(self, connector=None):
    if self.connectDeferred.called:
        # Reset deferred if it was already fired in a previous cycle
        self.connectDeferred = defer.Deferred()
        self.connectDeferred.addCallback(self.bind)
    connector.connect()  # Twisted connector.connect() → retries TCP connection
```

### State Reporting

**Session State Access:**
```python
def getSessionState(self):
    if self.smpp is None:
        return SMPPSessionStates.NONE
    else:
        return self.smpp.sessionState
```

**Possible States** (from smpp.twisted.protocol.SMPPSessionStates):
- `NONE`: Not initialized
- `OPEN`: TCP/SSL connected, waiting for bind response
- `BOUND_TRX`: Bound as transceiver
- `BOUND_RX`: Bound as receiver
- `BOUND_TX`: Bound as transmitter
- `UNBIND_PENDING`: Unbind sent, waiting for response
- `UNBOUND`: After unbind complete

**How to Query:**
```python
state = factory.getSessionState()  # Returns SMPPSessionStates enum or None
state_name = state.name if state else 'NONE'  # Convert to string
```

### Hidden Quirks & Gotchas

1. **No Exponential Backoff**: Delay is constant (e.g., always 10s), not 10s, 20s, 40s, ...
2. **No Max Retry Count**: Will retry forever unless stopped manually or config disabled
3. **Deferred Reuse**: On each reconnect cycle, `connectDeferred` is reset if already fired (line 136)
4. **Connection vs Bind**: Connection failure and bind failure are treated the same (both use `reconnectOnConnectionFailure`)
5. **Timer Cancellation**: `reconnectTimer` is canceled only when `stopConnectionRetrying()` is called
6. **Pending Messages**: While reconnecting, no messages are processed; queue consumer waits
7. **Session State Race**: Between "connection lost" and "reconnect scheduled", state is not reported (timing gap)

---

## SC-007: Failover (Multi-Connector Route Selection)

### Source Files
- `jasmin/routing/Routes.py` (FailoverRoute classes, lines 262–369)
- `jasmin/routing/RoutingTables.py` (getRouteFor method, lines 79–91)
- `jasmin/routing/router.py` (deliver_sm_callback, lines 154–220)

### Route Types & Selection

**Route Hierarchy:**
```
Route (base)
├── DefaultRoute
├── MTRoute
│   ├── StaticMTRoute
│   ├── RandomRoundrobinMTRoute
│   ├── FailoverMTRoute
│   └── BestQualityMTRoute (not implemented)
└── MORoute
    ├── StaticMORoute
    ├── RandomRoundrobinMORoute
    └── FailoverMORoute
```

### FailoverRoute Behavior

**For Outbound (MT) Messages:**

**Class:** `FailoverMTRoute` (Routes.py:340–369)
**Connectors:** Multiple smppc (SMPP client) connectors

**Initialization:**
```python
FailoverMTRoute(filters, connectors, rate)
self.seq = -1  # Sequence counter
self.connector = connectors  # List of connectors
self.rate = rate  # Billing rate (all connectors share same rate)
```

**Connector Selection Algorithm:**
```python
def getConnector(self):
    try:
        self.seq += 1
        return self.connector[self.seq]  # Return next connector in order
    except IndexError:
        return None  # Reached end of list
```

**Initialization on Match:**
```python
def matchFilters(self, routable):
    self.seq = -1  # Reset sequence when route is matched
    return MTRoute.matchFilters(self, routable)
```

**Key Point:** `seq` is reset to `-1` on each new message that matches this route (line 366), so first call to `getConnector()` returns `connector[0]`, second call returns `connector[1]`, etc.

### Router's Use of Failover

**Method:** `deliver_sm_callback(message)` (router.py:154–220)

**Failover Detection:**
```python
route = self.getMORoutingTable().getRouteFor(routable)
if repr(route) == 'FailoverMORoute':
    routedConnectors = route.getConnectors()  # Get ALL connectors
    route_type = 'failover'
else:
    routedConnectors = [route.getConnector()]  # Get single connector
    route_type = 'simple'
```

**Failover Connector List:**
```python
def getConnectors(self):  # FailoverMORoute only
    return self.connector  # Returns entire list
```

**Delivery to Thrower:**
```python
yield self.amqpBroker.publish(
    exchange='messaging',
    routing_key='deliver_sm_thrower.%s' % routedConnectors[0]._type,
    content=RoutedDeliverSmContent(
        routable=routable,
        connectors=routedConnectors,  # Pass ALL connectors
        route_type=route_type
    )
)
```

### Failover in Thrower (Deliver-SM Thrower)

**Source:** `jasmin/routing/throwers.py` (DeliverSmThrower, lines 220–543)

**Failover Logic in smpp_deliver_sm_callback:**
```python
# Thrower iterates through connectors until one succeeds
for connector in routed_content.connectors:
    if connector._type != 'smpps':
        continue
    
    # Try to deliver via this connector
    if connector.getAvailableSessionCount() > 0:
        # Connector has available session, try delivery
        ...attempt delivery...
        if success:
            break  # Stop failover
    else:
        # Connector unavailable, try next
        continue
```

**Key Algorithm:**
1. Router passes **all connectors** from FailoverRoute to thrower
2. Thrower iterates in order (first to last)
3. For each connector, checks if it has available sessions
4. Attempts delivery to first available connector
5. On failure, moves to next connector
6. If all fail, message is rejected and requeued

### Failover Constraints & Rules

**FailoverMORoute (MO messages from smpps servers):**
- **Allowed connector types:** `http`, `smpps` (MO-capable)
- **Constraint:** Cannot mix connector types (line 318–325)
  - E.g., cannot failover [http, smpps] — must be homogeneous

**FailoverMTRoute (MT messages to smppc clients):**
- **Allowed connector types:** `smppc` (SMPP client)
- **No mixing constraint** (differs from MO)

**Billing:**
- All connectors in a failover route share the same `rate`
- User is billed once, regardless of which failover connector is used

### Selection Order (No Randomization in Failover)

**Route Selection by RoutingTable:**
```python
def getRouteFor(self, routable):
    for r in self.table:  # Iterate in order (sorted by priority descending)
        route = list(r.values())[0]
        if route.matchFilters(routable):
            return route  # Return first matching route
    return None
```

**Within Failover:**
- First call to `getConnector()`: returns `connector[0]`
- Second call: returns `connector[1]`
- (No random selection, deterministic sequence)

**Within Route Matching:**
- Routes are stored in `self.table` as list of dicts: `[{order: route}, ...]`
- Sorted by order descending (line 63, RoutingTables.py)
- `getRouteFor` iterates in sorted order, returns first match
- No round-robin or random selection at route level

### Connector Availability Detection

**In Failover Decision (thrower.py):**
```python
# Each connector has:
def getAvailableSessionCount(self):
    return len([s for s in self.sessions if s.state == BOUND])
```

**Sessions:** Track bound SMPP sessions per connector
- Session created when bind completes
- Session destroyed when unbind completes or connection lost
- Available sessions = sessions in BOUND state

**Quirk:** Failover only uses availability (session count > 0), not connection state explicitly. Connector can be in `OPEN` state (connecting) but with 0 sessions — will be skipped.

### Side Effects of Failover Selection

1. **Message Delivery Attempt**: First available connector attempts delivery
2. **Billing**: Charged once regardless of which connector delivers
3. **DLR Routing**: DLR comes back via the connector that delivered (route back to originator)
4. **Failover Iteration**: If first fails, second is tried (within thrower callback)
5. **Rejection**: If all connectors fail, message is rejected and requeued

### Hidden Quirks & Gotchas

1. **No Connector Health Check**: Failover only checks session count, not heartbeat/ping
2. **No Priority Ordering**: First connector in list is always tried first (no weight/priority)
3. **No Retry Strategy**: Once all connectors exhausted, message is requeued (not retried immediately)
4. **MO Homogeneity Constraint**: FailoverMORoute cannot mix `http` and `smpps` (FailoverMTRoute has no such constraint)
5. **Session Count Race**: Between session count check and delivery attempt, session could be lost
6. **No Connector State Normalization**: If connector is `OPEN` (binding) or `UNBOUND`, it's skipped even if previous session succeeded
7. **Billing Shares Rate**: All connectors in failover route share the same rate; no per-connector billing override
8. **Sequence Reset on Match**: Each new message resets `seq = -1`, so failover iteration starts from connector[0] again (not round-robin across messages)

---

## Summary Table: SC-001 to SC-007

| SC | Component | Key Finding | Source |
|----|-----------|-------------|--------|
| SC-001 | Config Mandatory | `id`, `host`, `port`, `username`, `password` required; `host`, `port`, `username`, `password`, `systemType` need restart | smppccm.py:36, configs.py:40–123 |
| SC-001 | Config Defaults | 20+ optional fields with defaults (e.g., `bindOperation='transceiver'`, `reconnectDelay=10`) | configs.py:32–223 |
| SC-002 | Add | Creates connector in UNBOUND state, sets up AMQP queue, creates service (not started). Returns False if duplicate ID or AMQP not ready. | clients.py:224–287 |
| SC-002 | Remove | Stops service, stops consumer, removes from list. Requeues pending messages. | clients.py:290–318 |
| SC-002 | List | Returns array of connector details (id, session_state, service_status, start/stop counts). | clients.py:320–333 |
| SC-002 | Start | Checks session state in [NONE, UNBOUND], starts service (connects & binds), attaches AMQP consumer with prefetch=1. | clients.py:336–407 |
| SC-002 | Stop | Cancels AMQP consumer, requeues pending messages, stops service. Sets service_status=0. | clients.py:410–466 |
| SC-003 | Reconnect Trigger | Two triggers: connectionFailed (bind error) and connectionLost (socket close). Both check config flags & connectionRetry. | factory.py:102–129 |
| SC-003 | Delay | Fixed delay (e.g., 10s), no exponential backoff. Scheduled via reactor.callLater(). | factory.py:110, 125; configs.py:116, 120 |
| SC-003 | Retry Limit | No explicit retry limit. Retries forever unless `reconnectOn*=False` or `stopConnectionRetrying()` called. | factory.py:170–181 |
| SC-003 | State Reporting | getSessionState() returns enum or NONE. Possible: NONE, OPEN, BOUND_TRX/RX/TX, UNBOUND. | factory.py:200–204 |
| SC-007 | Route Selection | RoutingTable.getRouteFor() iterates in priority order, returns first match. No randomization at route level. | RoutingTables.py:79–91 |
| SC-007 | Failover Algorithm | For FailoverRoute, seq increments on getConnector(): returns connector[seq]. seq resets to -1 on matchFilters. | Routes.py:302–337 |
| SC-007 | Failover in Thrower | Thrower iterates through all connectors, tries first available (session count > 0), moves to next on failure. | throwers.py:424+ |
| SC-007 | Failover Constraints | FailoverMORoute: cannot mix connector types. FailoverMTRoute: all smppc. All share same rate. | Routes.py:318–325; RoutingTables.py:30–49 |

---

## Critical Implementation Notes for Go Rewrite

### Configuration Parity
- All 20+ config fields must map 1:1 to Go structs
- Restart requirement matrix must be enforced in Go CLI
- TLV validation rules are **per-connector**, not global (no value injection)

### Lifecycle Parity
- Add/Remove/Start/Stop must maintain same state machine
- Session state enum must include: NONE, OPEN, BOUND_TRX, BOUND_RX, BOUND_TX, UNBOUND, UNBIND_PENDING
- Stop must requeue pending messages (critical for no message loss)
- Consumer prefetch must be hardcoded to 1 per connector

### Reconnect Parity
- No exponential backoff — keep fixed delay
- No retry limit — continue forever unless stopped
- Deferred/promise handling on reconnect (reset on each cycle if already fired)
- Session state during reconnection (OPEN or previous)

### Failover Parity
- Router passes entire connector list to thrower for FailoverRoute
- Thrower iterates sequentially (not random)
- Sequence resets per message (not round-robin across messages)
- Session count check (>0) determines availability
- All connectors in failover share same billing rate
- MO failover cannot mix connector types

### Missing in Legacy (Design Gaps)
1. No connector health monitoring (besides session count)
2. No weighted/prioritized connector selection
3. No exponential backoff for reconnects
4. No retry limit (can retry forever)
5. No round-robin failover (each message tries connector[0] first)

---

## Testing & Validation Checklist

### SC-001 Configuration
- [ ] All 20+ fields persist and load correctly
- [ ] Restart requirement enforced (stop/start on update if field in RequireRestartKeys)
- [ ] TLV validation rules parsed and applied (tag, type, length, required)
- [ ] Invalid config values rejected (e.g., invalid data_coding, bindOperation)
- [ ] Defaults match legacy exactly

### SC-002 Lifecycle
- [ ] Add: Connector created, AMQP queue exists, service not running
- [ ] Add duplicate ID: Rejected with error
- [ ] Remove: Service stopped, consumer cancelled, connector deleted
- [ ] Remove non-existent: Error returned
- [ ] List: Shows correct count, session state, start/stop counters
- [ ] Start: Service running, consumer attached, prefetch=1
- [ ] Start invalid session state: Rejected (only NONE/UNBOUND allowed)
- [ ] Stop: Service stopped, consumer detached, pending messages requeued
- [ ] Stop already stopped: Error returned
- [ ] Restart (stop → start): Works without orphaning consumer

### SC-003 Reconnect
- [ ] Connection failure: Reconnects after delay if flag True
- [ ] Connection loss: Reconnects after delay if flag True
- [ ] Flags False: No reconnect, exits
- [ ] Delay is fixed (e.g., 10s every time)
- [ ] Session state reported: NONE, OPEN, BOUND_TRX, etc.
- [ ] Stop cancels reconnect timer

### SC-007 Failover
- [ ] FailoverRoute: All connectors returned by getConnectors()
- [ ] Sequence: First message tries connector[0], second tries connector[0] again (reset per message)
- [ ] Thrower iterates connectors in order
- [ ] Thrower skips unavailable (session count == 0)
- [ ] Thrower tries next if current fails
- [ ] Billing: Charged once regardless of which connector delivers
- [ ] MO failover: Rejects mixed connector types
- [ ] MT failover: Accepts all smppc

---

## File Reference Map

| File | Lines | Primary Content |
|------|-------|-----------------|
| `jasmin/protocols/smpp/configs.py` | 32–223 | SMPPClientConfig class: all config fields, types, defaults, validation |
| `jasmin/protocols/cli/smppccm.py` | 15–36, 134–164, 191–232 | Config key mapping, CLI input/output casting, config build/update |
| `jasmin/protocols/smpp/services.py` | 11–54 | SMPPClientService: startService/stopService lifecycle |
| `jasmin/protocols/smpp/factory.py` | 44–204 | SMPPClientFactory: reconnect logic, session state, bind operation |
| `jasmin/managers/clients.py` | 31–650 | SMPPClientManagerPB: add/remove/list/start/stop, queue management, persistence |
| `jasmin/routing/Routes.py` | 262–369 | FailoverRoute, FailoverMORoute, FailoverMTRoute: connector selection, getConnectors() |
| `jasmin/routing/RoutingTables.py` | 15–101 | RoutingTable.getRouteFor(): route matching and selection order |
| `jasmin/routing/router.py` | 154–220 | deliver_sm_callback: failover route detection and thrower publishing |
| `jasmin/routing/throwers.py` | 220–543 | DeliverSmThrower: failover iteration, session availability check |


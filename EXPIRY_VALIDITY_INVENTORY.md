# Jasmin Legacy Expiry/Validity Behaviors Inventory

**Task**: Inventory legacy and "Improved" expiry/validity behaviors for AMQP consumers (Router, Throwers, Clients) in the Jasmin project for A-011 (Message Expiry) Go rewrite.

**Date**: 2026-07-19  
**Scope**: Legacy Python codebase expiry/validity/expiration logic

---

## Executive Summary

The legacy Python codebase demonstrates a **partial and asymmetric** implementation of message expiry handling:

- **SMPPClientSMListener** (consumer of `submit.sm.*` queues): ✅ Implements expiry check
- **RouterPB** (consumer of `deliver.sm.*` queues): ❌ NO expiry check found
- **Throwers** (deliverSmThrower, etc.): ⚠️ NO expiry check; forward validity from PDU to HTTP/SMPP
- **HTTP Send Endpoint**: ⚠️ Handles `validity-period` parameter but no explicit queue-level expiry check

---

## Detailed Inventory by Component

### 1. SMPPClientSMListener (SMPP Client Consumer)
**File**: `jasmin/managers/listeners.py`  
**Consumer Type**: Consumes from `submit.sm.<cid>` queues  
**Status**: ✅ **Implements expiry check**

#### Expiry Check Logic
**Lines 182-188** (in `submit_sm_callback`):
```python
# If the message has expired in the queue
if 'headers' in message.content.properties and 'expiration' in message.content.properties['headers']:
    expiration_datetime = parser.parse(message.content.properties['headers']['expiration'])
    if expiration_datetime < datetime.now():
        self.log.info(
            "Discarding expired message[%s]: expiration is %s", msgid, expiration_datetime)
        yield self.rejectMessage(message)
        defer.returnValue(False)
```

#### Details
- **Header field**: `message.content.properties['headers']['expiration']`
- **Time format**: ISO 8601 string (parsed via `dateutil.parser.parse()`)
- **Comparison**: Direct datetime comparison: `expiration_datetime < datetime.now()`
- **Terminal action on expiry**: `rejectMessage()` (does NOT requeue; message is discarded)
- **Logging**: Info-level log with msgid and expiration timestamp
- **Decision point**: BEFORE attempting to send via SMPP

#### Logged Context (Lines 361-375, 399-413)
The listener also logs the expiration value in SMS-MT logs:
```python
'validity' not in amqpMessage.content.properties['headers'])
else amqpMessage.content.properties['headers']['expiration'],
```
Logs show `validity` as the field name in output, but internally uses `expiration` header.

---

### 2. RouterPB (MO Delivery Router)
**File**: `jasmin/routing/router.py`  
**Consumer Type**: Consumes from `deliver.sm.*` queues  
**Status**: ❌ **NO expiry check found**

#### Evidence
- **deliver_sm_callback** (lines 154-222): Processes `deliver.sm.*` messages
  - Extracts `msgid`, `scid`, routing info
  - Calls `getMORoutingTable().getRouteFor(routable)`
  - Publishes routed content to `deliver_sm_thrower.<type>` queues
  - **NO expiry check anywhere in the flow**

- **bill_request_submit_sm_resp_callback** (lines 236-263): Consumes billing responses
  - **NO expiry check**

#### Missing Behavior
The Router **should** (per A-011 specification) check if messages have expired before routing, similar to SMPPClientSMListener behavior. Currently, expired delivery messages are routed and left to the thrower layer to handle (if at all).

---

### 3. deliverSmThrower (Thrower Base)
**File**: `jasmin/routing/throwers.py`  
**Consumer Types**: 
- `http_deliver_sm_callback` (lines 237-422): HTTP delivery thrower
- `smpp_deliver_sm_callback` (lines 424-520+): SMPP delivery thrower

**Status**: ❌ **NO expiry check on queue consumption**  
✅ **Forwards validity_period from PDU to downstream**

#### Expiry Handling
- **No check on message receipt**: Throwers do NOT check AMQP message expiration headers before processing
- **Concern**: Expired delivery messages could be retried/requeued without awareness of expiry

#### Validity Period Forwarding

**HTTP Thrower** (lines 287-289):
```python
if ('validity_period' in RoutedDeliverSmContent.params and
            RoutedDeliverSmContent.params['validity_period'] is not None):
    args['validity'] = RoutedDeliverSmContent.params['validity_period']
```
- Extracts `validity_period` from PDU and maps to HTTP parameter `validity`
- Sends to HTTP endpoint for final delivery

**SMPP Thrower** (lines 424-520+):
- Forwards PDU as-is to SMPP server via `deliverer.sendRequest(pdu, ...)`
- PDU's `validity_period` field propagates unchanged

---

### 4. HTTP Send Endpoint
**File**: `jasmin/protocols/http/endpoints/send.py`  
**Role**: Entry point for SMS submission via HTTP API  
**Status**: ⚠️ **Handles validity period parameter, but no queue-level expiry check**

#### Validity Period Handling

**Lines 280-287**:
```python
# Set validity_period
if b'validity-period' in updated_request.args:
    delta = timedelta(minutes=int(updated_request.args[b'validity-period'][0]))
    param_updates['validity_period'] = datetime.today() + delta
    self.log.debug(
        "SubmitSmPDU validity_period is set to %s (+%s minutes)",
        routable.pdu.params['validity_period'],
        updated_request.args[b'validity-period'][0])
```

#### Details
- **HTTP parameter**: `validity-period` (in minutes)
- **Conversion**: Minutes → absolute datetime (`datetime.today() + timedelta(minutes=N)`)
- **PDU field**: Sets `validity_period` on SubmitSmPDU
- **Validation**: Validated via HttpAPICredentialValidator (lines 126-127)
- **Scope**: Only applies to SubmitSm (MT) path, not MO delivery

#### Validation Filter (jasmin/protocols/http/validation.py, lines 157-163)
```python
# Filtering validity_period
if (self.submit_sm and
    not self.user.mt_credential.getAuthorization('set_validity_period')):
    _r = self._get_binary_r('validity_period')
```
- User-level authorization check for setting validity period
- Authorization key: `'set_validity_period'`

---

### 5. SMPP Client Manager (Client Proxies & Content)
**Files**: `jasmin/managers/clients.py`, `jasmin/managers/proxies.py`, `jasmin/managers/content.py`

#### SMPPClientManagerPB.perspective_submit_sm
**File**: `jasmin/managers/clients.py` (lines 548-594)  
**Status**: ✅ **Passes validity_period to AMQP message expiration**

```python
def perspective_submit_sm(self, uid, cid, SubmitSmPDU, submit_sm_bill, priority=1, validity_period=None, ...):
    ...
    c = SubmitSmContent(
        uid=uid,
        body=PickledSubmitSmPDU,
        replyto=responseQueueName,
        submit_sm_bill=submit_sm_bill,
        priority=priority,
        expiration=validity_period,  # <-- Converts PDU validity_period to AMQP expiration header
        source_connector='httpapi' if source_connector == 'httpapi' else 'smppsapi',
        destination_cid=cid)
    yield self.amqpBroker.publish(exchange='messaging', routing_key=pubQueueName, content=c)
```

#### SubmitSmContent Class
**File**: `jasmin/managers/content.py` (lines 143-171)  
**Status**: ✅ **Stores expiration in AMQP message headers**

```python
def __init__(self, uid, body, replyto, submit_sm_bill=None, priority=1, expiration=None, ...):
    ...
    props['headers'] = {'source_connector': source_connector}
    if submit_sm_bill is not None:
        props['headers']['submit_sm_bill'] = submit_sm_bill
    if expiration is not None:
        props['headers']['expiration'] = expiration  # <-- Stores as-is in header
    PDU.__init__(self, body, properties=props)
```

- **Receives**: `expiration` parameter (datetime object or string from `validity_period`)
- **Stores**: In `message.content.properties['headers']['expiration']`
- **Format**: No transformation; stores raw validity_period value
- **Default**: None if validity_period is None

#### SMPPClientManagerPBProxy.submit_sm
**File**: `jasmin/managers/proxies.py` (lines 77-106)  
**Status**: ✅ **Extracts and formats validity_period**

```python
# Set the message validity date
if SubmitSmPDU.params['validity_period'] is not None:
    validity_period = SubmitSmPDU.params['validity_period'].strftime('%Y-%m-%d %H:%M:%S')
else:
    # Validity period is not set, the SMS-C will set its own default
    # validity_period to this message
    validity_period = None

return self.pb.callRemote(
    'submit_sm',
    uid=uid,
    cid=cid,
    SubmitSmPDU=self.pickle(SubmitSmPDU),
    submit_sm_bill=submit_sm_bill,
    priority=priority_flag,
    validity_period=validity_period)
```

- **Input**: `SubmitSmPDU.params['validity_period']` (datetime object from SMPP PDU)
- **Output format**: `'%Y-%m-%d %H:%M:%S'` (ISO-like, but without T separator and timezone)
- **Fallback**: None if PDU has no validity_period (SMSC will use its default)

---

### 6. SMPP Configurations & API
**File**: `jasmin/routing/jasminApi.py` (lines 121, 130)  
**File**: `jasmin/protocols/smpp/configs.py` (line 151)  
**Status**: ✅ **Declares validity_period authorization and config**

#### MtMessagingCredential
**jasmin/routing/jasminApi.py**, lines 121, 130:
```python
'set_validity_period': default_authorizations,  # Authorization key
...
'validity_period': re.compile(rb'^\\d+$'),  # Filter validation: must be numeric (minutes)
```

#### SMPPClientConfig
**jasmin/protocols/smpp/configs.py**, line 151:
```python
self.validity_period = kwargs.get('validity_period', None)
```
- Stores default validity_period for connector-level configuration

---

## Time Format Summary

| Component | Field | Format | Example |
|-----------|-------|--------|---------|
| SMPPClientSMListener | `expiration` (header) | ISO 8601 string | `"2026-07-19 03:09:05"` |
| SMPPClientManagerPBProxy | `validity_period` (param) | `'%Y-%m-%d %H:%M:%S'` | `"2026-07-19 03:09:05"` |
| HTTP Send API | `validity-period` (param) | Minutes (integer) | `"60"` |
| HTTP Send PDU | `validity_period` (PDU field) | Absolute datetime | `datetime(2026, 7, 19, 4, 9, 5)` |
| SMPP PDU | `validity_period` (PDU field) | Absolute datetime | `datetime(2026, 7, 19, 4, 9, 5)` |
| SubmitSmContent | `expiration` (header) | String format (varies) | `"2026-07-19 03:09:05"` |

---

## Expiry Decision Points (Terminal Actions)

| Consumer | Check | Decision | Action on Expired |
|----------|-------|----------|-------------------|
| SMPPClientSMListener | ✅ Yes (expiration header) | Before send via SMPP | Reject (NO requeue) |
| RouterPB | ❌ No | N/A | Routes normally |
| deliverSmThrower (HTTP) | ❌ No | N/A | Attempts delivery, retries on error |
| deliverSmThrower (SMPP) | ❌ No | N/A | Attempts delivery, retries on error |
| HTTP Send | ❌ No queue-level | Entry point | Accepts & enqueues |

---

## Logging Evidence

### SMPPClientSMListener Expiry Log
**listeners.py**, lines 185-186:
```python
self.log.info(
    "Discarding expired message[%s]: expiration is %s", msgid, expiration_datetime)
```

### SMS-MT Success Log (with validity)
**listeners.py**, lines 360-375:
```python
self.log.info(
    "SMS-MT [cid:%s] [queue-msgid:%s] [smpp-msgid:%s] [status:%s] [prio:%s] [dlr:%s] [validity:%s] \\
[from:%s] [to:%s] [content:%s] [tlvs:%s]",
    ...
    'none' if ('headers' not in amqpMessage.content.properties or
               'expiration' not in amqpMessage.content.properties['headers'])
    else amqpMessage.content.properties['headers']['expiration'],
    ...)
```

Note: Field name is `expiration` in header, but logged as `validity`.

---

## Key Findings for Go Rewrite (A-011)

### 1. **Asymmetric Implementation**
   - Only SMPPClientSMListener checks expiry before sending to SMPP
   - Router does NOT check; throwers do NOT check
   - **Recommendation**: Implement uniform expiry checks at Router and Thrower layers

### 2. **Time Format Variation**
   - HTTP API: minutes (integer)
   - PDU layer: absolute datetime
   - AMQP header: ISO 8601 string (`'%Y-%m-%d %H:%M:%S'`)
   - **Recommendation**: Standardize on ISO 8601 with timezone info for Go rewrite

### 3. **Terminal Action**
   - SMPPClientSMListener: Rejects (discards) expired messages, does NOT requeue
   - **Rationale**: Message expired while queued; no point retrying
   - **Recommendation**: Same approach for Router and Throwers

### 4. **Missing Improvements**
   - No metrics on expired message counts
   - No configurable expiry policies (e.g., drop vs. requeue)
   - No graceful degradation on partial expiry (e.g., in concatenated messages)
   - **Opportunity**: Add metrics and configurability in Go version

### 5. **Validity Period Propagation**
   - HTTP endpoint accepts `validity-period` in minutes
   - Converts to absolute datetime
   - Propagates through PDU to AMQP headers
   - Throwers forward to downstream (HTTP/SMPP)
   - **Status**: Working as designed; no issues found

---

## References

### Legacy Files Analyzed
- `jasmin/managers/listeners.py` (SMPPClientSMListener)
- `jasmin/routing/router.py` (RouterPB)
- `jasmin/routing/throwers.py` (deliverSmThrower)
- `jasmin/protocols/http/endpoints/send.py` (HTTP Send)
- `jasmin/managers/clients.py` (SMPPClientManagerPB)
- `jasmin/managers/proxies.py` (SMPPClientManagerPBProxy)
- `jasmin/managers/content.py` (SubmitSmContent)
- `jasmin/protocols/http/validation.py` (HttpAPICredentialValidator)
- `jasmin/routing/jasminApi.py` (MtMessagingCredential)
- `jasmin/protocols/smpp/configs.py` (SMPPClientConfig)

### Specification
- `spec/implementation/PHASE2_30_ROUTER_QOS_EXPIRY_SLICE.md` (A-011 requirements)

### Test Coverage
- `scripts/compat/capture_smpp_client_readiness_golden.py` (expiration test cases)
- `scripts/compat/capture_amqp_golden.py` (AMQP baseline with expiration)

---

## Recommendations for Go Implementation

1. **Router Layer** (A-011):
   - Add expiry check in `deliver_sm_callback` before routing
   - Reject expired messages with appropriate logging
   - Track expired message metrics

2. **Thrower Layer**:
   - Add expiry check on consumption (optional per config)
   - Consider retry behavior for near-expiry messages

3. **Time Format**:
   - Use RFC 3339 / ISO 8601 with timezone info (e.g., `2026-07-19T03:09:05Z`)
   - Avoid ambiguity with naive datetime

4. **Metrics**:
   - Counter: expired_messages_discarded
   - Counter: expired_messages_retried
   - Gauge: message_age_in_queue (percentiles)

5. **Configuration**:
   - `router.enable_expiry_check` (default: true)
   - `router.expiry_policy` (drop|requeue|delay)
   - `thrower.check_expiry` (optional for thrower layer)

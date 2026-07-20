# SMPP Session Management Legacy Contracts Inventory
## Jasmin Python Baseline (Phase 2.33)

**Task:** Inventory exact legacy contracts for SMPP session lifecycle after BIND

**Project:** Jasmin Python-to-Go Rewrite

**Baseline Commit:** Latest (jasmin-go repository)

**Scope:**
- SM-001: Sequence number generation and correlation
- SM-002: Enquire Link implementation (intervals, timeouts)
- SM-003: Inactivity Timer implementation
- SM-004: Response Timer implementation
- SM-005: Error handling and ESME error code mapping
- SM-006: Long submit_sm transaction lifecycle
- SM-007: Transaction timeout and recovery logic

---

## SM-001: Sequence Number Generation & Correlation

### Overview
Sequence numbers uniquely identify request/response pairs in SMPP. Jasmin uses a monotonically increasing counter to track pending transactions.

### Source Files
- `jasmin/protocols/smpp/protocol.py` (SMPPClientProtocol class)
- `smpp.twisted.protocol` (base class, inherited behavior)

### Contract Terms: Sequence Number Generation

**Source:** `jasmin/protocols/smpp/protocol.py:106–112` (claimSeqNum)

```python
def claimSeqNum(self):
    seqNum = twistedSMPPClientProtocol.claimSeqNum(self)  # Inherited from base class
    
    self.factory.stats.set('last_seqNum_at', datetime.now())
    self.factory.stats.set('last_seqNum', seqNum)
    
    return seqNum
```

**Extracted Contract:**

| Aspect | Value | Source |
|--------|-------|--------|
| Sequence counter type | 32-bit unsigned integer | smpp.twisted.protocol (base) |
| Starting value | Determined by base class (typically 1) | smpp.twisted.protocol |
| Increment | +1 per call | smpp.twisted.protocol |
| Wrap-around | At 2^32 - 1 → wraps to 0 or 1 | smpp.twisted.protocol |
| Claim method | `claimSeqNum()` | protocol.py:106 |
| Thread-safe? | **NOT VERIFIED** — must audit base class | smpp.twisted.protocol |
| Reset on reconnect? | **NOT VERIFIED** — must audit base class | smpp.twisted.protocol |

**Boundary Conditions:**
- Sequence 0: Often reserved (check base class behavior)
- Sequence 2^32 - 1: Wrap-around behavior TBD
- Sequence collision: If sequence wraps before response received, timeout or collision? **NOT DOCUMENTED**

**Side Effects of Calling claimSeqNum:**
1. Increments internal counter (in base class)
2. Updates factory stats: `last_seqNum_at` = current datetime
3. Updates factory stats: `last_seqNum` = claimed sequence number
4. Returns the sequence number

**Correlation Mechanism:**
```
REQUEST (outbound):
  1. Claim seqNum = S
  2. Set PDU.seqNum = S
  3. Start OutboundTransaction(seqNum=S, timer=responseTimerSecs)
  4. Send PDU

RESPONSE (inbound):
  1. Receive PDU with seqNum = S
  2. Look up OutboundTransaction by seqNum = S
  3. If found: clear timer, call callback
  4. If not found: ERROR (TBD — check error handling)
```

**Pseudocode: Sequence Number Lifecycle**

```
SEQUENCE NUMBER GENERATION PSEUDOCODE:

class SMPPClientProtocol:
    
    def claimSeqNum():
        # Call inherited method from smpp.twisted.protocol
        seqNum := twistedSMPPClientProtocol.claimSeqNum(self)
        
        # Update statistics
        factory.stats['last_seqNum_at'] := datetime.now()
        factory.stats['last_seqNum'] := seqNum
        
        return seqNum
    
    # Base class (smpp.twisted.protocol) manages:
    # - self.seqNum (internal counter)
    # - Wrap-around at 2^32
    # - Thread-safe increment (TBD: verify locks)
```

---

## SM-002: Enquire Link Implementation

### Overview
Enquire Link is a keep-alive ping sent periodically when no other activity occurs. It detects dead connections and prevents firewall timeout.

### Source Files
- `jasmin/protocols/smpp/configs.py` (configuration)
- `smpp.twisted.protocol.SMPPClientProtocol` (base class)
- `jasmin/protocols/smpp/protocol.py` (stats tracking)

### Configuration

**Source:** `jasmin/protocols/smpp/configs.py:67–71`

| Property | Value | Type | Unit | Default |
|----------|-------|------|------|---------|
| `enquireLinkTimerSecs` | User-configurable | int/float | seconds | **30** |
| Validation | Must be int or float (line 68–70) | — | — | — |

**Extracted Contract:**

```
ENQUIRE LINK TIMER CONTRACT:

INPUT:
  config.enquireLinkTimerSecs: 30 (default, configurable)
  
RULE:
  After enquireLinkTimerSecs have elapsed with NO OTHER SMPP ACTIVITY:
  - Send an enquire_link PDU
  - Set response timer = responseTimerSecs
  - On response: reset enquire_link timer
  - On timeout: consider connection dead, initiate reconnect
  
OUTPUT:
  Periodic keep-alive PDU sent to maintain connection
  
BOUNDARY CONDITIONS:
  - enquireLinkTimerSecs = 0: Enquire link disabled (TBD: verify)
  - enquireLinkTimerSecs = 1: Enquire link every 1 second (high overhead)
  - enquireLinkTimerSecs = 300: Enquire link every 5 minutes
  
INTERACTION WITH OTHER TIMERS:
  - Resets on ANY SMPP activity (submit_sm, deliver_sm, etc.)
  - Does NOT reset on data transmitted (only received?)
  - Does reset when enquire_link_resp received
```

### Base Class Behavior (smpp.twisted.protocol)

**Assumed behavior from base class** (verify in actual library):
- Timer started when session enters BOUND state
- Timer is reactor.callLater(enquireLinkTimerSecs, sendEnquireLink)
- Sending any PDU resets the timer
- No response to enquire_link within responseTimerSecs triggers reconnect

### Stats Tracking

**Source:** `jasmin/protocols/smpp/protocol.py:88–92` (PDUReceived)

```python
def doPDURequest(self, reqPDU, handler):
    twistedSMPPClientProtocol.doPDURequest(self, reqPDU, handler)
    
    # Stats for enquire_link
    if reqPDU.commandId == CommandId.enquire_link:
        self.factory.stats.set('last_received_elink_at', datetime.now())
```

Also in `sendPDU` (lines 92–97):
```python
if pdu.commandId == CommandId.enquire_link:
    self.factory.stats.set('last_sent_elink_at', datetime.now())
    self.factory.stats.inc('elink_count')
```

**Extracted Contract — Stats:**
- `last_sent_elink_at`: Timestamp of last transmitted enquire_link
- `last_received_elink_at`: Timestamp of last received enquire_link
- `elink_count`: Counter of enquire_link PDUs sent (increments on send, not on receive)

**Pseudocode: Enquire Link Lifecycle**

```
ENQUIRE LINK TIMER PSEUDOCODE:

INITIALIZATION (when entering BOUND state):
  enquire_link_timer := reactor.callLater(config.enquireLinkTimerSecs, onEnquireLinkTimeout)

ON ANY SMPP ACTIVITY (send or receive):
  if enquire_link_timer is active:
    enquire_link_timer.cancel()
    enquire_link_timer := reactor.callLater(config.enquireLinkTimerSecs, onEnquireLinkTimeout)

ON TIMEOUT (onEnquireLinkTimeout):
  seqNum := claimSeqNum()
  pdu := enquire_link(seqNum=seqNum)
  stats['last_sent_elink_at'] := datetime.now()
  stats['elink_count'] += 1
  
  startOutboundTransaction(pdu, timeout=config.responseTimerSecs)
    .addCallback(onEnquireLinkResp)
    .addErrback(onEnquireLinkTimeout)

ON ENQUIRE LINK RESPONSE:
  stats['last_received_elink_at'] := datetime.now()
  # Reset timer (done via activity reset above)

ON ENQUIRE LINK TIMEOUT (responseTimerSecs exceeded):
  log.error("No response to enquire_link in %s seconds" % config.responseTimerSecs)
  # Trigger reconnect (handled by transaction timeout logic)
```

---

## SM-003: Inactivity Timer Implementation

### Overview
Inactivity Timer tracks the maximum time allowed between ANY SMPP transactions (send or receive). If this period is exceeded without activity, the connection is considered dead and must reconnect.

### Configuration

**Source:** `jasmin/protocols/smpp/configs.py:74–76`

| Property | Value | Type | Unit | Default |
|----------|-------|------|------|---------|
| `inactivityTimerSecs` | User-configurable | int/float | seconds | **300** |
| Validation | Must be int or float | — | — | — |

**Extracted Contract:**

```
INACTIVITY TIMER CONTRACT:

INPUT:
  config.inactivityTimerSecs: 300 (default, configurable)
  
RULE:
  If NO SMPP PDU (in or out) is transmitted for inactivityTimerSecs:
  - Connection is considered DEAD
  - Trigger immediate reconnect (no backoff delay)
  
OUTPUT:
  Automatic reconnection on inactivity

BOUNDARY CONDITIONS:
  - inactivityTimerSecs = 0: Inactivity check disabled? (TBD)
  - inactivityTimerSecs = 1: Very aggressive (reconnect every 1 second if no activity)
  - inactivityTimerSecs < enquireLinkTimerSecs: Enquire link prevents inactivity timeout
  - inactivityTimerSecs > enquireLinkTimerSecs: Enquire link refreshes inactivity timer
  
INTERACTION WITH OTHER TIMERS:
  - Resets on ANY SMPP activity (in or out)
  - Resets when sending enquire_link
  - Resets when receiving enquire_link_resp
  - Does NOT reset on local timers or connection events
```

### Base Class Behavior

**Assumed from base class** (smpp.twisted.protocol, verify):
- Timer started when session enters BOUND state
- Timer is reactor.callLater(inactivityTimerSecs, onInactivityTimeout)
- Any PDU activity (send or receive) resets the timer
- On timeout: close connection and reconnect

### Interaction with Enquire Link

```
SCENARIO: inactivityTimerSecs = 300, enquireLinkTimerSecs = 30

No activity for 300 seconds:
  T=0: BOUND, both timers active
  T=30: No activity for 30 sec → send enquire_link (resets inactivity timer to 0)
  T=60: No activity since T=30 → send enquire_link (resets inactivity timer to 0)
  T=...: Enquire link continues every 30 seconds
  
  → Inactivity timer never fires (enquire link keeps resetting it)
  
SCENARIO: inactivityTimerSecs = 300, enquireLinkTimerSecs = 400

No activity for 400+ seconds:
  T=0: BOUND, both timers active
  T=300: No activity for 300 sec → INACTIVITY TIMEOUT FIRES
  → Close connection, reconnect
  
  → Enquire link never sent (inactivity timer fires first)
```

**Pseudocode: Inactivity Timer Lifecycle**

```
INACTIVITY TIMER PSEUDOCODE:

INITIALIZATION (when entering BOUND state):
  inactivity_timer := reactor.callLater(config.inactivityTimerSecs, onInactivityTimeout)
  last_activity_at := datetime.now()

ON ANY SMPP ACTIVITY (send or receive):
  if inactivity_timer is active:
    inactivity_timer.cancel()
    inactivity_timer := reactor.callLater(config.inactivityTimerSecs, onInactivityTimeout)
    last_activity_at := datetime.now()

ON TIMEOUT (onInactivityTimeout):
  elapsed := datetime.now() - last_activity_at
  if elapsed >= config.inactivityTimerSecs:
    log.error("Inactivity timeout after %s seconds" % elapsed)
    connection.close()
    triggerReconnect(delay=config.reconnectOnConnectionLossDelay)
```

---

## SM-004: Response Timer Implementation

### Overview
Response Timer sets a maximum time to wait for any PDU response (submit_sm_resp, bind_resp, etc.). Each outbound transaction (request) gets its own response timer.

### Configuration

**Source:** `jasmin/protocols/smpp/configs.py:79–82` (Client) and configs.py:270 (Server)

| Property | Value | Type | Unit | Default (Client) | Default (Server) |
|----------|-------|------|------|------------------|------------------|
| `responseTimerSecs` | User-configurable | int/float | seconds | **120** | **60** |
| Validation | Must be int or float | — | — | — | — |

**Extracted Contract:**

```
RESPONSE TIMER CONTRACT:

INPUT:
  config.responseTimerSecs: 120 (default, configurable per PDU)
  outbound_transaction = (request_pdu, seqNum, timeout_secs)
  
RULE:
  For each outbound request PDU:
  1. Claim a sequence number
  2. Start a response timer = responseTimerSecs
  3. Wait for response PDU with matching seqNum
  4. On response: cancel timer, call callback
  5. On timeout: errback with SMPPRequestTimoutError
  
OUTPUT:
  - PDU response correlation by seqNum
  - Timeout-based error handling for unresponsive peer
  
BOUNDARY CONDITIONS:
  - responseTimerSecs = 0: No timeout (wait forever)? TBD
  - responseTimerSecs = 1: Very aggressive timeout
  - responseTimerSecs = 120: Default (2 minutes)
  
TIMEOUT BEHAVIOR:
  - Exception: SMPPRequestTimoutError
  - Connection state: May trigger reconnect (TBD)
  - Message queueing: Rejected message requeued after requeue_delay (TBD)
```

### Transaction Correlation

**Source:** `jasmin/protocols/smpp/protocol.py:136–157` (endOutboundTransaction)

```python
def endOutboundTransaction(self, respPDU):
    txn = self.closeOutboundTransaction(respPDU.seqNum)  # Look up by seqNum
    
    if txn is not None:
        # Any status of SubmitSMResp must be handled as a normal status
        if isinstance(txn.request, SubmitSM) or respPDU.status == CommandStatus.ESME_ROK:
            if not isinstance(respPDU, txn.request.requireAck):
                # ERROR: Wrong response type
                txn.ackDeferred.errback(
                    SMPPProtocolError,
                    "Invalid PDU response type [%s] returned for request type [%s]" % (
                        type(respPDU), type(txn.request)))
                return
            # Do callback with result
            txn.ackDeferred.callback(SMPPOutboundTxnResult(self, txn.request, respPDU))
            return
        
        if isinstance(respPDU, GenericNack):
            txn.ackDeferred.errback(SMPPGenericNackTransactionError(respPDU, txn.request))
            return
        
        txn.ackDeferred.errback(SMPPTransactionError(respPDU, txn.request))
```

**Extracted Contract — Correlation:**

| Step | Operation | Error Handling |
|------|-----------|-----------------|
| 1 | Look up transaction by `respPDU.seqNum` | If not found: log error, drop response |
| 2 | Verify response type matches request type | If mismatch: errback with SMPPProtocolError |
| 3 | For SubmitSM: accept ANY status (including errors) | If status != ESME_ROK and not SubmitSM: error |
| 4 | For GenericNack: errback with SMPPGenericNackTransactionError | — |
| 5 | For other errors: errback with SMPPTransactionError | — |
| 6 | On success: callback with SMPPOutboundTxnResult | Includes request, response, protocol |

**Pseudocode: Response Timer & Correlation**

```
RESPONSE TIMER PSEUDOCODE:

class SMPPClientProtocol:
    outbound_transactions = {}  # seqNum -> Transaction
    
    def doSendRequest(pdu, timeout=config.responseTimerSecs):
        seqNum := claimSeqNum()
        pdu.seqNum := seqNum
        
        ackDeferred := Deferred()
        timer := reactor.callLater(timeout, onResponseTimeout, seqNum, timeout)
        
        txn := SMPPOutboundTxn(pdu, timer, ackDeferred)
        outbound_transactions[seqNum] := txn
        
        sendPDU(pdu)
        return ackDeferred
    
    def onResponseTimeout(seqNum, timeout):
        txn := outbound_transactions.pop(seqNum, None)
        if txn is not None:
            errMsg := "Response timeout after %d secs for PDU %s" % (timeout, txn.request)
            txn.ackDeferred.errback(SMPPRequestTimoutError(errMsg))
        else:
            log.error("Timeout for unknown transaction seqNum=%d" % seqNum)
    
    def PDUResponseReceived(respPDU):
        # Called by Twisted when response arrives
        endOutboundTransaction(respPDU)
    
    def endOutboundTransaction(respPDU):
        seqNum := respPDU.seqNum
        txn := outbound_transactions.pop(seqNum, None)  # Remove by seqNum
        
        if txn is None:
            log.error("Response to unknown transaction seqNum=%d" % seqNum)
            return
        
        # Cancel timer (already fired or still pending)
        if txn.timer is not None and txn.timer.active():
            txn.timer.cancel()
        
        # Type check
        if not isinstance(respPDU, txn.request.requireAck):
            txn.ackDeferred.errback(
                SMPPProtocolError,
                "Response type %s does not match request %s" % (
                    type(respPDU), txn.request.requireAck))
            return
        
        # Status handling
        if isinstance(respPDU, GenericNack):
            txn.ackDeferred.errback(SMPPGenericNackTransactionError(respPDU, txn.request))
        elif isinstance(txn.request, SubmitSM):
            # SubmitSM accepts ALL statuses (including ESME_RTHROTTLED, etc.)
            txn.ackDeferred.callback(SMPPOutboundTxnResult(self, txn.request, respPDU))
        elif respPDU.status != CommandStatus.ESME_ROK:
            # Other PDU types: only ESME_ROK is success
            txn.ackDeferred.errback(SMPPTransactionError(respPDU, txn.request))
        else:
            txn.ackDeferred.callback(SMPPOutboundTxnResult(self, txn.request, respPDU))
```

---

## SM-005: Error Handling & ESME Error Code Mapping

### ESME Error Codes Observed

**Source:** `jasmin/protocols/smpp/protocol.py` (stats tracking for errors)

| ESME Code | Name | Handling in Jasmin | Source |
|-----------|------|-------------------|--------|
| ESME_ROK (0) | OK | Success callback | protocol.py:74–75 |
| ESME_RTHROTTLED | Throttling error | Increment `throttling_error_count` | protocol.py:73 |
| Other (1–15, 255) | Various errors | Increment `other_submit_error_count` | protocol.py:75 |

**Extracted Contract — Error Code Mapping:**

```
SUBMIT_SM_RESP ERROR HANDLING:

When submit_sm_resp received:
  if status == ESME_ROK:
    stats['submit_sm_count'] += 1
    callback() with message_id
    
  else if status == ESME_RTHROTTLED (0x58):
    stats['throttling_error_count'] += 1
    errback() with status
    client-side REQUEUE after requeue_delay (TBD)
    
  else:  # All other error codes
    stats['other_submit_error_count'] += 1
    errback() with status
    client-side REQUEUE after requeue_delay (TBD)

IMPORTANT:
  - SubmitSM PDU accepts ANY status code (line 139)
  - Error status triggers callback, not errback (unlike other PDU types)
  - Caller is responsible for interpreting status code
```

**Source:** `jasmin/protocols/smpp/protocol.py:69–76` (PDUResponseReceived stats)

```python
def PDUResponseReceived(self, pdu):
    twistedSMPPClientProtocol.PDUResponseReceived(self, pdu)
    
    if pdu.commandId == CommandId.submit_sm_resp:
        if pdu.status == CommandStatus.ESME_RTHROTTLED:
            self.factory.stats.inc('throttling_error_count')
        elif pdu.status != CommandStatus.ESME_ROK:
            self.factory.stats.inc('other_submit_error_count')
        else:
            # We got a ESME_ROK
            self.factory.stats.inc('submit_sm_count')
```

**Pseudocode: Error Handling**

```
ERROR HANDLING PSEUDOCODE:

SUBMIT_SM_RESP PROCESSING:

def PDUResponseReceived(respPDU):
    if respPDU.commandId == CommandId.submit_sm_resp:
        
        if respPDU.status == CommandStatus.ESME_ROK:
            stats['submit_sm_count'] += 1
            return callback(message_id=respPDU.message_id)
        
        elif respPDU.status == CommandStatus.ESME_RTHROTTLED:
            stats['throttling_error_count'] += 1
            # Caller must handle requeue logic
            return errback(ESME_RTHROTTLED)
        
        else:
            stats['other_submit_error_count'] += 1
            return errback(respPDU.status)
    
    # For other PDU types (bind_resp, enquire_link_resp, etc.):
    if respPDU.status != CommandStatus.ESME_ROK:
        if isinstance(respPDU, GenericNack):
            return errback(SMPPGenericNackTransactionError)
        else:
            return errback(SMPPTransactionError)
    else:
        return callback(respPDU)
```

---

## SM-006: Long Submit_SM Transaction Lifecycle

### Overview
When a message exceeds single PDU size, Jasmin splits it into multiple submit_sm PDUs and tracks them with a Long Submit SM Transaction. All parts must receive responses before the message is complete.

### Configuration

**Source:** `jasmin/protocols/smpp/operations.py:36–37`

| Property | Value | Type | Default |
|----------|-------|------|---------|
| `long_content_max_parts` | Max segments per long message | int | **5** |
| `long_content_split` | Split method (sar or udh) | string | **sar** |

### Transaction Structure

**Source:** `jasmin/protocols/smpp/protocol.py:185–197` (startLongSubmitSmTransaction)

```python
def startLongSubmitSmTransaction(self, reqPDU, timeout):
    ackDeferred = defer.Deferred()
    timer = reactor.callLater(timeout, self.onLongSubmitSmTransactionTimeout, reqPDU, timeout)
    
    self.longSubmitSmTxns[reqPDU.LongSubmitSm['msg_ref_num']] = {
        'txn': SMPPOutboundTxn(reqPDU, timer, ackDeferred),
        'nack_count': reqPDU.LongSubmitSm['total_segments']
    }
    
    return ackDeferred
```

**Extracted Contract — Long Transaction:**

| Field | Meaning |
|-------|---------|
| `msg_ref_num` | Unique reference for all parts of message (0–255, wraps) |
| `total_segments` | Total number of parts in message (1–255) |
| `segment_seqnum` | Current part number (1 to total_segments) |
| `nack_count` | Pending ACKs (decrements per response, fires when 0) |
| `timer` | Per-transaction timeout (all parts must respond within this time) |

### Lifecycle Pseudocode

```
LONG SUBMIT_SM TRANSACTION PSEUDOCODE:

SENDING (multiple parts):

msg_ref_num := claimLongMsgRefNum()  # 1–255, wraps at 256
total_segments := calculateSegments(message_length)
timeout := config.responseTimerSecs

for i = 1 to total_segments:
    segment_seqnum := i
    pdu_part := createSubmitSmPdu(message[i])
    
    pdu_part.seqNum := claimSeqNum()  # Unique per part
    pdu_part.LongSubmitSm = {
        'msg_ref_num': msg_ref_num,
        'total_segments': total_segments,
        'segment_seqnum': segment_seqnum
    }
    
    # Set SAR (Segmentation and Reassembly) options OR UDH (User Data Header)
    if long_content_split == 'sar':
        pdu_part.sar_msg_ref_num := msg_ref_num
        pdu_part.sar_total_segments := total_segments
        pdu_part.sar_segment_seqnum := segment_seqnum
    elif long_content_split == 'udh':
        pdu_part.esm_class.UDHI_INDICATOR_SET := True
        pdu_part.short_message := UDH_HEADER + message_part
    
    sendPDU(pdu_part)
    
    # Start transaction for this part
    txn_part := startOutboundTransaction(pdu_part, timeout=timeout)
    txn_part.addCallback(endLongSubmitSmTransaction)
    txn_part.addErrback(endLongSubmitSmTransactionErr)

# Start the long transaction after first part
if i == 1:
    ackDeferred_long := startLongSubmitSmTransaction(pdu_part, timeout=timeout)

RESPONDING (per part):

def endLongSubmitSmTransaction(result):
    respPDU := result.response
    msg_ref_num := respPDU.seqNum → look up in txn map
    
    if msg_ref_num not in longSubmitSmTxns:
        error("Response for unknown msg_ref_num=%d" % msg_ref_num)
        return
    
    # Decrement pending ACKs
    nack_count := longSubmitSmTxns[msg_ref_num]['nack_count']
    nack_count := nack_count - 1
    longSubmitSmTxns[msg_ref_num]['nack_count'] := nack_count
    
    # All parts received?
    if nack_count == 0:
        txn := closeLongSubmitSmTransaction(msg_ref_num)
        txn.ackDeferred.callback(result)  # Callback on long txn
        return
    
    # More parts pending — continue waiting

def onLongSubmitSmTransactionTimeout(reqPDU, timeout):
    msg_ref_num := reqPDU.LongSubmitSm['msg_ref_num']
    txn := closeLongSubmitSmTransaction(msg_ref_num)
    errMsg := "Long submit_sm transaction timed out after %s secs" % timeout
    txn.ackDeferred.errback(SMPPRequestTimoutError(errMsg))

CLEANUP (on timeout or error):

def closeLongSubmitSmTransaction(msg_ref_num):
    txn := longSubmitSmTxns[msg_ref_num]['txn']
    del longSubmitSmTxns[msg_ref_num]
    
    # Cancel response timer
    if txn.timer is not None and txn.timer.active():
        txn.timer.cancel()
    
    return txn
```

**Boundary Conditions:**
- `msg_ref_num` wraps at 256 (0–255 → 0)
- All parts must respond within `responseTimerSecs` (NOT per-part timeout, but entire transaction)
- If ONE part times out, entire long message fails
- If ONE part receives error status, **caller** determines if full message failed (TBD)

---

## SM-007: Transaction Timeout & Recovery Logic

### Timeout Scenario 1: Bind Timeout

**Source:** `jasmin/protocols/smpp/configs.py:62–65` (sessionInitTimerSecs)

```
BIND TIMEOUT CONTRACT:

config.sessionInitTimerSecs = 30 (default)

Timeline:
  T=0: Send bind_transmitter PDU
  T=30: No bind_resp received → SMPPSessionInitTimoutError
  
ERROR HANDLING:
  - raise SMPPSessionInitTimoutError
  - Call bindFailed(reason)
  - Disconnect immediately
  - Trigger reconnect with delay
```

**Source:** `jasmin/protocols/smpp/protocol.py:130–134` (bindFailed)

```python
def bindFailed(self, reason):
    self.log.error("Bind failed [%s]. Disconnecting...", reason)
    self.disconnect()
    if reason.check(SMPPRequestTimoutError):
        raise SMPPSessionInitTimoutError(str(reason))
```

### Timeout Scenario 2: Submit SM Timeout

**Source:** `jasmin/protocols/smpp/configs.py:79–82` (responseTimerSecs)

```
SUBMIT_SM TIMEOUT CONTRACT:

config.responseTimerSecs = 120 (default, 2 minutes)

Timeline:
  T=0: Send submit_sm PDU with seqNum=42
  T=120: No submit_sm_resp received → SMPPRequestTimoutError
  
ERROR HANDLING:
  - Errback transaction with SMPPRequestTimoutError
  - Remove from outbound_transactions[42]
  - Caller receives errback → requeue message
  - Connection remains BOUND (unless error cascade)
```

### Timeout Scenario 3: Enquire Link Timeout

```
ENQUIRE LINK TIMEOUT CONTRACT:

config.enquireLinkTimerSecs = 30
config.responseTimerSecs = 120

Timeline:
  T=30: No activity → send enquire_link with seqNum=55
  T=150: No enquire_link_resp received → SMPPRequestTimoutError
  
ERROR HANDLING:
  - Errback transaction
  - Log error: "No response to keep-alive"
  - Trigger connection close and reconnect
  - Connection considered DEAD
```

### Timeout Scenario 4: Long Submit SM Timeout

**Source:** `jasmin/protocols/smpp/protocol.py:200–206` (onLongSubmitSmTransactionTimeout)

```python
def onLongSubmitSmTransactionTimeout(self, reqPDU, timeout):
    txn = self.closeLongSubmitSmTransaction(reqPDU.LongSubmitSm['msg_ref_num'])
    errMsg = 'Long submit_sm transaction timed out after %s secs: %s' % (timeout, reqPDU)
    txn.ackDeferred.errback(SMPPRequestTimoutError(errMsg))
```

```
LONG SUBMIT_SM TIMEOUT CONTRACT:

config.responseTimerSecs = 120
message split into 3 parts (SAR)

Timeline:
  T=0: Send part 1 (seqNum=10, msg_ref=5, seg=1/3)
  T=5: Send part 2 (seqNum=11, msg_ref=5, seg=2/3)
  T=10: Send part 3 (seqNum=12, msg_ref=5, seg=3/3)
  T=20: Receive submit_sm_resp for part 1 (seqNum=10, msg_ref=5)
  T=30: Receive submit_sm_resp for part 2 (seqNum=11, msg_ref=5)
  T=130: Part 3 response not received → TIMEOUT FIRES
  
ERROR HANDLING:
  - Errback long transaction for msg_ref=5
  - All three parts failed (even though two received)
  - Message not persisted (all or nothing semantics)
  - Caller receives single errback for entire message
```

### Recovery: Reconnect with Backoff

**Source:** `jasmin/protocols/smpp/factory.py:107–123` (reConnect)

```python
def clientConnectionFailed(self, connector, reason):
    self.log.error("Connection failed. Reason: %s", str(reason))
    
    if self.config.reconnectOnConnectionFailure and self.connectionRetry:
        self.log.info("Reconnecting after %d seconds ...",
                      self.config.reconnectOnConnectionFailureDelay)
        self.reconnectTimer = reactor.callLater(
            self.config.reconnectOnConnectionFailureDelay, self.reConnect, connector)
    else:
        self.connectDeferred.errback(reason)
        self.exitDeferred.callback(None)
        self.log.info("Exiting.")
```

```
RECONNECT RECOVERY CONTRACT:

On connection failure:
  delay := config.reconnectOnConnectionFailureDelay (default 10 seconds)
  if config.reconnectOnConnectionFailure == True:
    reactor.callLater(delay, reConnect)
  else:
    factory.exitDeferred.callback(None)
    exit

On connection lost:
  delay := config.reconnectOnConnectionLossDelay (default 10 seconds)
  if config.reconnectOnConnectionLoss == True:
    reactor.callLater(delay, reConnect)
  else:
    factory.exitDeferred.callback(None)
    exit

BACKOFF STRATEGY:
  - Fixed delay (NOT exponential backoff)
  - Default: 10 seconds for both loss and failure
  - No max retry limit (will retry forever if enabled)
  - connectionRetry flag can disable retries (e.g., on service stop)
```

### Sequence Number Cleanup on Reconnect

```
SEQUENCE NUMBER CLEANUP ON RECONNECT:

Timeline:
  T=0: BOUND, claimSeqNum() → seqNum=1,2,3,...
  T=50: Connection lost
  T=60: Reconnect delay expires
  T=65: New connection established, BIND sent
  T=95: New BIND response received
  T=100: Now BOUND again
  
QUESTION: Is seqNum reset to 1 or continued from previous value?

ANSWER: TBD (must audit smpp.twisted.protocol base class)
  - If seqNum persists: old seqNum=5 may collide with new txn seqNum=5
  - If seqNum resets: both behaviors are possible

IMPLICATION FOR TRANSACTION CORRELATION:
  - If seqNum collides, response might be matched to WRONG transaction
  - Must verify base class resets seqNum on OPEN → BOUND transition
```

---

## Summary Table: Timers & Their Interactions

| Timer | Config | Default | Purpose | Resets On | Triggers |
|-------|--------|---------|---------|-----------|----------|
| Session Init | `sessionInitTimerSecs` | 30s | Bind response timeout | bind_resp | reconnect on timeout |
| Enquire Link | `enquireLinkTimerSecs` | 30s | Send keep-alive PDU | any activity OR elink_resp | send enquire_link |
| Inactivity | `inactivityTimerSecs` | 300s | No PDU activity allowed | any activity | close & reconnect |
| Response | `responseTimerSecs` | 120s (client) / 60s (server) | Wait for response to request | response received | errback txn on timeout |
| PDU Read | `pduReadTimerSecs` | 10s | Read single PDU bytes | PDU complete | close & reconnect on timeout |

---

## Key Questions for Go Implementation

1. **Sequence Number Reset:** Does seqNum reset on reconnect or continue?
2. **Collision Handling:** What if seqNum wraps before old response received?
3. **Enquire Link Reset:** Does enquire_link reset inactivity timer?
4. **Long Txn Per-Part Timeout:** Is timeout per-part or per-entire-long-message?
5. **Requeue Logic:** Where does requeue happen for ESME_RTHROTTLED?
6. **Backoff Strategy:** Should fixed delay be replaced with exponential backoff?

---

## References & Related Contracts

- **Legacy SMPP Connector Config:** SC-001 (config fields, validation)
- **Lifecycle Operations:** SC-002 (add, remove, start, stop)
- **Reconnect Logic:** SC-003 (delay, retry limits, state reporting)
- **Failover Routing:** SC-007 (multi-connector selection)
- **Base Library:** smpp.twisted.protocol (verify all inherited behavior)

---

## Validation Checklist

- [ ] Sequence number generation tested (normal, wrap-around, collision)
- [ ] Enquire link sent and response handled
- [ ] Inactivity timeout fires and reconnects
- [ ] Response timeout fires and errbacks transaction
- [ ] Long submit_sm transaction completes all parts
- [ ] Long submit_sm transaction times out if any part missing
- [ ] Reconnect delay applied and connection re-established
- [ ] Stats tracked correctly (last_seqNum, elink_count, throttling_error_count)
- [ ] ESME error codes mapped correctly (ESME_ROK, ESME_RTHROTTLED, others)
- [ ] No seqNum collisions after reconnect
- [ ] No transaction memory leaks (all txns cleaned up)

---

## File Map

| File | Lines | Content |
|------|-------|---------|
| `jasmin/protocols/smpp/configs.py` | 32–223 | Configuration schema & validation |
| `jasmin/protocols/smpp/protocol.py` | 1–400 | Client & server protocol handlers |
| `jasmin/protocols/smpp/operations.py` | 1–400 | PDU creation (SubmitSM splitting, SAR/UDH) |
| `jasmin/protocols/smpp/factory.py` | 1–680 | Factory & lifecycle management |
| `jasmin/protocols/smpp/services.py` | 1–54 | Service wrappers |
| `smpp.twisted.protocol` | (external) | Base class (inherited) |

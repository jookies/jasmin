# Code Snippets: Expiry/Validity Handling in Jasmin Legacy

## 1. SMPPClientSMListener Expiry Check ✅ IMPLEMENTED

**File**: `jasmin/managers/listeners.py` (lines 182-188)

```python
# Verify if message is a SubmitSm PDU
if isinstance(SubmitSmPDU, SubmitSM) is False:
    self.log.error(
        "Received object[%s] is not an instance of SubmitSm: discarding this unknown object from queue",
        msgid)
    yield self.rejectMessage(message)
    defer.returnValue(False)

# If the message has expired in the queue
if 'headers' in message.content.properties and 'expiration' in message.content.properties['headers']:
    expiration_datetime = parser.parse(message.content.properties['headers']['expiration'])
    if expiration_datetime < datetime.now():
        self.log.info(
            "Discarding expired message[%s]: expiration is %s", msgid, expiration_datetime)
        yield self.rejectMessage(message)
        defer.returnValue(False)
```

**Key Points**:
- Checks for `expiration` key in message headers
- Parses using `dateutil.parser.parse()` (handles ISO 8601)
- Direct datetime comparison: `expiration_datetime < datetime.now()`
- Terminal action: `rejectMessage()` without requeue
- Logs at INFO level with msgid and timestamp

---

## 2. RouterPB.deliver_sm_callback ❌ MISSING EXPIRY CHECK

**File**: `jasmin/routing/router.py` (lines 154-222)

```python
@defer.inlineCallbacks
def deliver_sm_callback(self, message):
    """This callback is a queue listener
    It will only decide where to send the input message and republish it to the routedConnector
    The consumer will execute the remaining job of final delivery
    c.f. test_router.DeliverSmDeliveryTestCases for use cases
    """
    msgid = message.content.properties['message-id']
    scid = message.content.properties['headers']['connector-id']
    concatenated = message.content.properties['headers']['concatenated']
    will_be_concatenated = message.content.properties['headers']['will_be_concatenated']
    routable = pickle.loads(message.content.body)
    self.log.debug("Callbacked a deliver_sm with a DeliverSmPDU[%s] (?): %s", msgid, routable.pdu)

    # @todo: Implement MO throttling here, same as in
    # jasmin.managers.listeners.SMPPClientSMListener.submit_sm_callback
    self.deliver_sm_q.get().addCallback(self.deliver_sm_callback).addErrback(self.deliver_sm_errback)

    # ⚠️  NO EXPIRY CHECK HERE! Should add:
    # if 'expiration' in message.content.properties['headers']:
    #     expiration_datetime = parser.parse(message.content.properties['headers']['expiration'])
    #     if expiration_datetime < datetime.now():
    #         self.log.info("Discarding expired DeliverSmPDU[%s]: expiration is %s", msgid, expiration_datetime)
    #         yield self.rejectMessage(message)
    #         return

    # Routing
    route = self.getMORoutingTable().getRouteFor(routable)
    if route is None:
        self.log.info("No route matched this DeliverSmPDU with scid:%s and msgid:%s", scid, msgid)
        yield self.rejectMessage(message)
    else:
        # ... route to thrower
```

**Gap**: Comment on line 167-168 hints at known gap ("Implement MO throttling here")

---

## 3. HTTP Send Endpoint: Validity Period Handling

**File**: `jasmin/protocols/http/endpoints/send.py` (lines 280-287)

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

**Details**:
- Input: `validity-period` query parameter (minutes, integer)
- Conversion: `datetime.today() + timedelta(minutes=N)` → absolute datetime
- Output: Sets `routable.pdu.params['validity_period']` (datetime object)
- Logging: Debug level with both absolute and relative times

---

## 4. Validity Period Propagation: PDU → AMQP

**File**: `jasmin/managers/clients.py` (lines 548-594)

```python
@defer.inlineCallbacks
def perspective_submit_sm(self, uid, cid, SubmitSmPDU, submit_sm_bill, priority=1, validity_period=None,
                          pickled=True, dlr_url=None, dlr_level=1, dlr_method='POST', dlr_connector=None,
                          source_connector='httpapi'):
    """This will enqueue a submit_sm to a connector
    """
    # ... validation ...

    # Publishing a pickled PDU
    self.log.debug('Publishing SubmitSmPDU with routing_key=%s, priority=%s', pubQueueName, priority)
    c = SubmitSmContent(
        uid=uid,
        body=PickledSubmitSmPDU,
        replyto=responseQueueName,
        submit_sm_bill=submit_sm_bill,
        priority=priority,
        expiration=validity_period,  # <-- Converts PDU.validity_period to AMQP expiration header
        source_connector='httpapi' if source_connector == 'httpapi' else 'smppsapi',
        destination_cid=cid)
    yield self.amqpBroker.publish(exchange='messaging', routing_key=pubQueueName, content=c)
```

**Key Point**: `validity_period` parameter (datetime or string) → `expiration` AMQP header

---

## 5. SubmitSmContent: AMQP Header Construction

**File**: `jasmin/managers/content.py` (lines 146-171)

```python
class SubmitSmContent(PDU):
    """A SMPP SubmitSm Content"""

    def __init__(self, uid, body, replyto, submit_sm_bill=None, priority=1, expiration=None, msgid=None,
                 source_connector='httpapi', destination_cid=None):
        props = {}

        props['priority'] = priority
        props['message-id'] = msgid
        props['reply-to'] = replyto

        props['headers'] = {'source_connector': source_connector}
        if submit_sm_bill is not None:
            props['headers']['submit_sm_bill'] = submit_sm_bill
        if expiration is not None:
            props['headers']['expiration'] = expiration  # <-- Stores as-is

        PDU.__init__(self, body, properties=props)
```

**Details**:
- Receives `expiration` parameter (datetime or string from validity_period)
- Stores directly in `properties['headers']['expiration']`
- No transformation; passes through as-is
- Default: omitted from headers if None

---

## 6. SMPPClientManagerPBProxy: Validity Period Format

**File**: `jasmin/managers/proxies.py` (lines 91-106)

```python
@ConnectedPB
def submit_sm(self, cid, SubmitSmPDU, uid, submit_sm_bill=None):
    if not isinstance(SubmitSmPDU, SubmitSM):
        raise Exception("SubmitSmPDU is not an instance of SubmitSm")
    if submit_sm_bill is not None and not isinstance(submit_sm_bill, SubmitSmBill):
        raise Exception("submit_sm_bill is not an instance of SubmitSmBill")
    if submit_sm_bill is not None:
        submit_sm_bill = self.pickle(submit_sm_bill)

    # Set the message priority
    if SubmitSmPDU.params['priority_flag'] is not None:
        priority_flag = SubmitSmPDU.params['priority_flag']._value_
    else:
        priority_flag = 0

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

**Format Conversion**:
- Input: `datetime` object
- Format: `'%Y-%m-%d %H:%M:%S'` (no timezone, no 'T' separator)
- Output: String like `"2026-07-19 03:09:05"`

---

## 7. Validity Period in HTTP Logging

**File**: `jasmin/managers/listeners.py` (lines 360-375)

```python
self.log.info(
    "SMS-MT [cid:%s] [queue-msgid:%s] [smpp-msgid:%s] [status:%s] [prio:%s] [dlr:%s] [validity:%s] \
[from:%s] [to:%s] [content:%s] [tlvs:%s]",
    self.SMPPClientFactory.config.id,
    msgid,
    r.response.params['message_id'],
    r.response.status,
    amqpMessage.content.properties['priority'],
    _pdu.params['registered_delivery'].receipt,
    'none' if ('headers' not in amqpMessage.content.properties or
               'expiration' not in amqpMessage.content.properties['headers'])
    else amqpMessage.content.properties['headers']['expiration'],
    _pdu.params['source_addr'],
    _pdu.params['destination_addr'],
    logged_content,
    format_tlvs_for_log(_pdu, self.config.log_privacy))
```

**Note**: Field is named `expiration` in header but logged as `validity`

---

## 8. DeliverSmThrower: Validity Period Forwarding

**File**: `jasmin/routing/throwers.py` (lines 287-289)

```python
# Build optional arguments
if ('priority_flag' in RoutedDeliverSmContent.params and
            RoutedDeliverSmContent.params['priority_flag'] is not None):
    args['priority'] = priority_flag_name_map[RoutedDeliverSmContent.params['priority_flag'].name]
if ('data_coding' in RoutedDeliverSmContent.params and
            RoutedDeliverSmContent.params['data_coding'] is not None):
    args['coding'] = DataCodingEncoder().encode(RoutedDeliverSmContent.params['data_coding'])
if ('validity_period' in RoutedDeliverSmContent.params and
            RoutedDeliverSmContent.params['validity_period'] is not None):
    args['validity'] = RoutedDeliverSmContent.params['validity_period']  # <-- Forwards to HTTP as 'validity'
```

**Details**:
- Extracts `validity_period` from PDU
- Maps to HTTP parameter name `validity`
- Forwards to HTTP endpoint for final delivery
- ⚠️ No check for message expiration on queue consumption

---

## 9. Authorization Check: set_validity_period

**File**: `jasmin/protocols/http/validation.py` (lines 94, 157-163)

```python
if (self.submit_sm and
    not self.user.mt_credential.getAuthorization('set_validity_period')):
    # ... raise validation error ...

# Filtering validity_period
if (self.submit_sm and
    not self.user.mt_credential.getAuthorization('set_validity_period')):
    _r = self._get_binary_r('validity_period')
    if _r is not None and not self.user.mt_credential.getFilter('validity_period').match(_r):
        raise UrlArgsValidationError(
            'Value filter failed for user [%s] (validity_period filter mismatch).' % self.user)
```

**Details**:
- User must have `set_validity_period` authorization
- Filter: numeric pattern `^\d+$` (minutes)

---

## Summary of Data Flow

```
HTTP API
  ↓
  validity-period (minutes, integer)
  ↓
HTTP Send Endpoint (send.py)
  ↓
  datetime.today() + timedelta(minutes=N)
  ↓
SubmitSmPDU.params['validity_period'] (datetime object)
  ↓
SMPPClientManagerPB.perspective_submit_sm()
  ↓
  .strftime('%Y-%m-%d %H:%M:%S')
  ↓
SubmitSmContent (expiration parameter)
  ↓
  AMQP Header: 'expiration' = '2026-07-19 03:09:05'
  ↓
SMPPClientSMListener.submit_sm_callback()
  ↓
  parser.parse() → datetime comparison
  ↓
  [IF EXPIRED] rejectMessage() → discarded
  [IF VALID] sendRequest() → SMPP send
```

---

## Time Format Reference

| Context | Format | Example | Parser |
|---------|--------|---------|--------|
| HTTP Input | Minutes (int) | `60` | `int()` |
| PDU Object | datetime | `datetime(2026, 7, 19, 4, 9, 5)` | N/A |
| AMQP Header | String ISO | `"2026-07-19 03:09:05"` | `dateutil.parser.parse()` |
| Proxy Conversion | String ISO | `"2026-07-19 03:09:05"` | `.strftime('%Y-%m-%d %H:%M:%S')` |
| Comparison | datetime | `expiration_datetime < datetime.now()` | Python builtin |

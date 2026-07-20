# Quick Reference: Expiry/Validity Behaviors in Jasmin Legacy

## Summary Table

| Component | File | Check | Format | Action | Logs |
|-----------|------|-------|--------|--------|------|
| **SMPPClientSMListener** | `managers/listeners.py:182-188` | ✅ IMPLEMENTED | ISO 8601 string | Reject+discard | Info-level |
| **RouterPB** | `routing/router.py` | ❌ MISSING | N/A | Routes all | N/A |
| **deliverSmThrower** | `routing/throwers.py` | ❌ MISSING | N/A | Routes all | N/A |
| **HTTP Send** | `protocols/http/endpoints/send.py:280-287` | ⚠️ ENTRY ONLY | Minutes→datetime | Enqueue all | Debug-level |

## Critical Code Locations

### ✅ Expiry Check (SMPPClientSMListener)
```python
# jasmin/managers/listeners.py:182-188
if 'headers' in message.content.properties and 'expiration' in message.content.properties['headers']:
    expiration_datetime = parser.parse(message.content.properties['headers']['expiration'])
    if expiration_datetime < datetime.now():
        self.log.info("Discarding expired message[%s]: expiration is %s", msgid, expiration_datetime)
        yield self.rejectMessage(message)
```

### ❌ Router Gap (RouterPB.deliver_sm_callback)
```python
# jasmin/routing/router.py:154-222
# Does NOT check expiration; routes all messages
# Missing A-011 requirement
```

### ⚠️ Validity Period Propagation (HTTP→SMPP)
```python
# jasmin/managers/clients.py:548-594
c = SubmitSmContent(..., expiration=validity_period, ...)

# jasmin/managers/proxies.py:92-106
validity_period = SubmitSmPDU.params['validity_period'].strftime('%Y-%m-%d %H:%M:%S')
```

## Time Format Conversions

**HTTP Input** → `validity-period` (minutes, integer)  
**PDU Layer** → `validity_period` (absolute datetime)  
**AMQP Header** → `expiration` (ISO string: `'%Y-%m-%d %H:%M:%S'`)  
**SMPPClientSMListener Check** → Parse & compare: `expiration_datetime < datetime.now()`

## Asymmetry Problem

| Layer | Inbound | Check | Outbound |
|-------|---------|-------|----------|
| HTTP API | validity-period (min) | ❌ No | Sets PDU.validity_period |
| Client Manager | PDU.validity_period | ❌ No | AMQP expiration header |
| SMPP Listener | AMQP expiration header | ✅ **Yes** | Rejects if expired |
| Router | AMQP message | ❌ No | Routes to thrower |
| Thrower | AMQP message | ❌ No | Forwards to HTTP/SMPP |

**Gap**: Router and Throwers should check expiry per A-011.

## Logging Pattern

```python
# Expired message log (SMPPClientSMListener)
"Discarding expired message[{msgid}]: expiration is {expiration_datetime}"

# Success log includes validity field
"SMS-MT [...] [validity:{expiration}] [...]"
```

## For Go Implementation (A-011)

1. **Router.deliver_sm_callback**: Add expiry check before routing
2. **Thrower layers**: Consider expiry awareness for retry decisions
3. **Time format**: RFC 3339 ISO 8601 with timezone
4. **Terminal action**: Reject (don't requeue) expired messages
5. **Metrics**: Track expired_messages_discarded count

## Files to Review

| Priority | File | Purpose |
|----------|------|---------|
| **Critical** | `jasmin/managers/listeners.py` | Reference expiry check implementation |
| **Critical** | `jasmin/routing/router.py` | Identify router gap |
| **High** | `jasmin/managers/content.py` | Understand AMQP header structure |
| **High** | `jasmin/managers/clients.py` | Validity→expiration conversion |
| **Medium** | `jasmin/routing/throwers.py` | Understand thrower architecture |
| **Medium** | `spec/implementation/PHASE2_30_ROUTER_QOS_EXPIRY_SLICE.md` | A-011 specification |

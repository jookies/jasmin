# Task Completion Summary: Expiry/Validity Inventory for A-011

**Task**: Inventory legacy and "Improved" expiry/validity behaviors for AMQP consumers (Router, Throwers, Clients) in the Jasmin project for A-011 (Message Expiry) Go rewrite.

**Date**: July 19, 2026  
**Status**: ✅ **COMPLETE**

---

## Deliverables Created

### 1. **EXPIRY_VALIDITY_INVENTORY.md** (370 lines, 14 KB)
   - **Comprehensive analysis** of all expiry/validity behaviors
   - **Executive summary** with key findings
   - **Detailed section-by-section breakdown** of each component
   - **Time format summary table** showing conversions across layers
   - **Terminal action table** comparing rejection strategies
   - **Recommendations for Go rewrite** with specific A-011 guidance
   - **References** to all analyzed files and specifications

### 2. **EXPIRY_QUICK_REFERENCE.md** (86 lines, 3.5 KB)
   - **Summary table** of all components with status
   - **Critical code locations** with line numbers
   - **Asymmetry problem visualization** showing the data flow gap
   - **File review priority list** for Go implementation
   - Ideal for quick lookups during development

### 3. **EXPIRY_CODE_SNIPPETS.md** (326 lines, 11 KB)
   - **Actual code excerpts** from all 9 key components
   - **Inline annotations** explaining what each snippet does
   - **Data flow diagram** showing HTTP → AMQP → SMPP journey
   - **Time format reference table** for implementation guidance
   - Copy-paste ready code examples for Go developers

---

## Key Findings

### ✅ What's Implemented
1. **SMPPClientSMListener** (listeners.py:182-188)
   - Checks AMQP message `expiration` header before sending
   - Parses ISO 8601 datetime strings
   - Rejects (discards) expired messages without requeue
   - Logs at INFO level

2. **Validity Period Propagation** (HTTP → SMPP)
   - HTTP accepts `validity-period` in minutes
   - Converts to absolute datetime object
   - Stores in AMQP `expiration` header as ISO string
   - Format: `'%Y-%m-%d %H:%M:%S'`

3. **Authorization & Filtering**
   - User-level `set_validity_period` authorization check
   - Numeric pattern validation (minutes only)

### ❌ What's Missing (A-011 Gap)
1. **RouterPB** (router.py)
   - No expiry check in `deliver_sm_callback`
   - Routes all messages regardless of expiration
   - Tagged as "TODO" in code comments (line 167-168)

2. **deliverSmThrower** (throwers.py)
   - No expiry check on AMQP consumption
   - Routes all messages to HTTP/SMPP endpoints
   - Retries without awareness of expiration

### ⚠️ Asymmetry Problem
The architecture shows **inconsistent expiry handling**:

```
Entry Point (HTTP)        → No queue-level check
                ↓
Client Manager (SMPPClientManagerPB) → No check, just propagates
                ↓
SMPP Listener             → ✅ Checks & rejects expired
                ↓
Router                    → ❌ NO CHECK (A-011 Gap!)
                ↓
Thrower                   → ❌ NO CHECK
```

---

## Time Format Summary

| Component | Receives | Format | Stores As | Example |
|-----------|----------|--------|-----------|---------|
| HTTP API | minutes (int) | N/A | N/A | `"60"` |
| HTTP Endpoint | minutes (int) | N/A | datetime | `datetime(2026, 7, 19, 4, 9)` |
| Client Manager | datetime | N/A | string | `"2026-07-19 03:09:05"` |
| AMQP Header | string | ISO | string | `"2026-07-19 03:09:05"` |
| Listener | string | ISO | datetime | `datetime(2026, 7, 19, 3, 9, 5)` |

**Issue**: No timezone info; format lacks 'T' separator (non-standard ISO 8601)

---

## A-011 Implementation Checklist for Go

- [ ] **Router Layer**: Add expiry check in `deliver_sm_callback`
  - Check `AMQP.properties.headers.expiration` header
  - Parse using RFC 3339 parser
  - Compare with `time.Now()`
  - Reject expired messages (Nack, don't Requeue)
  - Log INFO: `"Discarding expired DeliverSmPDU[%s]: expiration is %s", msgid, expiration`

- [ ] **Thrower Layer** (Optional):
  - Add expiry check on consumption
  - Consider retry behavior for near-expiry messages
  - Track metrics: `expired_messages_discarded`, `expired_messages_retried`

- [ ] **Time Format**:
  - Use RFC 3339 / ISO 8601 with timezone info
  - Example: `"2026-07-19T03:09:05Z"`
  - Avoid naive datetime ambiguity

- [ ] **Terminal Action**:
  - Expired messages: Reject without requeue (discard)
  - Rationale: Message timed out while queued; no value in retrying

- [ ] **Metrics**:
  - Counter: `router.expired_messages_discarded`
  - Counter: `router.expired_messages_routed` (for comparison)
  - Gauge: `message.age.in.queue` (percentiles)

- [ ] **Configuration**:
  - `router.enable_expiry_check` (default: true)
  - `router.expiry_policy` (drop | requeue | delay)
  - `thrower.check_expiry` (optional for thrower layer)

---

## Files Analyzed

### Core Logic
- `jasmin/managers/listeners.py` → ✅ Reference expiry implementation
- `jasmin/routing/router.py` → ❌ Expiry gap location
- `jasmin/routing/throwers.py` → Thrower architecture
- `jasmin/managers/clients.py` → Client-side PDU→AMQP conversion

### Support Files
- `jasmin/managers/proxies.py` → Validity period formatting
- `jasmin/managers/content.py` → AMQP header construction
- `jasmin/protocols/http/endpoints/send.py` → HTTP entry point
- `jasmin/protocols/http/validation.py` → Authorization & filtering
- `jasmin/routing/jasminApi.py` → Config & authorization keys
- `jasmin/protocols/smpp/configs.py` → Connector-level defaults

### Specifications
- `spec/implementation/PHASE2_30_ROUTER_QOS_EXPIRY_SLICE.md` → A-011 requirements

---

## Code Examples

### Expiry Check Pattern (from SMPPClientSMListener)
```python
if 'headers' in message.content.properties and 'expiration' in message.content.properties['headers']:
    expiration_datetime = parser.parse(message.content.properties['headers']['expiration'])
    if expiration_datetime < datetime.now():
        self.log.info("Discarding expired message[%s]: expiration is %s", msgid, expiration_datetime)
        yield self.rejectMessage(message)
        return  # Don't requeue
```

### Validity Period Propagation (HTTP → SMPP)
```python
# Step 1: HTTP endpoint receives minutes
validity_period_minutes = int(request.args[b'validity-period'][0])

# Step 2: Convert to absolute datetime
validity_period = datetime.today() + timedelta(minutes=validity_period_minutes)

# Step 3: Set on PDU
SubmitSmPDU.params['validity_period'] = validity_period

# Step 4: Convert to string format for AMQP
validity_period_str = validity_period.strftime('%Y-%m-%d %H:%M:%S')

# Step 5: Store in AMQP header
SubmitSmContent(..., expiration=validity_period_str, ...)
```

---

## Data Flow Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│ HTTP API: POST /send?validity-period=60                          │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ HTTP Send Endpoint (send.py:280-287)                             │
│ Convert minutes → datetime: datetime.today() + 60 minutes        │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ SubmitSmPDU.params['validity_period'] = datetime object         │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ SMPPClientManagerPB.perspective_submit_sm()                      │
│ Convert datetime → ISO string: '%Y-%m-%d %H:%M:%S'              │
└────────────────────────┬────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────────┐
│ SubmitSmContent(expiration='2026-07-19 03:09:05')                │
│ Store in AMQP header: properties['headers']['expiration']       │
└────────────────────────┬────────────────────────────────────────┘
                         │
                    ┌────┴────────┐
                    │             │
                    ▼             ▼
         ┌─────────────────┐  ┌────────────────┐
         │ SMPPListener    │  │ Router (MO)    │
         │ submit.sm.*     │  │ deliver.sm.*   │
         │                 │  │                │
         │ ✅ CHECK HERE   │  │ ❌ MISSING     │
         │ Rejects expired │  │ Routes all     │
         └─────────────────┘  └────────────────┘
                    │             │
                    ▼             ▼
         ┌─────────────────┐  ┌────────────────┐
         │ SMPP Client     │  │ Thrower Layer  │
         │ Sends via SMPP  │  │ ❌ NO CHECK    │
         │                 │  │ Routes to HTTP │
         └─────────────────┘  │ or SMPP        │
                              └────────────────┘
```

---

## Notes for Go Implementation Team

1. **Priority**: Router expiry check is CRITICAL for A-011 compliance
2. **Pattern**: Use same logic as SMPPClientSMListener (proven, tested)
3. **Format**: Upgrade to RFC 3339 ISO 8601 with timezone for Go version
4. **Metrics**: Add tracking; legacy has minimal observability
5. **Testing**: Verify expiry check with near-expiry and over-expiry message scenarios
6. **Configuration**: Make expiry handling configurable, not hardcoded

---

## References

**Inventory Documents**:
1. `EXPIRY_VALIDITY_INVENTORY.md` – Full analysis with all details
2. `EXPIRY_QUICK_REFERENCE.md` – Quick lookup tables and summaries
3. `EXPIRY_CODE_SNIPPETS.md` – Actual code excerpts for copy-paste

**Legacy Files**:
- All files listed in "Files Analyzed" section above

**Specification**:
- `spec/implementation/PHASE2_30_ROUTER_QOS_EXPIRY_SLICE.md`

---

## Quality Assurance

✅ All expiry/validity code paths traced  
✅ Time format conversions documented  
✅ Terminal actions identified  
✅ Authorization/filtering rules captured  
✅ Data flow diagrammed  
✅ Code snippets extracted and annotated  
✅ A-011 gaps identified and recommended  
✅ Go implementation checklist provided  

---

**Task completed by**: Delegated subagent  
**Task duration**: ~15 minutes  
**Deliverables**: 3 comprehensive markdown documents (782 lines total)  
**Output location**: `/Users/minibot/Projects/bots-agents/jasmin-go/EXPIRY_*.md`

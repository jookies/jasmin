# Expiry/Validity Inventory - Document Index

## Quick Navigation

This directory now contains **5 comprehensive documents** documenting all legacy Jasmin expiry/validity behaviors relevant to A-011 (Message Expiry) Go rewrite.

---

## 📋 Document Guide

### 1. **TASK_COMPLETION_SUMMARY.md** ← START HERE
   **Best for**: Project managers, quick overview
   - Executive summary of findings
   - Key findings & gaps
   - A-011 implementation checklist
   - Data flow diagram
   - Quality assurance sign-off

### 2. **EXPIRY_VALIDITY_INVENTORY.md** ← DETAILED REFERENCE
   **Best for**: Go developers, architects
   - Comprehensive section-by-section breakdown
   - All 6 components analyzed in depth
   - Time format conversions
   - Expiry decision points table
   - Logging evidence & patterns
   - Recommendations for Go rewrite

### 3. **EXPIRY_QUICK_REFERENCE.md** ← FOR QUICK LOOKUPS
   **Best for**: During implementation, decision trees
   - Summary table of all components
   - Critical code locations with line numbers
   - Asymmetry visualization
   - File review priority list
   - Essential snippets inline

### 4. **EXPIRY_CODE_SNIPPETS.md** ← COPY-PASTE REFERENCE
   **Best for**: Implementation, pattern examples
   - Actual code excerpts (9 components)
   - Inline annotations & explanations
   - Complete data flow example
   - Time format reference table
   - Ready-to-use implementation patterns

### 5. **INVENTORY_INDEX.md** (this file)
   **Best for**: Navigation & overview
   - Document index and reading paths
   - Component coverage summary
   - File locations & references
   - Implementation roadmap

---

## 🎯 Reading Paths

### **Path A: Executive Summary (5 min)**
1. Read TASK_COMPLETION_SUMMARY.md
2. Note the key findings & A-011 checklist

### **Path B: Full Understanding (30 min)**
1. Start with TASK_COMPLETION_SUMMARY.md
2. Review EXPIRY_QUICK_REFERENCE.md for component overview
3. Deep dive into EXPIRY_VALIDITY_INVENTORY.md for each component
4. Refer to EXPIRY_CODE_SNIPPETS.md for implementation details

### **Path C: Fast Implementation (15 min)**
1. Scan EXPIRY_QUICK_REFERENCE.md for the gap
2. Find code pattern in EXPIRY_CODE_SNIPPETS.md
3. Reference EXPIRY_VALIDITY_INVENTORY.md for Go-specific guidance

---

## ✅ Key Findings Summary

### What's Implemented
- ✅ SMPPClientSMListener checks expiry before SMPP send
- ✅ HTTP→SMPP validity period propagation works
- ✅ Authorization & filtering for validity_period

### What's Missing (A-011 Gap)
- ❌ RouterPB does NOT check expiry in deliver_sm_callback
- ❌ deliverSmThrower does NOT check expiry on consumption
- ⚠️ Asymmetric implementation across layers

### Time Formats
- HTTP: minutes (integer)
- PDU: datetime object
- AMQP: ISO string `'%Y-%m-%d %H:%M:%S'`
- Go: Should upgrade to RFC 3339 with timezone

---

## 📊 Document Statistics

| Document | Lines | Size | Focus |
|----------|-------|------|-------|
| TASK_COMPLETION_SUMMARY.md | 280 | 12 KB | Overview, checklist, decisions |
| EXPIRY_VALIDITY_INVENTORY.md | 370 | 14 KB | Detailed analysis, recommendations |
| EXPIRY_QUICK_REFERENCE.md | 86 | 3.5 KB | Quick lookup, tables, priority |
| EXPIRY_CODE_SNIPPETS.md | 326 | 11 KB | Actual code, patterns, examples |
| INVENTORY_INDEX.md | ~120 | 5 KB | Navigation, references |
| **TOTAL** | **~1,180** | **45.5 KB** | Complete reference suite |

---

## 🔍 Component Coverage

### Consumers Analyzed
1. **SMPPClientSMListener** (managers/listeners.py)
   - ✅ Expiry check: YES
   - Status: Reference implementation

2. **RouterPB** (routing/router.py)
   - ❌ Expiry check: NO
   - Status: A-011 gap location

3. **deliverSmThrower** (routing/throwers.py)
   - ❌ Expiry check: NO
   - Status: Optional for Go

4. **HTTP Send** (protocols/http/endpoints/send.py)
   - ⚠️ Entry point only (no queue check)
   - Status: Working as designed

### Supporting Components
5. **SMPPClientManagerPB** (managers/clients.py)
   - Converts PDU→AMQP expiration

6. **SMPPClientManagerPBProxy** (managers/proxies.py)
   - Formats validity_period strings

7. **SubmitSmContent** (managers/content.py)
   - Stores expiration in AMQP headers

8. **HttpAPICredentialValidator** (protocols/http/validation.py)
   - Authorizes validity_period parameter

9. **Configuration & API** (routing/jasminApi.py, protocols/smpp/configs.py)
   - Declares validity_period authorization & filters

---

## 🚀 Go Implementation Steps

1. **Router Layer (CRITICAL for A-011)**
   - Add expiry check in deliver_sm_callback
   - Use pattern from SMPPClientSMListener
   - Reject expired messages without requeue
   - Add metrics

2. **Thrower Layer (OPTIONAL)**
   - Consider expiry awareness for retries
   - Make configurable

3. **Time Format**
   - Upgrade to RFC 3339 ISO 8601 with timezone
   - Example: `2026-07-19T03:09:05Z`

4. **Configuration**
   - Add `router.enable_expiry_check` (default: true)
   - Add metrics tracking

---

## 📝 File Locations

All documents are in: `/Users/minibot/Projects/bots-agents/jasmin-go/`

```
jasmin-go/
├── TASK_COMPLETION_SUMMARY.md
├── EXPIRY_VALIDITY_INVENTORY.md
├── EXPIRY_QUICK_REFERENCE.md
├── EXPIRY_CODE_SNIPPETS.md
└── INVENTORY_INDEX.md
```

---

## 🔗 Legacy File References

All analyzed legacy Python files:
- `jasmin/managers/listeners.py` (SMPPClientSMListener.submit_sm_callback)
- `jasmin/routing/router.py` (RouterPB.deliver_sm_callback)
- `jasmin/routing/throwers.py` (deliverSmThrower)
- `jasmin/protocols/http/endpoints/send.py` (HTTP Send endpoint)
- `jasmin/managers/clients.py` (SMPPClientManagerPB)
- `jasmin/managers/proxies.py` (SMPPClientManagerPBProxy)
- `jasmin/managers/content.py` (SubmitSmContent)
- `jasmin/protocols/http/validation.py` (HttpAPICredentialValidator)
- `jasmin/routing/jasminApi.py` (MtMessagingCredential)
- `jasmin/protocols/smpp/configs.py` (SMPPClientConfig)

Specifications:
- `spec/implementation/PHASE2_30_ROUTER_QOS_EXPIRY_SLICE.md` (A-011 spec)

---

## ✨ Quality Checklist

- ✅ All expiry/validity code paths traced
- ✅ Time format conversions documented with examples
- ✅ Terminal actions identified (reject, discard, requeue, etc.)
- ✅ Authorization & filtering rules captured
- ✅ Data flow diagrammed (entry to exit)
- ✅ Code snippets extracted with annotations
- ✅ A-011 gaps clearly identified
- ✅ Go implementation guidance provided
- ✅ 5 complementary documents with different focuses
- ✅ ~1,180 lines of reference material ready for use

---

**Created**: July 19, 2026  
**Task**: Inventory legacy and "Improved" expiry/validity behaviors for AMQP consumers  
**Status**: ✅ COMPLETE

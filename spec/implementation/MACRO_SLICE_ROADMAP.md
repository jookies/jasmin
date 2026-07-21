# Jasmin-Go Macro-Slice Roadmap

This roadmap transitions from micro-slice development to functional flow completion.
The goal is to replace Python components with Go in a "Strangler" fashion.

## Functional Macros

### Macro 1: Outbound SMS Gateway (MT Path)
**Goal:** Full HTTP `/send` to SMPP outbound flow.
- **1.1: HTTP Interface Parity** (H-001 to H-009) - **COMPLETE**
- **1.2: Advanced Segmentation & Encoding** (HE-001 to HE-006) - **COMPLETE**
- **1.3: Router & Billing Integration** (A-001, A-002, A-008, A-009, B-001 to B-011) - **IN PROGRESS (fixture-backed atomic submit, late-response billing, explicit settlement, and RouterPB subscription topology slices are published; cited rows remain `GO-PARTIAL`/`INVENTORIED` until production-flow closure)**
- **1.4: SMPP Outbound Stability** (SC-001, SC-002, SC-003, SC-004, SC-007) - **IN PROGRESS (Phase 2.33B repairs session ownership/control PDUs; Phase 2.34 integrates production submit pacing; configuration, management lifecycle, and failover remain)**

### Macro 2: Delivery Receipts (DLR Path)
**Goal:** Handle SMPP receipts and deliver to HTTP/SMPPS.
- **2.1: Receipt Detection & Correlation** (SP-002, SP-003, RD-001, RD-002)
- **2.2: Callback Infrastructure** (HC-003 to HC-007, A-006, A-007)

### Macro 3: Inbound SMS (MO Path)
**Goal:** Receive SMPP messages and deliver to user callbacks.
- **3.1: MO Detection & Persistence** (SP-004, RD-003, A-004)
- **3.2: Multipart Assembly & Egress** (SE-006, SS-004, A-005, HC-001, HC-002)

### Macro 4: Management & Operations
**Goal:** Production readiness.
- **4.1: Persistence & Config** (P-001 to P-010, C-001 to C-016)
- **4.2: Observability & Lifecycle** (D-001 to D-009, O-001 to O-008)

## Progress Metrics
- Functional flows completed: 0 / 4
- Total matrix rows: 205
- Untouched (INVENTORIED): 129
- Partial (GO-PARTIAL): 55
- Complete (MATCH/GO-COMPLETE): 21

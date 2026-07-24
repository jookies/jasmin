# Jasmin-Go Macro-Slice Roadmap

This roadmap transitions from micro-slice development to functional flow completion.
The goal is to replace Python components with Go in a "Strangler" fashion.

## Functional Macros

### Macro 1: Outbound SMS Gateway (MT Path)
**Goal:** Full HTTP `/send` to SMPP outbound flow.
- **1.1: HTTP Interface Parity** (H-001 to H-009) - **COMPLETE**
- **1.2: Advanced Segmentation & Encoding** (HE-001 to HE-006) - **COMPLETE**
- **1.3: Router & Billing Integration** (A-001, A-002, A-008, A-009, B-001 to B-011) - **IN PROGRESS (Wave 1D closes exact binary64 operation-order parity for B-008; Wave 1E adds fixture-backed quota dirty/timer semantics for B-009, while PostgreSQL authority, inbound DLR, protocol error, redelivery, and remaining production-flow closure are still pending)**
- **1.4: SMPP Outbound Stability** (SC-001, SC-002, SC-003, SC-004, SC-007) - **IN PROGRESS (Wave 1C implements production pre-admission availability-aware failover, route-pool persistence, independent reconnect controls, graceful unbind, verified SMSC TLS, and connector QoS; rows remain `GO-PARTIAL`/`INVENTORIED` until full legacy timer/configuration and measured concurrency evidence permits promotion)**

### Macro 2: Delivery Receipts (DLR Path)
**Goal:** Handle SMPP receipts and deliver to HTTP/SMPPS.
- **2.1: Receipt Detection & Correlation** (SP-002, SP-003, RD-001, RD-002) - **IN PROGRESS (live Redis projections, SMSC-ID normalization, submit-response and terminal correlation legs exist; AMQP consumer/retry/idempotency closure remains pending)**
- **2.2: Callback Infrastructure** (HC-003 to HC-007, A-006, A-007) - **IN PROGRESS (frozen HTTP/SMPPS thrower envelopes now project into typed callback/receipt inputs; HTTP retry lifecycle, AMQP consumers/publication and bound SMPPS session delivery remain pending)**

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
- Untouched (INVENTORIED): 125
- Partial (GO-PARTIAL): 58
- Complete (MATCH/GO-COMPLETE): 22

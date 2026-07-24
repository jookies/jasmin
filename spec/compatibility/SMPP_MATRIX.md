# SMPP 3.4 compatibility matrix

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Oracle: `jasmin/protocols/smpp/`, `jasmin/managers/`, SMPP tests and SMSC simulators.

## Wire and session contract

| ID | Area | Cases to fixture | Status |
|---|---|---|---|
| S-001 | framing | command length/id/status/sequence, partial/coalesced reads, malformed length | GO-PARTIAL |
| S-002 | bind TX/RX/TRX | success, wrong password/system_id, disabled user/group, IP restriction | INVENTORIED |
| S-003 | bind state | allowed commands by state and exact status codes | GO-PARTIAL |
| S-004 | limits | max bindings, duplicate sessions, ban/unbind behavior | INVENTORIED |
| S-005 | timers | response, enquire_link, inactivity, session-init and reconnect timers | GO-PARTIAL |
| S-006 | unbind/disconnect | graceful and abrupt paths, pending request behavior | INVENTORIED |
| S-007 | TLS | handshake, verification/config errors and reconnect | INVENTORIED |

## PDU contract

| SP-001 | `submit_sm` | mandatory/default fields, TON/NPI, esm_class, protocol, priority, schedule/validity | GO-PARTIAL |
| SP-002 | `submit_sm_resp` | success/error mapping, SMSC ID, correlation, ACK/requeue/retry | GO-PARTIAL |
| SP-003 | `deliver_sm` | MO versus DLR detection, receipt fields and payload | GO-PARTIAL |
| SP-004 | `data_sm` | configured DLR/MO behavior and response | INVENTORIED |
| SP-005 | `enquire_link` | request/response and timeout | GO-PARTIAL |
| SP-006 | standard TLV | message_payload, receipts, SAR and known optionals | GO-PARTIAL |
| SP-007 | vendor TLV | configured tag/name/type/value validation and fidelity | GO-PARTIAL |
| SP-008 | unknown TLV/PDU | legacy accept/reject/error behavior | GO-PARTIAL |

## Encoding and long messages

| ID | Area | Contract | Status |
|---|---|---|---|
| SE-001 | GSM 03.38 | encode/decode and boundaries | GO-PARTIAL |
| SE-002 | UCS2 | payload bytes and segmentation | GO-PARTIAL |
| SE-003 | binary | DCS and byte fidelity | GO-PARTIAL |
| SE-004 | SAR | reference/total/sequence and reassembly | MATCH |
| SE-005 | UDH | header/reference/ordering and reassembly | MATCH |
| SE-006 | multipart MO | Redis key, serialized pieces, 300-second TTL, final assembly | INVENTORIED |

## SMPP client connector lifecycle

| ID | Area | Contract | Status |
|---|---|---|---|
| SC-001 | configuration | every field/default and runtime-update versus restart-required fields | INVENTORIED |
| SC-002 | lifecycle | add/remove/list/start/stop and state transitions | INVENTORIED |
| SC-003 | reconnect | initial/reconnect delay, retry and state/stats | GO-PARTIAL |
| SC-004 | throughput | submit pacing and queue behavior | GO-PARTIAL |
| SC-005 | readiness | expiration decision, disconnected/unbound checks, age boundary, and settlement boundary | GO-PARTIAL |
| SC-006 | error retry | statuses, counts, delays, and retry-attempt boundary | GO-PARTIAL |
| SC-007 | failover | connector availability and ordered selection | INVENTORIED |
| SC-008 | submit response publish | optional `submit.sm.resp.<CID>` event/properties | GO-PARTIAL |

## SMPP server submission and delivery

| ID | Area | Contract | Status |
|---|---|---|---|
| SS-001 | credentials | authorizations, regex filters, defaults and throughput | INVENTORIED |
| SS-002 | routing/interception | tags, route choice, no-route and status override | INVENTORIED |
| SS-003 | billing | per-part charge/count and insufficient quota status | INVENTORIED |
| SS-004 | MO/DLR egress | RX/TRX selection, deliver_sm/data_sm mode and unbound behavior | INVENTORIED |
| SS-005 | parity with HTTP | same route, bill, segmentation and downstream PDU for equivalent message | INVENTORIED |

## DLR ID corpus

Must include uppercase/lowercase, leading zeros, decimal IDs, hexadecimal IDs, configured base conversions, message_payload receipts, optional receipt TLVs, malformed receipts, duplicate/reordered terminal receipts and expired/missing Redis correlation.

`GO-PARTIAL` means the Go wire adapter has executable coverage for a strict
subset of the row, while session, routing, lifecycle, or remaining PDU behavior
is still unimplemented. It must not be interpreted as full row parity.

For `SC-004`, the fixture-proven subset is connector throughput configuration
for ordinary JSON numbers and strings plus the serialized pacing-delay
decision. Phase 2.34 connects that production pacer to each concrete
`submit.sm.<CID>` delivery before readiness, including disabled throughput,
context-cancellable admission, consumer-generation fencing that aborts in-flight
socket writes without stale submission or dead-generation settlement, one
explicit requeue attempt on parent cancellation while the AMQP generation
remains live, socket submission, and correlated-response settlement under the
existing `prefetch_count=1` boundary. Python's boolean-as-integer/non-finite numeric
quirks, dynamic configuration, configurable windows, throughput statistics,
and distributed/failover pacing remain inventoried.

For `SC-005`, the fixture-proven subset is the listener's expiration-first
decision, disconnected/unbound readiness checks, strict maximum-age boundary,
legacy modulo-day age component, and configured delayed/immediate requeue
selection. Phase 2.34 additionally proves the concrete consumer's
pacing-before-readiness order and direct discard/requeue/submit settlement
boundary. Delayed readiness timer execution, runtime policy updates, and the
remaining retry lifecycle remain inventoried.

For `SC-006`, the fixture-proven subset is the listener's default retry-status
map, exact current-attempt/count boundary, configured delay selection, final ACK
decision, and retry-entry post-state for configured and unconfigured errors.
AMQP timer/ACK execution, DLR/billing/remaining response side effects, transport exceptions,
socket response correlation, and arbitrary config-literal quirks remain inventoried.

For `SC-008`, the oracle proves optional response publication occurs on disabled,
successful, final-error, and retried-error callback paths. The Go subset validates
ACK/requeue publication context, constructs the exact fixture-proven message ID and
created-at property set, selects the `messaging` exchange/request `reply-to` key,
and preserves supplied opaque protocol-2 response bytes. Live AMQP publish,
status-to-pickle generation, confirms/recovery, queue ownership, and arbitrary
routing keys remain inventoried.

For `S-005` and `SP-005`, Phase 2.33B adds frozen-oracle wire fixtures for
`enquire_link` and `enquire_link_resp`, one serialized session writer, periodic
request emission, sequence-matched response handling, missed-response termination,
and context-safe shutdown. Exact separation of the legacy response, enquire,
inactivity, and read timers, arbitrary timer reconfiguration, and full reconnect
statistics remain inventoried.

For `SP-002`, `SC-003`, and `SC-005`, Phase 2.33B additionally proves
successful sequence-matched bind-response gating, a single post-bind connection
reader, pending-request cleanup, and exactly one terminal settlement attempt on
successful response, timeout, write failure, cancellation, and connection loss;
broker confirmation and redelivery are not guaranteed. `SC-006`
remains a standalone policy projection: status mapping, attempt state, and delayed
requeue are not yet connected to Session settlement. Response publication,
DLR/billing side effects, and protocol-2 AMQP `SubmitSM` semantic decoding remain
inventoried.

For `S-003`, the fixture-backed subset invokes Jasmin's concrete
`PDURequestReceived` and `PDUDataRequestReceived` methods and proves unsupported
PDU rejection with `ESME_RSYSERR`, `submit_sm`/`data_sm` rejection while
`BOUND_RX` with `ESME_RINVBNDSTS`, and delegation of the accepted command set to
the vendored Twisted SMPP server base. The Go helper matches all committed cases.
Inherited state-machine behavior, duplicate-bind execution, transport/session
wiring, disconnect semantics, timers, and a runnable SMPPS server remain
inventoried.

For `SP-007`, the fixture-backed subset invokes the frozen pure vendor-TLV
pipeline and proves tag parsing, connector type resolution, Int1/2/4/8 and
string/raw-byte encoding, ordered SMPP TLV headers, required-tag enforcement,
encoded max-length boundaries, and duplicate-tag last-wins validation. HTTP/REST
shape normalization, connector configuration lifecycle, concrete SMPPc/SMPPs
injection, inbound decoder preservation, and end-to-end PDU forwarding remain
inventoried.

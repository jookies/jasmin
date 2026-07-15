# Routing, interception and billing compatibility matrix

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Sources: `jasmin/routing/*`, router/HTTP/SMPP integrations and routing tests.

## Routables and filters

| ID | Contract | Required fixture | Status |
|---|---|---|---|
| RT-001 | MO/MT routables | connector/user/message fields, PDU reference, datetime and type restrictions | INVENTORIED |
| RT-002 | tags | add/remove/has/get, duplicate/type behavior | INVENTORIED |
| RT-003 | locked fields | locking and interceptor mutation rejection | INVENTORIED |
| RF-001 | Transparent | unconditional match | GO-PARTIAL |
| RF-002 | Connector | MO-only source connector and type validation | GO-PARTIAL |
| RF-003 | User | MT-only user identity | GO-PARTIAL |
| RF-004 | Group | MT-only group identity | GO-PARTIAL |
| RF-005 | SourceAddr | regex bytes/string and missing value | GO-PARTIAL |
| RF-006 | DestinationAddr | regex bytes/string and missing value | GO-PARTIAL |
| RF-007 | ShortMessage | message/payload selection and regex | GO-PARTIAL |
| RF-008 | DateInterval | inclusive boundaries and invalid ranges | GO-PARTIAL |
| RF-009 | TimeInterval | inclusive boundaries and midnight behavior | GO-PARTIAL |
| RF-010 | Tag | tag type/value matching | GO-PARTIAL |
| RF-011 | EvalPy | globals, result conversion, exception and security boundary | INVENTORIED |
| RF-012 | compatibility | allowed filter classes by MO/MT table | GO-PARTIAL |

## Routing tables and route types

| ID | Contract | Required fixture | Status |
|---|---|---|---|
| RR-001 | order | descending evaluation and first match | INVENTORIED |
| RR-002 | replacement | add at existing order replaces previous route | INVENTORIED |
| RR-003 | default | only order 0 and fallback behavior | INVENTORIED |
| RR-004 | no match | HTTP/SMPP rejection mapping | INVENTORIED |
| RR-005 | filter composition | all filters combined with AND and evaluation failures | INVENTORIED |
| RR-006 | Static MO/MT | destination connector and MT rate | INVENTORIED |
| RR-007 | RandomRoundrobin MO/MT | legacy random choice and eligible connector set | INVENTORIED |
| RR-008 | Failover MO/MT | connector order, availability predicate and exhaustion | INVENTORIED |
| RR-009 | connector types | valid destinations for MO versus MT | INVENTORIED |
| RR-010 | BestQualityMTRoute | document upstream stub/non-working status | INVENTORIED |

## Interceptors

| ID | Contract | Required fixture | Status |
|---|---|---|---|
| RI-001 | table order | descending order, first match and order-0 default | INVENTORIED |
| RI-002 | script inputs | `routable`, `smpp_status`, `http_status` globals | INVENTORIED |
| RI-003 | mutation | PDU fields, tags and locked fields | INVENTORIED |
| RI-004 | rejection | HTTP/SMPP status overrides and return forms | INVENTORIED |
| RI-005 | failure | syntax/runtime/timeout/PB failure behavior | INVENTORIED |
| RI-006 | sidecar | legacy Python execution remains isolated and resource-bounded in compatibility mode | INVENTORIED |

## Billing and quotas

| ID | Contract | Required fixture | Status |
|---|---|---|---|
| B-001 | route rate | rated/unrated route and visible unit rate | INVENTORIED |
| B-002 | multipart | charge and submit-count delta per generated segment | INVENTORIED |
| B-003 | unlimited | `None`/`ND` balance and count behavior | INVENTORIED |
| B-004 | insufficient balance | boundary/equality/below-charge and protocol error mapping | INVENTORIED |
| B-005 | insufficient count | boundary and protocol error mapping | INVENTORIED |
| B-006 | early decrement | 1–100 percent and enqueue-time delta | INVENTORIED |
| B-007 | late decrement | successful `submit_sm_resp` remainder and error behavior | INVENTORIED |
| B-008 | float rounding | IEEE-754 corpus, repeated/multipart charges and API/jCli formatting | INVENTORIED |
| B-009 | persistence timer | quota mutation dirty flag, periodic persistence and crash window | INVENTORIED |
| B-010 | redelivery | duplicate/reordered billing events and exact legacy delta | INVENTORIED |
| B-011 | HTTP/SMPP parity | equivalent message produces same bill/route/segments | INVENTORIED |

No improved ledger behavior is considered parity. Ledger tests and schemas are separate from the legacy compatibility engine.

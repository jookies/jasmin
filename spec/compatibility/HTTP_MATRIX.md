# HTTP and REST compatibility matrix

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Oracle: `jasmin/protocols/http/`, `jasmin/protocols/rest/`, related tests and docs.

## Legacy HTTP endpoints

| ID | Endpoint | Contract to fixture | Status |
|---|---|---|---|
| H-001 | `GET/POST /send` | Both methods supported; request forms and response content type | INVENTORIED |
| H-002 | `/send` required fields | `username`, `password`, `to`, exactly one of `content`/`hex-content` | INVENTORIED |
| H-003 | `/send` optional fields | `from`, `coding`, `priority`, `sdt`, `validity-period`, DLR fields, tags, TLVs | INVENTORIED |
| H-004 | `/send` auth/state | wrong credentials, disabled user/group, missing authorization | INVENTORIED |
| H-005 | `/send` value filters | source/destination/content regex and defaults | INVENTORIED |
| H-006 | `/send` route/interceptor | no route, interceptor HTTP/SMPP status override, locked fields | INVENTORIED |
| H-007 | `/send` quotas | balance, submit count, throughput and multipart segment count | INVENTORIED |
| H-008 | `/send` success | status, exact `Success "<uuid>"` body and UUID shape | INVENTORIED |
| H-009 | `/send` errors | validation order and exact status/body for 400/403/412/500 paths | INVENTORIED |
| H-010 | `/rate` | auth, destination route, unit rate, segment count, JSON shape | INVENTORIED |
| H-011 | `/balance` | balance/count JSON and exact `ND` representation | INVENTORIED |
| H-012 | `/ping` | exact `Jasmin/PONG` body | INVENTORIED |
| H-013 | `/metrics` | names, HELP/TYPE, labels, values and content type | INVENTORIED |

## Encoding and segmentation

| ID | Case | Required observations | Status |
|---|---|---|---|
| HE-001 | GSM 03.38 | septet handling, extension table, segment boundaries | INVENTORIED |
| HE-002 | UCS2 | byte order, surrogate/non-BMP handling and limits | INVENTORIED |
| HE-003 | binary/hex | validation, payload fidelity and DCS | INVENTORIED |
| HE-004 | SAR | reference, total/sequence TLVs and charge count | INVENTORIED |
| HE-005 | UDH | header bytes, concatenation reference and charge count | INVENTORIED |
| HE-006 | custom TLV | name/tag/type validation and encoded value | INVENTORIED |

## MO and DLR HTTP callbacks

| ID | Callback | Contract | Status |
|---|---|---|---|
| HC-001 | MO GET | exact fields: id/from/to/origin-connector/content/binary plus optional metadata | INVENTORIED |
| HC-002 | MO POST | form fields/body and content encoding | INVENTORIED |
| HC-003 | DLR level 1 | id/status/level/connector fields | INVENTORIED |
| HC-004 | DLR level 2/3 | SMSC ID, dates, counters, error and text fields | INVENTORIED |
| HC-005 | ACK | success only on HTTP 200 plus exact `ACK/Jasmin` | INVENTORIED |
| HC-006 | retry | timeout, connection error, missing ACK, status-specific behavior, max attempts/delay | INVENTORIED |
| HC-007 | method | legacy GET/POST normalization and validation | INVENTORIED |

## REST API

| ID | Endpoint/behavior | Contract | Status |
|---|---|---|---|
| R-001 | `/ping` | response status/body/schema | INVENTORIED |
| R-002 | `/secure/send` | Basic Auth, JSON input, underscore/hyphen mapping, `{"data":...}` output | INVENTORIED |
| R-003 | `/secure/sendbatch` | globals/overrides, destinations, binary, messageCount and batch ID | INVENTORIED |
| R-004 | scheduling | relative/absolute time interpretation and restart behavior | INVENTORIED |
| R-005 | callbacks | callback/errback cardinality, query fields and terminal status | INVENTORIED |
| R-006 | `/secure/balance` | auth and response schema/types | INVENTORIED |
| R-007 | `/secure/rate` | request mapping and response schema/types | INVENTORIED |
| R-008 | QoS | per-worker throughput and smart QoS semantics | INVENTORIED |
| R-009 | errors | auth/validation/upstream failure statuses and body schema | INVENTORIED |

## Fixture policy

Fixtures record request bytes/parameters, response status, headers, body bytes, normalized nondeterministic fields and the exact upstream test/source citation. UUIDs/timestamps may be normalized only by explicit fixture metadata.

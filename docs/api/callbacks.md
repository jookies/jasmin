# HTTP callbacks an integrator must implement

Synevyr makes two compatibility callbacks to integrator-owned endpoints:

* a delivery receipt (DLR) reports submit or carrier receipt state for an MT
  message; and
* a mobile-originated (MO) callback delivers an inbound message routed to an
  HTTP connector.

Both use the same acknowledgement contract.

> **A successful callback receiver must return an HTTP 2xx status and a body
> that, after surrounding whitespace is trimmed, is exactly `ACK/Jasmin`.**
>
> A 2xx with an empty body, `OK`, JSON, or different capitalization is a
> failure. `ACK/Jasmin` with a non-2xx status is also a failure. This exact body
> is inherited compatibility behavior Q-005
> (`docs/reference/legacy-behaviours.md:15`,
> `internal/core/dlr/http_thrower.go:117`,
> `internal/core/mo/http_thrower.go:118`).

A minimal successful response is:

```http
HTTP/1.1 200 OK
Content-Type: text/plain
Content-Length: 10

ACK/Jasmin
```

The gateway does not inspect the response `Content-Type`. It reads at most
4 KiB and compares the trimmed body
(`internal/core/dlr/http_thrower.go:17`,
`internal/core/mo/http_thrower.go:21`).

**Current implementation discrepancy:** both throwers reject only status 400
and above. A final 3xx response whose body trims to `ACK/Jasmin` is therefore
currently accepted, despite Q-005 and the contract above requiring success
status. Do not depend on this defect; return 2xx and avoid redirects
(`internal/core/dlr/http_thrower.go:117`,
`internal/core/mo/http_thrower.go:118`,
`docs/reference/legacy-behaviours.md:26`).

## Common request behavior

For a connector configured as GET, callback fields are added to the URL query.
Existing unrelated query fields are preserved. Otherwise the gateway sends
POST with an `application/x-www-form-urlencoded` body
(`internal/core/dlr/http_thrower.go:85`,
`internal/core/dlr/http_thrower.go:127`,
`internal/core/mo/http_thrower.go:86`).

Every callback request includes:

```text
Content-Type: application/x-www-form-urlencoded
Accept: text/plain
```

The DLR `User-Agent` is `Jasmin gateway/1.0 DLRThrower`; the MO `User-Agent` is
`Jasmin gateway/1.0 deliverSmHttpThrower`
(`internal/core/dlr/http_thrower.go:107`,
`internal/core/mo/http_thrower.go:108`).

No callback authentication or signature header is added by these throwers; the
three headers above are the only ones they set
(`internal/core/dlr/http_thrower.go:107`,
`internal/core/mo/http_thrower.go:108`). Use an unguessable HTTPS URL, network
controls, or authentication implemented outside this compatibility contract.

The callback HTTP client timeout defaults to 30 seconds for both workers
(`internal/app/dlrthrower/service.go:91`,
`internal/app/mothrower/service.go:94`).

## Delivery-receipt callback

The application chooses this callback on `/send` with `dlr-url`, and may select
GET or POST with `dlr-method`. The HTTP request is form-encoded in either case
(`internal/transport/httpcompat/handler.go:429`,
`internal/core/dlr/http_thrower.go:85`).

### Levels

The requested `dlr-level` controls which events are delivered:

| Requested level | Callback sequence |
|---:|---|
| 1 | One submit-response callback. Its form field is `level=1` (`internal/core/dlr/correlation.go:174`). |
| 2 | No submit-response callback. After a successful submit establishes correlation, a carrier receipt produces one callback with `level=2`; a failed submit produces no callback (`internal/core/dlr/correlation.go:203`, `internal/core/dlr/correlation.go:363`). |
| 3 | A submit-response callback with `level=1`, followed after successful correlation by a carrier-receipt callback with `level=2` (`internal/core/dlr/correlation.go:180`, `internal/core/dlr/correlation.go:203`). |

![Callbacks produced by each requested delivery-receipt level](../assets/diagrams/dlr-levels.svg)

Level 2 is the one that surprises integrators: a failed submit never establishes
correlation, so it produces no callback at all rather than a failure callback.
Request level 3 when you need to distinguish "rejected at submit" from "still
waiting".

The requested value 3 therefore does not normally arrive as `level=3` on the
wire: it requests both actual levels. If a level-3 forward is supplied directly
to the thrower, it uses the same extended field set as level 2
(`internal/core/dlr/http_thrower.go:65`).

If the submit response is not successful, level 3 produces its level-1 failure
but no later level-2 callback, because the correlation record is removed
(`internal/core/dlr/correlation.go:194`).

### Fields

The following fields are always present:

| Field | Meaning and example |
|---|---|
| `id` | Synevyr queue message ID returned by `/send`, for example `86d15f1a-f75b-43f0-ab2e-a9bbde7acdc8` (`internal/core/dlr/http_thrower.go:69`, `internal/core/dlr/correlation.go:187`). |
| `level` | Actual event level: `1` for the submit response or `2` for the carrier receipt (`internal/core/dlr/correlation.go:60`). |
| `message_status` | Submit response status for level 1, for example `ESME_ROK`; carrier receipt state for level 2, for example `DELIVRD` (`internal/core/dlr/correlation.go:187`, `internal/core/dlr/correlation.go:371`). |
| `connector` | Level 1: routed connector ID. Level 2: raw SMSC receipt ID, an inherited quirk (`internal/core/dlr/correlation.go:60`, `internal/core/dlr/correlation.go:371`). |

Levels 2 and 3 include these additional fields:

| Field | Meaning and example |
|---|---|
| `id_smsc` | Coded SMSC receipt ID used for correlation, for example `6AAD5` (`internal/core/dlr/http_thrower.go:74`, `internal/core/dlr/correlation.go:375`). |
| `sub` | SMSC receipt's submitted-count text. A present one-to-three digit value is zero-padded to width three; absent is `ND` (`internal/core/dlr/receipt_parse.go:38`, `internal/core/dlr/receipt_parse.go:105`). |
| `dlvrd` | SMSC receipt's delivered-count text, with the same width-three padding and `ND` default (`internal/core/dlr/receipt_parse.go:39`, `internal/core/dlr/receipt_parse.go:105`). |
| `subdate` | SMSC receipt submission-date text, for example `2101011200`; absent is `ND` (`internal/core/dlr/receipt_parse.go:40`, `internal/core/dlr/receipt_parse.go:55`). |
| `donedate` | SMSC receipt completion-date text, for example `2101011201`; absent is `ND` (`internal/core/dlr/receipt_parse.go:41`, `internal/core/dlr/receipt_parse.go:55`). |
| `err` | SMSC receipt error text. A present value is zero-padded to width three; absent is `ND` (`internal/core/dlr/receipt_parse.go:43`, `internal/core/dlr/receipt_parse.go:105`). |
| `text` | SMSC receipt text after `text:` or `Text:`; absent is empty (`internal/core/dlr/receipt_parse.go:44`, `internal/core/dlr/receipt_parse.go:100`). |

These fields are forwarded receipt text, not independently normalized API
types. The callback builder always includes every extended key for level 2/3,
even when its value is empty
(`internal/core/dlr/http_thrower.go:73`).

### Exact request examples

A level-1 POST has this exact form-body shape:

```text
connector=c1&id=m1&level=1&message_status=ESME_ROK
```

For a level-2 GET, the request may be:

```text
GET /dlr?connector=6aad5&dlvrd=001&donedate=2101011201&err=000&id=m2&id_smsc=6AAD5&level=2&message_status=DELIVRD&sub=001&subdate=2101011200&text=ok HTTP/1.1
```

The examples use the same values asserted by the callback tests
(`internal/core/dlr/http_thrower_test.go:12`,
`internal/core/dlr/http_thrower_test.go:42`). Query/form key order is produced
by Go's form encoder; receivers must match names, not order.

### SMSC ID compatibility quirk

Q-006 is load-bearing for existing integrations. The submit-response SMSC ID is
uppercased and has leading zeros removed. Depending on the connector's
`dlr_msg_id_bases`, the receipt ID may also be converted decimal-to-uppercase
hex or hex-to-decimal before it becomes `id_smsc`
(`docs/reference/legacy-behaviours.md:27`,
`internal/core/dlr/msgid.go:34`,
`internal/core/dlr/msgid.go:44`).

Do not assume `connector` equals `id_smsc` on a level-2 callback. `connector`
is the raw receipt ID while `id_smsc` is the coded ID
(`internal/core/dlr/correlation.go:371`).

## Mobile-originated callback

An MO callback delivers a routed carrier `deliver_sm` to an HTTP connector.
The connector's configured method determines GET versus POST. Only a
case-insensitive configured `GET` produces GET; every other value is sent as
POST (`internal/core/mo/http_thrower.go:86`).

### Fields

Fields produced for every routed `deliver_sm` are:

| Field | Meaning and example |
|---|---|
| `id` | Gateway message ID for this inbound delivery, for example `m1` (`internal/core/mo/http_thrower.go:60`). |
| `from` | SMPP `source_addr`, for example `12345` (`internal/core/mo/content.go:49`). |
| `to` | SMPP `destination_addr`, for example `447700900000` (`internal/core/mo/content.go:49`). |
| `origin-connector` | ID of the source SMSC connector, for example `smpp-in` (`internal/core/mo/http_thrower.go:63`). |
| `content` | Selected raw message bytes represented as the form string (`internal/core/mo/http_thrower.go:64`). |
| `binary` | The same bytes as lowercase hexadecimal; use this for lossless binary handling, for example `68656c6c6f204d4f` (`internal/core/mo/http_thrower.go:65`). |
| `priority` | Decimal SMPP `priority_flag`. A routed `deliver_sm` supplies it (`internal/core/mo/http_thrower.go:66`, `internal/core/mo/content.go:47`). |
| `coding` | The raw one-byte SMPP `data_coding`, **not decimal text**. Coding 8 is form-encoded as `%08` (`internal/core/mo/http_thrower.go:69`, `internal/core/mo/http_thrower_test.go:48`). |

`content` is selected from a non-empty `short_message` first, then
`message_payload`, then an explicitly present empty `short_message`. A message
with neither is not sent to the callback
(`internal/core/mo/content.go:14`).

The optional fields are:

| Field | Presence and meaning |
|---|---|
| `validity` | Present when the inbound PDU has a non-empty `validity_period`; value is forwarded text (`internal/core/mo/content.go:60`). |
| `tlv_params` | Present when standard optional TLVs were decoded. Its form value is a JSON object of name-to-string values (`internal/core/mo/http_thrower.go:77`, `internal/core/mo/tlv.go:37`). |
| `custom_tlvs` | Present when vendor TLVs were captured. Its form value is a JSON array of objects with `tag`, `length`, `type`, and `value` (`internal/core/mo/http_thrower.go:80`, `internal/core/mo/tlv.go:54`). |

The standard names eligible for `tlv_params`, in emission order, are
`user_message_reference`, `source_port`, `destination_port`,
`sar_msg_ref_num`, `sar_total_segments`, `sar_segment_seqnum`, `payload_type`,
`privacy_indicator`, `callback_num`, `language_indicator`, `its_session_info`,
`network_error_code`, `message_state`, and `receipted_message_id`
(`internal/core/mo/tlv.go:10`).
`its_session_info` remains in that compatibility order but cannot currently be
emitted because the decoded PDU path has no encoder for it
(`internal/core/mo/tlv_params.go:30`).

Captured vendor values are lower-case hex with type `OctetString`; `length` is
the captured byte length
(`internal/core/mo/content.go:63`).

### Exact request examples

For a `deliver_sm` containing `hello MO`, priority 0, and coding 0, the exact
POST form body is:

```text
binary=68656c6c6f204d4f&coding=%00&content=hello+MO&from=12345&id=m1&origin-connector=smpp-in&priority=0&to=447700900000
```

A GET example with coding 8, priority 2, and validity is:

```text
GET /mo?binary=78&coding=%08&content=x&from=SENDER&id=m2&origin-connector=c&priority=2&to=999&validity=000000000100000R HTTP/1.1
```

These byte values are covered by
`internal/core/mo/http_thrower_test.go:15` and
`internal/core/mo/http_thrower_test.go:48`.

## Failure and retry policy

For a simple HTTP route, the following all count as failure:

* request construction or transport error;
* the 30-second HTTP timeout;
* a non-2xx response under the integrator contract; or
* a response body that does not trim to exactly `ACK/Jasmin`.

The status-check implementation discrepancy for 3xx is documented at the top
of this page. The throwers implement the body and status checks at
`internal/core/dlr/http_thrower.go:111` and
`internal/core/mo/http_thrower.go:112`.

Both worker configurations default to `max_retries = 3` and
`retry_delay = 30` seconds
(`internal/config/thrower_sections.go:30`). `max_retries` means three retries
after the initial attempt: with defaults there are up to four total attempts,
each retry delayed by a fixed 30 seconds. The delivery is then purged
(`internal/core/dlr/thrower_consumer.go:86`,
`internal/core/dlr/thrower_consumer.go:143`,
`internal/core/mo/thrower_consumer.go:313`).

The corresponding default configuration is:

```ini
[dlr-thrower]
http_timeout = 30
retry_delay = 30
max_retries = 3

[deliversm-thrower]
http_timeout = 30
retry_delay = 30
max_retries = 3
```

These defaults are parsed for both sections by
`internal/config/thrower_sections.go:30`.

Q-019 is intentional compatibility behavior: HTTP 404 is not special. It
retries and is eventually purged exactly like other failures
(`docs/reference/legacy-behaviours.md:40`,
`internal/core/dlr/thrower_consumer.go:46`,
`internal/core/mo/thrower_consumer.go:53`).

MO failover routes are the exception. The consumer tries their HTTP connectors
in order within one delivery. It stops at the first acknowledged connector; if
all fail, it rejects without scheduling the normal delayed retry
(`internal/core/mo/thrower_consumer.go:173`). A simple MO route and every HTTP
DLR use the retry-then-purge policy.

Receivers should make processing idempotent by `id` plus the event level or
payload. The gateway may deliver the same callback again whenever it did not
observe a valid acknowledgement.

## Maturity

DLR and long-message paths have not yet completed validation through a
third-party SMPP client
(`docs/plans/017-smpp-production-readiness.md:129`), and the gateway has never
run on a real carrier SMSC
(`docs/plans/017-smpp-production-readiness.md:142`). Modern DLR metric
instrumentation also has no call sites yet
(`docs/plans/017-smpp-production-readiness.md:107`). Treat callback logs and
application-side idempotency as essential while those production-readiness
gates remain open.

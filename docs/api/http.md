# Legacy HTTP API

This is the compatibility HTTP interface for submitting MT messages and reading
the corresponding account data. The same handler registers `/send`, `/balance`,
`/rate`, `/ping`, and `/metrics`
(`internal/transport/httpcompat/handler.go:71`).

This interface deliberately retains Jasmin-facing response bytes. In
particular, errors from `/send` are plain text rather than JSON. Do not discard
the HTTP status, and do not parse a successful submit as JSON.

## Listener

The default listener is `0.0.0.0:1401`. `API_BIND` and `API_PORT` can change
those two defaults; explicit `[http-api]` values take precedence. The other
defaults relevant to this interface are shown here
(`internal/config/sections.go:218`):

```ini
[http-api]
bind = 0.0.0.0
port = 1401
billing_feature = true
long_content_max_parts = 5
long_content_split = udh
```

The single gateway process mounts the REST compatibility facade in front of
this handler without changing the legacy paths
(`internal/transport/restcompat/handler.go:31`).

## Request encoding and authentication

`/send`, `/balance`, and `/rate` accept GET query parameters, POST form fields,
or a POST `application/json` object. Form and query processing keeps the first
value for a repeated key. JSON string and number values become strings; booleans
become `yes` or `no`. JSON nulls, arrays, and objects are rejected, except that
`custom_tlvs` retains its raw JSON value
(`internal/transport/httpcompat/handler.go:520`).

Authentication uses the `username` and `password` request parameters. A
disabled user, a user in a disabled group, an unknown user, and a bad password
all produce the same authentication failure. This is intentional: the
authenticator checks both the user and group enabled state
(`internal/core/http.go:25`,
`internal/app/outbound/config.go:933`,
`internal/app/outbound/config.go:968`).

After password authentication, the user's MT credential controls whether they
may send, query balance, query rate, send long content, or set DLR level,
DLR method, source, priority, validity, hex content, and scheduled delivery.
It also applies regular-expression filters to destination, source, priority,
validity period, and text content
(`internal/core/mtcredential/credential.go:15`,
`internal/core/mtcredential/validate.go:66`,
`internal/core/mtcredential/validate.go:109`).

Omitting `from` permits the user's configured default source address to be
inserted. Sending `from=` is different: it counts as explicitly setting the
source, is authorization- and filter-checked, and suppresses the default
(`internal/transport/httpcompat/credentials.go:79`,
`internal/transport/httpcompat/handler.go:613`).

## `GET|POST /send`

`/send` validates, authenticates, applies credentials and filters, routes,
segments, charges, and durably admits the message before replying
(`internal/transport/httpcompat/handler.go:247`,
`internal/core/submit_service.go:309`,
`internal/core/submit_service.go:552`).

An application can make a form submit with:

```sh
curl --fail-with-body \
  --data-urlencode 'username=alice' \
  --data-urlencode 'password=secret' \
  --data-urlencode 'to=15551234567' \
  --data-urlencode 'content=hello' \
  http://127.0.0.1:1401/send
```

On HTTP 200, the body is exactly the following byte pattern, with no trailing
newline:

```text
Success "<message-id>"
```

For example, if the generated ID is
`86d15f1a-f75b-43f0-ab2e-a9bbde7acdc8`, the exact response bytes are:

```text
Success "86d15f1a-f75b-43f0-ab2e-a9bbde7acdc8"
```

The handler constructs this text directly and sets `Content-Type: text/plain`
(`internal/transport/httpcompat/handler.go:351`,
`internal/transport/httpcompat/handler.go:656`).

### Parameters

The presence of a parameter matters. In particular, exactly one of `content`
and `hex-content` must be present as a key, although its value may be empty
(`internal/transport/httpcompat/handler.go:355`).

| Parameter | Type | Required | Default and constraints |
|---|---|---:|---|
| `username` | string | yes | 1 through 16 characters (`internal/transport/httpcompat/handler.go:31`). |
| `password` | string | yes | 1 through 16 characters (`internal/transport/httpcompat/handler.go:32`). |
| `to` | string | yes | An optional leading `+`, then one or more decimal digits (`internal/transport/httpcompat/handler.go:29`). |
| `content` | string | one of content/hex | Text input. With coding 0 it is converted to the legacy GSM 03.38 representation, replacing unencodable characters (`internal/core/submit_service.go:254`, `internal/core/submit_service.go:630`). |
| `hex-content` | hex string | one of content/hex | Decoded as hexadecimal before routing. Invalid hex reaches the submit path and becomes a 500 error (`internal/core/submit_service.go:619`). |
| `from` | string | no | No syntax check at this boundary. Subject to source authorization/filtering; if absent, the credential's default source may apply (`internal/transport/httpcompat/handler.go:408`, `internal/transport/httpcompat/handler.go:613`). |
| `coding` | integer | no | `0`. Allowed values are 0–10, 13, and 14, excluding 11 and 12 (`internal/transport/httpcompat/handler.go:30`, `internal/transport/httpcompat/handler.go:419`). This value becomes the SMPP `data_coding` and selects segmentation limits (`internal/core/submit_service.go:448`, `internal/core/segmentation/segmentation.go:123`). |
| `priority` | integer | no | `0`. When non-empty, one of 0, 1, 2, or 3 (`internal/transport/httpcompat/handler.go:33`, `internal/transport/httpcompat/handler.go:425`). |
| `sdt` | string | no | Scheduled delivery time in the SMPP 3.4 section 7.1.1 form `YYMMDDhhmmsstnnp` — fifteen decimal digits then `+`, `-` or `R`, sixteen characters total (validated at `internal/transport/httpcompat/handler.go:34`, parsed at `internal/transport/httpcompat/handler.go:466`). With `+` or `-` the value is absolute and the final two digits are a UTC offset in quarter-hours: `260730123456008+` is 12:34:56 at UTC+02:00. With `R` the digits are an offset from now, so `000000020000000R` means two hours from now. A malformed value is rejected with `Argument [sdt] has an invalid value: [...]` rather than being silently sent immediately. |
| `validity-period` | integer | no | Decimal minutes, with no documented upper bound at this boundary (`internal/transport/httpcompat/handler.go:35`, `internal/transport/httpcompat/handler.go:449`). |
| `dlr` | string | no | `no`; exactly `yes` or `no` when non-empty (`internal/transport/httpcompat/handler.go:36`). |
| `dlr-url` | string | no | Must begin with `http://` or `https://` when non-empty (`internal/transport/httpcompat/handler.go:37`). |
| `dlr-level` | integer | no | 1, 2, or 3. Defaults to 1 when DLR delivery is enabled (`internal/transport/httpcompat/handler.go:38`, `internal/transport/httpcompat/handler.go:429`). |
| `dlr-method` | string | no | `POST`; case-insensitive `GET` or `POST`, normalized to upper case (`internal/transport/httpcompat/handler.go:39`, `internal/transport/httpcompat/handler.go:436`). |
| `tags` | string | no | Comma-separated values containing only ASCII letters, digits, `-`, and commas (`internal/transport/httpcompat/handler.go:40`, `internal/transport/httpcompat/handler.go:445`). |
| `custom_tlvs` | JSON value or JSON text | no | Supply a JSON value in an `application/json` request, or JSON text in a form/query parameter. See “Custom TLVs” below (`internal/transport/httpcompat/handler.go:281`, `internal/core/tlv/normalize.go:18`). |

Additional scalar parameters survive request parsing but are not mapped into
the submit request (`internal/transport/httpcompat/handler.go:408`).

### DLR selection

DLR processing is enabled if `dlr=yes`, or if either `dlr-url` or `dlr-level`
is non-empty. Thus `dlr=no` does not disable a supplied callback URL. The level
defaults to 1 and the method to POST
(`internal/transport/httpcompat/handler.go:429`).

An HTTP callback record is stored only when DLR is enabled **and** `dlr-url` is
non-empty. Asking for `dlr=yes` without a URL therefore does not create an HTTP
callback destination (`internal/core/submit_service.go:497`). The callback
request and acknowledgement contract is in
[callbacks.md](callbacks.md).

### Custom TLVs

The HTTP JSON front door accepts `custom_tlvs`. The preferred representation is
an object whose keys are decimal or `0x`-prefixed tags, optionally followed by
a type:

```json
{
  "custom_tlvs": {
    "0x1401:OctetString": "1401778070000018542",
    "0x1400": "1707167205648943173"
  }
}
```

It also accepts legacy four-element arrays such as
`[[5121, null, "Int8", 1707167205648943173]]`, lists of `{tag, value}` objects,
and two- or three-element arrays. The exact accepted shapes and tag rules are
defined at `internal/core/tlv/normalize.go:13`.

Caller order is wire order when raw JSON is supplied. Connector rules can
declare a type, maximum encoded length, or required tag; unconfigured tags pass
through (`internal/core/http.go:81`,
`internal/core/tlv/tlv.go:239`). A normalization error is a 500 response whose
body begins `Error "Unknown error: ` rather than a validation-status response
(`internal/transport/httpcompat/handler.go:281`).

### Coding, splitting, and charging

The split method is an operator setting: `udh` prefixes each part with a User
Data Header; `sar` leaves the short message unprefixed and adds SAR metadata.
Only those two values are valid. The default maximum is five parts and the wire
limit is 255 (`internal/core/submit_service.go:188`,
`internal/core/segmentation/segmentation.go:218`).

The actual byte limits selected by `coding` are:

| `coding` values | Single-part payload | Payload per multipart part |
|---|---:|---:|
| 3, 6, 7, 10 | 140 bytes | 134 bytes |
| 2, 4, 5, 8, 9, 13, 14 | 140 bytes | 134 bytes |
| 0, 1, and other classified values | 160 bytes | 153 bytes |

The classification declares the groups at
`internal/core/segmentation/segmentation.go:123`; the single-part path doubles
the declared 70-unit limit for the 16-bit group at
`internal/core/segmentation/segmentation.go:173`.

Each emitted part is charged separately: the route rate is multiplied by the
number of parts. Throughput, however, is checked once for the logical submit,
not once per part (`internal/core/submit_service.go:342`,
`internal/core/submit_service.go:390`).

The `http_long_content` credential check is not a reliable predictor of the
part count. It compares the raw `content` byte length with the classifier's
declared single limit before coding-0 conversion, ignores `hex-content`, and
does not apply the 16-bit group's doubling used by the real segmenter
(`internal/transport/httpcompat/handler.go:599`,
`internal/core/segmentation/segmentation.go:123`,
`internal/core/segmentation/segmentation.go:173`). For example, coding 8 text
of 71 through 140 bytes is credential-classified as long even though the
segmenter emits one part; long hex content is not classified as long by that
credential gate. This appears to be an implementation defect, but it is the
current authorization behavior.

**Important truncation behavior:** if a payload needs more than
`long_content_max_parts`, the segmenter emits only that many parts and marks the
unconsumed tail internally. The HTTP response does not expose the truncation,
and billing uses only the emitted part count
(`internal/core/segmentation/segmentation.go:194`,
`internal/core/segmentation/segmentation.go:249`,
`internal/core/submit_service.go:389`). An integrator must enforce its own
length ceiling if silent tail loss is unacceptable.

### Throughput ceiling

`http_throughput` is a per-user submits-per-second ceiling. It is implemented as
minimum spacing from the last accepted submit, not as a token bucket: the first
submit passes, there is no burst allowance or queue, and a rejected request
does not advance the clock. State is process-local and resets on restart.
Zero, a negative value, or an unset value means unlimited
(`internal/core/throughput/limiter.go:28`,
`internal/core/throughput/limiter.go:35`).

The check happens after route selection but before billing and consumes one
slot per logical message. Rejection is HTTP 403 with exact body:

```text
Error "User throughput exceeded"
```

(`internal/core/submit_service.go:342`,
`internal/transport/httpcompat/handler.go:332`).

### Error responses

Every `/send` error body is `text/plain`, has no trailing newline, and is
rendered as `Error "<message>"`, with Go string escaping where needed
(`internal/transport/httpcompat/handler.go:647`). The table gives the complete
handler-owned contract. Text in angle brackets is dynamic.

| Status | Exact body bytes or template | Cause |
|---:|---|---|
| 400 | `Error "Mandatory argument [to] is not found."` | Missing/empty `to`; `username` and `password` use the same template (`internal/transport/httpcompat/handler.go:355`). |
| 400 | `Error "Argument [<name>] has an invalid value: [<value>]."` | A constrained parameter fails its regular expression (`internal/transport/httpcompat/handler.go:369`, `internal/transport/httpcompat/handler.go:388`). |
| 400 | `Error "content or hex-content not present."` | Neither content key is present (`internal/transport/httpcompat/handler.go:396`). |
| 400 | `Error "content and hex-content cannot be used both in same request."` | Both content keys are present (`internal/transport/httpcompat/handler.go:401`). |
| 400 | `Error "Invalid JSON request"` | The JSON document cannot be decoded (`internal/transport/httpcompat/handler.go:523`). |
| 400 | `Error "Invalid JSON value for argument [<name>]"` | A non-TLV JSON value is null, an array, or an object (`internal/transport/httpcompat/handler.go:537`). |
| 400 | `Error "Invalid form request"` | Form parsing fails (`internal/transport/httpcompat/handler.go:558`). |
| 400 | `Error "Authorization failed for user [<user>] (<detail>)."` | MT credential authorization fails. The exact detail phrases are listed below (`internal/transport/httpcompat/credentials.go:30`). |
| 400 | `Error "Value filter failed for user [<user>] (<key> filter mismatch)."` | An MT value filter rejects the request (`internal/transport/httpcompat/credentials.go:39`). |
| 400 | `Error "<filter error>"` | A submit-path interceptor returns `ErrFilterRejected`. The built-in rejection is `Error "request rejected by filters"`; wrapped implementations can supply other text (`internal/transport/httpcompat/handler.go:324`, `internal/core/submit_service.go:314`). |
| 403 | `Error "Authentication failure for username:<user>"` | Unknown/disabled user or group, or bad password (`internal/transport/httpcompat/handler.go:510`). |
| 403 | `Error "User throughput exceeded"` | Per-user HTTP throughput exceeded (`internal/transport/httpcompat/handler.go:335`). |
| 500 | `Error "Unknown error: <normalization error>"` | `custom_tlvs` normalization fails (`internal/transport/httpcompat/handler.go:284`). |
| 500 | `Error "Cannot send submit_sm, check SMPPClientManagerPB log file for details"` | Submitter is unavailable, no route/live connector is available, or quota is insufficient (`internal/transport/httpcompat/handler.go:302`, `internal/transport/httpcompat/handler.go:341`). |
| 500 | `Error "<propagated error>"` | Any other submit failure, including invalid hexadecimal content (`internal/transport/httpcompat/handler.go:346`). |
| 405 | `Error "Method not allowed"` | Any method other than GET or POST (`internal/transport/httpcompat/handler.go:247`, `internal/transport/httpcompat/handler.go:666`). |

The authorization `<detail>` is exactly one of:

| Credential check | Exact detail |
|---|---|
| `http_send` | `Cannot send MT messages` |
| `http_long_content` | `Long content not authorized` |
| `set_dlr_level` | `Setting dlr level not authorized` |
| `http_set_dlr_method` | `Setting dlr method not authorized` |
| `set_source_address` | `Setting source address not authorized` |
| `set_priority` | `Setting priority not authorized` |
| `set_validity_period` | `Setting validity period not authorized` |
| `set_hex_content` | `Setting hex content not authorized` |
| `set_schedule_delivery_time` | `Setting schedule delivery time not authorized` |

These strings are the handler's explicit mapping
(`internal/transport/httpcompat/credentials.go:47`).

Not verified: the complete set of concrete text for the final
`<propagated error>` row. That text comes from routing, interception, TLV, storage,
and publication implementations and is deliberately passed through by
`internal/transport/httpcompat/handler.go:346`.

## `GET|POST /balance`

Required parameters are `username` and `password`. The user must have the
`http_balance` authorization (`internal/transport/httpcompat/handler.go:187`,
`internal/core/mtcredential/validate.go:165`).

```sh
curl --fail-with-body \
  'http://127.0.0.1:1401/balance?username=alice&password=secret'
```

The exact HTTP 200 byte template, including spaces and quoted values, is:

```json
{"balance": "<balance-or-ND>", "sms_count": "<count-or-ND>"}
```

There is no trailing newline. An unlimited balance or submit count is the
literal string `ND`
(`internal/transport/httpcompat/handler.go:219`,
`internal/transport/httpcompat/handler.go:670`).

Balance errors are JSON **strings**, not objects:

| Status | Exact body bytes or template | Cause |
|---:|---|---|
| 400 | `"Mandatory argument [username] is not found."` | Missing/empty username. Password uses the same template (`internal/transport/httpcompat/handler.go:197`). |
| 400 | `"Invalid JSON request"` | Invalid JSON document (`internal/transport/httpcompat/handler.go:192`, `internal/transport/httpcompat/handler.go:523`). |
| 400 | `"Invalid JSON value for argument [<name>]"` | Unsupported JSON value (`internal/transport/httpcompat/handler.go:537`). |
| 400 | `"Invalid form request"` | Form parsing failed (`internal/transport/httpcompat/handler.go:558`). |
| 400 | `"Authorization failed for user [<user>] (Cannot check balance)."` | `http_balance` denied (`internal/transport/httpcompat/credentials.go:70`). |
| 403 | `"Authentication failure for username:<user>"` | Authentication or enabled-state failure (`internal/transport/httpcompat/handler.go:510`). |
| 500 | `"Balance backend is not configured"` | No balance reader (`internal/transport/httpcompat/handler.go:210`). |
| 500 | `"<propagated error>"` | A non-authentication authenticator error or balance-reader error is serialized verbatim (`internal/transport/httpcompat/handler.go:500`, `internal/transport/httpcompat/handler.go:214`). |
| 405 | `Error "Method not allowed"` | Method other than GET or POST; this one is plain text (`internal/transport/httpcompat/handler.go:187`, `internal/transport/httpcompat/handler.go:666`). |

Not verified: concrete `<propagated error>` values. The handler exposes
authenticator and balance backend `error.Error()` values without constraining
that vocabulary (`internal/transport/httpcompat/handler.go:500`,
`internal/transport/httpcompat/handler.go:214`).

## `GET|POST /rate`

Required parameters are `username`, `password`, and `to`. Unlike `/send`, this
handler checks only that `to` is non-empty; it does not apply the `/send`
destination syntax regular expression. The user must have `http_rate`
authorization (`internal/transport/httpcompat/handler.go:150`,
`internal/core/mtcredential/validate.go:173`).

```sh
curl --fail-with-body \
  'http://127.0.0.1:1401/rate?username=alice&password=secret&to=15551234567'
```

The exact HTTP 200 byte template is:

```json
{"unit_rate": <number>, "submit_sm_count": <integer>}
```

There is no trailing newline. Integral rates retain a `.0`, for example:

```json
{"unit_rate": 1.0, "submit_sm_count": 1}
```

(`internal/transport/httpcompat/handler.go:182`,
`internal/transport/httpcompat/handler.go:682`).

Rate errors are JSON strings:

| Status | Exact body bytes or template | Cause |
|---:|---|---|
| 400 | `"Mandatory argument [username] is not found."` | Missing username; password and `to` use the same template (`internal/transport/httpcompat/handler.go:160`). |
| 400 | `"Invalid JSON request"` | Invalid JSON document (`internal/transport/httpcompat/handler.go:155`, `internal/transport/httpcompat/handler.go:523`). |
| 400 | `"Invalid JSON value for argument [<name>]"` | Unsupported JSON value (`internal/transport/httpcompat/handler.go:537`). |
| 400 | `"Invalid form request"` | Form parsing failed (`internal/transport/httpcompat/handler.go:558`). |
| 400 | `"Authorization failed for user [<user>] (Cannot check rate)."` | `http_rate` denied (`internal/transport/httpcompat/credentials.go:72`). |
| 403 | `"Authentication failure for username:<user>"` | Authentication or enabled-state failure (`internal/transport/httpcompat/handler.go:510`). |
| 500 | `"Rate backend is not configured"` | No rate reader (`internal/transport/httpcompat/handler.go:173`). |
| 500 | `"<propagated error>"` | A non-authentication authenticator error or rate-reader error is serialized verbatim (`internal/transport/httpcompat/handler.go:500`, `internal/transport/httpcompat/handler.go:177`). |
| 405 | `Error "Method not allowed"` | Method other than GET or POST; this one is plain text (`internal/transport/httpcompat/handler.go:150`, `internal/transport/httpcompat/handler.go:666`). |

Not verified: concrete `<propagated error>` values. The handler passes through
authenticator and rate backend error text
(`internal/transport/httpcompat/handler.go:500`,
`internal/transport/httpcompat/handler.go:177`).

## `GET|POST /ping`

This endpoint takes no parameters and performs no authentication.

```sh
curl --fail-with-body http://127.0.0.1:1401/ping
```

HTTP 200 has exactly these bytes, without a newline or `Content-Type` header:

```text
Jasmin/PONG
```

GET and POST are accepted. Every other method receives HTTP 405 and exact body
`Error "Method not allowed"`
(`internal/transport/httpcompat/handler.go:142`).

## `GET /metrics`

This endpoint takes no parameters and performs no authentication. It is GET
only and returns HTTP 200 with `Content-Type: text/plain`
(`internal/transport/httpcompat/handler.go:224`).

```sh
curl --fail-with-body http://127.0.0.1:1401/metrics
```

The body is Prometheus text. Every metric is emitted as exact lines in this
order:

```text
# TYPE <name> counter
# HELP <name> <help text>
<name> <integer>
```

HTTP metrics use the `httpapi_` prefix; SMSC client metrics use `smppc_` and a
`cid` label; SMPP server metrics use `smppsapi_`. The response ends with two
empty lines (`internal/core/stats/stats.go:179`). The HTTP metric names and help
text are enumerated at `internal/core/stats/stats.go:20`, the SMPPc metrics at
`internal/core/stats/stats.go:33`, and the SMPP server metrics at
`internal/core/stats/stats.go:49`.

The exact body is necessarily runtime-dependent because connector IDs and
counter values are runtime data. With no metric registries and no connectors,
the renderer returns a single newline byte; production supplies at least the
HTTP registry (`internal/core/stats/stats.go:186`,
`internal/app/outbound/runtime.go:387`).

Any method other than GET receives HTTP 405 and exact body
`Error "Method not allowed"`
(`internal/transport/httpcompat/handler.go:226`).

## What a successful submit proves

A successful `/send` means the logical parts and their outbox publication
events were committed atomically to PostgreSQL; it does not mean that RabbitMQ
published them, that an SMSC accepted them, or that a handset received them
(`internal/core/submittransaction/service.go:57`,
`internal/infra/storage/postgres_submittransaction.go:115`).
Use a DLR callback where the application needs later carrier state.

The gateway has not yet been run against a real carrier SMSC
(`docs/plans/017-smpp-production-readiness.md:142`). There is no sustained-load
or 24-hour-soak evidence
(`docs/plans/017-smpp-production-readiness.md:137`), and modern metrics
instrumentation is incomplete
(`docs/plans/017-smpp-production-readiness.md:101`). Treat this API as
compatibility-tested software awaiting those production-readiness gates, not as
a carrier-proven service.

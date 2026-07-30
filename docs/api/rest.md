# Secure REST API

The REST compatibility layer provides JSON resources at `/secure/send`,
`/secure/sendbatch`, `/secure/balance`, and `/secure/rate`. Its standalone
listener also provides a JSON `/ping`. It delegates validation, credentials,
routing, billing, and single-message submission to the legacy HTTP handler
rather than maintaining a second submit path
(`internal/transport/restcompat/handler.go:1`,
`internal/transport/restcompat/handler.go:108`).

The `/secure/*` paths are also mounted on the main HTTP listener. Setting
`rest_api.listen_address` additionally enables the historical standalone REST
view; only that view gives `/ping` the REST JSON response
(`internal/transport/restcompat/config.go:18`,
`internal/transport/restcompat/handler.go:48`).

## Configuration

This is a complete example using the code defaults and an example standalone
listen address:

```json
{
  "rest_api": {
    "listen_address": "127.0.0.1:8080",
    "http_throughput_per_worker": 8,
    "smart_qos": true,
    "max_pending_tasks": 10000,
    "max_attempts": 3,
    "retry_delay_seconds": 1,
    "callback_max_attempts": 5,
    "callback_retry_delay_seconds": 1
  }
}
```

The field names are the gateway JSON contract
(`internal/transport/restcompat/config.go:18`), and the defaults come from
`internal/transport/restcompat/config.go:9`. An omitted or empty
`listen_address` does not create the standalone listener; `/secure/*` remains
available on the main listener
(`internal/transport/restcompat/config.go:18`).

`http_throughput_per_worker` paces asynchronous batch work. `smart_qos` can
reduce that rate when observed request duration rises. These settings are
separate from the per-user `http_throughput` ceiling described in
[http.md](http.md#throughput-ceiling)
(`internal/transport/restcompat/batch.go:395`).

## Common wire contract

Secure resources require HTTP Basic authentication. The decoded token must
contain `username:password`; splitting is at the first colon, so a password may
contain additional colons. Credentials in a JSON body are ignored and replaced
by Basic-auth credentials
(`internal/transport/restcompat/handler.go:470`,
`internal/transport/restcompat/handler.go:573`).

The user and group enabled-state rules and MT credential checks are the same as
for the legacy API because requests are delegated to it. A structurally valid
Basic token is not proof of authentication until the delegated operation runs
(`internal/transport/restcompat/handler.go:450`,
`internal/app/outbound/config.go:933`).

`Accept` may be absent or admit `*/*`, `application/*`, `application/json`, or a
media type ending in `+json`, with a positive quality value. Negotiation runs
before authentication. Other values receive HTTP 415
(`internal/transport/restcompat/handler.go:420`,
`internal/transport/restcompat/handler.go:521`).

Every REST response is compact JSON with no trailing newline and these headers:

```text
Content-Type: application/json
Powered-By: Jasmin 0.11.1
Vary: Accept
```

(`internal/transport/restcompat/handler.go:556`).

Successful operations use:

```json
{"data":<value>}
```

Errors created by the REST facade use:

```json
{
  "title": "<summary>",
  "description": "<optional detail>",
  "link": {
    "text": "Documentation related to this error",
    "href": "<help URL>",
    "rel": "help"
  }
}
```

Optional empty fields are omitted
(`internal/transport/restcompat/handler.go:221`,
`internal/transport/restcompat/handler.go:544`). Errors returned by a delegated
legacy endpoint instead preserve its HTTP status and entire body string:

```json
{"message":"<legacy response body>"}
```

(`internal/transport/restcompat/handler.go:450`).

The common exact errors are:

| Status | Exact body bytes |
|---:|---|
| 401 | `{"title":"Authentication required","description":"Please provide a valid Basic auth token","link":{"text":"Documentation related to this error","href":"http://docs.jasminsms.com/en/latest/apis/rest/index.html","rel":"help"}}` |
| 401 | `{"title":"Invalid token","description":"Please provide a valid Basic auth token","link":{"text":"Documentation related to this error","href":"http://docs.jasminsms.com/en/latest/apis/rest/index.html","rel":"help"}}` |
| 405 | `{"title":"405 Method Not Allowed"}` |
| 415 | `{"title":"Unsupported media type","description":"This API supports JSON media type only.","link":{"text":"Documentation related to this error","href":"http://docs.jasminsms.com/en/latest/apis/rest/index.html","rel":"help"}}` |

The missing- and malformed-token branches are at
`internal/transport/restcompat/handler.go:470`; method and media-type handling
are at `internal/transport/restcompat/handler.go:420`. Invalid base64 appends
the Go decoder's detail to the title (`Invalid token: <decoder error>`), so that
specific title is not stable across Go implementations
(`internal/transport/restcompat/handler.go:483`).

An authenticated `OPTIONS` request to a secure resource returns HTTP 200,
body `null`, and an `Allow` header containing the resource's primary method
(`internal/transport/restcompat/handler.go:437`).

## `POST /secure/send`

The request body must be exactly one JSON object and must not exceed 4 MiB.
Trailing JSON content, `null`, and other top-level shapes are rejected
(`internal/transport/restcompat/handler.go:24`,
`internal/transport/restcompat/handler.go:498`).

The schema is the `/send` parameter table in
[http.md](http.md#parameters), with underscores accepted as aliases for hyphens:
`hex_content`, `validity_period`, `dlr_url`, `dlr_level`, and `dlr_method`.
Every underscore in a key is converted to a hyphen except in `custom_tlvs`
(`internal/transport/restcompat/handler.go:573`). Basic auth supplies
`username` and `password`.

```sh
curl --fail-with-body \
  --user 'alice:secret' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --data '{"to":"15551234567","content":"hello","dlr_level":3,"dlr_url":"https://app.example/dlr"}' \
  http://127.0.0.1:1401/secure/send
```

If the delegated `/send` generated
`86d15f1a-f75b-43f0-ab2e-a9bbde7acdc8`, the exact response bytes are:

```json
{"data":"Success \"86d15f1a-f75b-43f0-ab2e-a9bbde7acdc8\""}
```

The facade wraps the legacy plain-text success as a JSON string
(`internal/transport/restcompat/handler.go:241`,
`internal/transport/restcompat/handler.go:460`).

An invalid request body receives HTTP 412. The exact invariant envelope is:

```json
{"title":"Cannot parse JSON data","description":"Got unparseable json data: <decoder detail>"}
```

The decoder detail is supplied by Go and depends on the malformed input
(`internal/transport/restcompat/handler.go:243`). Every validation,
authentication, authorization, routing, quota, throughput, and storage error
from `/send` keeps that endpoint's status and becomes:

```json
{"message":"Error \"<legacy message>\""}
```

(`internal/transport/restcompat/handler.go:450`). See
[the legacy error table](http.md#error-responses) for the exact inner string.

## `GET /secure/balance`

Basic auth supplies the only inputs. The user must have the same
`http_balance` authorization required by `/balance`
(`internal/transport/restcompat/handler.go:371`).

```sh
curl --fail-with-body \
  --user 'alice:secret' \
  --header 'Accept: application/json' \
  http://127.0.0.1:1401/secure/balance
```

For balance `10.23` and unlimited submit count, the exact response is:

```json
{"data":{"balance":"10.23","sms_count":"ND"}}
```

The facade parses the delegated JSON object before wrapping it
(`internal/transport/restcompat/handler.go:450`). Failures retain their legacy
status and body inside `{"message":"..."}`; the complete underlying responses
are in [http.md](http.md#getpost-balance).

## `GET /secure/rate`

`to` is the required query parameter. Query names containing underscores are
translated to hyphens before delegation; Basic auth supplies the credentials
(`internal/transport/restcompat/handler.go:386`).

```sh
curl --fail-with-body \
  --user 'alice:secret' \
  --header 'Accept: application/json' \
  'http://127.0.0.1:1401/secure/rate?to=15551234567'
```

For rate `0.02` and one submit, the exact response is:

```json
{"data":{"unit_rate":0.02,"submit_sm_count":1}}
```

The delegated result is decoded and re-encoded compactly
(`internal/transport/restcompat/handler.go:460`). Errors use the delegated
status and `{"message":"<legacy body>"}`. See
[http.md](http.md#getpost-rate) for the inner response contract.

## `GET /ping`

This JSON form exists on the standalone REST listener and does not require
authentication. The combined main listener leaves `/ping` as the legacy text
endpoint (`internal/transport/restcompat/handler.go:48`,
`internal/transport/restcompat/handler.go:119`).

```sh
curl --fail-with-body \
  --header 'Accept: application/json' \
  http://127.0.0.1:8080/ping
```

The exact HTTP 200 body is:

```json
{"data":"Jasmin/PONG"}
```

The standalone handler delegates to legacy `/ping` and wraps the body
(`internal/transport/restcompat/handler.go:314`). Other methods receive HTTP
405 and exact body `{"title":"405 Method Not Allowed"}`.

## `POST /secure/sendbatch`

This endpoint accepts work asynchronously. Production supplies a dispatcher
context and PostgreSQL store, so the route is registered in the gateway. A
library caller that constructs the handler without a batch context gets an
authenticated 404 instead
(`internal/transport/restcompat/handler.go:31`,
`internal/app/outbound/runtime.go:396`).

### Request schema

```json
{
  "globals": {
    "from": "Example",
    "coding": 0
  },
  "batch_config": {
    "schedule_at": "60s",
    "callback_url": "https://app.example/batch-ok",
    "errback_url": "https://app.example/batch-error"
  },
  "messages": [
    {
      "to": ["15551234567", "15557654321"],
      "content": "hello"
    }
  ]
}
```

All three top-level fields are optional in the decoder:

| Field | Type and semantics |
|---|---|
| `globals` | Object merged into every message. A message's own value wins (`internal/transport/restcompat/batch.go:157`, `internal/transport/restcompat/batch.go:211`). |
| `batch_config` | Object containing the scheduling and callback fields described below (`internal/transport/restcompat/batch.go:165`). Unrecognized members are ignored. |
| `messages` | Array of message objects. A missing array is accepted as a zero-task batch (`internal/transport/restcompat/batch.go:192`). |

Each message uses the `/secure/send` fields. `to` may be one string, one JSON
number, or an array of strings and numbers; an array expands to one durable task
per destination (`internal/transport/restcompat/batch.go:220`,
`internal/transport/restcompat/batch.go:266`). A row without `to`, or without
both `content` and `hex_content`/`hex-content`, is silently skipped
(`internal/transport/restcompat/batch.go:215`). `messageCount` reports expanded,
non-skipped tasks, not input rows.

`batch_config` fields are:

| Field | Required | Default and constraints |
|---|---:|---|
| `schedule_at` | no | Immediate. Either digits followed by `s`, such as `60s`, or `YYYY-MM-DD HH:MM:SS` in the gateway process's local time zone. Past absolute times fail (`internal/transport/restcompat/batch.go:303`). |
| `callback_url` | no | Absolute `http` or `https` URL without embedded credentials; called after each successful task (`internal/transport/restcompat/batch.go:177`, `internal/transport/restcompat/batch.go:350`). |
| `errback_url` | no | Same URL constraints; called after each terminally failed task (`internal/transport/restcompat/batch.go:184`, `internal/transport/restcompat/batch.go:514`). |

The absolute-time parser's error description says
`YYYY-MM-DD mm:hh:ss`, but the actual parser is year-month-day, then
hour:minute:second. This is an inherited response-text defect; use the format in
the table (`internal/transport/restcompat/batch.go:323`).

### Authentication behavior

Before parsing a batch, the facade authenticates it by making an internal
`/balance` request. Consequently the current implementation requires
`http_balance`; it does **not** consult the `http_bulk` authorization
(`internal/transport/restcompat/handler.go:270`,
`internal/transport/restcompat/handler.go:342`). This is surprising current
behavior, not a recommendation.

The accepted batch stores a SHA-256 password proof, not plaintext. Each task
checks that proof and the user's current enabled state when it actually runs
(`internal/transport/restcompat/batch.go:203`,
`internal/transport/restcompat/batch_store.go:45`,
`internal/core/http.go:31`). The production authenticator accepts that delayed
proof only for users configured with a SHA-256 digest. A migrated MD5-only user
can pass the initial balance authentication and then have every task fail
authentication (`internal/app/outbound/config.go:933`,
`internal/app/outbound/config.go:955`).

### Admission response

```sh
curl --fail-with-body \
  --user 'alice:secret' \
  --header 'Accept: application/json' \
  --header 'Content-Type: application/json' \
  --data '{
    "batch_config":{"schedule_at":"60s"},
    "messages":[{"to":["15551234567","15557654321"],"content":"hello"}]
  }' \
  http://127.0.0.1:1401/secure/sendbatch
```

If the generated batch ID is
`0c526681-e87f-4892-8398-5a980c3a94e0`, the exact response bytes are:

```json
{"data":{"batchId":"0c526681-e87f-4892-8398-5a980c3a94e0","messageCount":2,"scheduled":"60s"}}
```

An immediate batch omits `scheduled`
(`internal/transport/restcompat/handler.go:303`). IDs are random UUIDs generated
server-side; the request schema has no caller-supplied idempotency key
(`internal/transport/restcompat/batch.go:152`,
`internal/transport/restcompat/batch.go:650`).

### Task execution and retries

Each expanded task later passes through `/send`, so it receives the same
parameter validation, credentials, filters, user throughput, routing,
segmentation, and charging behavior
(`internal/transport/restcompat/batch.go:454`). The batch worker's own default
pace is eight messages per second and may be reduced by smart QoS
(`internal/transport/restcompat/config.go:9`,
`internal/transport/restcompat/batch.go:395`).

By default, a task gets at most three total attempts. Retries occur only for
HTTP 429, 502, 503, 504, and a 403 body containing `throughput exceeded`.
The retry delays are exponential: one second after attempt 1, then two seconds
after attempt 2. Other failures, including the legacy handler's common 500
submit error, become terminal immediately
(`internal/transport/restcompat/batch.go:496`,
`internal/transport/restcompat/batch.go:617`,
`internal/transport/restcompat/config.go:9`).

### Completion callbacks

For each terminal task, `callback_url` is selected on success and `errback_url`
on failure. Delivery is an HTTP GET whose query contains:

| Parameter | Meaning |
|---|---|
| `batchId` | Server-generated batch ID. |
| `to` | Expanded destination for this task. |
| `status` | `1` for success, `0` for failure. |
| `statusText` | Exact `/send` success body, or failure text such as `HTTPAPI error: Error "..."`. |

The fields and GET method are defined at
`internal/transport/restcompat/batch.go:567`; status selection is at
`internal/transport/restcompat/batch_store.go:216`.

Any HTTP 2xx acknowledges a batch callback; its response body is ignored. The
HTTP client timeout defaults to 30 seconds
(`internal/transport/restcompat/batch.go:582`,
`internal/transport/restcompat/handler.go:72`). By default there are five total
callback attempts, after delays of 1, 2, 4, and 8 seconds. After attempt 5, the
callback is abandoned but the already-terminal task is not resubmitted
(`internal/transport/restcompat/batch.go:526`,
`internal/transport/restcompat/config.go:9`).

Batch completion callbacks are not DLRs and do not require `ACK/Jasmin`. The
MO/DLR callback contract is documented in
[callbacks.md](callbacks.md).

### Batch errors

The facade uses HTTP 412 for authentication, JSON, and schema failures unless a
more specific status is stated
(`internal/transport/restcompat/handler.go:270`). Stable batch errors include:

| Status | Exact body bytes |
|---:|---|
| 412 | `{"title":"Authentication failed","description":"Authentication failed for user: <user>"}` |
| 412 | `{"title":"Cannot parse globals","description":"globals must be a JSON object"}` |
| 412 | `{"title":"Cannot parse batch_config","description":"batch_config must be a JSON object"}` |
| 412 | `{"title":"Cannot parse messages","description":"messages must be a JSON array"}` |
| 412 | `{"title":"Cannot parse messages","description":"every message must be a JSON object"}` |
| 412 | `{"title":"Cannot parse scheduled_at value","description":"schedule_at must be a string"}` |
| 412 | `{"title":"Cannot parse scheduled_at value","description":"relative schedule is out of range"}` |
| 412 | `{"title":"Cannot parse callback_url","description":"value must be a string"}` |
| 412 | `{"title":"Cannot parse callback_url","description":"value must be an absolute HTTP(S) URL"}` |
| 412 | `{"title":"Cannot parse callback_url","description":"value must be an absolute HTTP(S) URL without embedded credentials"}` |
| 412 | `{"title":"Cannot parse errback_url","description":"value must be a string"}` |
| 412 | `{"title":"Cannot parse errback_url","description":"value must be an absolute HTTP(S) URL"}` |
| 412 | `{"title":"Cannot parse errback_url","description":"value must be an absolute HTTP(S) URL without embedded credentials"}` |
| 412 | `{"title":"Cannot parse destination","description":"to array values must be strings or numbers"}` |
| 412 | `{"title":"Cannot parse destination","description":"to must be a string, number or array"}` |
| 429 | `{"title":"Batch queue is full","description":"The request expands beyond the configured durable batch backlog limit."}` |
| 429 | `{"title":"Batch queue is full","description":"The durable sendbatch backlog has reached its configured limit."}` |
| 503 | `{"title":"Cannot persist batch","description":"<storage error>"}` |

The first authentication response is constructed at
`internal/transport/restcompat/handler.go:270`; object and array validation is at
`internal/transport/restcompat/batch.go:157`; URL errors are at
`internal/transport/restcompat/batch.go:177`; schedule errors are at
`internal/transport/restcompat/batch.go:303`; destination errors are at
`internal/transport/restcompat/batch.go:266`; the two backlog boundaries are at
`internal/transport/restcompat/batch.go:229` and
`internal/transport/restcompat/handler.go:294`.

Not verified: every concrete JSON decoder, UUID entropy, PostgreSQL, and
schedule error description. Those errors are included dynamically by
`internal/transport/restcompat/handler.go:279`,
`internal/transport/restcompat/batch.go:152`, and
`internal/transport/restcompat/handler.go:300`.

## Durability and safe client retries

Production acknowledges `/secure/sendbatch` only after PostgreSQL has
atomically inserted the batch and every expanded task. Backlog counting and
insertion occur in one transaction, and the HTTP success follows that commit
(`internal/infra/storage/postgres_rest_batch.go:66`,
`internal/transport/restcompat/batch.go:373`,
`internal/transport/restcompat/handler.go:294`). Leased task and callback claims
are recovered after a worker restart
(`internal/infra/storage/postgres_rest_batch.go:127`,
`internal/transport/restcompat/batch.go:123`).

Each stored task has a stable UUID. If a worker restarts after its `/send` was
durably admitted but before the batch row was completed, the submit service
recognizes that same task ID and returns without charging or publishing it
again (`internal/core/http.go:60`,
`internal/core/submit_service.go:245`). This protects retries of an already
stored task; it does not deduplicate a second client request.

`/secure/send` also acknowledges only after its parts and outbox records commit
to PostgreSQL
(`internal/core/submittransaction/service.go:57`,
`internal/infra/storage/postgres_submittransaction.go:115`).
Neither endpoint's HTTP 200 means carrier acceptance.

For both endpoints, a client that receives HTTP 200 must not retry merely
because carrier delivery is still pending. If the connection fails before the
client receives the response, the outcome is ambiguous: retrying the request
creates new server IDs and can submit and charge duplicates. The public request
schemas expose no idempotency key
(`internal/core/submit_service.go:407`,
`internal/transport/restcompat/batch.go:152`). Record the returned ID when
available, and make batch callbacks idempotent because callback delivery is
lease-based and the HTTP request and completion record are separate operations
(`internal/transport/restcompat/batch.go:526`).

## Maturity

The REST durability boundary is implemented with PostgreSQL, but the wider
gateway has not run on a real carrier link
(`docs/plans/017-smpp-production-readiness.md:142`). There is no load or soak
evidence (`docs/plans/017-smpp-production-readiness.md:137`), and retry,
reconnect, and restart charging reconciliation remains an open gate
(`docs/plans/017-smpp-production-readiness.md:92`). Those gaps matter most for
large scheduled batches; do not infer carrier-proven throughput or
exactly-once end-to-end SMS delivery from durable API admission.

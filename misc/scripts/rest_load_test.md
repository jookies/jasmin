# `rest_load_test.py` — Jasmin REST API load tester

A self-contained Python script that drives traffic through Jasmin's REST API
(`/secure/send` and `/secure/sendbatch`) so you can exercise the submit path
end-to-end (REST → HTTP API → SMPP), measure throughput and latency, and
stress the TLV code path (`custom_tlvs`).

- File: [`rest_load_test.py`](./rest_load_test.py)
- Dependencies: **Python 3.10+ only** — no pip install, pure stdlib (`http.client`, `threading`, `argparse`)
- No external test framework (no `locust`, no `aiohttp`, no `requests`) — plain stdlib threads

---

## 1. Prerequisites

Before this script can get `200 OK` responses you need all of the following up:

1. **Jasmin REST API process** reachable at `--url` (default
   `http://127.0.0.1:1401`).
   The stock `jasmin:0.12-tlv` container starts `jasmind.py` only — REST is
   **not** included. Either:
   - build/run `docker/Dockerfile.restapi`, or
   - run `jasmin-restapi.py` on your Mac pointing at the same RabbitMQ/Redis.
2. **A Jasmin user** with credentials and enough balance.
   Create one via `jCli` (telnet `localhost 8990`, `jcliadmin`/`jclipwd`):
   ```
   jcli : user -a
   > uid foo
   > username foo
   > password bar
   > gid g1
   > ok
   ```
3. **An MT route** that maps your destination numbers to a running SMPP
   connector.
4. **SMSC** (or SMPPSim) bound and accepting PDUs.
5. **Connector TLV rules** (if using `--tlv`). The connector config declares
   each tag's type and validation rules — see section 5. The load test
   script sends `{tag: value}` pairs; the connector resolves the encoding
   type (Int8, OctetString, etc.) at dispatch time.

Without #1 the script will fail with `connection_error`. Without #2–#4 you'll
get HTTP 412/403 from Jasmin with "Authentication failed" or "Cannot route".

---

## 2. Quick start

**Single smoke message**
```bash
python misc/scripts/rest_load_test.py \
  --url http://127.0.0.1:1401 \
  --username foo --password bar \
  --count 1 -v
```
Expect one `[ok]` line and a summary with `ok=1`.

**200 messages, 50/s, with custom TLVs**
```bash
python misc/scripts/rest_load_test.py \
  --username foo --password bar \
  --count 200 --concurrency 20 --rate 50 \
  --to '+1202555{i:04d}' --content 'load {i}' \
  --tlv 0x1400:1707167205648943173 \
  --tlv 0x1401:1401778070000018542
```

**60-second burst via batch endpoint (50 msgs per HTTP call)**
```bash
python misc/scripts/rest_load_test.py \
  --username foo --password bar \
  --mode sendbatch --duration 60 \
  --concurrency 8 --batch-size 50 --rate 500
```

**CI/CD style with JSON stats & fail threshold**
```bash
python misc/scripts/rest_load_test.py \
  --username foo --password bar --count 500 \
  --json-out /tmp/load.json --fail-on-error-rate 0.01
```

---

## 3. CLI reference

### Connection
| flag | default | notes |
| --- | --- | --- |
| `--url` | `http://127.0.0.1:1401` | Also honours `JASMIN_REST_URL` env var. |
| `--username` | *required* | Jasmin user (Basic Auth). |
| `--password` | *required* | Jasmin password. |
| `--insecure` | off | Disable TLS verify (self-signed `rest-api.cfg`). |
| `--timeout` | `10` | Per-request HTTP timeout (seconds). |

### Workload
| flag | default | notes |
| --- | --- | --- |
| `--mode` | `send` | `send` = one POST per SMS; `sendbatch` = one POST per `--batch-size` messages. |
| `--count N` | — | Total messages. **Mutually exclusive with `--duration`.** |
| `--duration SEC` | — | Time-bounded run. |
| `--concurrency N` | `10` | Worker threads. |
| `--rate R` | `0` | Target msgs/sec (token bucket). `0` = unbounded. |
| `--batch-size N` | `50` | Messages per `/secure/sendbatch` call. |
| `--ramp-up SEC` | `0` | Stagger worker starts over this many seconds. |

### Message content
| flag | default | notes |
| --- | --- | --- |
| `--to` | `+11234567890` | Destination MSISDN. `{i}` placeholder expands to the message index (e.g. `'+1202555{i:04d}'`). |
| `--from` | — | Source address (also supports `{i}`). |
| `--content` | `load test {i}` | Message body. |
| `--priority` | — | `0..3`. |
| `--dlr-mask` | — | e.g. `1` (delivery only), `7` (all). |
| `--validity-period` | — | Passed through as-is. |

### TLVs

Repeat `--tlv TAG:VALUE[:TYPE]` as many times as needed. Type is optional
(resolved from connector config when omitted).

```bash
# Type from connector config (simplest)
--tlv 0x1400:1707167205648943173
--tlv 0x1401:1401778070000018542

# Explicit type override (appended at end)
--tlv 0x1400:1707167205648943173:OctetString
--tlv 0x1401:1401778070000018542:Int8
--tlv 0x1402:9638ecb402...a042:OctetString

# Named standard SMPP optional param (top-level body field)
--tlv source_port:1234
--tlv user_message_reference:42
```

- **Numeric tag** → sent as `{"0xTAG": value}` or `{"0xTAG:Type": value}`.
- **Named tag** → placed as a top-level JSON field.
- Unknown non-numeric names are rejected at parse time — use a numeric tag
  for custom TLVs.

### Reporting
| flag | default | notes |
| --- | --- | --- |
| `--log-every N` | `100` | Print progress every N completed requests (stderr). |
| `-v` / `--verbose` | off | Log every request (use sparingly under load). |
| `--json-out PATH` | — | Write final summary as JSON to PATH. |
| `--fail-on-error-rate F` | — | Exit non-zero if observed error rate > F (e.g. `0.01`). |

---

## 4. Output

Progress line (stderr, every `--log-every`):
```
progress: total=400 ok=398 fail=2 thrpt=47.3/s p95=88.5ms
```

Final summary (stdout):
```
=== Load test summary ===
  endpoint          : POST /secure/send
  mode              : send
  requested         : 500 msgs
  concurrency       : 20
  target rate       : 100.0/s
  ok                : 498
  failed            : 2
  error rate        : 0.40%
  elapsed           : 5.03s
  throughput        : 99.4/s
  latency ms        : min=12.4 p50=31.0 p95=88.5 p99=124.7 max=201.3 avg=37.2
  errors:
        2  HTTP 412
```

JSON summary (`--json-out`):
```json
{
  "ok": 498,
  "failed": 2,
  "total": 500,
  "error_rate": 0.004,
  "elapsed_sec": 5.03,
  "throughput_per_sec": 99.4,
  "latency_ms": {"min": 12.4, "p50": 31.0, "p95": 88.5, "p99": 124.7, "max": 201.3, "avg": 37.2},
  "errors": {"HTTP 412": 2}
}
```

---

## 5. TLV architecture

### How TLVs flow through Jasmin

```
REST / HTTP caller                    Jasmin connector config (jCli)
─────────────────                     ─────────────────────────────
custom_tlvs: {                        custom_tlvs:
  "0x1401": 170716720...,              0x1401,Int8,8,required;
  "0x1400": 140177807...               0x1400,Int8,8,required
}

        │                                        │
        ▼                                        │
  normalize_custom_tlvs()                        │
  → [(0x1401, None, None, 170...)]               │
        │                                        │
        ▼                                        │
  SubmitSM(custom_tlvs=...) → pickle → AMQP     │
        │                                        │
        ▼                                        ▼
  listeners.py ─── resolve_tlv_types() ◄── connector rules
        │          → [(0x1401, None, 'Int8', 170...)]
        │
        ├── validate_custom_tlvs()
        │   - required tag missing? → reject
        │   - encoded length > max? → reject
        │
        ▼
  sendDataRequest(pdu)
        │
        ▼
  PDUEncoder.encodeRawParams(pdu.custom_tlvs)
        │
        ▼
  SMPP wire: 14 01 00 08 17 b1 13 87 50 e6 c8 45
```

### Connector config (validation-only, no default injection)

The connector declares each vendor TLV tag's **type**, **max byte length**,
and whether it's **required**. It does NOT carry a default value — values come
from the submitter at submit time.

**jCli format**: `tag,type,max_length,required|optional`

```
smppccm -u smalert
> custom_tlvs 0x1401,Int8,8,required;0x1400,Int8,8,required
> ok
```

| field | values | notes |
|---|---|---|
| `tag` | hex (`0x1401`) or decimal (`5121`) | Vendor-range: `0x1400`–`0x3FFF` |
| `type` | `Int1`, `Int2`, `Int4`, `Int8`, `OctetString`, `COctetString` | Determines wire encoding. Must match what the upstream SMSC expects. |
| `max_length` | positive integer, or `-` for unlimited | Max **encoded** byte length. `Int8` = always 8. `OctetString` = byte count of UTF-8 value. |
| `required` | `required` or `optional` (default) | If `required`, submit is rejected when the tag is absent from per-message `custom_tlvs`. |

### REST API `custom_tlvs` format

The `custom_tlvs` field is a JSON dict where each key is a hex tag (with an
optional `:Type` suffix) and each value is the TLV payload.

#### Example 1 — Type resolved from connector config (simplest)

```json
{
  "to": "+919216217231",
  "from": "ABXOTP",
  "content": "Your OTP is 5249",
  "custom_tlvs": {
    "0x1401": "1401778070000018542",
    "0x1400": "1707167205648943173"
  }
}
```

The connector config declares `0x1401,OctetString,20,required` so
Jasmin encodes the value as a 19-byte UTF-8 OctetString on the wire.
The caller doesn't need to know the encoding type.

#### Example 2 — Caller declares type explicitly

```json
{
  "custom_tlvs": {
    "0x1401:OctetString": "1401778070000018542",
    "0x1400:Int8": 1707167205648943173,
    "0x1402:OctetString": "9c70f8165e4cadbb1965d9d105d5543c3ade38aa74dac1b58a76faeec3b413bc"
  }
}
```

The `:Type` suffix in the key overrides whatever the connector config
declares. Useful when the same tag needs different encodings across
different callers, or for ad-hoc tags not in the connector config.

#### Example 3 — Interceptor-generated TLV (0x1402 hash)

The caller sends only 0x1400 and 0x1401; an MT interceptor script
computes SHA-256 and injects 0x1402 automatically:

```json
{
  "custom_tlvs": {
    "0x1401": "1401778070000018542",
    "0x1400": "1707167205648943173"
  }
}
```

The interceptor appends `0x1402` to the PDU before dispatch:
```
wire: 0x1400 len=19 '1707167205648943173'
      0x1401 len=19 '1401778070000018542'
      0x1402 len=64 '9c70f8165e4cadbb1965d9d105d5543c3ade38aa74dac1b58a76faeec3b413bc'
```

See `misc/scripts/interceptor_hash_tlv.py` for the interceptor script.

#### Example 4 — Legacy tuple format (backward compatible)

```json
{
  "custom_tlvs": [
    [5121, null, "OctetString", "1401778070000018542"],
    [5120, null, "Int8", 1707167205648943173]
  ]
}
```

Still works but not recommended — use the dict format above.

#### Key format summary

| key format | type source | example |
|---|---|---|
| `"0x1401"` | Connector config | `"0x1401": "value"` |
| `"0x1401:OctetString"` | Caller (explicit) | `"0x1401:OctetString": "value"` |
| `"0x1401:Int8"` | Caller (explicit) | `"0x1401:Int8": 1707167205648943173` |
| *(not in key)* | Interceptor script | Interceptor calls `routable.addCustomTlv(0x1402, 'OctetString', hash)` |

#### Type resolution priority

1. **Caller-provided type** (`"0x1401:Int8"`) → highest priority
2. **Connector config type** (`0x1401,OctetString,20,required`) → when caller omits type
3. **OctetString fallback** → when tag is not in connector config at all

#### `--tlv` flag in `rest_load_test.py`

The load test script mirrors the same three formats:

```bash
# Type from connector config
--tlv 0x1400:1707167205648943173

# Explicit type (appended at end)
--tlv 0x1400:1707167205648943173:OctetString
--tlv 0x1401:1401778070000018542:Int8

# Named standard optional param
--tlv source_port:1234
```

### Supported TLV types

| type | wire encoding | typical use |
|---|---|---|
| `Int1` | 1 byte unsigned (`>B`) | Flags, small enums |
| `Int2` | 2 bytes big-endian (`>H`) | Ports, short IDs |
| `Int4` | 4 bytes big-endian (`>I`) | Standard IDs (up to ~4 billion) |
| `Int8` | 8 bytes big-endian (`>Q`) | Large IDs (up to ~18 quintillion) |
| `OctetString` | Raw UTF-8 bytes | Text identifiers, tokens |
| `COctetString` | UTF-8 bytes + NUL terminator | C-style strings |

### Inbound TLVs (receive direction)

Vendor-range TLVs on incoming `deliver_sm` / MO / DLR PDUs are captured
by a decode-side patch (`install_pdu_decoder_patch` in
`jasmin/tools/tlv_encoder.py`) and attached to `pdu.custom_tlvs`. They
appear in the `SMS-MO` log line via `format_tlvs_for_log()` and are
available to any downstream code that iterates `pdu.custom_tlvs`.

### Wire debug logging

Set environment variable `JASMIN_TLV_WIRE_LOG=1` on the Jasmin container
to log every outgoing PDU's hex bytes at INFO level (logger name
`jasmin.tlv.wire`). Useful for verifying TLVs actually reach the socket:

```bash
docker run -d --name jasmin-tlv \
  -e JASMIN_TLV_WIRE_LOG=1 \
  ...
  jasmin:0.12-tlv

docker logs jasmin-tlv 2>&1 | grep jasmin.tlv.wire
```

Output:
```
OUT pdu=submit_sm seq=4 len=161 tlvs=0x1401,0x1400 hex=0000...14010008...
```

---

## 6. Tuning tips

- Start with `--mode send --concurrency 10 --rate 20` to confirm the end-to-end
  path works, then raise the rate.
- If you see a wall of `HTTP 412`, check user credentials, balance and route
  configuration in jCli — this is not a performance limit, it's a config gap.
- `connection_error` → REST API is not listening on `--url`.
- `timeout` at high rate → REST API worker pool is saturated; try
  `--mode sendbatch --batch-size 50` to reduce HTTP overhead, or increase the
  REST API throughput in `misc/config/rest-api.cfg`.
- For throughput testing, `--mode sendbatch` is the honest upper bound; for
  latency-per-message, use `--mode send`.
- Ctrl+C prints a partial summary and exits cleanly — safe to abort mid-run.

---

## 7. Exit codes

| code | meaning |
| --- | --- |
| 0 | Run completed within `--fail-on-error-rate` threshold (or no threshold set). |
| 1 | Error rate exceeded `--fail-on-error-rate`. |
| non-zero | argparse validation error (missing required flags, etc.). |

---

## 8. Related files in this repo

| file | role |
|---|---|
| [`jasmin/tools/tlv_encoder.py`](../../jasmin/tools/tlv_encoder.py) | `normalize_custom_tlvs()`, `resolve_tlv_types()`, `validate_custom_tlvs()`, encode/decode patches, wire logger |
| [`jasmin/tools/tlv.py`](../../jasmin/tools/tlv.py) | `format_tlvs_for_log()` — log formatter for `[tlvs:...]` |
| [`jasmin/protocols/rest/api.py`](../../jasmin/protocols/rest/api.py) | REST endpoints (`SendResource`, `SendBatchResource`), `custom_tlvs` preservation |
| [`jasmin/protocols/rest/tasks.py`](../../jasmin/protocols/rest/tasks.py) | REST → HTTP API forwarder (JSON body when TLVs present) |
| [`jasmin/protocols/http/endpoints/send.py`](../../jasmin/protocols/http/endpoints/send.py) | HTTP API `/send` — normalizes `custom_tlvs` on ingress |
| [`jasmin/protocols/smpp/operations.py`](../../jasmin/protocols/smpp/operations.py) | `SMPPOperationFactory`, patch installation |
| [`jasmin/protocols/smpp/configs.py`](../../jasmin/protocols/smpp/configs.py) | `SMPPClientConfig.custom_tlvs` validation schema |
| [`jasmin/protocols/cli/smppccm.py`](../../jasmin/protocols/cli/smppccm.py) | jCli `custom_tlvs` parser (`parseTlvString`) |
| [`jasmin/managers/listeners.py`](../../jasmin/managers/listeners.py) | Dispatch-time TLV type resolution + validation |
| [`misc/config/rest-api.cfg`](../config/rest-api.cfg) | REST API bind host/port |
| [`misc/scripts/sms_logger.py`](./sms_logger.py) | Sibling helper — logs sent SMS to DB |

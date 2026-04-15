# `rest_load_test.py` — Jasmin REST API load tester

A self-contained Python script that drives traffic through Jasmin's REST API
(`/secure/send` and `/secure/sendbatch`) so you can exercise the submit path
end-to-end (REST → HTTP API → SMPP), measure throughput and latency, and
stress the new TLV code path (`custom_tlvs`).

- File: [`rest_load_test.py`](./rest_load_test.py)
- Dependencies: Python 3.10+, `requests` (already in Jasmin's `requirements.txt`)
- No external test framework (no `locust`, no `aiohttp`) — plain stdlib threads

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
   connector (you already have SMPPSim on `192.168.1.22:2779`).
4. **SMPPSim** (or any SMSC) bound and accepting PDUs.

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

**200 messages, 50/s, with a custom TLV**
```bash
python misc/scripts/rest_load_test.py \
  --username foo --password bar \
  --count 200 --concurrency 20 --rate 50 \
  --to '+1202555{i:04d}' --content 'load {i}' \
  --tlv 0x1400:hello
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

### TLVs (the reason we built this)
Repeat `--tlv TAG:VALUE` as many times as needed.

- **Numeric tag** (integer or `0xNNNN`) is placed into
  `custom_tlvs` as a `(tag, type, length, value)` tuple, which is what
  `jasmin/tools/tlv.py` and `jasmin/protocols/rest/api.py` expect.
  ```
  --tlv 0x1400:hello
  --tlv 12288:world
  ```
- **Named tag** must be a known SMPP optional-param name (e.g.
  `source_port`, `user_message_reference`, `payload_type`,
  `sar_msg_ref_num`, `message_state`…). It's placed as a top-level field in
  the JSON body and flows through the regular optional-param path.
  ```
  --tlv source_port:1234
  --tlv user_message_reference:42
  ```
- Unknown non-numeric names are rejected at argument-parse time — use a
  numeric tag if you mean "custom TLV".

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

## 5. JSON body shapes (for reference)

What the script puts on the wire. These match
`jasmin/protocols/rest/api.py`.

**`POST /secure/send`** (one message per call)
```json
{
  "to": "+1202555000042",
  "content": "load 42",
  "from": "Jasmin",
  "priority": 1,
  "dlr-mask": 7,
  "custom_tlvs": [[5120, 0, 5, "hello"]]
}
```

**`POST /secure/sendbatch`** (N messages per call)
```json
{
  "globals": {},
  "messages": [
    {"to": "+120255500", "content": "m0"},
    {"to": "+120255501", "content": "m1",
     "custom_tlvs": [[5120, 0, 5, "hello"]]}
  ]
}
```

Both endpoints require `Authorization: Basic base64(user:pass)`.

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
| 2 | `requests` package missing (install it). |
| non-zero | argparse validation error (missing required flags, etc.). |

---

## 8. Related files in this repo

- [`jasmin/protocols/rest/api.py`](../../jasmin/protocols/rest/api.py) — REST
  endpoints (`SendResource`, `SendBatchResource`), auth middleware, the
  `custom_tlvs` preservation logic that this script targets.
- [`jasmin/protocols/rest/tasks.py`](../../jasmin/protocols/rest/tasks.py) —
  REST → HTTP API forwarder with JSON body when TLVs are present.
- [`jasmin/tools/tlv.py`](../../jasmin/tools/tlv.py) — TLV formatter used in
  logs; the tuple shape we put on the wire is what this module consumes.
- [`misc/config/rest-api.cfg`](../config/rest-api.cfg) — REST API bind host/port.
- [`misc/scripts/sms_logger.py`](./sms_logger.py) — sibling helper that logs
  sent SMS to a database; useful to run alongside this load test.

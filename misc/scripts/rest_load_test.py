#!/usr/bin/env python3
"""Load-test Jasmin's REST API (/secure/send and /secure/sendbatch).

Drives traffic through the REST API using Basic Auth and the JSON body schema
that Jasmin's rest layer expects (see jasmin/protocols/rest/api.py). Supports
standard optional params, arbitrary custom TLVs via --tlv, fixed-count or
time-bounded runs, concurrency, target rate limiting, and latency percentiles.

Prerequisites
-------------
- Jasmin REST API process is reachable at --url (default http://127.0.0.1:1401).
  The stock jasmin:0.12-tlv image starts jasmind only; to exercise REST use
  docker/Dockerfile.restapi or run jasmin-restapi.py separately.
- A Jasmin user with balance (Basic Auth creds passed via --username/--password).
- An MT route that maps the destinations to a working SMPP connector.

Examples
--------
Single-send smoke:
  ./rest_load_test.py --url http://127.0.0.1:1401 \\
      --username foo --password bar --count 1 -v

200 msgs at 50/s via /secure/send with a custom TLV 0x1400:
  ./rest_load_test.py --username foo --password bar \\
      --count 200 --concurrency 20 --rate 50 \\
      --to '+1202555{i:04d}' --content 'load {i}' \\
      --tlv 0x1400:hello

60-second burst via /secure/sendbatch, 50 messages per HTTP POST:
  ./rest_load_test.py --username foo --password bar \\
      --mode sendbatch --duration 60 --concurrency 8 \\
      --batch-size 50 --rate 500

JSON summary for CI:
  ./rest_load_test.py --username foo --password bar --count 500 \\
      --json-out /tmp/load.json --fail-on-error-rate 0.01

TLV syntax
----------
  --tlv 0x1400:hello          numeric tag  -> custom_tlvs tuple
  --tlv 12288:world           decimal tag  -> custom_tlvs tuple
  --tlv source_port:1234      named (known SMPP optional) -> top-level param

Notes
-----
- Only depends on Python stdlib + `requests` (already in Jasmin's requirements).
- Ctrl+C prints a partial summary and exits cleanly.
- Exit code is non-zero if observed error rate > --fail-on-error-rate.
"""

from __future__ import annotations

import argparse
import base64
import json
import os
import signal
import statistics
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from typing import Any

try:
    import requests
    from requests.auth import HTTPBasicAuth
except ImportError:
    sys.stderr.write("This script needs the 'requests' package (already a Jasmin dep).\n"
                     "Install with: pip install requests\n")
    sys.exit(2)


# ---------------------------------------------------------------------------
# TLV parsing
# ---------------------------------------------------------------------------

# Standard SMPP v3.4 optional-parameter names that the HTTP API accepts as
# top-level fields (non-exhaustive; add more as needed). Anything not in this
# set and not numeric is rejected at arg-parse time.
_NAMED_OPTIONAL_PARAMS = {
    "source_port", "source_addr_subunit", "source_network_type",
    "source_bearer_type", "source_telematics_id",
    "destination_port", "dest_addr_subunit", "dest_network_type",
    "dest_bearer_type", "dest_telematics_id",
    "qos_time_to_live",
    "payload_type", "additional_status_info_text",
    "receipted_message_id", "ms_msg_wait_facilities",
    "privacy_indicator",
    "source_subaddress", "dest_subaddress",
    "user_message_reference", "user_response_code",
    "language_indicator",
    "sar_msg_ref_num", "sar_total_segments", "sar_segment_seqnum",
    "sc_interface_version",
    "callback_num_pres_ind", "callback_num_atag", "number_of_messages",
    "callback_num",
    "dpf_result", "set_dpf", "ms_availability_status",
    "network_error_code", "message_payload", "delivery_failure_reason",
    "more_messages_to_send", "message_state",
    "ussd_service_op",
    "display_time",
    "sms_signal",
    "ms_validity",
    "alert_on_message_delivery",
    "its_reply_type", "its_session_info",
}


def parse_tlv_arg(raw: str) -> tuple[str, Any]:
    """Parse a --tlv TAG:VALUE argument.

    Returns ('custom', (tag_int, 0, len, value_str)) for numeric tags, or
    ('named', (name, value_str)) for known SMPP optional param names.
    """
    if ":" not in raw:
        raise argparse.ArgumentTypeError(
            "--tlv expects TAG:VALUE, got %r" % raw)
    tag, value = raw.split(":", 1)
    tag = tag.strip()
    value = value.strip()
    if not tag:
        raise argparse.ArgumentTypeError("--tlv TAG is empty in %r" % raw)

    # Numeric tag -> custom_tlvs tuple (tag, type, length, value)
    try:
        if tag.lower().startswith("0x"):
            tag_int = int(tag, 16)
        else:
            tag_int = int(tag)
    except ValueError:
        tag_int = None

    if tag_int is not None:
        if not (0 <= tag_int <= 0xFFFF):
            raise argparse.ArgumentTypeError(
                "--tlv numeric tag %s out of SMPP range 0x0000..0xFFFF" % tag)
        # The 2nd/3rd positions mirror jasmin/tools/tlv.py expectations; length
        # is the byte-length of the value in its encoded form.
        return ("custom", (tag_int, 0, len(value.encode("utf-8")), value))

    # Named tag must be a known optional-param name.
    name = tag.lower()
    if name not in _NAMED_OPTIONAL_PARAMS:
        raise argparse.ArgumentTypeError(
            "--tlv named tag %r is not a known SMPP optional param. "
            "Use a numeric tag like 0x1400 for custom TLVs." % tag)
    return ("named", (name, value))


# ---------------------------------------------------------------------------
# Rate limiter (simple token bucket, thread-safe)
# ---------------------------------------------------------------------------

class RateLimiter:
    """Bounds submissions to roughly `rate` ops/sec. rate <= 0 disables."""

    def __init__(self, rate: float) -> None:
        self.rate = float(rate)
        self._lock = threading.Lock()
        self._tokens = 0.0
        self._last = time.monotonic()

    def acquire(self) -> None:
        if self.rate <= 0:
            return
        with self._lock:
            now = time.monotonic()
            self._tokens = min(self.rate, self._tokens + (now - self._last) * self.rate)
            self._last = now
            if self._tokens >= 1.0:
                self._tokens -= 1.0
                return
            # Sleep just long enough for one token to materialise.
            needed = (1.0 - self._tokens) / self.rate
        time.sleep(needed)
        with self._lock:
            self._tokens = max(0.0, self._tokens - 1.0)
            self._last = time.monotonic()


# ---------------------------------------------------------------------------
# Request builders
# ---------------------------------------------------------------------------

def build_message_body(args: argparse.Namespace, i: int) -> dict:
    """Build a single-message dict suitable for /secure/send or a batch entry."""
    body: dict[str, Any] = {
        "to": args.to.format(i=i),
        "content": args.content.format(i=i),
    }
    if args.from_:
        body["from"] = args.from_.format(i=i)
    if args.priority is not None:
        body["priority"] = args.priority
    if args.dlr_mask is not None:
        body["dlr-mask"] = args.dlr_mask
    if args.validity_period:
        body["validity-period"] = args.validity_period

    # TLVs
    custom: list[list] = []
    for kind, payload in args.tlv:
        if kind == "custom":
            custom.append(list(payload))
        else:  # named
            name, value = payload
            body[name] = value
    if custom:
        # REST layer forwards this to the HTTP API as JSON, preserving the
        # underscore form (see jasmin/protocols/rest/api.py L154-168).
        body["custom_tlvs"] = custom
    return body


def build_send_payload(args: argparse.Namespace, i: int) -> dict:
    return build_message_body(args, i)


def build_batch_payload(args: argparse.Namespace, start_i: int, size: int) -> dict:
    return {
        "globals": {},
        "messages": [build_message_body(args, start_i + k) for k in range(size)],
    }


# ---------------------------------------------------------------------------
# Worker
# ---------------------------------------------------------------------------

class Stats:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self.ok = 0
        self.failed = 0
        self.latencies_ms: list[float] = []
        self.errors: dict[str, int] = {}
        self.started_at = time.monotonic()
        self.ended_at: float | None = None

    def record(self, ok: bool, latency_ms: float, err: str | None = None) -> None:
        with self._lock:
            if ok:
                self.ok += 1
            else:
                self.failed += 1
                if err:
                    self.errors[err] = self.errors.get(err, 0) + 1
            self.latencies_ms.append(latency_ms)

    @property
    def total(self) -> int:
        return self.ok + self.failed

    def snapshot(self) -> dict:
        with self._lock:
            elapsed = (self.ended_at or time.monotonic()) - self.started_at
            lats = sorted(self.latencies_ms)
            def pct(p: float) -> float:
                if not lats:
                    return 0.0
                k = max(0, min(len(lats) - 1, int(round((p / 100.0) * (len(lats) - 1)))))
                return lats[k]
            return {
                "ok": self.ok,
                "failed": self.failed,
                "total": self.ok + self.failed,
                "error_rate": (self.failed / (self.ok + self.failed)) if (self.ok + self.failed) else 0.0,
                "elapsed_sec": round(elapsed, 3),
                "throughput_per_sec": round((self.ok + self.failed) / elapsed, 2) if elapsed > 0 else 0.0,
                "latency_ms": {
                    "min": round(lats[0], 2) if lats else 0,
                    "p50": round(pct(50), 2),
                    "p95": round(pct(95), 2),
                    "p99": round(pct(99), 2),
                    "max": round(lats[-1], 2) if lats else 0,
                    "avg": round(sum(lats) / len(lats), 2) if lats else 0,
                },
                "errors": dict(self.errors),
            }


def submit_one(session: requests.Session, url: str, auth: HTTPBasicAuth,
               payload: dict, timeout: float, verify: bool) -> tuple[bool, float, str | None, str | None]:
    """Returns (ok, latency_ms, error_label, response_text_snippet)."""
    t0 = time.monotonic()
    try:
        r = session.post(url, json=payload, auth=auth, timeout=timeout, verify=verify)
        dt = (time.monotonic() - t0) * 1000.0
        if 200 <= r.status_code < 300:
            return True, dt, None, r.text[:200]
        return False, dt, f"HTTP {r.status_code}", r.text[:200]
    except requests.exceptions.Timeout:
        return False, (time.monotonic() - t0) * 1000.0, "timeout", None
    except requests.exceptions.ConnectionError as e:
        return False, (time.monotonic() - t0) * 1000.0, "connection_error", str(e)[:200]
    except Exception as e:  # pragma: no cover
        return False, (time.monotonic() - t0) * 1000.0, type(e).__name__, str(e)[:200]


# ---------------------------------------------------------------------------
# Main driver
# ---------------------------------------------------------------------------

_stop_flag = threading.Event()


def _install_sigint_handler() -> None:
    def _handler(signum, frame):
        _stop_flag.set()
        sys.stderr.write("\n[interrupt] finishing in-flight requests and exiting...\n")
    signal.signal(signal.SIGINT, _handler)


def run(args: argparse.Namespace) -> int:
    _install_sigint_handler()

    endpoint = "/secure/send" if args.mode == "send" else "/secure/sendbatch"
    url = args.url.rstrip("/") + endpoint
    auth = HTTPBasicAuth(args.username, args.password)
    stats = Stats()
    limiter = RateLimiter(args.rate)
    verify = not args.insecure

    if args.mode == "sendbatch":
        # Each worker task = one HTTP POST carrying `batch_size` messages.
        ops_per_task = args.batch_size
    else:
        ops_per_task = 1

    # Decide work distribution
    use_duration = args.duration is not None
    total_ops = args.count if not use_duration else None
    task_counter = [0]  # number of messages handed out so far
    counter_lock = threading.Lock()

    def next_task_index() -> int | None:
        """Return starting message index for the next task, or None if done."""
        with counter_lock:
            if _stop_flag.is_set():
                return None
            if use_duration:
                if time.monotonic() - stats.started_at >= args.duration:
                    return None
                start = task_counter[0]
                task_counter[0] += ops_per_task
                return start
            # count mode
            if task_counter[0] >= total_ops:
                return None
            start = task_counter[0]
            task_counter[0] += ops_per_task
            return start

    session = requests.Session()
    # Tune connection pool to match concurrency to avoid warnings under load.
    adapter = requests.adapters.HTTPAdapter(
        pool_connections=args.concurrency, pool_maxsize=args.concurrency)
    session.mount("http://", adapter)
    session.mount("https://", adapter)

    def worker() -> None:
        while True:
            start = next_task_index()
            if start is None:
                return
            limiter.acquire()

            if args.mode == "send":
                payload = build_send_payload(args, start)
                messages_in_call = 1
            else:
                # Clamp last batch to remaining count in --count mode.
                size = ops_per_task
                if not use_duration and total_ops is not None:
                    size = min(ops_per_task, total_ops - start)
                    if size <= 0:
                        return
                payload = build_batch_payload(args, start, size)
                messages_in_call = size

            ok, dt, err, snippet = submit_one(
                session, url, auth, payload, args.timeout, verify)

            # For batch we count each message as one observation with the same
            # latency so percentiles reflect the per-message view.
            for _ in range(messages_in_call):
                stats.record(ok, dt, err)

            if args.verbose:
                if ok:
                    print(f"[ok]  {dt:7.1f}ms  {snippet}")
                else:
                    print(f"[err] {dt:7.1f}ms  {err}  {snippet}")

            # progress
            if args.log_every and stats.total and (stats.total % args.log_every == 0):
                snap = stats.snapshot()
                print(
                    "progress: total=%d ok=%d fail=%d thrpt=%.1f/s p95=%.1fms"
                    % (snap["total"], snap["ok"], snap["failed"],
                       snap["throughput_per_sec"], snap["latency_ms"]["p95"]),
                    file=sys.stderr,
                )

    # Optional ramp-up: stagger worker starts so concurrency rises linearly.
    threads: list[threading.Thread] = []
    if args.ramp_up > 0 and args.concurrency > 1:
        step = args.ramp_up / args.concurrency
    else:
        step = 0

    with ThreadPoolExecutor(max_workers=args.concurrency) as pool:
        futures = []
        for i in range(args.concurrency):
            if step:
                time.sleep(step)
            if _stop_flag.is_set():
                break
            futures.append(pool.submit(worker))

        for f in as_completed(futures):
            exc = f.exception()
            if exc is not None:
                sys.stderr.write(f"worker crashed: {exc!r}\n")

    stats.ended_at = time.monotonic()
    snap = stats.snapshot()

    # Final report
    print("\n=== Load test summary ===")
    print(f"  endpoint          : POST {endpoint}")
    print(f"  mode              : {args.mode}" + (f" (batch_size={args.batch_size})" if args.mode == "sendbatch" else ""))
    print(f"  requested         : " + (f"{args.count} msgs" if not use_duration else f"{args.duration}s duration"))
    print(f"  concurrency       : {args.concurrency}")
    print(f"  target rate       : " + (f"{args.rate}/s" if args.rate > 0 else "unbounded"))
    print(f"  ok                : {snap['ok']}")
    print(f"  failed            : {snap['failed']}")
    print(f"  error rate        : {snap['error_rate']*100:.2f}%")
    print(f"  elapsed           : {snap['elapsed_sec']}s")
    print(f"  throughput        : {snap['throughput_per_sec']}/s")
    lat = snap["latency_ms"]
    print(f"  latency ms        : min={lat['min']} p50={lat['p50']} p95={lat['p95']} p99={lat['p99']} max={lat['max']} avg={lat['avg']}")
    if snap["errors"]:
        print("  errors:")
        for k, v in sorted(snap["errors"].items(), key=lambda kv: -kv[1]):
            print(f"    {v:6d}  {k}")

    if args.json_out:
        with open(args.json_out, "w") as fh:
            json.dump(snap, fh, indent=2)
        print(f"\nwrote JSON summary to {args.json_out}")

    # Exit code
    if args.fail_on_error_rate is not None and snap["error_rate"] > args.fail_on_error_rate:
        sys.stderr.write(
            f"error rate {snap['error_rate']:.4f} > threshold {args.fail_on_error_rate}; exiting non-zero.\n")
        return 1
    return 0


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    p = argparse.ArgumentParser(
        prog="rest_load_test.py",
        description="Load-test Jasmin REST API (/secure/send, /secure/sendbatch).",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    p.add_argument("--url", default=os.environ.get("JASMIN_REST_URL", "http://127.0.0.1:1401"),
                   help="Jasmin REST base URL (default: %(default)s)")
    p.add_argument("--username", required=True, help="Jasmin user (Basic Auth)")
    p.add_argument("--password", required=True, help="Jasmin password (Basic Auth)")
    p.add_argument("--mode", choices=("send", "sendbatch"), default="send")

    g = p.add_mutually_exclusive_group(required=True)
    g.add_argument("--count", type=int, help="total messages to send")
    g.add_argument("--duration", type=int, help="run for N seconds")

    p.add_argument("--concurrency", type=int, default=10)
    p.add_argument("--rate", type=float, default=0.0,
                   help="target msgs/sec across all workers; 0 = unbounded")
    p.add_argument("--batch-size", type=int, default=50,
                   help="messages per /secure/sendbatch POST (sendbatch mode)")

    p.add_argument("--to", default="+11234567890",
                   help="destination MSISDN; supports {i} placeholder")
    p.add_argument("--from", dest="from_", default=None,
                   help="source address; supports {i} placeholder")
    p.add_argument("--content", default="load test {i}",
                   help="message body; supports {i} placeholder")
    p.add_argument("--priority", type=int, choices=(0, 1, 2, 3), default=None)
    p.add_argument("--dlr-mask", dest="dlr_mask", type=int, default=None,
                   help="e.g. 1 (delivery only) or 7 (all)")
    p.add_argument("--validity-period", dest="validity_period", default=None)

    p.add_argument("--tlv", action="append", type=parse_tlv_arg, default=[],
                   metavar="TAG:VALUE",
                   help="repeatable; numeric TAG (e.g. 0x1400) -> custom_tlvs, "
                        "named TAG (e.g. source_port) -> top-level optional param")

    p.add_argument("--timeout", type=float, default=10.0)
    p.add_argument("--ramp-up", dest="ramp_up", type=float, default=0.0,
                   help="seconds to ramp workers 1..N")
    p.add_argument("--insecure", action="store_true",
                   help="disable TLS verification (self-signed rest-api.cfg)")
    p.add_argument("--log-every", dest="log_every", type=int, default=100)
    p.add_argument("--json-out", dest="json_out", default=None,
                   help="write final stats as JSON to PATH")
    p.add_argument("--fail-on-error-rate", dest="fail_on_error_rate", type=float,
                   default=None,
                   help="exit non-zero if observed error rate > FRACTION (e.g. 0.01)")
    p.add_argument("-v", "--verbose", action="store_true",
                   help="log every request (use sparingly under load)")

    args = p.parse_args(argv)

    # sanity
    if args.mode == "sendbatch" and args.batch_size < 1:
        p.error("--batch-size must be >= 1")
    if args.rate < 0:
        p.error("--rate must be >= 0")
    if args.concurrency < 1:
        p.error("--concurrency must be >= 1")
    if args.count is not None and args.count < 1:
        p.error("--count must be >= 1")
    if args.duration is not None and args.duration < 1:
        p.error("--duration must be >= 1")

    return args


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    return run(args)


if __name__ == "__main__":
    sys.exit(main())

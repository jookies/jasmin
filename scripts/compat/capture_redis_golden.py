#!/usr/bin/env python3
"""Capture Redis serialization used by the frozen Python implementation."""

from __future__ import annotations

import argparse
import base64
import datetime
import json
import pickle
from pathlib import Path

from twisted.internet import defer, task
from smpp.pdu.operations import DeliverSM

from jasmin.redis.client import ConnectionWithConfiguration
from jasmin.redis.configs import RedisForJasminConfig

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat" / "fixtures" / "redis" / "baseline.json"


def normalize_field(value):
    if isinstance(value, bytes):
        try:
            utf8 = value.decode("utf-8")
        except UnicodeDecodeError:
            utf8 = None
        return {
            "type": "bytes",
            "base64": base64.b64encode(value).decode("ascii"),
            "hex": value.hex(),
            "utf8": utf8,
        }
    return {"type": value.__class__.__name__, "value": str(value)}


@defer.inlineCallbacks
def capture(_reactor, output: Path):
    config = RedisForJasminConfig()
    redis = yield ConnectionWithConfiguration(config)
    if config.password is not None:
        yield redis.auth(config.password)
        yield redis.select(config.dbid)
    yield redis.flushdb()

    cases = []
    definitions = [
        (
            "http_dlr_request",
            "dlr:11111111-1111-4111-8111-111111111111",
            {
                "sc": "httpapi",
                "url": "https://example.invalid/dlr",
                "level": 3,
                "method": "POST",
                "connector": "connector-a",
                "expiry": 86400,
            },
            86400,
            "dlr:<queue-message-id>",
        ),
        (
            "smpps_dlr_request",
            "dlr:22222222-2222-4222-8222-222222222222",
            {
                "sc": "smppsapi",
                "system_id": "client-a",
                "source_addr_ton": "AddrTon.INTERNATIONAL",
                "source_addr_npi": "AddrNpi.ISDN",
                "source_addr": "1111",
                "dest_addr_ton": "AddrTon.INTERNATIONAL",
                "dest_addr_npi": "AddrNpi.ISDN",
                "destination_addr": "2222",
                "sub_date": "2026-01-02 03:04:05.678901",
                "rd_receipt": "RegisteredDeliveryReceipt.SMSC_DELIVERY_RECEIPT_REQUESTED",
                "expiry": 86400,
            },
            86400,
            "dlr:<queue-message-id>",
        ),
        (
            "smpp_to_queue_id",
            "queue-msgid:436949",
            {
                "msgid": "11111111-1111-4111-8111-111111111111",
                "connector_type": "httpapi",
            },
            86400,
            "queue-msgid:<normalized-smpp-message-id>",
        ),
    ]

    for case_id, key, values, ttl_seconds, key_pattern in definitions:
        yield redis.hmset(key, values)
        yield redis.expire(key, ttl_seconds)
        observed_ttl = yield redis.ttl(key)
        if not ttl_seconds - 2 <= observed_ttl <= ttl_seconds:
            raise AssertionError(f"unexpected TTL for {key}: {observed_ttl}")
        stored = yield redis.hgetall(key)
        cases.append(
            {
                "id": case_id,
                "key_pattern": key_pattern,
                "redis_type": "hash",
                "fields": {str(k): normalize_field(v) for k, v in sorted(stored.items())},
                "ttl_seconds": ttl_seconds,
            }
        )

    pdu = DeliverSM(
        seqNum=9,
        source_addr="15551230000",
        destination_addr="4040",
        short_message=b"part-one",
    )
    part = {
        "pdu": pdu,
        "total_segments": 2,
        "msg_ref_num": 42,
        "segment_seqnum": 1,
    }
    key = "longDeliverSm:connector-a:42:4040"
    yield redis.hset(key, 1, pickle.dumps(part, 2))
    yield redis.expire(key, 300)
    observed_ttl = yield redis.ttl(key)
    if not 298 <= observed_ttl <= 300:
        raise AssertionError(f"unexpected TTL for {key}: {observed_ttl}")
    stored = yield redis.hgetall(key)
    cases.append(
        {
            "id": "multipart_deliver_sm_part",
            "key_pattern": "longDeliverSm:<connector-id>:<reference>:<destination>",
            "redis_type": "hash",
            "fields": {str(k): normalize_field(v) for k, v in sorted(stored.items())},
            "ttl_seconds": 300,
        }
    )

    yield redis.flushdb()
    yield redis.disconnect()

    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(
        json.dumps({"schema_version": 1, "baseline_commit": BASELINE, "cases": cases}, indent=2, sort_keys=True)
        + "\n",
        encoding="utf-8",
    )
    print(f"redis_golden={output} cases={len(cases)} status=ok")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    task.react(lambda reactor: capture(reactor, args.output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

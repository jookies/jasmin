#!/usr/bin/env python3
"""Validate golden fixtures using only the Python standard library."""

from __future__ import annotations

import base64
import csv
import hashlib
import json
import pickletools
from pathlib import Path

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
FIXTURES = {
    "http": ROOT / "compat/fixtures/http/baseline.json",
    "smpp": ROOT / "compat/fixtures/smpp/baseline.json",
    "amqp": ROOT / "compat/fixtures/amqp/baseline.json",
    "redis": ROOT / "compat/fixtures/redis/baseline.json",
}
COVERAGE = ROOT / "spec/compatibility/FIXTURE_COVERAGE.csv"
EXPECTED_CASE_IDS = {
    "http": {
        "ping",
        "rate_valid",
        "balance_unlimited",
        "send_missing_password",
        "send_bad_password",
        "send_no_live_connector",
        "rate_disabled_user",
        "balance_disabled_group",
        "send_json_bad_password",
    },
    "smpp": {
        "bind_transceiver",
        "submit_sm_ascii",
        "submit_sm_sar_part",
        "deliver_sm_mo",
        "submit_sm_resp_ok",
        "deliver_sm_dlr_message_payload",
        "submit_sm_unknown_vendor_tlv",
    },
    "amqp": {
        "submit_sm_httpapi",
        "submit_sm_resp",
        "dlr_lookup_submit_sm_resp",
        "dlr_http_thrower",
        "dlr_smpps_thrower",
        "bill_submit_sm_resp",
        "routed_deliver_sm_http",
    },
    "redis": {
        "http_dlr_request",
        "smpps_dlr_request",
        "smpp_to_queue_id",
        "multipart_deliver_sm_part",
    },
}
EXPECTED_COVERAGE_SHA256 = "cc41ed0517755e95c63fb1607616fd62fcdec797284b89cfffe64244791f2b02"
EXPECTED_AMQP_CASE_SHA256 = {
    "submit_sm_httpapi": "e696cb539f3b187d99c368e6e69bc6db8a29e3e636bef8ec90d162adc2aa1706",
    "submit_sm_resp": "89768110d1c535cfd625a89aba82d0be827f9e5c1005de7ff30469b6cc300906",
    "dlr_lookup_submit_sm_resp": "0a60a46c7ba0c7fb8c259521b4d0d2e8dd30afbf0a1a4ac810476a00b171c089",
    "dlr_http_thrower": "0f4f4312b3865e1b0b96cd6b43ceabca70be5b61c06a7730e2342ce045eeb17c",
    "dlr_smpps_thrower": "9ed9c69d382ec371474c9deef4d5b965f6b17c96607722b172277a9b71c5fe1a",
    "bill_submit_sm_resp": "df3f4da44a9b4177350dfeb4fc01d54fe2d243265d14a8ad598cb32bb1c9b106",
    "routed_deliver_sm_http": "154cc6fe1f9f369e65c7422961f4f3cc01be98cf42052a6dcfac5a7de43de3dc",
}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def decode64(value: str, context: str) -> bytes:
    try:
        return base64.b64decode(value, validate=True)
    except Exception as exc:
        raise AssertionError(f"{context}: invalid base64: {exc}") from exc


def validate_common(surface: str, document: dict) -> None:
    require(document.get("schema_version") == 1, f"{surface}: schema_version")
    require(document.get("baseline_commit") == BASELINE, f"{surface}: baseline_commit")
    cases = document.get("cases")
    require(isinstance(cases, list) and len(cases) > 0, f"{surface}: cases must be non-empty")
    if not isinstance(cases, list):
        raise AssertionError(f"{surface}: cases must be a list")
    ids = [case.get("id") for case in cases]
    require(all(isinstance(case_id, str) and case_id for case_id in ids), f"{surface}: invalid case id")
    require(len(ids) == len(set(ids)), f"{surface}: duplicate case ids")
    require(set(ids) == EXPECTED_CASE_IDS[surface], f"{surface}: unexpected or missing case ids")


def validate_http(document: dict) -> None:
    for case in document["cases"]:
        response = case["response"]
        body = decode64(response["body_base64"], f"http/{case['id']}")
        if response["body_utf8"] is not None:
            require(body.decode("utf-8") == response["body_utf8"], f"http/{case['id']}: utf8 mismatch")
        require(100 <= response["status"] <= 599, f"http/{case['id']}: status")


def validate_smpp(document: dict) -> None:
    for case in document["cases"]:
        wire = bytes.fromhex(case["wire_hex"])
        require(len(wire) >= 16, f"smpp/{case['id']}: PDU shorter than header")
        require(int.from_bytes(wire[:4], "big") == len(wire), f"smpp/{case['id']}: command_length mismatch")
        if case["direction"] == "encode":
            require(case["roundtrip_wire_hex"] == case["wire_hex"], f"smpp/{case['id']}: encode roundtrip")
        if case.get("roundtrip_wire_hex") is None:
            require(bool(case.get("roundtrip_error")), f"smpp/{case['id']}: missing roundtrip error")


def validate_amqp(document: dict) -> None:
    require(
        document.get("capture_transport") == "rabbitmq-publish-consume",
        "amqp: capture_transport",
    )
    require(
        {case["id"] for case in document["cases"]} == set(EXPECTED_AMQP_CASE_SHA256),
        "amqp: unexpected or missing case ids",
    )
    for case in document["cases"]:
        case_id = case["id"]
        case_digest = hashlib.sha256(
            json.dumps(case, sort_keys=True, separators=(",", ":")).encode("utf-8")
        ).hexdigest()
        require(
            case_digest == EXPECTED_AMQP_CASE_SHA256[case_id],
            f"amqp/{case_id}: trusted case fingerprint",
        )
        body = case["body"]
        payload = decode64(body["wire_base64"], f"amqp/{case_id}")
        require(hashlib.sha256(payload).hexdigest() == body["wire_sha256"], f"amqp/{case_id}: wire hash")
        if body["wire_kind"] == "text":
            require(payload.decode("utf-8") == body["text"], f"amqp/{case_id}: text mismatch")
            require(body["pickle_protocol"] is None, f"amqp/{case_id}: text marked as pickle")
        elif body["pickle_protocol"] is not None:
            require(len(payload) >= 2 and payload[0] == 0x80, f"amqp/{case_id}: invalid pickle prefix")
            require(payload[1] == body["pickle_protocol"], f"amqp/{case_id}: pickle protocol mismatch")
            try:
                opcodes = list(pickletools.genops(payload))
            except Exception as exc:
                raise AssertionError(f"amqp/{case_id}: malformed pickle opcode stream: {exc}") from exc
            require(bool(opcodes) and opcodes[-1][0].name == "STOP", f"amqp/{case_id}: pickle missing STOP")


def validate_redis(document: dict) -> None:
    for case in document["cases"]:
        require(case["redis_type"] == "hash", f"redis/{case['id']}: type")
        require(case["ttl_seconds"] > 0, f"redis/{case['id']}: ttl")
        require(case["fields"], f"redis/{case['id']}: empty fields")
        for field, value in case["fields"].items():
            if value.get("type") == "bytes":
                raw = decode64(value["base64"], f"redis/{case['id']}/{field}")
                require(raw.hex() == value["hex"], f"redis/{case['id']}/{field}: hex mismatch")


def main() -> int:
    documents = {}
    for surface, path in FIXTURES.items():
        require(path.is_file(), f"missing fixture: {path}")
        document = json.loads(path.read_text(encoding="utf-8"))
        validate_common(surface, document)
        documents[surface] = document

    validate_http(documents["http"])
    validate_smpp(documents["smpp"])
    validate_amqp(documents["amqp"])
    validate_redis(documents["redis"])

    require(COVERAGE.is_file(), f"missing coverage map: {COVERAGE}")
    require(
        hashlib.sha256(COVERAGE.read_bytes()).hexdigest() == EXPECTED_COVERAGE_SHA256,
        "coverage map differs from approved manifest",
    )
    with COVERAGE.open(newline="", encoding="utf-8") as handle:
        rows = list(csv.DictReader(handle))
    mapped = {(row["surface"], row["case_id"]) for row in rows}
    expected = {
        (surface, case["id"])
        for surface, document in documents.items()
        for case in document["cases"]
    }
    require(len(mapped) == len(rows), "coverage map has duplicate surface/case_id rows")
    require(
        mapped == expected,
        f"coverage mismatch missing={sorted(expected - mapped)} extra={sorted(mapped - expected)}",
    )
    require(all(row["coverage"] in {"partial", "full"} for row in rows), "invalid coverage state")
    require(all(row["contract_ids"] for row in rows), "fixture without contract mapping")

    counts = ", ".join(f"{name}={len(doc['cases'])}" for name, doc in documents.items())
    print(f"fixtures=valid baseline={BASELINE} {counts}, coverage_rows={len(rows)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

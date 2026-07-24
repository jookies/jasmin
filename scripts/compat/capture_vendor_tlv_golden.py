#!/usr/bin/env python3
"""Capture Jasmin's pure vendor custom-TLV pipeline from the frozen oracle."""
from __future__ import annotations

import argparse
import base64
import json
from pathlib import Path
from typing import Any

from jasmin.tools import tlv_encoder

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat/fixtures/vendor-tlv/baseline.json"


def value(kind: str, raw: Any) -> dict:
    return {"kind": kind, "value": raw}


def decode_value(spec: dict) -> Any:
    kind = spec["kind"]
    raw = spec["value"]
    if kind == "string":
        return raw
    if kind == "int":
        return int(raw)
    if kind == "bytes":
        return base64.b64decode(raw, validate=True)
    raise ValueError(f"unknown fixture value kind: {kind}")


def encode_value(value_: Any) -> dict:
    if isinstance(value_, (bytes, bytearray)):
        return value("bytes", base64.b64encode(bytes(value_)).decode("ascii"))
    if isinstance(value_, int):
        return value("int", str(value_))
    if isinstance(value_, str):
        return value("string", value_)
    raise TypeError(f"unsupported captured value: {type(value_).__name__}")


def decode_tlvs(items: list[dict]) -> list[tuple]:
    return [
        (
            int(item["tag"]),
            item.get("length"),
            item.get("type"),
            decode_value(item["value"]),
        )
        for item in items
    ]


def encode_tlvs(items: list[tuple]) -> list[dict]:
    return [
        {
            "tag": str(item[0]),
            "length": item[1],
            "type": item[2],
            "value": encode_value(item[3]),
        }
        for item in items
    ]


def capture(case: dict) -> dict:
    operation = case["operation"]
    input_ = case["input"]
    try:
        if operation == "parse_tag":
            tag, type_ = tlv_encoder._parse_tag_key(input_["tag_key"])
            expected = {"result": {"tag": str(tag), "type": type_}}
        elif operation == "resolve":
            result = tlv_encoder.resolve_tlv_types(
                decode_tlvs(input_["tlvs"]), input_["rules"]
            )
            expected = {"result": encode_tlvs(result)}
        elif operation == "encode_value":
            result = tlv_encoder.encode_tlv_value(
                decode_value(input_["value"]), input_.get("type")
            )
            expected = {"hex": result.hex()}
        elif operation == "encode_custom":
            result = tlv_encoder.encode_custom_tlvs(decode_tlvs(input_["tlvs"]))
            expected = {"hex": result.hex()}
        elif operation == "validate":
            ok, message = tlv_encoder.validate_custom_tlvs(
                decode_tlvs(input_["tlvs"]), input_["rules"]
            )
            expected = {"result": {"ok": ok, "message": message}}
        else:
            raise ValueError(f"unknown operation: {operation}")
    except Exception as exc:  # Exact oracle exception is fixture evidence.
        expected = {
            "error": {
                "class": exc.__class__.__name__,
                "message": str(exc),
            }
        }
    return {"id": case["id"], "operation": operation, "input": input_, "expected": expected}


def tlv(tag: int, kind: str, raw: Any, type_: str | None = None, length: int | None = None) -> dict:
    return {"tag": str(tag), "length": length, "type": type_, "value": value(kind, raw)}


def rule(tag: int, type_: str, length: int | None = None, required: bool = False) -> dict:
    return {"tag": tag, "type": type_, "length": length, "required": required}


def build_document() -> dict:
    raw_deadbeef = base64.b64encode(bytes.fromhex("deadbeef")).decode("ascii")
    cases = [
        {"id": "parse_hex_untyped", "operation": "parse_tag", "input": {"tag_key": " 0x1401 "}},
        {"id": "parse_decimal_int4", "operation": "parse_tag", "input": {"tag_key": "5121:Int4"}},
        {"id": "parse_invalid_type_errors", "operation": "parse_tag", "input": {"tag_key": "0x1401:Bogus"}},
        {"id": "parse_negative_tag", "operation": "parse_tag", "input": {"tag_key": "-1"}},
        {"id": "parse_arbitrary_precision_tag", "operation": "parse_tag", "input": {"tag_key": "18446744073709551616"}},
        {"id": "resolve_declared_int8_decimal", "operation": "resolve", "input": {"tlvs": [tlv(0x1401, "string", "1707167205648943173")], "rules": [rule(0x1401, "Int8")]}},
        {"id": "resolve_declared_int2_hex", "operation": "resolve", "input": {"tlvs": [tlv(0x1401, "string", "0x1401")], "rules": [rule(0x1401, "Int2")]}},
        {"id": "resolve_unknown_defaults_octet", "operation": "resolve", "input": {"tlvs": [tlv(0x7777, "string", "abc")], "rules": []}},
        {"id": "resolve_explicit_type_wins", "operation": "resolve", "input": {"tlvs": [tlv(0x1401, "int", "5", "Int2")], "rules": [rule(0x1401, "Int8")]}},
        {"id": "resolve_invalid_integer_errors", "operation": "resolve", "input": {"tlvs": [tlv(0x1401, "string", "not-an-int")], "rules": [rule(0x1401, "Int2")]}},
        {"id": "resolve_negative_integer", "operation": "resolve", "input": {"tlvs": [tlv(0x1401, "string", "-1")], "rules": [rule(0x1401, "Int8")]}},
        {"id": "resolve_arbitrary_precision_integer", "operation": "resolve", "input": {"tlvs": [tlv(0x1401, "string", "18446744073709551616")], "rules": [rule(0x1401, "Int8")]}},
        {"id": "resolve_preserves_unbounded_tags", "operation": "resolve", "input": {"tlvs": [tlv(-1, "string", "x"), tlv(65536, "string", "y")], "rules": [rule(0xFFFF, "OctetString"), rule(0, "OctetString")]}},
        {"id": "encode_int1_max", "operation": "encode_value", "input": {"type": "Int1", "value": value("int", "255")}},
        {"id": "encode_int2_big_endian", "operation": "encode_value", "input": {"type": "Int2", "value": value("int", "5121")}},
        {"id": "encode_int4_big_endian", "operation": "encode_value", "input": {"type": "Int4", "value": value("int", "16909060")}},
        {"id": "encode_int8_vendor_id", "operation": "encode_value", "input": {"type": "Int8", "value": value("int", "1707167205648943173")}},
        {"id": "encode_octet_unicode", "operation": "encode_value", "input": {"type": "OctetString", "value": value("string", "café")}},
        {"id": "encode_coctet_terminator", "operation": "encode_value", "input": {"type": "COctetString", "value": value("string", "abc")}},
        {"id": "encode_bytes_verbatim", "operation": "encode_value", "input": {"type": "Int2", "value": value("bytes", raw_deadbeef)}},
        {"id": "encode_int1_overflow_errors", "operation": "encode_value", "input": {"type": "Int1", "value": value("int", "256")}},
        {"id": "encode_custom_order_and_headers", "operation": "encode_custom", "input": {"tlvs": [tlv(0x1401, "int", "5121", "Int2"), tlv(0x1400, "string", "x", "COctetString")]}},
        {"id": "encode_custom_masks_unbounded_tags", "operation": "encode_custom", "input": {"tlvs": [tlv(-1, "string", "x", "OctetString"), tlv(65536, "string", "y", "OctetString")]}},
        {"id": "validate_missing_required", "operation": "validate", "input": {"tlvs": [], "rules": [rule(0x1401, "OctetString", 5, True)]}},
        {"id": "validate_exact_max", "operation": "validate", "input": {"tlvs": [tlv(0x1401, "string", "12345", "OctetString")], "rules": [rule(0x1401, "OctetString", 5, True)]}},
        {"id": "validate_over_max", "operation": "validate", "input": {"tlvs": [tlv(0x1401, "string", "123456", "OctetString")], "rules": [rule(0x1401, "OctetString", 5, True)]}},
        {"id": "validate_coctet_counts_nul", "operation": "validate", "input": {"tlvs": [tlv(0x1401, "string", "abc", "COctetString")], "rules": [rule(0x1401, "COctetString", 3)]}},
        {"id": "validate_duplicate_last_wins", "operation": "validate", "input": {"tlvs": [tlv(0x1401, "string", "too-long", "OctetString"), tlv(0x1401, "string", "ok", "OctetString")], "rules": [rule(0x1401, "OctetString", 2, True)]}},
        {"id": "validate_unknown_tag_allowed", "operation": "validate", "input": {"tlvs": [tlv(0x7777, "string", "unbounded", "OctetString")], "rules": [rule(0x1401, "OctetString", 2, False)]}},
    ]
    return {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": [
            "jasmin/tools/tlv_encoder.py:_parse_tag_key",
            "jasmin/tools/tlv_encoder.py:resolve_tlv_types",
            "jasmin/tools/tlv_encoder.py:encode_tlv_value",
            "jasmin/tools/tlv_encoder.py:encode_custom_tlvs",
            "jasmin/tools/tlv_encoder.py:validate_custom_tlvs",
        ],
        "cases": [capture(case) for case in cases],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    document = build_document()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"vendor_tlv_golden={args.output} cases={len(document['cases'])} status=ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

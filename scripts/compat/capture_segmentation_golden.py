#!/usr/bin/env python3
"""Capture deterministic payload-segmentation fixtures from the frozen Python oracle."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
from pathlib import Path

from jasmin.protocols.smpp.operations import SMPPOperationFactory

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat" / "fixtures" / "segmentation" / "baseline.json"


def encoded(raw: bytes) -> dict:
    return {
        "base64": base64.b64encode(raw).decode("ascii"),
        "hex": raw.hex(),
        "length": len(raw),
        "sha256": hashlib.sha256(raw).hexdigest(),
    }


def classification(data_coding: int) -> dict:
    if data_coding in (3, 6, 7, 10):
        return {"bits": 8, "single_limit": 140, "multipart_payload_bytes": 134}
    if data_coding in (2, 4, 5, 8, 9, 13, 14):
        return {"bits": 16, "single_limit": 70, "multipart_payload_bytes": 134}
    return {"bits": 7, "single_limit": 160, "multipart_payload_bytes": 153}


def ascii_payload(length: int) -> bytes:
    alphabet = b"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
    return bytes(alphabet[index % len(alphabet)] for index in range(length))


def ucs2_payload(units: int) -> bytes:
    code_units = (b"\x06\x23", b"\x06\x31", b"\x06\x46", b"\x06\x28")
    return b"".join(code_units[index % len(code_units)] for index in range(units))


def capture_case(
    case_id: str,
    payload: bytes,
    data_coding: int,
    split_method: str,
    max_parts: int = 5,
    initial_reference: int = 41,
) -> dict:
    factory = SMPPOperationFactory(
        long_content_max_parts=max_parts,
        long_content_split=split_method,
    )
    factory.lastLongMsgRefNum = initial_reference
    current = factory.SubmitSM(short_message=payload, data_coding=data_coding)

    parts = []
    consumed = bytearray()
    emitted_reference = None
    while True:
        short_message = current.params["short_message"]
        sar = None
        udh = None
        raw_part = short_message
        if "sar_msg_ref_num" in current.params:
            sar = {
                "reference": int(current.params["sar_msg_ref_num"]),
                "total": int(current.params["sar_total_segments"]),
                "sequence": int(current.params["sar_segment_seqnum"]),
            }
            emitted_reference = sar["reference"]
        elif len(short_message) >= 6 and short_message[:3] == b"\x05\x00\x03":
            header = short_message[:6]
            raw_part = short_message[6:]
            udh = {
                "bytes": encoded(header),
                "reference": header[3],
                "total": header[4],
                "sequence": header[5],
            }
            emitted_reference = udh["reference"]

        consumed.extend(raw_part)
        parts.append(
            {
                "sequence": len(parts) + 1,
                "payload": encoded(raw_part),
                "short_message": encoded(short_message),
                "sar": sar,
                "udh": udh,
            }
        )
        if not hasattr(current, "nextPdu"):
            break
        current = current.nextPdu

    if bytes(consumed) != payload[: len(consumed)]:
        raise AssertionError(f"{case_id}: emitted content is not an input prefix")

    return {
        "id": case_id,
        "input": {
            "data_coding": data_coding,
            "split_method": split_method,
            "max_parts": max_parts,
            "initial_reference": initial_reference,
            "payload": encoded(payload),
        },
        "classification": classification(data_coding),
        "emitted_reference": emitted_reference,
        "consumed_payload_bytes": len(consumed),
        "truncated": len(consumed) != len(payload),
        "parts": parts,
    }


def capture(output: Path) -> None:
    cases = [
        # 1. GSM 7-bit boundaries (Limit 160, Slice 153)
        capture_case("gsm7_single_160", ascii_payload(160), 0, "sar"),
        capture_case("gsm7_sar_161", ascii_payload(161), 0, "sar"),
        capture_case("gsm7_udh_161", ascii_payload(161), 0, "udh"),
        # Extension char at 153rd byte (should be split naively by Jasmin)
        capture_case("gsm7_split_ext_at_153", ascii_payload(152) + b"\x1b\x3c" + ascii_payload(20), 0, "sar"),
        
        # 2. 8-bit boundaries (Limit 140, Slice 134)
        capture_case("eight_bit_single_140", ascii_payload(140), 3, "sar"),
        capture_case("eight_bit_sar_141", ascii_payload(141), 3, "sar"),
        capture_case("eight_bit_udh_141", ascii_payload(141), 3, "udh"),
        
        # 3. UCS2 boundaries (Limit 140 [70 units], Slice 134 [67 units])
        capture_case("ucs2_single_70_units", ucs2_payload(70), 8, "sar"),
        capture_case("ucs2_sar_71_units", ucs2_payload(71), 8, "sar"),
        capture_case("ucs2_udh_71_units", ucs2_payload(71), 8, "udh"),
        # Split surrogate pair at 134th byte (67th unit)
        # SlicedMaxSmLength = 134 bytes.
        # \xd8\x3d\xde\x0a (Emoji)
        capture_case("ucs2_split_surrogate_at_67", ucs2_payload(66) + b"\xd8\x3d\xde\x0a" + ucs2_payload(5), 8, "sar"),

        # 4. Special cases
        capture_case("invalid_dcs_255_fallback_sar_161", ascii_payload(161), 255, "sar"),
        capture_case("gsm7_reference_rollover_sar_161", ascii_payload(161), 0, "sar", initial_reference=255),
        capture_case("gsm7_max_parts_two_truncates", ascii_payload(400), 0, "sar", max_parts=2),
    ]
    output.parent.mkdir(parents=True, exist_ok=True)
    document = {"schema_version": 1, "baseline_commit": BASELINE, "cases": cases}
    output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"segmentation_golden={output} cases={len(cases)} status=ok")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    capture(args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

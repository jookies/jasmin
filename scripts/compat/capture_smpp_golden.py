#!/usr/bin/env python3
"""Capture deterministic SMPP wire fixtures from the frozen Python oracle."""

from __future__ import annotations

import argparse
import base64
import json
from io import BytesIO
from pathlib import Path

from smpp.pdu.operations import BindTransceiver, DeliverSM, SubmitSM, SubmitSMResp
from smpp.pdu.pdu_encoding import PDUEncoder
from smpp.pdu.pdu_types import CommandStatus

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat" / "fixtures" / "smpp" / "baseline.json"


def normalize(value):
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
    if value is None or isinstance(value, (bool, int, float, str)):
        return value
    if isinstance(value, dict):
        return {str(k): normalize(v) for k, v in sorted(value.items(), key=lambda item: str(item[0]))}
    if isinstance(value, (list, tuple)):
        return [normalize(item) for item in value]
    return {"type": value.__class__.__name__, "value": str(value)}


def summary(pdu):
    return {
        "command_id": str(pdu.id),
        "command_status": str(pdu.status),
        "sequence_number": int(pdu.seqNum),
        "parameters": normalize(pdu.params),
    }


def encoded_case(case_id, pdu, encoder):
    wire = encoder.encode(pdu)
    decoded = encoder.decode(BytesIO(wire))
    return {
        "id": case_id,
        "direction": "encode",
        "wire_hex": wire.hex(),
        "decoded": summary(decoded),
        "roundtrip_wire_hex": encoder.encode(decoded).hex(),
    }


def decoded_case(case_id, wire_hex, encoder):
    wire = bytes.fromhex(wire_hex)
    decoded = encoder.decode(BytesIO(wire))
    try:
        roundtrip_wire_hex = encoder.encode(decoded).hex()
        roundtrip_error = None
    except (TypeError, ValueError) as exc:
        roundtrip_wire_hex = None
        roundtrip_error = f"{exc.__class__.__name__}: {exc}"
    return {
        "id": case_id,
        "direction": "decode",
        "wire_hex": wire_hex.lower(),
        "decoded": summary(decoded),
        "roundtrip_wire_hex": roundtrip_wire_hex,
        "roundtrip_error": roundtrip_error,
    }


def capture(output: Path) -> None:
    encoder = PDUEncoder()
    cases = [
        encoded_case(
            "bind_transceiver",
            BindTransceiver(seqNum=1, system_id="client", password="secret", system_type=""),
            encoder,
        ),
        encoded_case(
            "submit_sm_ascii",
            SubmitSM(
                seqNum=7,
                source_addr="1111",
                destination_addr="2222",
                short_message=b"hello",
            ),
            encoder,
        ),
        encoded_case(
            "submit_sm_sar_part",
            SubmitSM(
                seqNum=8,
                source_addr="1111",
                destination_addr="2222",
                short_message=b"part-one",
                sar_msg_ref_num=4660,
                sar_total_segments=2,
                sar_segment_seqnum=1,
            ),
            encoder,
        ),
        encoded_case(
            "deliver_sm_mo",
            DeliverSM(
                seqNum=9,
                source_addr="15551230000",
                destination_addr="4040",
                short_message=b"MO hello",
            ),
            encoder,
        ),
        encoded_case(
            "submit_sm_resp_ok",
            SubmitSMResp(seqNum=7, status=CommandStatus.ESME_ROK, message_id="0000436949"),
            encoder,
        ),
        decoded_case(
            "deliver_sm_dlr_message_payload",
            "0000009200000005000000000001693c00000032313635333532303730330000003737383800040000000001000000000424004f69643a30303030343336393439207375626d697420646174653a3135303432313135303820646f6e6520646174653a3135303432313135303820737461743a44454c49565244206572723a30303000001e00063661616435000427000102",
            encoder,
        ),
        decoded_case(
            "submit_sm_unknown_vendor_tlv",
            "000000650000000400000000000005860005004a6f6a6f7300010135373331383833303831323900030000003135313131303134353630373030302b000100f1001a504c454153452049474e4f52452054484953204d455353414745140300053338363336",
            encoder,
        ),
    ]
    output.parent.mkdir(parents=True, exist_ok=True)
    document = {"schema_version": 1, "baseline_commit": BASELINE, "cases": cases}
    output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"smpp_golden={output} cases={len(cases)} status=ok")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    capture(args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

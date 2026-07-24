#!/usr/bin/env python3
"""Capture Jasmin SMPPS inbound-command gate behavior from the frozen oracle."""
from __future__ import annotations

import argparse
import json
from pathlib import Path

from smpp.pdu.operations import (
    BindTransmitter,
    DataSM,
    DeliverSM,
    EnquireLink,
    SubmitSM,
    Unbind,
    UnbindResp,
)
from smpp.pdu.pdu_encoding import PDUEncoder
from smpp.pdu.pdu_types import CommandStatus

from jasmin.protocols.smpp import protocol

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat/fixtures/smpps-bind-state/baseline.json"
ENCODER = PDUEncoder()


def _pdu(name: str):
    constructors = {
        "bind_transmitter": lambda: BindTransmitter(seqNum=1, system_id="u", password="p"),
        "submit_sm": lambda: SubmitSM(seqNum=2, source_addr="a", destination_addr="b", short_message=b"x"),
        "data_sm": lambda: DataSM(seqNum=3, source_addr="a", destination_addr="b"),
        "unbind": lambda: Unbind(seqNum=4),
        "unbind_resp": lambda: UnbindResp(seqNum=5),
        "enquire_link": lambda: EnquireLink(seqNum=6),
        "deliver_sm": lambda: DeliverSM(seqNum=7, source_addr="a", destination_addr="b", short_message=b"x"),
    }
    return constructors[name]()


def _wire_command_id(request) -> int:
    return int.from_bytes(ENCODER.encode(request)[4:8], "big")


def _wire_command_status(request, status) -> int:
    ack_request = request if hasattr(request, "requireAck") else _pdu("bind_transmitter")
    response = ack_request.requireAck(seqNum=ack_request.seqNum, status=status)
    return int.from_bytes(ENCODER.encode(response)[8:12], "big")


def capture_case(case: dict) -> dict:
    instance = protocol.SMPPServerProtocol.__new__(protocol.SMPPServerProtocol)
    instance.sessionState = getattr(protocol.SMPPSessionStates, case["state"])
    instance.user = None
    events = []
    instance.cancelOutboundTransactions = lambda error: events.append({"kind": "cancel", "error": error.__class__.__name__})

    request = _pdu(case["command"])

    def fatal_error(req, message, status):
        events.append({"kind": "reject", "status": str(status), "command_status": _wire_command_status(req, status)})
        return "fatal"

    instance.fatalErrorOnRequest = fatal_error
    original_delegate = protocol.twistedSMPPServerProtocol.PDURequestReceived

    def delegated(self, req):
        events.append({"kind": "delegate"})
        return "delegated"

    protocol.twistedSMPPServerProtocol.PDURequestReceived = delegated
    try:
        if case["entry_point"] == "PDUDataRequestReceived":
            instance.PDUDataRequestReceived(request)
        else:
            instance.PDURequestReceived(request)
    finally:
        protocol.twistedSMPPServerProtocol.PDURequestReceived = original_delegate

    terminal = [event for event in events if event["kind"] in {"reject", "delegate"}]
    if len(terminal) != 1:
        raise RuntimeError(f"{case['id']}: expected one terminal event, got {events!r}")
    outcome = terminal[0]
    if outcome["kind"] == "reject":
        expected = {
            "action": "reject",
            "status": outcome["status"],
            "command_status": outcome["command_status"],
        }
    else:
        expected = {
            "action": "delegate",
            "status": str(CommandStatus.ESME_ROK),
            "command_status": _wire_command_status(request, CommandStatus.ESME_ROK),
        }
    return {
        "id": case["id"],
        "input": {
            "state": case["state"],
            "command": case["command"],
            "command_id": _wire_command_id(request),
            "entry_point": case["entry_point"],
        },
        "expected": expected,
    }


def build_document() -> dict:
    cases = [
        dict(id="unsupported_deliver_sm_rejected", state="BOUND_TRX", command="deliver_sm", entry_point="PDURequestReceived"),
        dict(id="submit_sm_bound_rx_rejected", state="BOUND_RX", command="submit_sm", entry_point="PDUDataRequestReceived"),
        dict(id="data_sm_bound_rx_rejected", state="BOUND_RX", command="data_sm", entry_point="PDUDataRequestReceived"),
        dict(id="bind_transmitter_open_delegated", state="OPEN", command="bind_transmitter", entry_point="PDURequestReceived"),
        dict(id="submit_sm_bound_tx_delegated", state="BOUND_TX", command="submit_sm", entry_point="PDURequestReceived"),
        dict(id="data_sm_bound_trx_delegated", state="BOUND_TRX", command="data_sm", entry_point="PDURequestReceived"),
        dict(id="unbind_bound_trx_delegated", state="BOUND_TRX", command="unbind", entry_point="PDURequestReceived"),
        dict(id="enquire_link_open_delegated", state="OPEN", command="enquire_link", entry_point="PDURequestReceived"),
        dict(id="unbind_resp_unbound_delegated", state="UNBOUND", command="unbind_resp", entry_point="PDURequestReceived"),
    ]
    return {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": [
            "jasmin/protocols/smpp/protocol.py:SMPPServerProtocol.PDURequestReceived",
            "jasmin/protocols/smpp/protocol.py:SMPPServerProtocol.PDUDataRequestReceived",
        ],
        "cases": [capture_case(case) for case in cases],
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    document = build_document()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"smpps_bind_state_golden={args.output} cases={len(document['cases'])} status=ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

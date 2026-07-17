#!/usr/bin/env python3
"""Capture deterministic SMPP client response-publication behavior from the frozen oracle."""
from __future__ import annotations

import argparse
import base64
import datetime
import hashlib
import json
import pickle
import sys
from pathlib import Path
from types import SimpleNamespace

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from twisted.internet import defer
from smpp.pdu.operations import SubmitSM, SubmitSMResp
from smpp.pdu.pdu_types import CommandId, CommandStatus, RegisteredDelivery, RegisteredDeliveryReceipt

from jasmin.managers import content as manager_content
from jasmin.managers import listeners

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
PICKLE_PROTOCOL = 2
DEFAULT_OUTPUT = ROOT / "compat/fixtures/smpp-client-response-publish/baseline.json"


class FixedDateTime(datetime.datetime):
    @classmethod
    def now(cls, tz=None):
        value = cls(2026, 1, 2, 3, 4, 5, 678901)
        return value if tz is None else value.replace(tzinfo=tz)


class _Logger:
    def __init__(self):
        self.errors = []

    def debug(self, *args, **kwargs):
        pass

    def info(self, *args, **kwargs):
        pass

    def error(self, *args, **kwargs):
        self.errors.append(args)


class _Broker:
    def __init__(self):
        self.publishes = []

    def publish(self, exchange, routing_key, content):
        self.publishes.append({"exchange": exchange, "routing_key": routing_key, "content": content})
        return defer.succeed(None)


def _normalize(value):
    if isinstance(value, bytes):
        return {"type": "bytes", "base64": base64.b64encode(value).decode("ascii")}
    if value is None or isinstance(value, (bool, int, float, str)):
        return value
    if isinstance(value, dict):
        return {str(key): _normalize(item) for key, item in sorted(value.items(), key=lambda pair: str(pair[0]))}
    raise TypeError(f"unsupported property value: {type(value)!r}")


def _publication_contract(publication):
    content = publication["content"]
    if not isinstance(content.body, bytes):
        raise TypeError(f"unexpected response body type: {type(content.body)!r}")
    return {
        "exchange": publication["exchange"],
        "routing_key": publication["routing_key"],
        "properties": _normalize(content.properties),
        "body_base64": base64.b64encode(content.body).decode("ascii"),
        "body_sha256": hashlib.sha256(content.body).hexdigest(),
        "pickle_protocol": content.body[1] if len(content.body) >= 2 and content.body[0] == 0x80 else None,
    }


def capture_case(case):
    manager_content.datetime.datetime = FixedDateTime
    requeues = []
    acks = []
    cleared = []
    broker = _Broker()
    listener = listeners.SMPPClientSMListener.__new__(listeners.SMPPClientSMListener)
    listener.config = SimpleNamespace(
        submit_error_retrial=case["rules"],
        log_privacy=True,
        publish_submit_sm_resp=case["enabled"],
    )
    listener.SMPPClientFactory = SimpleNamespace(config=SimpleNamespace(id="fixture"))
    listener.submit_retrials = {case["id"]: case["current_attempt"]}
    listener.amqpBroker = broker
    listener.pickleProtocol = PICKLE_PROTOCOL
    logger = _Logger()
    listener.log = logger

    def reject_and_requeue(message, delay=True):
        requeues.append({"delivery_tag": message.delivery_tag, "delay_seconds": delay})
        return defer.succeed(None)

    def ack_message(message):
        acks.append(message.delivery_tag)
        return defer.succeed(None)

    def clear_reject_timer(msgid):
        cleared.append(msgid)

    listener.rejectAndRequeueMessage = reject_and_requeue
    listener.ackMessage = ack_message
    listener.clearRejectTimer = clear_reject_timer

    request = SubmitSM(source_addr="A", destination_addr="1", short_message=b"x")
    request.params["registered_delivery"] = RegisteredDelivery(
        RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED
    )
    response = SubmitSMResp(
        seqNum=7,
        status=getattr(CommandStatus, case["status"]),
        message_id="0000436949" if case["status"] == "ESME_ROK" else None,
    )
    result = SimpleNamespace(request=request, response=response)
    message = SimpleNamespace(
        delivery_tag=7,
        routing_key="submit.sm.fixture",
        content=SimpleNamespace(
            properties={
                "message-id": case["id"],
                "priority": 1,
                "reply-to": case["reply_to"],
                "headers": {},
            },
        ),
    )

    outcome = listener.submit_sm_resp_event(result, message)
    if not outcome.called:
        raise RuntimeError("listener callback did not complete synchronously")
    if hasattr(outcome, "result") and isinstance(outcome.result, BaseException):
        raise outcome.result
    if logger.errors:
        raise RuntimeError(f"legacy callback swallowed errors: {logger.errors!r}")

    response_publishes = [item for item in broker.publishes if item["routing_key"] == case["reply_to"]]
    dlr_publishes = [item for item in broker.publishes if item["routing_key"] == "dlr.submit_sm_resp"]
    if len(dlr_publishes) != 1:
        raise RuntimeError(f"unexpected DLR publish count: {len(dlr_publishes)}")
    expected_response_count = 1 if case["enabled"] else 0
    if len(response_publishes) != expected_response_count:
        raise RuntimeError(f"unexpected response publish count: {len(response_publishes)}")
    if len(broker.publishes) != 1 + expected_response_count:
        raise RuntimeError(f"unexpected publish path: {broker.publishes!r}")

    retried = bool(requeues)
    if retried:
        if outcome.result is not False or acks or cleared:
            raise RuntimeError("unexpected retry terminal state")
        action = "requeue"
    else:
        if outcome.result is not None or acks != [7] or cleared != [case["id"]]:
            raise RuntimeError("unexpected final terminal state")
        action = "ack"

    return {
        "action": action,
        "publication": _publication_contract(response_publishes[0]) if response_publishes else None,
    }


def build_document():
    cases = [
        dict(id="disabled_success_does_not_publish", enabled=False, status="ESME_ROK", current_attempt=1, rules={}, reply_to="submit.sm.resp.user-1"),
        dict(id="enabled_success_publishes", enabled=True, status="ESME_ROK", current_attempt=1, rules={}, reply_to="submit.sm.resp.user-1"),
        dict(id="enabled_final_error_publishes", enabled=True, status="ESME_RREPLACEFAIL", current_attempt=1, rules={}, reply_to="submit.sm.resp.user-2"),
        dict(id="enabled_retried_error_publishes", enabled=True, status="ESME_RSYSERR", current_attempt=1, rules={"ESME_RSYSERR": {"count": 2, "delay": 30}}, reply_to="submit.sm.resp.user-3"),
    ]
    return {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": [
            "jasmin/managers/listeners.py:SMPPClientSMListener.submit_sm_resp_event",
            "jasmin/managers/content.py:SubmitSmRespContent",
        ],
        "pickle_protocol": PICKLE_PROTOCOL,
        "cases": [
            {
                "id": case["id"],
                "input": {
                    "enabled": case["enabled"],
                    "status": case["status"],
                    "current_attempt": case["current_attempt"],
                    "reply_to": case["reply_to"],
                },
                "expected": capture_case(case),
            }
            for case in cases
        ],
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    document = build_document()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"smpp_client_response_publish_golden={args.output} cases={len(document['cases'])} status=ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

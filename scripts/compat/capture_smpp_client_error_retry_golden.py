#!/usr/bin/env python3
"""Capture deterministic SMPP client submit-error retry decisions from the frozen oracle."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from types import SimpleNamespace

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from twisted.internet import defer
from smpp.pdu.operations import SubmitSM
from smpp.pdu.pdu_types import CommandId, CommandStatus, RegisteredDelivery, RegisteredDeliveryReceipt

from jasmin.managers import listeners
from jasmin.managers.configs import SMPPClientSMListenerConfig

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
DEFAULT_OUTPUT = ROOT / "compat/fixtures/smpp-client-error-retry/baseline.json"


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
        self.publishes.append({"exchange": exchange, "routing_key": routing_key})
        return defer.succeed(None)


def _normalize_rules(rules):
    return [
        {"status": status, "count": rule["count"], "delay_seconds": rule["delay"]}
        for status, rule in sorted(rules.items())
    ]


def capture_case(case):
    requeues = []
    acks = []
    cleared = []
    broker = _Broker()
    listener = listeners.SMPPClientSMListener.__new__(listeners.SMPPClientSMListener)
    listener.config = SimpleNamespace(
        submit_error_retrial=case["rules"],
        log_privacy=True,
        publish_submit_sm_resp=False,
    )
    listener.SMPPClientFactory = SimpleNamespace(config=SimpleNamespace(id="fixture"))
    listener.submit_retrials = {case["id"]: case["current_attempt"]}
    listener.amqpBroker = broker
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
    response = SimpleNamespace(
        status=getattr(CommandStatus, case["status"]),
        id=CommandId.submit_sm_resp,
        params={},
    )
    result = SimpleNamespace(request=request, response=response)
    message = SimpleNamespace(
        delivery_tag=7,
        routing_key="submit.sm.fixture",
        content=SimpleNamespace(
            properties={"message-id": case["id"], "priority": 1, "headers": {}},
        ),
    )

    outcome = listener.submit_sm_resp_event(result, message)
    if not outcome.called:
        raise RuntimeError("listener callback did not complete synchronously")
    if hasattr(outcome, "result") and isinstance(outcome.result, BaseException):
        raise outcome.result

    retried = bool(requeues)
    if logger.errors:
        raise RuntimeError(f"legacy callback swallowed errors: {logger.errors!r}")
    if retried:
        if outcome.result is not False or acks or cleared:
            raise RuntimeError(f"unexpected retry terminal state: result={outcome.result!r} acks={acks!r} cleared={cleared!r}")
        if requeues != [{"delivery_tag": 7, "delay_seconds": case["rules"][case["status"]]["delay"]}]:
            raise RuntimeError(f"unexpected requeue: {requeues!r}")
    else:
        if outcome.result is not None or acks != [7] or cleared != [case["id"]]:
            raise RuntimeError(f"unexpected final terminal state: result={outcome.result!r} acks={acks!r} cleared={cleared!r} errors={logger.errors!r}")
    expected_publishes = [{"exchange": "messaging", "routing_key": "dlr.submit_sm_resp"}]
    if broker.publishes != expected_publishes:
        raise RuntimeError(f"unexpected publish path: {broker.publishes!r}")

    return {
        "action": "requeue" if retried else "ack",
        "requeue_delay_seconds": requeues[0]["delay_seconds"] if retried else None,
        "retry_entry_after": listener.submit_retrials.get(case["id"]),
    }


def build_document():
    defaults = SMPPClientSMListenerConfig().submit_error_retrial
    cases = [
        dict(id="syserr_first_requeues", status="ESME_RSYSERR", current_attempt=1, rules=defaults),
        dict(id="syserr_count_boundary_acks", status="ESME_RSYSERR", current_attempt=2, rules=defaults),
        dict(id="throttled_penultimate_requeues", status="ESME_RTHROTTLED", current_attempt=19, rules=defaults),
        dict(id="throttled_count_boundary_acks", status="ESME_RTHROTTLED", current_attempt=20, rules=defaults),
        dict(id="msgqful_first_requeues", status="ESME_RMSGQFUL", current_attempt=1, rules=defaults),
        dict(id="msgqful_count_boundary_acks", status="ESME_RMSGQFUL", current_attempt=2, rules=defaults),
        dict(id="invsched_first_requeues", status="ESME_RINVSCHED", current_attempt=1, rules=defaults),
        dict(id="invsched_count_boundary_acks", status="ESME_RINVSCHED", current_attempt=2, rules=defaults),
        dict(id="unconfigured_status_acks_preserving_entry", status="ESME_RREPLACEFAIL", current_attempt=1, rules=defaults),
        dict(id="custom_zero_delay_requeues", status="ESME_RSYSERR", current_attempt=2, rules={"ESME_RSYSERR": {"count": 3, "delay": 0}}),
        dict(id="custom_count_boundary_acks", status="ESME_RSYSERR", current_attempt=3, rules={"ESME_RSYSERR": {"count": 3, "delay": 0}}),
    ]
    return {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": [
            "jasmin/managers/configs.py:SMPPClientSMListenerConfig",
            "jasmin/managers/listeners.py:SMPPClientSMListener.submit_sm_resp_event",
        ],
        "defaults": _normalize_rules(defaults),
        "cases": [
            {
                "id": case["id"],
                "input": {
                    "status": case["status"],
                    "current_attempt": case["current_attempt"],
                    "rules": _normalize_rules(case["rules"]),
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
    print(f"smpp_client_error_retry_golden={args.output} cases={len(document['cases'])} status=ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

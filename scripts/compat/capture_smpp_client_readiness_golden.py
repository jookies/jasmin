#!/usr/bin/env python3
"""Capture deterministic SMPP client readiness behavior from the frozen oracle."""
from __future__ import annotations

import argparse
import json
import pickle
import sys
from datetime import datetime as RealDateTime, timedelta
from pathlib import Path
from types import SimpleNamespace

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT))

from twisted.internet import defer
from smpp.pdu.operations import SubmitSM

from jasmin.managers import listeners
from jasmin.managers.configs import SMPPClientSMListenerConfig

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
DEFAULT_OUTPUT = ROOT / "compat/fixtures/smpp-client-readiness/baseline.json"
NOW = RealDateTime(2026, 1, 2, 12, 0, 0)


class _Logger:
    def debug(self, *args, **kwargs):
        pass

    def info(self, *args, **kwargs):
        pass

    def error(self, *args, **kwargs):
        pass

    def critical(self, *args, **kwargs):
        raise RuntimeError("legacy callback entered unexpected critical path")


class _Queue:
    def get(self):
        return defer.Deferred()


class _DateTime(RealDateTime):
    @classmethod
    def now(cls, tz=None):
        if tz is None:
            return NOW
        return NOW.replace(tzinfo=tz)


class _SMPP:
    def __init__(self, bound):
        self._bound = bound

    def isBound(self):
        return self._bound


def _iso(value):
    return value.isoformat()


def capture_case(case):
    rejects = []
    requeues = []
    original_datetime = listeners.datetime
    listener = listeners.SMPPClientSMListener.__new__(listeners.SMPPClientSMListener)
    listener.SMPPClientFactory = SimpleNamespace(
        config=SimpleNamespace(id="fixture", submit_sm_throughput=0),
        smpp=_SMPP(case["bound"]) if case["connected"] else None,
    )
    listener.config = SimpleNamespace(
        submit_max_age_smppc_not_ready=case["max_age_seconds"],
        submit_retrial_delay_smppc_not_ready=case["retry_delay_seconds"],
    )
    listener.submit_sm_q = _Queue()
    listener.submit_retrials = {}
    listener.qos_last_submit_sm_at = None
    listener.log = _Logger()

    def reject_message(message, requeue=0):
        rejects.append({"delivery_tag": message.delivery_tag, "requeue": requeue})
        return defer.succeed(None)

    def reject_and_requeue(message, delay=True):
        requeues.append({"delivery_tag": message.delivery_tag, "delay_seconds": delay})
        return defer.succeed(None)

    listener.rejectMessage = reject_message
    listener.rejectAndRequeueMessage = reject_and_requeue

    headers = {"created_at": _iso(NOW - timedelta(seconds=case["created_age_seconds"]))}
    if case["expiration_offset_seconds"] is not None:
        headers["expiration"] = _iso(NOW + timedelta(seconds=case["expiration_offset_seconds"]))
    message = SimpleNamespace(
        delivery_tag=7,
        content=SimpleNamespace(
            body=pickle.dumps(SubmitSM(source_addr="A", destination_addr="1", short_message=b"x"), protocol=2),
            properties={"message-id": case["id"], "headers": headers},
        ),
    )

    listeners.datetime = _DateTime
    try:
        outcome = listener.submit_sm_callback(message)
        if not outcome.called:
            raise RuntimeError("listener callback did not complete synchronously")
        if hasattr(outcome, "result") and isinstance(outcome.result, BaseException):
            raise outcome.result
    finally:
        listeners.datetime = original_datetime

    if outcome.result is not False:
        raise RuntimeError(f"unexpected callback result: {outcome.result!r}")
    if len(rejects) + len(requeues) != 1:
        raise RuntimeError(f"expected one terminal action, got rejects={rejects!r} requeues={requeues!r}")
    if listener.submit_retrials != {case["id"]: 1}:
        raise RuntimeError(f"unexpected retrial post-state: {listener.submit_retrials!r}")
    if rejects:
        return {"action": "discard", "requeue_delay_seconds": None, "retry_count": 1}
    return {
        "action": "requeue",
        "requeue_delay_seconds": requeues[0]["delay_seconds"],
        "retry_count": 1,
    }


def build_document():
    defaults = SMPPClientSMListenerConfig()
    cases = [
        dict(id="expired_discard_precedes_disconnected", connected=False, bound=False, created_age_seconds=10, expiration_offset_seconds=-1, max_age_seconds=1200, retry_delay_seconds=30),
        dict(id="expiration_at_now_is_not_expired", connected=False, bound=False, created_age_seconds=10, expiration_offset_seconds=0, max_age_seconds=1200, retry_delay_seconds=30),
        dict(id="disconnected_fresh_delayed_requeue", connected=False, bound=False, created_age_seconds=10, expiration_offset_seconds=None, max_age_seconds=1200, retry_delay_seconds=30),
        dict(id="disconnected_boundary_requeues", connected=False, bound=False, created_age_seconds=1200, expiration_offset_seconds=None, max_age_seconds=1200, retry_delay_seconds=30),
        dict(id="disconnected_over_age_discards", connected=False, bound=False, created_age_seconds=1201, expiration_offset_seconds=None, max_age_seconds=1200, retry_delay_seconds=30),
        dict(id="disconnected_zero_delay_requeues", connected=False, bound=False, created_age_seconds=10, expiration_offset_seconds=None, max_age_seconds=1200, retry_delay_seconds=0),
        dict(id="unbound_fresh_delayed_requeue", connected=True, bound=False, created_age_seconds=10, expiration_offset_seconds=None, max_age_seconds=1200, retry_delay_seconds=30),
        dict(id="unbound_over_age_discards", connected=True, bound=False, created_age_seconds=1201, expiration_offset_seconds=None, max_age_seconds=1200, retry_delay_seconds=30),
        dict(id="multi_day_age_uses_seconds_component", connected=False, bound=False, created_age_seconds=86410, expiration_offset_seconds=None, max_age_seconds=1200, retry_delay_seconds=30),
        dict(id="future_created_at_wraps_seconds_component", connected=False, bound=False, created_age_seconds=-1, expiration_offset_seconds=None, max_age_seconds=1200, retry_delay_seconds=30),
    ]
    return {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": [
            "jasmin/managers/configs.py:SMPPClientSMListenerConfig",
            "jasmin/managers/listeners.py:SMPPClientSMListener.submit_sm_callback",
        ],
        "defaults": {
            "max_age_seconds": defaults.submit_max_age_smppc_not_ready,
            "retry_delay_seconds": defaults.submit_retrial_delay_smppc_not_ready,
        },
        "cases": [{"id": case["id"], "input": {k: v for k, v in case.items() if k != "id"}, "expected": capture_case(case)} for case in cases],
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    document = build_document()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"smpp_client_readiness_golden={args.output} cases={len(document['cases'])} status=ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

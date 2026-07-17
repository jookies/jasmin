#!/usr/bin/env python3
"""Capture deterministic SMPP client pacing behavior from the frozen oracle."""
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

from jasmin.managers import listeners
from jasmin.protocols.smpp.configs import SMPPClientConfig

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
DEFAULT_OUTPUT = ROOT / "compat/fixtures/smpp-client-pacing/baseline.json"
NOW = RealDateTime(2026, 1, 1, 12, 0, 0)


class _Logger:
    def debug(self, *args, **kwargs):
        pass

    def error(self, *args, **kwargs):
        pass

    def critical(self, *args, **kwargs):
        raise RuntimeError("legacy callback entered unexpected critical error path")


class _Queue:
    def get(self):
        return defer.Deferred()


class _DateTime(RealDateTime):
    @classmethod
    def now(cls, tz=None):
        if tz is None:
            return NOW
        return NOW.replace(tzinfo=tz)


class _Message:
    delivery_tag = 1
    content = SimpleNamespace(
        body=pickle.dumps(object(), protocol=2),
        properties={"message-id": "fixture-message"},
    )


def capture_config(value_marker):
    kwargs = {"id": "fixture"}
    if value_marker != "default":
        kwargs["submit_sm_throughput"] = value_marker
    try:
        config = SMPPClientConfig(**kwargs)
    except Exception as exc:
        return {"value": None, "error_type": type(exc).__name__, "error": str(exc)}
    return {
        "value": config.submit_sm_throughput,
        "error_type": None,
        "error": None,
    }


def capture_wait(throughput, elapsed_seconds):
    waits = []
    rejects = []
    original_datetime = listeners.datetime
    original_slow_down = listeners.qos.slow_down

    def slow_down(seconds):
        waits.append(seconds)
        return defer.succeed(None)

    listener = listeners.SMPPClientSMListener.__new__(listeners.SMPPClientSMListener)
    listener.SMPPClientFactory = SimpleNamespace(
        config=SimpleNamespace(id="fixture", submit_sm_throughput=throughput)
    )
    listener.submit_sm_q = _Queue()
    listener.submit_retrials = {}
    listener.qos_last_submit_sm_at = (
        None if elapsed_seconds is None else NOW - timedelta(seconds=elapsed_seconds)
    )
    setattr(listener, "log", _Logger())

    def reject_message(message, requeue=0):
        rejects.append((message.delivery_tag, requeue))
        return defer.succeed(None)

    setattr(listener, "rejectMessage", reject_message)

    listeners.datetime = _DateTime
    listeners.qos.slow_down = slow_down
    try:
        outcome = listener.submit_sm_callback(_Message())
        if not outcome.called:
            raise RuntimeError("listener callback did not complete synchronously")
        if hasattr(outcome, "result") and isinstance(outcome.result, BaseException):
            raise outcome.result
    finally:
        listeners.datetime = original_datetime
        listeners.qos.slow_down = original_slow_down

    if outcome.result is not False:
        raise RuntimeError(f"unexpected callback result: {outcome.result!r}")
    if rejects != [(1, 0)]:
        raise RuntimeError(f"unexpected controlled-terminal rejects: {rejects!r}")
    if throughput > 0 and listener.qos_last_submit_sm_at != NOW:
        raise RuntimeError(
            f"pacing post-state was not updated: {listener.qos_last_submit_sm_at!r}"
        )
    return waits[0] if waits else 0.0


def build_document():
    cases = [
        {
            "id": "default_config",
            "input": {"throughput": "default", "elapsed_seconds": None},
            "expected": {"config": capture_config("default"), "wait_seconds": None},
        },
        {
            "id": "non_numeric_rejected",
            "input": {"throughput": "fast", "elapsed_seconds": None},
            "expected": {"config": capture_config("fast"), "wait_seconds": None},
        },
    ]
    pacing = [
        ("unlimited_zero", 0, 0.01),
        ("negative_disables_pacing", -1, 0.01),
        ("first_message_no_wait", 1, None),
        ("first_message_large_interval_component", 9.99999999975e-11, None),
        ("first_message_large_interval_zero_component", 1e-10, None),
        ("one_mps_same_timestamp", 1, 0.0),
        ("one_mps_fast", 1, 0.25),
        ("one_mps_boundary", 1, 1.0),
        ("four_mps_fast", 4, 0.10),
        ("fractional_two_point_five_mps", 2.5, 0.10),
        ("half_even_interval_7812_microseconds", 128, 0.0),
        ("half_even_interval_2_microseconds", 400000, 0.0),
        ("half_even_interval_zero_microseconds", 2000000, 0.0),
        ("tiny_positive_large_interval_component", 9.99999999975e-11, 0.0),
        ("large_interval_one_microsecond_elapsed", 1e-10, 0.000001),
        ("large_interval_three_microseconds_elapsed", 1e-10, 0.000003),
        ("tiny_positive_large_interval_one_microsecond_elapsed", 9.99999999975e-11, 0.000001),
        ("backward_clock_zero_interval", 2000000, -0.000001),
        ("half_mps_legacy_microseconds", 0.5, 0.25),
    ]
    for case_id, throughput, elapsed in pacing:
        cases.append(
            {
                "id": case_id,
                "input": {"throughput": throughput, "elapsed_seconds": elapsed},
                "expected": {
                    "config": capture_config(throughput),
                    "wait_seconds": capture_wait(throughput, elapsed),
                },
            }
        )
    return {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": [
            "jasmin/protocols/smpp/configs.py:SMPPClientConfig",
            "jasmin/managers/listeners.py:SMPPClientSMListener.submit_sm_callback",
        ],
        "cases": cases,
    }


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    document = build_document()
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"smpp_client_pacing_golden={args.output} cases={len(document['cases'])} status=ok")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

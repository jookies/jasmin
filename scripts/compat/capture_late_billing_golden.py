#!/usr/bin/env python3
"""Capture the frozen RouterPB late submit-response billing callback."""

from __future__ import annotations

import argparse
import hashlib
import json
import logging
from pathlib import Path
from types import SimpleNamespace

from twisted.internet import defer

from jasmin.routing.jasminApi import Group, User
from jasmin.routing.router import RouterPB

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat/fixtures/late-billing/baseline.json"
SOURCE = [
    "jasmin/routing/router.py:97-109",
    "jasmin/routing/router.py:235-263",
    "jasmin/managers/content.py:201-212",
]


class _Queue:
    def get(self):
        return defer.Deferred()


def _user(uid: str, balance):
    user = User(uid, Group("group-1"), "fixture", "password")
    credential = user.mt_credential
    assert credential is not None
    if balance is not None:
        credential.setQuota("balance", float(balance))
    return user


def capture_case(case: dict) -> dict:
    router = RouterPB.__new__(RouterPB)
    user = None if case["balance"] == "missing" else _user(case["uid"], case["balance"])
    router.users = [] if user is None else [user]
    router.log = logging.getLogger("late-billing-fixture")
    router.bill_request_submit_sm_resp_q = _Queue()
    actions = []

    def ack_message(message):
        del message
        actions.append("ack")
        return defer.succeed(None)

    def reject_message(message):
        del message
        actions.append("reject")
        return defer.succeed(None)

    router.ackMessage = ack_message
    router.rejectMessage = reject_message
    message = SimpleNamespace(
        content=SimpleNamespace(
            properties={
                "message-id": case["message_id"],
                "headers": {"user-id": case["uid"], "amount": case["amount"]},
            }
        )
    )
    completed = router.bill_request_submit_sm_resp_callback(message)
    if not completed.called:
        raise AssertionError(f"callback did not complete for {case['id']}")
    if len(actions) > 1:
        raise AssertionError(f"multiple terminal actions for {case['id']}: {actions}")

    if user is None:
        balance_after = None
    else:
        credential = user.mt_credential
        assert credential is not None
        balance_after = credential.getQuota("balance")
    return {
        "id": case["id"],
        "input": {
            "routing_key": f"bill_request.submit_sm_resp.{case['uid']}",
            "message_id": case["message_id"],
            "user_id": case["uid"],
            "amount": case["amount"],
            "balance": case["balance"],
        },
        "expected": {
            "action": actions[0] if actions else "none",
            "balance_after": balance_after,
        },
    }


def capture(output: Path) -> None:
    cases = [
        {"id": "missing_user_rejects", "uid": "1", "balance": "missing", "amount": "0.75", "message_id": "bill-missing"},
        {"id": "insufficient_balance_rejects_unchanged", "uid": "2", "balance": 0.74, "amount": "0.75", "message_id": "bill-insufficient"},
        {"id": "exact_balance_acks_and_decrements", "uid": "3", "balance": 0.75, "amount": "0.75", "message_id": "bill-exact"},
        {"id": "sufficient_balance_acks_and_decrements", "uid": "4", "balance": 2.0, "amount": "0.75", "message_id": "bill-sufficient"},
        {"id": "zero_amount_acks_without_delta", "uid": "5", "balance": 2.0, "amount": "0", "message_id": "bill-zero"},
        {"id": "opaque_user_id_acks_and_decrements", "uid": "alice_1", "balance": 2.0, "amount": "0.75", "message_id": "bill-opaque"},
        {"id": "unlimited_balance_has_no_terminal_action", "uid": "6", "balance": None, "amount": "0.75", "message_id": "bill-unlimited"},
    ]
    captured = [capture_case(case) for case in cases]
    corpus_sha256 = hashlib.sha256(
        json.dumps(captured, sort_keys=True, separators=(",", ":")).encode("utf-8")
    ).hexdigest()
    document = {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": SOURCE,
        "cases_sha256": corpus_sha256,
        "cases": captured,
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"late_billing_golden={output} cases={len(captured)} sha256={corpus_sha256} status=ok")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    capture(args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

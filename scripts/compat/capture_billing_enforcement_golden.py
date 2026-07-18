#!/usr/bin/env python3
"""Capture submit-time billing mutation from the frozen RouterPB boundary."""

from __future__ import annotations

import argparse
import hashlib
import json
import logging
from pathlib import Path

from jasmin.routing.jasminApi import Group, SmppClientConnector, User
from jasmin.routing.router import RouterPB
from jasmin.routing.Routes import StaticMTRoute

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat/fixtures/billing-enforcement/baseline.json"
SOURCE = [
    "jasmin/protocols/http/endpoints/send.py:319-355",
    "jasmin/protocols/smpp/factory.py:448-466",
    "jasmin/routing/router.py:319-363",
]


def make_user(spec: dict) -> User:
    user = User(spec["uid"], Group(spec.get("gid", "group-1")), "fixture", "password")
    if spec.get("balance") is not None:
        user.mt_credential.setQuota("balance", float(spec["balance"]))
    if spec.get("early_percent") is not None:
        user.mt_credential.setQuota("early_decrement_balance_percent", int(spec["early_percent"]))
    if spec.get("submit_sm_count") is not None:
        user.mt_credential.setQuota("submit_sm_count", int(spec["submit_sm_count"]))
    return user


def quota(user: User, name: str):
    return user.mt_credential.getQuota(name)


def capture_case(case: dict) -> dict:
    user = make_user(case["user"])
    route = StaticMTRoute([], SmppClientConnector("connector-a"), float(case["route_rate"]))
    bill = route.getBillFor(user)
    segments = int(case["segments"])

    balance = quota(user, "balance")
    count = quota(user, "submit_sm_count")
    required_total_balance = bill.getTotalAmounts() * segments
    required_count = bill.getAction("decrement_submit_sm_count") * segments
    requirements = []
    if balance is not None and required_total_balance > 0:
        requirements.append(required_total_balance <= balance)
    if count is not None:
        requirements.append(required_count <= count)

    router = RouterPB.__new__(RouterPB)
    router.users = [user]
    router.log = logging.getLogger("billing-enforcement-fixture")
    result = router.chargeUserForSubmitSms(
        user,
        bill,
        submit_sm_count=segments,
        requirements=[
            {"condition": condition, "error_message": "fixture requirement rejected"}
            for condition in requirements
        ],
    )

    return {
        "id": case["id"],
        "input": {
            "route_rate": case["route_rate"],
            "segments": segments,
            "user": case["user"],
        },
        "bill": {
            "submit_sm_amount_per_segment": bill.getAmount("submit_sm"),
            "submit_sm_resp_amount_per_segment": bill.getAmount("submit_sm_resp"),
            "decrement_submit_sm_count_per_segment": bill.getAction("decrement_submit_sm_count"),
            "required_total_balance": required_total_balance,
            "required_submit_sm_count": required_count,
        },
        "expected": {
            "accepted": result is True,
            "balance_after": quota(user, "balance"),
            "submit_sm_count_after": quota(user, "submit_sm_count"),
        },
    }


def capture(output: Path) -> None:
    cases = [
        {
            "id": "split_exact_total_authorized_early_only_applied",
            "route_rate": 1.5,
            "segments": 1,
            "user": {"uid": "u-1", "balance": 1.5, "early_percent": 50, "submit_sm_count": None},
        },
        {
            "id": "split_below_total_rejected_unchanged",
            "route_rate": 1.5,
            "segments": 1,
            "user": {"uid": "u-2", "balance": 1.49, "early_percent": 50, "submit_sm_count": None},
        },
        {
            "id": "multipart_split_uses_segment_multiplier",
            "route_rate": 1.0,
            "segments": 3,
            "user": {"uid": "u-3", "balance": 3.0, "early_percent": 50, "submit_sm_count": 3},
        },
        {
            "id": "multipart_count_below_rejects_without_balance_mutation",
            "route_rate": 1.0,
            "segments": 3,
            "user": {"uid": "u-4", "balance": 10.0, "early_percent": 100, "submit_sm_count": 2},
        },
        {
            "id": "balance_equality_full_early",
            "route_rate": 2.0,
            "segments": 2,
            "user": {"uid": "u-5", "balance": 4.0, "early_percent": 100, "submit_sm_count": 2},
        },
        {
            "id": "unlimited_quotas_are_not_mutated",
            "route_rate": 9.0,
            "segments": 4,
            "user": {"uid": "u-6", "balance": None, "early_percent": None, "submit_sm_count": None},
        },
        {
            "id": "unrated_route_still_decrements_count",
            "route_rate": 0.0,
            "segments": 2,
            "user": {"uid": "u-7", "balance": 0.0, "early_percent": None, "submit_sm_count": 2},
        },
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
    print(f"billing_enforcement_golden={output} cases={len(captured)} sha256={corpus_sha256} status=ok")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    capture(args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

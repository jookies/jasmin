#!/usr/bin/env python3
"""Capture frozen RouterPB quota persistence-timer decisions."""
from __future__ import annotations

import argparse
import hashlib
import json
import logging
from pathlib import Path

from jasmin.routing.jasminApi import Group, MtMessagingCredential, User
from jasmin.routing.router import RouterPB

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat/fixtures/billing-persistence/baseline.json"
SOURCE = [
    "jasmin/routing/router.py:119-151",
    "tests/routing/test_router.py:1089-1193",
]


def make_user(uid: str, dirty: bool) -> User:
    credential = MtMessagingCredential()
    credential.setQuota("balance", 2.0)
    credential.quotas_updated = dirty
    return User(uid, Group("group-1"), "user_" + uid, "password", credential)


def capture_case(case: dict) -> dict:
    router = RouterPB.__new__(RouterPB)
    router.log = logging.getLogger("billing-persistence-fixture")
    router.users = [make_user(str(index + 1), dirty) for index, dirty in enumerate(case["dirty_before"])]
    calls = []
    rearms = []

    def persist(profile="jcli-prod", scope="all"):
        del profile
        calls.append(scope)
        return case["persist_result"]

    def rearm():
        rearms.append("rearm")

    router.perspective_persist = persist
    router.activatePersistenceTimer = rearm
    router.persistenceTimerExpired()
    return {
        "id": case["id"],
        "input": {
            "dirty_before": case["dirty_before"],
            "persist_result": case["persist_result"],
        },
        "expected": {
            "persist_scopes": calls,
            "dirty_after": [
                user.mt_credential.quotas_updated
                for user in router.users
                if user.mt_credential is not None
            ],
            "rearm_count": len(rearms),
        },
    }


def capture(output: Path) -> None:
    cases = [
        {"id": "clean_tick_only_rearms", "dirty_before": [False, False], "persist_result": True},
        {"id": "one_dirty_persists_groups_then_users", "dirty_before": [True], "persist_result": True},
        {"id": "first_dirty_only_is_cleared", "dirty_before": [True, True], "persist_result": True},
        {"id": "legacy_false_return_still_clears", "dirty_before": [True], "persist_result": False},
    ]
    captured = [capture_case(case) for case in cases]
    digest = hashlib.sha256(json.dumps(captured, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    document = {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": SOURCE,
        "cases_sha256": digest,
        "cases": captured,
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"billing_persistence_golden={output} cases={len(captured)} sha256={digest} status=ok")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    capture(args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

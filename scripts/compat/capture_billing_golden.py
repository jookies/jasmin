#!/usr/bin/env python3
"""Capture deterministic billing behavior from the frozen oracle."""
from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

# Add project root to sys.path for jasmin imports
ROOT = Path(__file__).resolve().parents[2]
sys.path.append(str(ROOT))

from jasmin.routing.Bills import SubmitSmBill
from jasmin.routing.Routes import StaticMTRoute, StaticMORoute
from jasmin.routing.jasminApi import Group, SmppClientConnector, User

BASELINE = "4a7a2bcbaa5bce053f959d28992cae9bfa6f241c"
DEFAULT_OUTPUT = ROOT / "compat/fixtures/billing/baseline.json"

def build_user(uid, balance=None, early_percent=None, sm_count=None):
    u = User(uid, Group(100), "fixture", "password")
    if balance is not None:
        u.mt_credential.setQuota('balance', float(balance))
    if early_percent is not None:
        u.mt_credential.setQuota('early_decrement_balance_percent', int(early_percent))
    if sm_count is not None:
        u.mt_credential.setQuota('submit_sm_count', int(sm_count))
    return u

def capture_case(case_id, route_rate, user_spec):
    u = build_user(user_spec["uid"], user_spec.get("balance"), user_spec.get("early_percent"), user_spec.get("sm_count"))
    connector = SmppClientConnector("dst")
    route = StaticMTRoute([], connector, float(route_rate))
    
    bill = route.getBillFor(u)
    
    return {
        "id": case_id,
        "route_rate": route_rate,
        "user": user_spec,
        "expected": {
            "submit_sm_amount": bill.getAmount('submit_sm'),
            "submit_sm_resp_amount": bill.getAmount('submit_sm_resp'),
            "decrement_submit_sm_count": bill.getAction('decrement_submit_sm_count')
        }
    }

def capture(output):
    cases = [
        capture_case("rated_100_percent", 1.5, {"uid": 1, "balance": 10.0, "early_percent": 100}),
        capture_case("rated_50_percent", 1.5, {"uid": 2, "balance": 10.0, "early_percent": 50}),
        capture_case("rated_1_percent", 1.5, {"uid": 3, "balance": 10.0, "early_percent": 1}),
        capture_case("rated_default_percent", 1.5, {"uid": 4, "balance": 10.0, "early_percent": None}),
        capture_case("unlimited_balance", 1.5, {"uid": 5, "balance": None}),
        capture_case("unrated_route", 0.0, {"uid": 6, "balance": 10.0}),
        capture_case("with_sm_count", 1.5, {"uid": 7, "balance": 10.0, "sm_count": 100}),
        capture_case("with_sm_count_unlimited", 1.5, {"uid": 8, "balance": 10.0, "sm_count": None}),
        capture_case("rounding_check", 0.1, {"uid": 9, "balance": 1.0, "early_percent": 33}),
    ]
    
    doc = {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": "jasmin/routing/Routes.py",
        "cases": cases
    }
    
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(doc, indent=2, sort_keys=True) + "\n")
    print(f"billing_golden={output} cases={len(cases)} status=ok")

def main():
    p = argparse.ArgumentParser()
    p.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    a = p.parse_args()
    capture(a.output)
    return 0

if __name__ == "__main__":
    raise SystemExit(main())

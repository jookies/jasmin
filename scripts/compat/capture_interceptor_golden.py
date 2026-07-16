#!/usr/bin/env python3
"""Capture deterministic interceptor behavior from the frozen oracle."""
from __future__ import annotations

import argparse
import json
import sys
import pickle
import datetime as dt
from pathlib import Path

# Add project root to sys.path for jasmin imports
ROOT = Path(__file__).resolve().parents[2]
sys.path.append(str(ROOT))

from jasmin.routing.Routables import SimpleRoutablePDU
from jasmin.routing.jasminApi import Connector, Group, User
from smpp.pdu.operations import SubmitSM

BASELINE = "05e193ef974514299b042b4742a781b0a701467a"
DEFAULT_OUTPUT = ROOT / "compat/fixtures/interceptor/baseline.json"

def build_routable():
    pdu = SubmitSM(
        source_addr='20203060',
        destination_addr='123456',
        short_message='hello world',
    )
    connector = Connector('abc')
    user = User(1, Group(100), 'username', 'password')
    return SimpleRoutablePDU(connector, pdu, user, dt.datetime(2023, 10, 27, 10, 0, 0))

def run_script(pyCode, routable):
    # This logic matches jasmin/interceptor/interceptor.py
    gl = {
        'routable': routable,
        'smpp_status': None,
        'http_status': None,
        'dt': dt,
    }
    try:
        exec(pyCode, gl)
    except Exception as e:
        return {"error": str(e)}
    
    # Extract tags
    tags = list(routable._tags)

    def decode_bytes(val):
        if isinstance(val, bytes):
            return val.decode('ascii', errors='replace')
        return val
    
    return {
        "routable": {
            "source_addr": decode_bytes(routable.pdu.params['source_addr']),
            "destination_addr": decode_bytes(routable.pdu.params['destination_addr']),
            "short_message": decode_bytes(routable.pdu.params['short_message']),
            "tags": tags,
        },
        "smpp_status": gl['smpp_status'],
        "http_status": gl['http_status'],
    }

def capture_case(case_id, pyCode):
    r = build_routable()
    result = run_script(pyCode, r)
    return {
        "id": case_id,
        "script": pyCode,
        "expected": result
    }

def capture(output):
    cases = [
        capture_case("noop", "pass"),
        capture_case("change_src", "routable.pdu.params['source_addr'] = '9999'"),
        capture_case("add_tag", "routable.addTag('newtag')"),
        capture_case("multiple_mutations", "routable.pdu.params['short_message'] = 'changed'; routable.addTag('t1'); routable.addTag('t2')"),
        capture_case("set_statuses", "smpp_status = 64; http_status = 403"),
        capture_case("syntax_error", "some invalid code !!!"),
    ]
    
    doc = {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": "jasmin/interceptor/interceptor.py",
        "cases": cases
    }
    
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(doc, indent=2, sort_keys=True) + "\n")
    print(f"interceptor_golden={output} cases={len(cases)} status=ok")

def main():
    p = argparse.ArgumentParser()
    p.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    a = p.parse_args()
    capture(a.output)
    return 0

if __name__ == "__main__":
    raise SystemExit(main())

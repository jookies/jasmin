#!/usr/bin/env python3
"""Capture deterministic legacy HTTP responses from the frozen Python oracle."""

from __future__ import annotations

import argparse
import base64
import json
import re
import tempfile
import shutil
from pathlib import Path

from twisted.internet import defer, task

from jasmin.managers.clients import SMPPClientManagerPB
from jasmin.managers.configs import SMPPClientPBConfig
from jasmin.protocols.http.configs import HTTPApiConfig
from jasmin.protocols.http.server import HTTPApi
from jasmin.routing.Routes import DefaultRoute
from jasmin.routing.configs import RouterPBConfig
from jasmin.routing.jasminApi import Group, SmppClientConnector, User
from jasmin.routing.router import RouterPB
from tests.protocols.http.twisted_web_test_utils import DummySite

# Ensure we have a writable directory for logs/store
TMP_DIR = Path(tempfile.mkdtemp(prefix="jasmin_capture_"))

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat" / "fixtures" / "http" / "baseline.json"


def _headers(request) -> dict[str, list[str]]:
    values: dict[str, list[str]] = {}
    for name, raw_values in request.responseHeaders.getAllRawHeaders():
        key = name.decode("latin1") if isinstance(name, bytes) else str(name)
        values[key.lower()] = sorted(
            value.decode("latin1") if isinstance(value, bytes) else str(value)
            for value in raw_values
        )
    return dict(sorted(values.items()))


def _case(case_id: str, method: str, path: str, arguments, response, json_body=None):
    body = response.value()
    try:
        body_utf8 = body.decode("utf-8")
    except UnicodeDecodeError:
        body_utf8 = None
    normalized_arguments = {
        (k.decode() if isinstance(k, bytes) else str(k)): (
            v.decode() if isinstance(v, bytes) else str(v)
        )
        for k, v in sorted((arguments or {}).items(), key=lambda item: str(item[0]))
    }
    return {
        "id": case_id,
        "request": {
            "method": method,
            "path": f"/{path}",
            "arguments": normalized_arguments,
            "json": json_body,
        },
        "response": {
            "status": response.responseCode,
            "headers": _headers(response),
            "body_base64": base64.b64encode(body).decode("ascii"),
            "body_utf8": body_utf8,
        },
    }


@defer.inlineCallbacks
def capture(_reactor, output: Path):
    router_config = RouterPBConfig()
    router_config.log_file = str(TMP_DIR / "router.log")
    router_config.store_path = str(TMP_DIR / "store")
    (TMP_DIR / "store").mkdir(exist_ok=True)

    router = RouterPB(router_config)
    group = Group(1)
    user = User(1, group, "nathalie", "correct")
    router.groups.append(group)
    router.users.append(user)
    router.mt_routing_table.add(DefaultRoute(SmppClientConnector("abc")), 0)

    manager_config = SMPPClientPBConfig()
    manager_config.authentication = False
    manager_config.log_file = str(TMP_DIR / "manager.log")
    manager = SMPPClientManagerPB(manager_config)
    
    api_config = HTTPApiConfig()
    api_config.log_file = str(TMP_DIR / "httpapi.log")
    web = DummySite(HTTPApi(router, manager, api_config))
    cases = []

    try:
        # 1. Basics & Auth
        cases.append(_case("ping", "GET", "ping", {}, (yield web.get(b"ping"))))
        
        args = {b"username": b"nathalie", b"password": b"correct", b"to": b"06155423", b"content": b"hi"}
        cases.append(_case("send_no_live_connector", "POST", "send", args, (yield web.post(b"send", args))))

        args = {b"username": b"nathalie", b"password": b"wrong", b"to": b"06155423", b"content": b"hi"}
        cases.append(_case("send_bad_password", "POST", "send", args, (yield web.post(b"send", args))))

        # 2. Validation & Missing fields
        args = {b"username": b"nathalie", b"password": b"correct", b"content": b"hi"}
        cases.append(_case("send_missing_to", "POST", "send", args, (yield web.post(b"send", args))))
        
        args = {b"username": b"nathalie", b"password": b"correct", b"to": b"06155423"}
        cases.append(_case("send_missing_content", "POST", "send", args, (yield web.post(b"send", args))))

        # 3. Optional parameters
        args = {
            b"username": b"nathalie", b"password": b"correct", b"to": b"123456", b"content": b"test",
            b"from": b"JASMIN", b"coding": b"8", b"priority": b"2", b"dlr": b"yes", b"dlr-level": b"2",
            b"dlr-method": b"GET", b"tags": b"tag1,tag2"
        }
        cases.append(_case("send_all_optional", "POST", "send", args, (yield web.post(b"send", args))))

        # 4. Filters
        user.mt_credential.setValueFilter('destination_address', re.compile(r'^33\d+$'))
        args = {b"username": b"nathalie", b"password": b"correct", b"to": b"44123456", b"content": b"hi"}
        cases.append(_case("send_filter_dest_mismatch", "POST", "send", args, (yield web.post(b"send", args))))
        user.mt_credential.setValueFilter('destination_address', re.compile(r'.*'))

        user.mt_credential.setValueFilter('source_address', re.compile(r'^\d+$'))
        args = {b"username": b"nathalie", b"password": b"correct", b"to": b"33123456", b"content": b"hi", b"from": b"ALPHA"}
        cases.append(_case("send_filter_src_mismatch", "POST", "send", args, (yield web.post(b"send", args))))
        user.mt_credential.setValueFilter('source_address', re.compile(r'.*'))

        # 5. Authorizations
        user.mt_credential.setAuthorization('set_source_address', False)
        args = {b"username": b"nathalie", b"password": b"correct", b"to": b"33123456", b"content": b"hi", b"from": b"123"}
        cases.append(_case("send_auth_src_addr_forbidden", "POST", "send", args, (yield web.post(b"send", args))))
        user.mt_credential.setAuthorization('set_source_address', True)

        # 6. Quotas
        user.mt_credential.setQuota('balance', 0.0)
        args = {b"username": b"nathalie", b"password": b"correct", b"to": b"33123456", b"content": b"hi"}
        cases.append(_case("send_insufficient_balance", "POST", "send", args, (yield web.post(b"send", args))))
        user.mt_credential.setQuota('balance', None)

        # 7. JSON
        payload = {"username": "nathalie", "password": "correct", "to": "12345", "content": "json test"}
        response = yield web.post(b"send", json_data=payload, headers={b"Content-type": [b"application/json"]})
        cases.append(_case("send_json_valid", "POST", "send", {}, response, payload))

    finally:
        router.cancelPersistenceTimer()
    
    output.parent.mkdir(parents=True, exist_ok=True)
    document = {"schema_version": 1, "baseline_commit": BASELINE, "cases": cases}
    output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"http_golden={output} cases={len(cases)} status=ok")
    shutil.rmtree(TMP_DIR)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    task.react(lambda reactor: capture(reactor, args.output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

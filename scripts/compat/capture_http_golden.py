#!/usr/bin/env python3
"""Capture deterministic legacy HTTP responses from the frozen Python oracle."""

from __future__ import annotations

import argparse
import base64
import json
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
    router = RouterPB(RouterPBConfig())
    group = Group(1)
    user = User(1, group, "nathalie", "correct")
    router.groups.append(group)
    router.users.append(user)
    router.mt_routing_table.add(DefaultRoute(SmppClientConnector("abc")), 0)

    manager_config = SMPPClientPBConfig()
    manager_config.authentication = False
    manager = SMPPClientManagerPB(manager_config)
    web = DummySite(HTTPApi(router, manager, HTTPApiConfig()))
    cases = []

    try:
        response = yield web.get(b"ping")
        cases.append(_case("ping", "GET", "ping", {}, response))

        args = {b"username": b"nathalie", b"password": b"correct", b"to": b"06155423"}
        response = yield web.get(b"rate", args)
        cases.append(_case("rate_valid", "GET", "rate", args, response))

        args = {b"username": b"nathalie", b"password": b"correct"}
        response = yield web.get(b"balance", args)
        cases.append(_case("balance_unlimited", "GET", "balance", args, response))

        args = {b"username": b"nathalie", b"to": b"06155423", b"content": b"hello"}
        response = yield web.post(b"send", args)
        cases.append(_case("send_missing_password", "POST", "send", args, response))

        args = {b"username": b"nathalie", b"password": b"wrong", b"to": b"06155423", b"content": b"hello"}
        response = yield web.post(b"send", args)
        cases.append(_case("send_bad_password", "POST", "send", args, response))

        args = {b"username": b"nathalie", b"password": b"correct", b"to": b"06155423", b"content": b"hello"}
        response = yield web.post(b"send", args)
        cases.append(_case("send_no_live_connector", "POST", "send", args, response))

        user.disable()
        args = {b"username": b"nathalie", b"password": b"correct", b"to": b"06155423"}
        response = yield web.get(b"rate", args)
        cases.append(_case("rate_disabled_user", "GET", "rate", args, response))
        user.enable()

        group.disable()
        args = {b"username": b"nathalie", b"password": b"correct"}
        response = yield web.get(b"balance", args)
        cases.append(_case("balance_disabled_group", "GET", "balance", args, response))
        group.enable()

        payload = {
            "username": "nathalie",
            "password": "wrong",
            "to": "06155423",
            "content": "hello",
        }
        response = yield web.post(
            b"send", json_data=payload, headers={b"Content-type": [b"application/json"]}
        )
        cases.append(_case("send_json_bad_password", "POST", "send", {}, response, payload))
    finally:
        router.cancelPersistenceTimer()

    output.parent.mkdir(parents=True, exist_ok=True)
    document = {"schema_version": 1, "baseline_commit": BASELINE, "cases": cases}
    output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"http_golden={output} cases={len(cases)} status=ok")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    task.react(lambda reactor: capture(reactor, args.output))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

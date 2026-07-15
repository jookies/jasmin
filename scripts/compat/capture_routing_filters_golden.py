#!/usr/bin/env python3
"""Capture deterministic routing-filter truth tables from the frozen Python oracle."""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
from datetime import date, datetime, time
from pathlib import Path

from jasmin.routing.Filters import (
    ConnectorFilter,
    DateIntervalFilter,
    DestinationAddrFilter,
    GroupFilter,
    ShortMessageFilter,
    SourceAddrFilter,
    TagFilter,
    TimeIntervalFilter,
    TransparentFilter,
    UserFilter,
)
from jasmin.routing.Routables import SimpleRoutablePDU
from jasmin.routing.jasminApi import Connector, Group, User
from smpp.pdu.operations import SubmitSM

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat" / "fixtures" / "routing-filters" / "baseline.json"
FILTER_CLASSES = {
    "transparent": TransparentFilter,
    "connector": ConnectorFilter,
    "user": UserFilter,
    "group": GroupFilter,
    "source_addr": SourceAddrFilter,
    "destination_addr": DestinationAddrFilter,
    "short_message": ShortMessageFilter,
    "date_interval": DateIntervalFilter,
    "time_interval": TimeIntervalFilter,
    "tag": TagFilter,
}


def encoded(value: bytes) -> dict:
    return {
        "base64": base64.b64encode(value).decode("ascii"),
        "length": len(value),
        "sha256": hashlib.sha256(value).hexdigest(),
    }


def routable_spec(**overrides) -> dict:
    spec = {
        "direction": "mt",
        "connector_id": "abc",
        "user_id": 1,
        "group_id": 100,
        "source_addr": encoded(b"20203060"),
        "destination_addr": encoded(b"20203060"),
        "short_message": encoded(b"hello world"),
        "message_payload": None,
        "timestamp": "2024-01-15T06:00:00",
        "tags": [],
    }
    spec.update(overrides)
    return spec


def build_routable(spec: dict) -> SimpleRoutablePDU:
    pdu = SubmitSM()
    for name in ("source_addr", "destination_addr", "short_message", "message_payload"):
        descriptor = spec[name]
        if descriptor is None:
            pdu.params.pop(name, None)
        else:
            pdu.params[name] = base64.b64decode(descriptor["base64"], validate=True)
    group = Group(spec["group_id"])
    user = User(spec["user_id"], group, "fixture-user", "fixture-password")
    routable = SimpleRoutablePDU(
        Connector(spec["connector_id"]),
        pdu,
        user,
        datetime.fromisoformat(spec["timestamp"]),
    )
    for tag in spec["tags"]:
        routable.addTag(tag)
    return routable


def build_filter(spec: dict):
    kind = spec["type"]
    if kind == "transparent":
        return TransparentFilter()
    if kind == "connector":
        return ConnectorFilter(Connector(spec["connector_id"]))
    if kind == "user":
        group = Group(spec.get("group_id", 100))
        return UserFilter(User(spec["user_id"], group, "filter-user", "filter-password"))
    if kind == "group":
        return GroupFilter(Group(spec["group_id"]))
    if kind == "source_addr":
        return SourceAddrFilter(spec["pattern"])
    if kind == "destination_addr":
        return DestinationAddrFilter(spec["pattern"])
    if kind == "short_message":
        return ShortMessageFilter(spec["pattern"])
    if kind == "date_interval":
        return DateIntervalFilter([date.fromisoformat(spec["start"]), date.fromisoformat(spec["end"])])
    if kind == "time_interval":
        return TimeIntervalFilter([time.fromisoformat(spec["start"]), time.fromisoformat(spec["end"])])
    if kind == "tag":
        return TagFilter(spec["tag"])
    raise AssertionError(f"unknown filter type: {kind}")


def capture_case(case_id: str, filter_spec: dict, route_spec: dict | None = None) -> dict:
    route_spec = routable_spec() if route_spec is None else route_spec
    try:
        matched = bool(build_filter(filter_spec).match(build_routable(route_spec)))
        error_type = None
    except Exception as error:  # Exact legacy failure class is part of the fixture.
        matched = None
        error_type = type(error).__name__
    captured_route = dict(route_spec)
    captured_route["tags"] = [str(tag) for tag in route_spec["tags"]]
    return {
        "id": case_id,
        "source": "jasmin/routing/Filters.py",
        "filter": filter_spec,
        "routable": captured_route,
        "expected": {"matched": matched, "error_type": error_type},
    }


def capture(output: Path) -> None:
    cases = [
        capture_case("transparent_mt", {"type": "transparent"}),
        capture_case("transparent_mo", {"type": "transparent"}, routable_spec(direction="mo")),
        capture_case("connector_match", {"type": "connector", "connector_id": "abc"}),
        capture_case("connector_miss", {"type": "connector", "connector_id": "other"}),
        capture_case("user_match", {"type": "user", "user_id": 1}),
        capture_case("user_miss", {"type": "user", "user_id": 2}),
        capture_case("group_match", {"type": "group", "group_id": 100}),
        capture_case("group_miss", {"type": "group", "group_id": 200}),
        capture_case("source_match", {"type": "source_addr", "pattern": r"^20\d+0$"}),
        capture_case("source_miss", {"type": "source_addr", "pattern": r"^30\d+"}),
        capture_case("source_utf8_replacement", {"type": "source_addr", "pattern": "^A�B$"}, routable_spec(source_addr=encoded(b"A\xffB"))),
        capture_case("source_missing_key", {"type": "source_addr", "pattern": ".*"}, routable_spec(source_addr=None)),
        capture_case("destination_match", {"type": "destination_addr", "pattern": r"^20\d+"}),
        capture_case("destination_miss", {"type": "destination_addr", "pattern": r"^30\d+"}),
        capture_case("destination_utf8_replacement", {"type": "destination_addr", "pattern": "^A�B$"}, routable_spec(destination_addr=encoded(b"A\xffB"))),
        capture_case("destination_missing_key", {"type": "destination_addr", "pattern": ".*"}, routable_spec(destination_addr=None)),
        capture_case("short_message_match", {"type": "short_message", "pattern": r"^hello.*$"}),
        capture_case("short_message_precedes_payload", {"type": "short_message", "pattern": "^payload$"}, routable_spec(short_message=encoded(b"short"), message_payload=encoded(b"payload"))),
        capture_case("message_payload_fallback", {"type": "short_message", "pattern": "^payload$"}, routable_spec(short_message=None, message_payload=encoded(b"payload"))),
        capture_case("message_content_missing", {"type": "short_message", "pattern": ".*"}, routable_spec(short_message=None, message_payload=None)),
        capture_case("short_message_utf8_replacement", {"type": "short_message", "pattern": "^A�B$"}, routable_spec(short_message=encoded(b"A\xffB"))),
        capture_case("date_before", {"type": "date_interval", "start": "2024-01-10", "end": "2024-01-20"}, routable_spec(timestamp="2024-01-09T06:00:00")),
        capture_case("date_start_inclusive", {"type": "date_interval", "start": "2024-01-10", "end": "2024-01-20"}, routable_spec(timestamp="2024-01-10T23:59:59")),
        capture_case("date_inside", {"type": "date_interval", "start": "2024-01-10", "end": "2024-01-20"}),
        capture_case("date_end_inclusive", {"type": "date_interval", "start": "2024-01-10", "end": "2024-01-20"}, routable_spec(timestamp="2024-01-20T00:00:00")),
        capture_case("date_after", {"type": "date_interval", "start": "2024-01-10", "end": "2024-01-20"}, routable_spec(timestamp="2024-01-21T00:00:00")),
        capture_case("date_reversed_matches_nothing", {"type": "date_interval", "start": "2024-01-20", "end": "2024-01-10"}),
        capture_case("time_before", {"type": "time_interval", "start": "03:00:00", "end": "09:00:00"}, routable_spec(timestamp="2024-01-15T02:59:59")),
        capture_case("time_start_inclusive", {"type": "time_interval", "start": "03:00:00", "end": "09:00:00"}, routable_spec(timestamp="2024-01-15T03:00:00")),
        capture_case("time_inside", {"type": "time_interval", "start": "03:00:00", "end": "09:00:00"}),
        capture_case("time_end_inclusive", {"type": "time_interval", "start": "03:00:00", "end": "09:00:00"}, routable_spec(timestamp="2024-01-15T09:00:00")),
        capture_case("time_after", {"type": "time_interval", "start": "03:00:00", "end": "09:00:00"}, routable_spec(timestamp="2024-01-15T09:00:01")),
        capture_case("time_reversed_no_midnight_wrap", {"type": "time_interval", "start": "23:00:00", "end": "02:00:00"}, routable_spec(timestamp="2024-01-15T00:30:00")),
        capture_case("tag_integer_normalized_match", {"type": "tag", "tag": 300}, routable_spec(tags=[300, 300])),
        capture_case("tag_string_match", {"type": "tag", "tag": "blue"}, routable_spec(tags=["blue"])),
        capture_case("tag_miss", {"type": "tag", "tag": "missing"}, routable_spec(tags=["blue", "300"])),
    ]
    compatibility = {
        kind: list(filter_class.usedFor)
        for kind, filter_class in FILTER_CLASSES.items()
    }
    document = {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": "jasmin/routing/Filters.py",
        "compatibility": compatibility,
        "cases": cases,
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"routing_filter_golden={output} cases={len(cases)} status=ok")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    capture(args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

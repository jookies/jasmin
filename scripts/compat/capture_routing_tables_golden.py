#!/usr/bin/env python3
"""Capture deterministic static routing-table behavior from the frozen oracle."""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path

from jasmin.routing.Filters import DestinationAddrFilter, SourceAddrFilter, TransparentFilter, UserFilter
from jasmin.routing.Routables import RoutableDeliverSm, RoutableSubmitSm
from jasmin.routing.Routes import DefaultRoute, StaticMORoute, StaticMTRoute
from jasmin.routing.RoutingTables import MORoutingTable, MTRoutingTable
from jasmin.routing.jasminApi import Group, HttpConnector, SmppClientConnector, SmppServerSystemIdConnector, User
from smpp.pdu.operations import DeliverSM, SubmitSM

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat/fixtures/routing-tables/baseline.json"


def connector(spec):
    if spec["type"] == "smppc": return SmppClientConnector(spec["id"])
    if spec["type"] == "http": return HttpConnector(spec["id"], "http://127.0.0.1")
    if spec["type"] == "smpps": return SmppServerSystemIdConnector(spec["id"])
    raise AssertionError(spec)


def build_filter(spec):
    if spec["type"] == "transparent": return TransparentFilter()
    if spec["type"] == "user":
        return UserFilter(User(spec["user_id"], Group(100), "fixture", "password"))
    if spec["type"] == "source": return SourceAddrFilter(spec["pattern"])
    if spec["type"] == "destination": return DestinationAddrFilter(spec["pattern"])
    raise AssertionError(spec)


def build_route(spec):
    target = connector(spec["connector"])
    if spec["kind"] == "default": return DefaultRoute(target, float(spec.get("rate", 0.0)))
    filters = [build_filter(item) for item in spec["filters"]]
    if spec["direction"] == "mt": return StaticMTRoute(filters, target, float(spec.get("rate", 0.0)))
    return StaticMORoute(filters, target, float(spec.get("rate", 0.0)))


def build_routable(spec):
    if spec["direction"] == "mt":
        pdu = SubmitSM(source_addr=spec["source"].encode(), destination_addr=spec["destination"].encode(), short_message=b"x")
        return RoutableSubmitSm(pdu, User(spec["user_id"], Group(100), "fixture", "password"))
    pdu = DeliverSM(source_addr=spec["source"].encode(), destination_addr=spec["destination"].encode(), short_message=b"x")
    return RoutableDeliverSm(pdu, HttpConnector("src", "http://127.0.0.1"))


def route(kind, direction, cid, ctype, filters=(), rate=0.0):
    return {"kind": kind, "direction": direction, "connector": {"id": cid, "type": ctype}, "filters": list(filters), "rate": rate}


def add(order, spec): return {"op": "add", "order": order, "route": spec}
def remove(order): return {"op": "remove", "order": order}
def flush(): return {"op": "flush"}

def mt_route(cid, filters, rate=0.0): return route("static", "mt", cid, "smppc", filters, rate)
def mo_route(cid, filters): return route("static", "mo", cid, "http", filters)
def mt_default(cid="default", rate=0.0): return route("default", "mt", cid, "smppc", (), rate)
def mo_default(cid="default"): return route("default", "mo", cid, "smpps", ())

def query(direction, source="x", destination="x", user_id=2):
    return {"direction": direction, "source": source, "destination": destination, "user_id": user_id}


def capture_case(case_id, direction, operations, query_spec=None):
    table = MTRoutingTable() if direction == "mt" else MORoutingTable()
    remove_results = []
    error_type = None
    try:
        for operation in operations:
            if operation["op"] == "add": table.add(build_route(operation["route"]), operation["order"])
            elif operation["op"] == "remove": remove_results.append(table.remove(operation["order"]))
            else: table.flush()
    except Exception as error:
        error_type = type(error).__name__
    orders = [list(item)[0] for item in table.getAll()]
    selected = None
    if error_type is None and query_spec is not None:
        selected_route = table.getRouteFor(build_routable(query_spec))
        if selected_route is not None:
            selected = {"connector_id": selected_route.getConnector().cid, "rate": selected_route.getRate()}
    return {"id": case_id, "source": "jasmin/routing/RoutingTables.py", "direction": direction,
            "operations": operations, "query": query_spec,
            "expected": {"orders": orders, "remove_results": remove_results, "selected": selected, "error_type": error_type}}


def capture(output):
    t = {"type": "transparent"}; u1 = {"type": "user", "user_id": 1}
    d10 = {"type": "destination", "pattern": r"^10\d+"}; s10 = {"type": "source", "pattern": r"^10\d+"}; d90 = {"type": "destination", "pattern": r"^90\d+"}
    cases = [
      capture_case("mt_descending_order", "mt", [add(0,mt_default()),add(2,mt_route("d",[d10])),add(1,mt_route("u",[u1])),add(3,mt_route("t",[t]))]),
      capture_case("mo_descending_order", "mo", [add(0,mo_default()),add(2,mo_route("dst",[d90])),add(1,mo_route("src",[s10])),add(3,mo_route("top",[t]))]),
      capture_case("replace_same_order", "mt", [add(2,mt_route("old",[t])),add(2,mt_route("new",[t]))], query("mt")),
      capture_case("remove_existing", "mt", [add(2,mt_route("x",[t])),remove(2)]),
      capture_case("remove_missing", "mt", [remove(9)]),
      capture_case("flush_table", "mt", [add(1,mt_route("x",[t])),flush()]),
      capture_case("mt_first_match_user", "mt", [add(0,mt_default()),add(2,mt_route("dest",[d10])),add(1,mt_route("user",[u1]))], query("mt",destination="200",user_id=1)),
      capture_case("mt_first_match_destination", "mt", [add(0,mt_default()),add(2,mt_route("dest",[d10])),add(1,mt_route("user",[u1]))], query("mt",destination="100",user_id=2)),
      capture_case("mt_default_fallback", "mt", [add(0,mt_default("fallback")),add(1,mt_route("user",[u1]))], query("mt",user_id=2)),
      capture_case("mt_no_match", "mt", [add(1,mt_route("user",[u1]))], query("mt",user_id=2)),
      capture_case("mo_first_match_source", "mo", [add(0,mo_default()),add(2,mo_route("dest",[d90])),add(1,mo_route("source",[s10]))], query("mo",source="100",destination="200")),
      capture_case("mo_first_match_destination", "mo", [add(0,mo_default()),add(2,mo_route("dest",[d90])),add(1,mo_route("source",[s10]))], query("mo",source="x",destination="900")),
      capture_case("mo_default_fallback", "mo", [add(0,mo_default("fallback")),add(1,mo_route("source",[s10]))], query("mo")),
      capture_case("mo_no_match", "mo", [add(1,mo_route("source",[s10]))], query("mo")),
      capture_case("and_filters_match", "mt", [add(1,mt_route("and",[u1,d10]))], query("mt",destination="100",user_id=1)),
      capture_case("and_filters_short_circuit_miss", "mt", [add(1,mt_route("and",[u1,d10]))], query("mt",destination="200",user_id=1)),
      capture_case("negative_order_rejected", "mt", [add(-1,mt_route("x",[t]))]),
      capture_case("nondefault_at_zero_rejected", "mt", [add(0,mt_route("x",[t]))]),
      capture_case("default_at_nonzero_rejected", "mt", [add(1,mt_default())]),
      capture_case("mt_wrong_connector_rejected", "mt", [add(1,route("static","mt","xxx","http",[t]))]),
      capture_case("mo_wrong_connector_rejected", "mo", [add(1,route("static","mo","xxx","smppc",[t]))]),
      capture_case("mt_visible_rate", "mt", [add(1,mt_route("rated",[t],2.3))], query("mt")),
      capture_case("mo_rate_ignored", "mo", [add(1,route("static","mo","free","http",[t],2.3))], query("mo")),
    ]
    doc={"schema_version":1,"baseline_commit":BASELINE,"source":"jasmin/routing/RoutingTables.py","cases":cases}
    output.parent.mkdir(parents=True,exist_ok=True);output.write_text(json.dumps(doc,indent=2,sort_keys=True)+"\n")
    print(f"routing_table_golden={output} cases={len(cases)} status=ok")


def main():
    p=argparse.ArgumentParser();p.add_argument("--output",type=Path,default=DEFAULT_OUTPUT);a=p.parse_args();capture(a.output);return 0
if __name__=="__main__": raise SystemExit(main())

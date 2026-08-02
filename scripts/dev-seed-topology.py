#!/usr/bin/env python3
"""Seed a deliberately heavy, deliberately broken deployment for testing the
topology map.

A dev stack has four connectors and three routes, which tells you nothing about
how the map behaves on a real deployment or whether the problem rail is worth
reading. This provisions ~150 entities through the same admin API an operator
uses — no direct database writes — including faults planted on purpose:

  * connectors pointed at black-holed addresses, which start and never bind
  * routes into connectors that do not exist at all
  * a connector pool whose primary is dead and whose backup is healthy
  * routes into termination connectors, to prove "term" routing resolves

Everything it creates is prefixed (see PREFIX) so `--clean` can remove exactly
what it added and nothing else.

Usage:
  scripts/dev-seed-topology.py            # seed against the local dev stack
  scripts/dev-seed-topology.py --clean    # remove everything it created
  scripts/dev-seed-topology.py --scale 3  # three times the entity count

It targets the dev admin listener from scripts/dev.sh. Never point it at a real
deployment: it creates users with a known password.
"""

from __future__ import annotations

import argparse
import json
import sys
import urllib.error
import urllib.request

BASE = "http://127.0.0.1:8404"
USERNAME = "admin"
PASSWORD = "Welcome1!"

# Everything this script creates carries this prefix, which is what makes
# --clean safe to run on a stack that also holds hand-made entities.
PREFIX = "load"

# Route orders are allocated from here upward so the seeded routes never
# collide with the dev config's own (0, 10, 11).
ROUTE_BASE = 1000
MO_ROUTE_BASE = 500


class Client:
    """Minimal session client: cookie + CSRF, like the browser does it."""

    def __init__(self, base: str) -> None:
        self.base = base
        self.cookie = ""
        self.csrf = ""

    def login(self) -> None:
        body = json.dumps({"username": USERNAME, "password": PASSWORD}).encode()
        request = urllib.request.Request(
            f"{self.base}/api/login", data=body,
            headers={"Content-Type": "application/json"}, method="POST",
        )
        with urllib.request.urlopen(request, timeout=10) as response:
            payload = json.load(response)
            self.cookie = response.headers.get("Set-Cookie", "").split(";")[0]
        self.csrf = payload["csrf_token"]

    def call(self, method: str, path: str, body: dict | None = None) -> tuple[int, str]:
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(
            f"{self.base}{path}", data=data, method=method,
            headers={
                "Content-Type": "application/json",
                "Cookie": self.cookie,
                "X-CSRF-Token": self.csrf,
            },
        )
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                return response.status, response.read().decode()
        except urllib.error.HTTPError as error:
            return error.code, error.read().decode()

    def get_json(self, path: str):
        status, text = self.call("GET", path)
        if status != 200:
            raise SystemExit(f"GET {path} failed: {status} {text}")
        return json.loads(text)


class Tally:
    def __init__(self) -> None:
        self.created = 0
        self.failed: list[str] = []

    def record(self, what: str, status: int, text: str) -> None:
        if status in (200, 201):
            self.created += 1
        else:
            self.failed.append(f"{what}: {status} {text.strip()[:120]}")

    def report(self, verb: str) -> None:
        print(f"\n{verb} {self.created} entities")
        if self.failed:
            print(f"{len(self.failed)} call(s) failed:")
            for line in self.failed[:15]:
                print(f"  {line}")
            if len(self.failed) > 15:
                print(f"  … and {len(self.failed) - 15} more")


# --------------------------------------------------------------------- seed --

def seed(client: Client, scale: int) -> None:
    tally = Tally()

    groups = [f"{PREFIX}-grp-{index:02d}" for index in range(1, 4 * scale + 1)]
    for gid in groups:
        tally.record(f"group {gid}", *client.call("POST", "/api/groups", {"gid": gid}))

    # Users, spread across the groups. A few are disabled so the map has idle
    # accounts to render alongside live ones.
    for index in range(1, 10 * scale + 1):
        username = f"{PREFIX}-partner-{index:02d}"
        tally.record(username, *client.call("POST", "/api/users", {
            "username": username,
            "password": "SeedPassw0rd!",
            "group_id": groups[index % len(groups)],
            "disabled": index % 9 == 0,
            "balance": 100.0 + index,
            "submit_sm_count": 10_000,
        }))

    # SMPP bind accounts.
    for index in range(1, 5 * scale + 1):
        system_id = f"{PREFIX}-esme-{index:02d}"
        tally.record(system_id, *client.call("POST", "/api/smpps-users", {
            "system_id": system_id,
            "password": "bindpw01",
            "max_bindings": 2,
        }))

    # Saved filters, referenced by the routes below.
    for index in range(1, 3 * scale + 1):
        fid = f"{PREFIX}f{index:02d}"
        tally.record(fid, *client.call("POST", "/api/filters", {
            "fid": fid,
            "type": "DestinationAddrFilter",
            "args": {"destination_addr": f"^{30 + index}"},
        }))

    # HTTP destinations for the MO routes.
    destinations = []
    for index in range(1, 3 * scale + 1):
        cid = f"{PREFIX}-mo-dest-{index:02d}"
        destinations.append(cid)
        tally.record(cid, *client.call("POST", "/api/http-connectors", {
            "cid": cid,
            "baseurl": f"https://partner{index:02d}.example.test/mo",
            "method": "POST",
        }))

    # Connectors. The fake SMSC in the dev stack accepts binds, so connectors
    # pointed at it go BOUND; the ones pointed into 10.255.255.0/24 are
    # black-holed and stay CONNECTING forever, which is the fault the map is
    # meant to surface.
    healthy, dead = [], []
    for index in range(1, 6 * scale + 1):
        cid = f"{PREFIX}-carrier-{index:02d}"
        healthy.append(cid)
        tally.record(cid, *client.call("POST", "/api/connectors", {
            "cid": cid, "host": "smppsim", "port": 2775,
            "system_id": "smppclient1", "password": "password",
            "bind": "transceiver", "desired_started": True,
        }))
    for index in range(1, 2 * scale + 1):
        cid = f"{PREFIX}-dead-{index:02d}"
        dead.append(cid)
        tally.record(cid, *client.call("POST", "/api/connectors", {
            "cid": cid, "host": f"10.255.255.{index}", "port": 2775,
            "system_id": "nobody", "password": "nopass",
            "bind": "transceiver", "desired_started": True,
        }))

    # Termination connectors already present in the dev config are reused by
    # the "term" routes below; the map must resolve those against the
    # termination list, not the SMPP one.
    termination = [
        entry["cid"] for entry in client.get_json("/api/termination-connectors")
    ] if client.call("GET", "/api/termination-connectors")[0] == 200 else []

    order = ROUTE_BASE
    def next_order() -> int:
        nonlocal order
        order += 1
        return order

    # Healthy routes, one per connector, with a filter chain.
    for index, cid in enumerate(healthy):
        tally.record(f"route->{cid}", *client.call("POST", "/api/routes", {
            "order": next_order(), "rate": 0.01 + index / 1000, "connector_id": cid,
            "filters": [{"type": "destination_addr", "pattern": f"^{40 + index}"}],
        }))

    # Routes into connectors that will never bind — broken paths.
    #
    # Every seeded route carries a destination filter. Without one it is a
    # catch-all, and because these orders are higher than the dev config's own
    # routes it would swallow *all* traffic — the first version of this script
    # sent every smoke message to a connector that does not exist, which made
    # the whole gateway look dead and every counter on the map read zero.
    for index, cid in enumerate(dead):
        tally.record(f"route->{cid}", *client.call("POST", "/api/routes", {
            "order": next_order(), "rate": 0.05, "connector_id": cid,
            "filters": [{"type": "destination_addr", "pattern": f"^7{index:02d}"}],
        }))

    # Routes into connectors that do not exist at all — a different fault, and
    # one no table view can show.
    for index in range(1, scale + 1):
        tally.record("route->ghost", *client.call("POST", "/api/routes", {
            "order": next_order(), "rate": 0.09,
            "connector_id": f"{PREFIX}-ghost-{index:02d}",
            "filters": [{"type": "destination_addr", "pattern": f"^8{index:02d}"}],
        }))

    # A failover pool whose primary is dead and whose backup is healthy.
    if dead and healthy:
        tally.record("route->pool", *client.call("POST", "/api/routes", {
            "order": next_order(), "rate": 0.03,
            "connector_ids": [dead[0], healthy[0]],
            "filters": [{"type": "destination_addr", "pattern": "^9100"}],
        }))

    # Termination routes, carrying the engine's own "term" connector type.
    for index, cid in enumerate(termination):
        tally.record(f"route->term:{cid}", *client.call("POST", "/api/routes", {
            "order": next_order(), "rate": 0.02,
            "connector_id": cid, "connector_type": "term",
            "filters": [{"type": "destination_addr", "pattern": f"^96{index:02d}"}],
        }))

    # MO routes back out to the HTTP destinations.
    mo_order = MO_ROUTE_BASE
    for index, cid in enumerate(destinations):
        mo_order += 1
        source = healthy[index % len(healthy)] if healthy else ""
        tally.record(f"mo-route {mo_order}", *client.call("POST", "/api/mo-routes", {
            "order": mo_order,
            "filter_connector_id": source,
            "connector": {
                "type": "http", "cid": cid,
                "url": f"https://partner{index + 1:02d}.example.test/mo",
                "method": "POST",
            },
        }))

    # Pull credentials, scoped to the termination connectors. These are the
    # customer's side of the delivery path, and the map grades them by silence:
    # a token issued and never used, or one that stopped polling, is the fault
    # with no connection to observe.
    for index in range(1, 3 * scale + 1):
        label = f"{PREFIX}-puller-{index:02d}"
        tally.record(label, *client.call("POST", "/api/message-consumers", {
            "id": label,
            "label": label,
            "scope": {
                "connectors": termination or [],
                "include_text": index % 2 == 0,
            },
        }))

    tally.report("Created")


# -------------------------------------------------------------------- clean --

def clean(client: Client) -> None:
    tally = Tally()

    # Routes first: a connector cannot be deleted while a route names it.
    for route in client.get_json("/api/routes"):
        if route.get("managed_by") != "admin":
            continue
        candidates = route.get("connector_ids") or [route.get("connector_id", "")]
        # Order is the reliable marker, not the connector id: the seeded
        # termination routes point at connectors this script did not create
        # (terminate-local, test-static) and a prefix test leaves them behind
        # as high-order catch-alls that swallow every later send.
        seeded = route["id"] > ROUTE_BASE or any(
            str(cid).startswith(PREFIX) for cid in candidates
        )
        if seeded:
            tally.record(f"route {route['id']}",
                         *client.call("DELETE", f"/api/routes/{route['id']}"))

    for route in client.get_json("/api/mo-routes"):
        if route.get("managed_by") != "admin":
            continue
        if route["id"] > MO_ROUTE_BASE:
            tally.record(f"mo-route {route['id']}",
                         *client.call("DELETE", f"/api/mo-routes/{route['id']}"))

    for consumer in client.get_json("/api/message-consumers"):
        if str(consumer.get("label", "")).startswith(PREFIX):
            tally.record(f"consumer {consumer['id']}",
                         *client.call("DELETE", f"/api/message-consumers/{consumer['id']}"))

    for path, key in (
        ("/api/connectors", "id"),
        ("/api/http-connectors", "cid"),
        ("/api/filters", "fid"),
        ("/api/smpps-users", "id"),
        ("/api/users", "username"),
        ("/api/groups", "gid"),
    ):
        for entry in client.get_json(path):
            if entry.get("managed_by") == "config":
                continue
            identity = str(entry.get(key, ""))
            if identity.startswith(PREFIX):
                tally.record(f"{path} {identity}",
                             *client.call("DELETE", f"{path}/{identity}"))

    tally.report("Removed")


# ------------------------------------------------------------------ summary --

def summarize(client: Client) -> None:
    graph = client.get_json("/api/topology")
    top = [node for node in graph["nodes"] if not node.get("parent")]
    degraded = [edge for edge in graph["edges"] if edge.get("degraded")]

    print(f"\ntopology: {len(graph['nodes'])} nodes "
          f"({len(top)} cards, {len(graph['nodes']) - len(top)} children), "
          f"{len(graph['edges'])} edges")
    print("problems: " + ", ".join(
        f"{p['label']} {p['count']}" for p in graph["problems"]))
    if degraded:
        print("degraded edges:")
        for edge in degraded[:10]:
            print(f"  {edge['from']} -> {edge['to']}: {edge.get('reason')}")
        if len(degraded) > 10:
            print(f"  … and {len(degraded) - 10} more")
    print("\ncards per column:")
    columns: dict[str, int] = {}
    for node in top:
        columns[node["column"]] = columns.get(node["column"], 0) + 1
    for column in ("ingress", "policy", "core", "queues", "egress", "carriers", "infra"):
        if column in columns:
            print(f"  {column:9} {columns[column]}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--clean", action="store_true", help="remove seeded entities")
    parser.add_argument("--scale", type=int, default=2, help="entity multiplier (default 2)")
    parser.add_argument("--base", default=BASE, help=f"admin base URL (default {BASE})")
    args = parser.parse_args()

    client = Client(args.base)
    try:
        client.login()
    except Exception as error:  # noqa: BLE001 - a dev script, any failure is fatal
        print(f"cannot reach the admin API at {args.base}: {error}", file=sys.stderr)
        print("is the dev stack up? scripts/dev.sh up", file=sys.stderr)
        return 1

    if args.clean:
        clean(client)
    else:
        seed(client, max(1, args.scale))
    summarize(client)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

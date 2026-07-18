#!/usr/bin/env python3
"""Capture RouterPB.addAmqpBroker subscription declarations at the real callback boundary."""

from __future__ import annotations

import argparse
import hashlib
import json
import logging
from pathlib import Path

from twisted.internet import defer

import jasmin.queues.factory as queue_factory_module
import jasmin.routing.router as router_module

RouterPB = router_module.RouterPB

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat/fixtures/router-amqp-subscriptions/baseline.json"
SOURCE = [
    "jasmin/routing/router.py:75-109",
    "jasmin/queues/factory.py:200-219",
]
SOURCE_SHA256 = {
    "jasmin/routing/router.py": "8e77729e98b18cd935201af74847875a7a8fa03c819c05e40273dba539ada214",
    "jasmin/queues/factory.py": "fd1b60965f9cff6c97c63275d45c56bfe6979006c4144cb58cae76f8a42dd42e",
}


def verify_imported_source() -> None:
    imported = {
        "jasmin/routing/router.py": Path(router_module.__file__).resolve(),
        "jasmin/queues/factory.py": Path(queue_factory_module.__file__).resolve(),
    }
    for source, path in imported.items():
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        if digest != SOURCE_SHA256[source]:
            raise AssertionError(f"frozen source mismatch for {source}: {digest}")


class _Queue:
    def get(self):
        return defer.Deferred()


class _Client:
    def __init__(self, queues):
        self.queues = queues

    def queue(self, consumer_tag):
        self.queues.append(consumer_tag)
        return defer.succeed(_Queue())


class _Channel:
    def __init__(self, operations):
        self.operations = operations

    def exchange_declare(self, exchange, type, passive=False, durable=False,
                         auto_delete=False, internal=False, nowait=False,
                         arguments=None):
        self.operations.append({
            "operation": "exchange_declare",
            "exchange": exchange,
            "type": type,
            "passive": passive,
            "durable": durable,
            "auto_delete": auto_delete,
            "internal": internal,
            "no_wait": nowait,
            "arguments": arguments,
        })
        return defer.succeed(None)

    def queue_bind(self, queue, exchange, routing_key, nowait=False, arguments=None):
        self.operations.append({
            "operation": "queue_bind",
            "queue": queue,
            "exchange": exchange,
            "routing_key": routing_key,
            "no_wait": nowait,
            "arguments": arguments,
        })
        return defer.succeed(None)

    def basic_consume(self, queue, consumer_tag, no_local=False, no_ack=False,
                      exclusive=False, nowait=False, arguments=None):
        self.operations.append({
            "operation": "basic_consume",
            "queue": queue,
            "consumer_tag": consumer_tag,
            "auto_ack": no_ack,
            "exclusive": exclusive,
            "no_local": no_local,
            "no_wait": nowait,
            "arguments": arguments,
        })
        return defer.succeed(None)


class _Broker:
    connected = True

    def __init__(self):
        self.operations = []
        self.queue_lookups = []
        self.chan = _Channel(self.operations)
        self.client = _Client(self.queue_lookups)

    def named_queue_declare(self, queue, durable=False, exclusive=False,
                            auto_delete=False, nowait=False, arguments=None):
        self.operations.append({
            "operation": "queue_declare",
            "queue": queue,
            "durable": durable,
            "exclusive": exclusive,
            "auto_delete": auto_delete,
            "no_wait": nowait,
            "arguments": arguments,
        })
        return defer.succeed(None)


def capture(output: Path) -> None:
    verify_imported_source()
    router = RouterPB.__new__(RouterPB)
    router.log = logging.getLogger("router-amqp-subscriptions-fixture")
    broker = _Broker()
    completed = router.addAmqpBroker(broker)
    if not completed.called:
        raise AssertionError("RouterPB.addAmqpBroker did not complete synchronously")
    result = []
    completed.addCallbacks(lambda value: result.append(("ok", value)),
                           lambda failure: result.append(("error", str(failure.value))))
    if not result or result[0][0] != "ok":
        raise AssertionError(f"RouterPB.addAmqpBroker failed: {result}")
    expected_lookups = ["RouterPB-delivers", "RouterPB-billrequests"]
    if broker.queue_lookups != expected_lookups:
        raise AssertionError(f"unexpected queue lookups: {broker.queue_lookups}")
    case = {
        "id": "router_pb_add_amqp_broker",
        "input": {"connected": True},
        "expected": {
            "operations": broker.operations,
            "queue_lookups": broker.queue_lookups,
        },
    }
    cases = [case]
    cases_sha256 = hashlib.sha256(
        json.dumps(cases, sort_keys=True, separators=(",", ":")).encode("utf-8")
    ).hexdigest()
    document = {
        "schema_version": 1,
        "baseline_commit": BASELINE,
        "source": SOURCE,
        "cases_sha256": cases_sha256,
        "cases": cases,
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(f"router_amqp_subscriptions_golden={output} cases=1 operations={len(broker.operations)} sha256={cases_sha256} status=ok")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    capture(args.output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

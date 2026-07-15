#!/usr/bin/env python3
"""Capture deterministic legacy AMQP Content contracts."""

from __future__ import annotations

import argparse
import base64
import datetime
import hashlib
import json
import os
import pickle
from pathlib import Path

from smpp.pdu.operations import DeliverSM, SubmitSM, SubmitSMResp
from smpp.pdu.pdu_types import AddrNpi, AddrTon, CommandId, CommandStatus

from twisted.internet import defer, task

from jasmin.managers import content as manager_content
from jasmin.managers.content import (
    DLR,
    DLRContentForHttpapi,
    DLRContentForSmpps,
    SubmitSmContent,
    SubmitSmRespBillContent,
    SubmitSmRespContent,
)
from jasmin.queues.configs import AmqpConfig
from jasmin.queues.factory import AmqpFactory
from jasmin.routing.content import RoutedDeliverSmContent
from jasmin.routing.jasminApi import HttpConnector

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
LEGACY_PICKLE_PROTOCOL = 2
ROOT = Path(__file__).resolve().parents[2]
DEFAULT_OUTPUT = ROOT / "compat" / "fixtures" / "amqp" / "baseline.json"


class FixedDateTime(datetime.datetime):
    @classmethod
    def now(cls, tz=None):
        value = cls(2026, 1, 2, 3, 4, 5, 678901)
        return value if tz is None else value.replace(tzinfo=tz)


def normalize(value):
    if isinstance(value, bytes):
        return {"type": "bytes", "base64": base64.b64encode(value).decode(), "hex": value.hex()}
    if value is None or isinstance(value, (bool, int, float, str)):
        return value
    if isinstance(value, dict):
        return {str(k): normalize(v) for k, v in sorted(value.items(), key=lambda item: str(item[0]))}
    if isinstance(value, (list, tuple)):
        return [normalize(v) for v in value]
    return {"type": value.__class__.__name__, "value": str(value)}


def body_contract(body):
    if isinstance(body, bytes):
        payload = body
        wire_kind = "bytes"
        text = None
        pickle_protocol = payload[1] if len(payload) >= 2 and payload[0] == 0x80 else None
    elif isinstance(body, str):
        payload = body.encode("utf-8")
        wire_kind = "text"
        text = body
        pickle_protocol = None
    else:
        raise TypeError(f"unexpected AMQP Content body type: {type(body)!r}")

    result = {
        "python_type": f"{body.__class__.__module__}.{body.__class__.__qualname__}",
        "wire_kind": wire_kind,
        "wire_base64": base64.b64encode(payload).decode("ascii"),
        "wire_sha256": hashlib.sha256(payload).hexdigest(),
        "pickle_protocol": pickle_protocol,
    }
    if text is not None:
        result["text"] = text
    return result


def source_case(case_id, routing_key, content):
    return {"id": case_id, "routing_key": routing_key, "content": content}


def captured_fixture(source, content):
    return {
        "id": source["id"],
        "routing_key": source["routing_key"],
        "properties": normalize(content.properties),
        "body": body_contract(content.body),
    }


@defer.inlineCallbacks
def capture(output: Path):
    manager_content.datetime.datetime = FixedDateTime
    manager_content.randomUniqueId = lambda *_args: "00000000-0000-4000-8000-000000000001"

    submit = SubmitSM(
        seqNum=7,
        source_addr="1111",
        destination_addr="2222",
        short_message=b"hello",
    )
    submit_resp = SubmitSMResp(
        seqNum=7,
        status=CommandStatus.ESME_ROK,
        message_id="0000436949",
    )
    deliver = DeliverSM(
        seqNum=9,
        source_addr="15551230000",
        destination_addr="4040",
        short_message=b"MO hello",
    )

    cases = [
        source_case(
            "submit_sm_httpapi",
            "submit.sm.connector-a",
            SubmitSmContent(
                uid="user-1",
                body=pickle.dumps(submit, protocol=LEGACY_PICKLE_PROTOCOL),
                replyto="submit.sm.resp.user-1",
                priority=2,
                expiration="2026-01-02 03:09:05",
                msgid="11111111-1111-4111-8111-111111111111",
                source_connector="httpapi",
                destination_cid="connector-a",
            ),
        ),
        source_case(
            "submit_sm_resp",
            "submit.sm.resp.user-1",
            SubmitSmRespContent(
                submit_resp,
                "11111111-1111-4111-8111-111111111111",
                pickleProtocol=LEGACY_PICKLE_PROTOCOL,
            ),
        ),
        source_case(
            "dlr_lookup_submit_sm_resp",
            "dlr.submit_sm_resp",
            DLR(
                CommandId.submit_sm_resp,
                "11111111-1111-4111-8111-111111111111",
                CommandStatus.ESME_ROK,
                smpp_msgid=b"0000436949",
            ),
        ),
        source_case(
            "dlr_http_thrower",
            "dlr_thrower.http",
            DLRContentForHttpapi(
                "DELIVRD",
                "11111111-1111-4111-8111-111111111111",
                "https://example.invalid/dlr",
                3,
                dlr_connector="connector-a",
                id_smsc="0000436949",
                sub="001",
                dlvrd="001",
                subdate="2601020304",
                donedate="2601020305",
                err="000",
                text="hello",
                method="POST",
            ),
        ),
        source_case(
            "dlr_smpps_thrower",
            "dlr_thrower.smpps",
            DLRContentForSmpps(
                "DELIVRD",
                "11111111-1111-4111-8111-111111111111",
                "client-a",
                "1111",
                "2222",
                FixedDateTime.now(),
                AddrTon.INTERNATIONAL,
                AddrNpi.ISDN,
                AddrTon.INTERNATIONAL,
                AddrNpi.ISDN,
                err=0,
            ),
        ),
        source_case(
            "bill_submit_sm_resp",
            "bill_request.submit_sm_resp.user-1",
            SubmitSmRespBillContent("bill-1", "user-1", 0.75),
        ),
        source_case(
            "routed_deliver_sm_http",
            "deliver_sm_thrower.http",
            RoutedDeliverSmContent(
                deliver,
                "22222222-2222-4222-8222-222222222222",
                "connector-a",
                [HttpConnector("http-a", "https://example.invalid/mo", "POST")],
            ),
        ),
    ]

    config = AmqpConfig()
    config.host = os.environ.get("AMQP_BROKER_HOST", "rabbitmq")
    config.port = int(os.environ.get("AMQP_BROKER_PORT", "5672"))
    config.reconnectOnConnectionFailure = False
    config.reconnectOnConnectionLoss = False
    amqp = AmqpFactory(config)
    captured = []

    yield amqp.connect()
    yield amqp.getChannelReadyDeferred()
    try:
        exchange = "compat.golden"
        yield amqp.chan.exchange_declare(exchange=exchange, type="topic", durable=False)
        client = amqp.client
        if client is None:
            raise RuntimeError("AMQP client was not initialized after channel readiness")
        for index, source in enumerate(cases):
            queue_name = f"compat.golden.{source['id']}"
            consumer_tag = f"compat-golden-{index}"
            yield amqp.named_queue_declare(queue=queue_name)
            yield amqp.chan.queue_bind(
                queue=queue_name,
                exchange=exchange,
                routing_key=source["routing_key"],
            )
            yield amqp.chan.basic_consume(
                queue=queue_name,
                no_ack=False,
                consumer_tag=consumer_tag,
            )
            queue = yield client.queue(consumer_tag)
            yield amqp.publish(
                exchange=exchange,
                routing_key=source["routing_key"],
                content=source["content"],
            )
            message = yield queue.get()
            yield amqp.chan.basic_ack(message.delivery_tag)
            captured.append(captured_fixture(source, message.content))
            yield queue.close()
    finally:
        yield amqp.disconnect()

    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(
        json.dumps(
            {
                "schema_version": 1,
                "baseline_commit": BASELINE,
                "capture_transport": "rabbitmq-publish-consume",
                "cases": captured,
            },
            indent=2,
            sort_keys=True,
        )
        + "\n",
        encoding="utf-8",
    )
    print(f"amqp_golden={output} cases={len(captured)} transport=rabbitmq status=ok")


def main(reactor):
    parser = argparse.ArgumentParser()
    parser.add_argument("--output", type=Path, default=DEFAULT_OUTPUT)
    args = parser.parse_args()
    return capture(args.output)


if __name__ == "__main__":
    task.react(main)

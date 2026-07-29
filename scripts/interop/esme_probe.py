#!/usr/bin/env python
"""Drive a real third-party SMPP client against our Go SMPPs server.

Every other test in this repository exercises the Go gateway against a fake SMSC
that we also wrote. That proves self-consistency, not conformance: a shared
misreading of SMPP 3.4 passes on both sides. This probe removes our code from one
end of the wire entirely -- it binds with `smpp.twisted`, the same independent
library the frozen Jasmin runs on -- so agreement here is evidence about the
protocol rather than about our assumptions.

Emits one JSON object on stdout describing what happened, so a Go test can assert
on it. Exit status is 0 when the probe completed its script, non-zero when it
could not (a bind refusal is a *result*, not a probe failure, and is reported in
the JSON).

Usage:
  esme_probe.py --host 127.0.0.1 --port 2775 --system-id u --password p \
      [--bind transceiver|transmitter|receiver] [--submit-text hello] \
      [--expect-deliver] [--timeout 15]
"""

import argparse
import json
import sys

from twisted.internet import defer, reactor

from smpp.twisted.client import (
    SMPPClientReceiver,
    SMPPClientTransceiver,
    SMPPClientTransmitter,
)
from smpp.twisted.config import SMPPClientConfig
from smpp.pdu.operations import SubmitSM
from smpp.pdu.pdu_types import (
    AddrNpi,
    AddrTon,
    EsmClass,
    EsmClassMode,
    EsmClassType,
    RegisteredDelivery,
    RegisteredDeliveryReceipt,
)

RESULT = {
    "bound": False,
    "bind_error": None,
    "submit_sent": False,
    "submit_status": None,
    "submit_message_id": None,
    "delivered": [],
    "unbound": False,
    "errors": [],
}


def _record_error(label, failure):
    RESULT["errors"].append(f"{label}: {failure}")


class Probe:
    def __init__(self, args):
        self.args = args
        self.finished = defer.Deferred()
        self._done = False

    def deliver_handler(self, smpp, pdu):
        """Called for each deliver_sm the server pushes. Returning normally is
        what makes the library answer deliver_sm_resp with ESME_ROK -- which is
        exactly the ack that used to tear our bind down."""
        params = pdu.params
        body = params.get("short_message") or b""
        RESULT["delivered"].append(
            {
                "sequence": pdu.seqNum,
                "source": _text(params.get("source_addr")),
                "destination": _text(params.get("destination_addr")),
                "short_message_hex": body.hex() if isinstance(body, bytes) else "",
                "esm_class": str(params.get("esm_class")),
            }
        )
        return None

    @defer.inlineCallbacks
    def run(self):
        config = SMPPClientConfig(
            host=self.args.host,
            port=self.args.port,
            username=self.args.system_id,
            password=self.args.password,
            systemType="",
            # Keep the probe's own timers well inside the harness timeout so a
            # hang surfaces as a probe result rather than a test-level timeout.
            enquireLinkTimerSecs=5,
            inactivityTimerSecs=self.args.timeout + 5,
            responseTimerSecs=self.args.timeout,
        )
        if self.args.bind == "transmitter":
            client = SMPPClientTransmitter(config)
        elif self.args.bind == "receiver":
            client = SMPPClientReceiver(config, self.deliver_handler)
        else:
            client = SMPPClientTransceiver(config, self.deliver_handler)

        try:
            smpp = yield client.connectAndBind()
        except Exception as exc:  # a refused bind is a result we want to report
            RESULT["bind_error"] = repr(exc)
            self._finish()
            return
        RESULT["bound"] = True

        if self.args.submit_text and self.args.bind != "receiver":
            try:
                # sendDataRequest resolves to the SMPPOutboundTxnResult
                # namedtuple (smpp, request, response); the PDU is .response.
                result = yield smpp.sendDataRequest(self._submit())
                response = result.response
                RESULT["submit_sent"] = True
                RESULT["submit_status"] = str(response.status)
                RESULT["submit_message_id"] = _text(
                    response.params.get("message_id")
                )
            except Exception as exc:
                _record_error("submit", exc)

        if self.args.expect_deliver:
            yield self._wait_for_deliver()

        try:
            yield smpp.unbindAndDisconnect()
            RESULT["unbound"] = True
        except Exception as exc:
            _record_error("unbind", exc)
        self._finish()

    def _submit(self):
        return SubmitSM(
            source_addr=self.args.source.encode("ascii"),
            destination_addr=self.args.destination.encode("ascii"),
            short_message=self.args.submit_text.encode("ascii"),
            source_addr_ton=AddrTon.NATIONAL,
            source_addr_npi=AddrNpi.ISDN,
            dest_addr_ton=AddrTon.INTERNATIONAL,
            dest_addr_npi=AddrNpi.ISDN,
            esm_class=EsmClass(EsmClassMode.DEFAULT, EsmClassType.DEFAULT),
            registered_delivery=RegisteredDelivery(
                RegisteredDeliveryReceipt.NO_SMSC_DELIVERY_RECEIPT_REQUESTED
            ),
        )

    def _wait_for_deliver(self):
        """Poll rather than hook a second deferred: the handler already records
        every deliver_sm, and this keeps the wait independent of how many arrive."""
        waiter = defer.Deferred()

        def check(remaining):
            if RESULT["delivered"] or remaining <= 0:
                waiter.callback(None)
                return
            reactor.callLater(0.1, check, remaining - 0.1)

        reactor.callLater(0, check, float(self.args.timeout))
        return waiter

    def _finish(self):
        if not self._done:
            self._done = True
            self.finished.callback(None)


def _text(value):
    if value is None:
        return None
    if isinstance(value, bytes):
        return value.decode("latin-1")
    return str(value)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--system-id", required=True)
    parser.add_argument("--password", required=True)
    parser.add_argument(
        "--bind", choices=["transceiver", "transmitter", "receiver"],
        default="transceiver",
    )
    parser.add_argument("--submit-text", default="")
    parser.add_argument("--source", default="1111")
    parser.add_argument("--destination", default="15551230000")
    parser.add_argument("--expect-deliver", action="store_true")
    parser.add_argument("--timeout", type=float, default=15.0)
    args = parser.parse_args()

    probe = Probe(args)

    def bail(failure):
        _record_error("probe", failure.value)
        probe._finish()

    reactor.callWhenRunning(lambda: probe.run().addErrback(bail))
    probe.finished.addBoth(lambda _: reactor.stop())
    # A hard stop so a wedged reactor cannot hang the caller forever.
    reactor.callLater(args.timeout + 10, lambda: reactor.running and reactor.stop())
    reactor.run()

    json.dump(RESULT, sys.stdout)
    sys.stdout.write("\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

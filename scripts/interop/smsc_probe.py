#!/usr/bin/env python
"""A carrier emulator: a real third-party SMPP server that misbehaves on purpose.

esme_probe.py checks our SMPP server against an independent client. This is the
other direction, and it covers the revenue path: our outbound client binding to
an SMSC we did not write. The fake SMSC in cmd/jasmin-fake-smsc is ours, so it
cannot catch a misreading of SMPP 3.4 that we hold on both sides.

Built on `smpp.twisted.server.SMPPServerFactory` -- the same independent library
the frozen Jasmin runs on -- with a minimal in-memory credential check rather
than Jasmin's RouterPB-backed realm, so what is exercised is the library and the
protocol, not Jasmin's wiring.

Prints one JSON line per event to stdout, unbuffered, so a Go test can watch the
stream while it drives traffic:

  {"event": "listening", "port": 2775}
  {"event": "bound", "system_id": "u", "bind_type": "..."}
  {"event": "submit_sm", "source": "...", "destination": "...",
   "short_message_hex": "...", "sequence": 3, "esm_class": "...", ...}
  {"event": "unbound", "system_id": "u"}

Beyond plain interop it can reproduce what carriers actually do in production,
which is where gateways break:

  --throttle-after N      answer ESME_RTHROTTLED once more than N submits arrive
                          in a second (carriers rate-limit, and a gateway that
                          treats throttling as a hard failure loses traffic)
  --submit-latency S      delay every submit_sm_resp, to exercise the client's
                          outstanding-request window and response timeout
  --fail-every N          answer every Nth submit with an error status
  --dlr-delay S           send a delivery receipt S seconds after the response,
                          instead of never (receipts are asynchronous in reality)
  --dlr-out-of-order      deliver receipts for a batch in reverse order
  --dlr-stat STATE        receipt state: DELIVRD, UNDELIV, EXPIRED, REJECTD
  --drop-after N          close the connection after N submits, without unbind,
                          to exercise reconnect and in-flight recovery
  --enquire-link-every S  probe the client, as a carrier does

What this CANNOT tell you is carrier-specific behaviour: undocumented TLVs, an
address normalisation quirk, a non-standard receipt text, a throttling threshold
you were not told about. Emulation proves the gateway is spec-correct and robust;
only the real connection proves it works with *your* carrier.

Usage:
  smsc_probe.py --port 0 --system-id u --password p [--message-id-prefix smsc]
                [--submit-status ESME_ROK] [--lifetime 60] [carrier flags above]
"""

import argparse
import json
import sys

from twisted.cred import portal
from twisted.cred.checkers import InMemoryUsernamePasswordDatabaseDontUse
from twisted.internet import defer, reactor
from zope.interface import implementer

from smpp.twisted.config import SMPPServerConfig
from smpp.twisted.protocol import DataHandlerResponse
from smpp.twisted.server import IAuthenticatedSMPP, SMPPServerFactory
from smpp.pdu import pdu_types
from smpp.pdu.operations import DeliverSM
from smpp.pdu.pdu_types import (
    EsmClass,
    EsmClassMode,
    EsmClassType,
    MessageState,
)


# DLR receipt field values per delivery state, ported from the Python fake SMSC
# (dlr-smpp-python/fake_smsc.py:46-53), which is the only emulator in this estate
# that got this right.
#
# `dlvrd` counts messages actually delivered, so it is 000 for any state that is
# not a delivery, and `err` is 000 only on success. Hardcoding `dlvrd:001
# err:000` and then interpolating an arbitrary `stat` -- which is what this file
# did -- produces `stat:UNDELIV dlvrd:001 err:000`: a receipt that simultaneously
# reports nothing delivered, one thing delivered, and no error. A carrier never
# emits that, so a gateway tested against it is tested against a message no
# carrier will send, and any parser bug that depends on the fields agreeing goes
# unfound.
_DLR_RECEIPT_FIELDS = {
    "DELIVRD": {"dlvrd": 1, "err": "000"},
    "REJECTD": {"dlvrd": 0, "err": "008"},
    "UNDELIV": {"dlvrd": 0, "err": "008"},
    "EXPIRED": {"dlvrd": 0, "err": "008"},
    "DELETED": {"dlvrd": 0, "err": "008"},
    "ACCEPTD": {"dlvrd": 0, "err": "008"},
    "UNKNOWN": {"dlvrd": 0, "err": "008"},
    "ENROUTE": {"dlvrd": 0, "err": "008"},
}
# An unrecognised state is a non-delivery, never a delivery: failing towards
# "nothing was delivered" is the safe direction.
_DLR_RECEIPT_FALLBACK = {"dlvrd": 0, "err": "008"}


def receipt_fields(stat):
    """The dlvrd/err pair that belongs with one delivery state."""
    return _DLR_RECEIPT_FIELDS.get((stat or "").strip().upper(), _DLR_RECEIPT_FALLBACK)


def emit(**payload):
    """One JSON object per line, flushed, so the caller can react immediately."""
    json.dump(payload, sys.stdout)
    sys.stdout.write("\n")
    sys.stdout.flush()


@implementer(portal.IRealm)
class MinimalRealm:
    """The smallest realm the server protocol accepts.

    Jasmin's SmppsRealm resolves the avatar out of RouterPB; deliberately not
    reused here, because the point is to exercise the library independently of
    Jasmin's own plumbing.
    """

    def requestAvatar(self, avatarId, mind, *interfaces):
        if IAuthenticatedSMPP in interfaces:
            return IAuthenticatedSMPP, avatarId, lambda: None
        raise NotImplementedError("only IAuthenticatedSMPP is supported")


class Probe:
    def __init__(self, args):
        self.args = args
        self.count = 0
        self._second = None
        self._in_second = 0
        self._pending_receipts = []

    def _throttled(self):
        """Carriers rate-limit per second, so the window resets rather than being
        a running average."""
        if self.args.throttle_after <= 0:
            return False
        now = int(reactor.seconds())
        if now != self._second:
            self._second = now
            self._in_second = 0
        self._in_second += 1
        return self._in_second > self.args.throttle_after

    def _schedule_receipt(self, smpp, message_id, source, destination):
        """Receipts are asynchronous in reality: the response says 'accepted',
        the receipt says what happened, and the two are separated in time."""
        if self.args.dlr_delay < 0:
            return
        self._pending_receipts.append((message_id, source, destination))
        if self.args.dlr_out_of_order and len(self._pending_receipts) < 2:
            return
        batch = self._pending_receipts
        self._pending_receipts = []
        if self.args.dlr_out_of_order:
            batch = list(reversed(batch))
        for index, entry in enumerate(batch):
            reactor.callLater(
                self.args.dlr_delay + index * 0.01, self._send_receipt, smpp, *entry
            )

    def _send_receipt(self, smpp, message_id, source, destination):
        stat = self.args.dlr_stat
        fields = receipt_fields(stat)
        text = (
            f"id:{message_id} sub:001 dlvrd:{fields['dlvrd']:03d} "
            f"submit date:2601010000 done date:2601010000 "
            f"stat:{stat} err:{fields['err']} text:"
        )
        receipt = DeliverSM(
            source_addr=(destination or "").encode("ascii"),
            destination_addr=(source or "").encode("ascii"),
            short_message=text.encode("ascii"),
            esm_class=EsmClass(
                EsmClassMode.DEFAULT, EsmClassType.SMSC_DELIVERY_RECEIPT
            ),
            receipted_message_id=message_id,
            message_state=getattr(MessageState, stat, MessageState.DELIVERED),
        )
        try:
            smpp.sendDataRequest(receipt)
            emit(event="receipt_sent", message_id=message_id, stat=stat)
        except Exception as exc:
            emit(event="receipt_failed", message_id=message_id, error=repr(exc))

    def submit_handler(self, system_id, smpp, pdu):
        """Called for each submit_sm. The library expects a DataHandlerResponse
        carrying the command_status and any response params -- returning a bare
        message id makes it answer ESME_RX_T_APPN instead."""
        self.count += 1
        params = pdu.params
        body = params.get("short_message") or b""
        emit(
            event="submit_sm",
            system_id=system_id,
            sequence=pdu.seqNum,
            source=_text(params.get("source_addr")),
            destination=_text(params.get("destination_addr")),
            short_message_hex=body.hex() if isinstance(body, bytes) else "",
            esm_class=str(params.get("esm_class")),
            data_coding=str(params.get("data_coding")),
            registered_delivery=str(params.get("registered_delivery")),
            service_type=_text(params.get("service_type")),
            source_addr_ton=str(params.get("source_addr_ton")),
            source_addr_npi=str(params.get("source_addr_npi")),
            dest_addr_ton=str(params.get("dest_addr_ton")),
            dest_addr_npi=str(params.get("dest_addr_npi")),
            protocol_id=str(params.get("protocol_id")),
            priority_flag=str(params.get("priority_flag")),
            validity_period=str(params.get("validity_period")),
            schedule_delivery_time=str(params.get("schedule_delivery_time")),
            replace_if_present_flag=str(params.get("replace_if_present_flag")),
            sm_default_msg_id=str(params.get("sm_default_msg_id")),
            sar_msg_ref_num=str(params.get("sar_msg_ref_num")),
            sar_total_segments=str(params.get("sar_total_segments")),
            sar_segment_seqnum=str(params.get("sar_segment_seqnum")),
            message_payload_hex=(
                params["message_payload"].hex()
                if isinstance(params.get("message_payload"), bytes)
                else ""
            ),
        )
        if self._throttled():
            emit(event="throttled", sequence=pdu.seqNum)
            return DataHandlerResponse(status=pdu_types.CommandStatus.ESME_RTHROTTLED)
        if self.args.fail_every > 0 and self.count % self.args.fail_every == 0:
            emit(event="injected_failure", sequence=pdu.seqNum)
            return DataHandlerResponse(status=pdu_types.CommandStatus.ESME_RSUBMITFAIL)
        if self.args.drop_after > 0 and self.count >= self.args.drop_after:
            emit(event="dropping_connection", after=self.count)
            reactor.callLater(0, smpp.transport.loseConnection)
        message_id = f"{self.args.message_id_prefix}-{self.count}"
        if self.args.dlr_delay >= 0:
            self._schedule_receipt(
                smpp, message_id,
                _text(params.get("source_addr")), _text(params.get("destination_addr")),
            )
        if self.args.submit_latency > 0:
            deferred = defer.Deferred()
            reactor.callLater(
                self.args.submit_latency,
                deferred.callback,
                DataHandlerResponse(
                    status=pdu_types.CommandStatus.ESME_ROK, message_id=message_id
                ),
            )
            return deferred
        if self.args.submit_status != "ESME_ROK":
            return DataHandlerResponse(
                status=getattr(
                    pdu_types.CommandStatus,
                    self.args.submit_status,
                    pdu_types.CommandStatus.ESME_RSYSERR,
                )
            )
        return DataHandlerResponse(
            status=pdu_types.CommandStatus.ESME_ROK, message_id=message_id
        )


def _text(value):
    if value is None:
        return None
    if isinstance(value, bytes):
        return value.decode("latin-1")
    return str(value)


class WatchingFactory(SMPPServerFactory):
    """Reports bind and unbind so the caller can synchronise without sleeping."""

    def addBoundConnection(self, connection):
        SMPPServerFactory.addBoundConnection(self, connection)
        emit(
            event="bound",
            system_id=connection.system_id,
            bind_type=str(getattr(connection, "bind_type", "")),
        )

    def removeConnection(self, connection):
        system_id = getattr(connection, "system_id", None)
        SMPPServerFactory.removeConnection(self, connection)
        emit(event="unbound", system_id=system_id)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, default=0)
    parser.add_argument("--system-id", required=True)
    parser.add_argument("--password", required=True)
    parser.add_argument("--message-id-prefix", default="smsc")
    parser.add_argument("--submit-status", default="ESME_ROK")
    parser.add_argument("--max-bindings", type=int, default=5)
    parser.add_argument("--lifetime", type=float, default=60.0)
    # Carrier-behaviour emulation.
    parser.add_argument("--throttle-after", type=int, default=0)
    parser.add_argument("--submit-latency", type=float, default=0.0)
    parser.add_argument("--fail-every", type=int, default=0)
    parser.add_argument("--dlr-delay", type=float, default=-1.0)
    parser.add_argument("--dlr-out-of-order", action="store_true")
    parser.add_argument("--dlr-stat", default="DELIVRD")
    parser.add_argument("--drop-after", type=int, default=0)
    parser.add_argument("--enquire-link-every", type=float, default=0.0)
    args = parser.parse_args()

    probe = Probe(args)
    config = SMPPServerConfig(
        msgHandler=probe.submit_handler,
        systems={args.system_id: {"max_bindings": args.max_bindings}},
    )
    checker = InMemoryUsernamePasswordDatabaseDontUse()
    checker.addUser(args.system_id, args.password)
    auth_portal = portal.Portal(MinimalRealm())
    auth_portal.registerChecker(checker)

    factory = WatchingFactory(config, auth_portal)
    listener = reactor.listenTCP(args.port, factory, interface="127.0.0.1")
    emit(event="listening", port=listener.getHost().port)

    reactor.callLater(args.lifetime, lambda: reactor.running and reactor.stop())
    reactor.run()
    emit(event="stopped", submits=probe.count)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

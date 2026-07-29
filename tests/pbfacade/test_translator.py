import pickle
import unittest

from jasmin.pbfacade.avatars import (
    ClientManagerAvatar,
    RouterAvatar,
    bill_spec,
    connector_object,
    connector_spec,
    group_spec,
    pdu_body,
    pdu_wire,
    pdu_wires,
    user_spec,
)
from jasmin.pbfacade.client import FacadeError
from jasmin.protocols.smpp.configs import SMPPClientConfig
from jasmin.routing.Bills import SubmitSmBill
from jasmin.routing.jasminApi import Group, User
from jasmin.routing.Interceptors import StaticMTInterceptor
from jasmin.routing.Routes import StaticMORoute, StaticMTRoute
from smpp.pdu.operations import SubmitSM
from smpp.pdu.pdu_types import AddrNpi, AddrTon, PriorityFlag, ReplaceIfPresentFlag


class RecordingClient:
    def __init__(self, results=None):
        self.results = results or {}
        self.calls = []

    def call(self, method, params=None):
        self.calls.append((method, params))
        return self.results.get(method, True)


class RejectingClient:
    def call(self, method, params=None):
        raise FacadeError("invalid_params", "delete_queues is not supported")


class TranslatorTest(unittest.TestCase):
    def test_group_and_user_pickle_are_confined_and_projected(self):
        group = Group("customers")
        user = User("user-1", group, "alice", "secret")
        group_result = group_spec(group)
        user_result = user_spec(user)

        self.assertEqual("customers", group_result["gid"])
        self.assertEqual(user.password.hex(), user_result["password_md5"])
        self.assertEqual("user-1", user_result["external_id"])
        self.assertEqual("customers", user_result["group_id"])
        self.assertIsInstance(pickle.loads(__import__("base64").b64decode(user_result["legacy_pickle"])), User)

    def test_connector_round_trip_preserves_frozen_management_fields(self):
        original = SMPPClientConfig(
            id="conn-1",
            host="smsc.example",
            port=2776,
            username="system",
            password="password",
            bindOperation="transmitter",
            reconnectOnConnectionLoss=False,
            submit_sm_throughput=2.5,
            addressTon=AddrTon.ALPHANUMERIC,
            addressNpi=AddrNpi.INTERNET,
            source_addr_ton=AddrTon.NATIONAL,
            source_addr_npi=AddrNpi.ISDN,
            dest_addr_ton=AddrTon.INTERNATIONAL,
            dest_addr_npi=AddrNpi.PRIVATE,
            priority_flag=PriorityFlag.LEVEL_3,
            replace_if_present_flag=ReplaceIfPresentFlag.REPLACE,
        )
        projected = connector_spec(original)
        restored = connector_object(projected)
        self.assertEqual(5, projected["addr_ton"])
        self.assertEqual(14, projected["addr_npi"])
        self.assertEqual(2, projected["src_ton"])
        self.assertEqual(1, projected["dst_ton"])
        self.assertEqual(3, projected["priority"])
        self.assertEqual(1, projected["replace_if_present_flag"])
        self.assertEqual(original.id, restored.id)
        self.assertEqual(original.host, restored.host)
        self.assertEqual(original.bindOperation, restored.bindOperation)
        self.assertEqual(original.reconnectOnConnectionLoss, restored.reconnectOnConnectionLoss)
        self.assertEqual(original.submit_sm_throughput, restored.submit_sm_throughput)
        self.assertEqual(original.addressTon, restored.addressTon)
        self.assertEqual(original.addressNpi, restored.addressNpi)
        self.assertEqual(original.source_addr_ton, restored.source_addr_ton)
        self.assertEqual(original.dest_addr_npi, restored.dest_addr_npi)
        self.assertEqual(original.priority_flag, restored.priority_flag)
        self.assertEqual(original.replace_if_present_flag, restored.replace_if_present_flag)

    def test_submit_pdu_uses_base64_for_the_normalized_go_boundary(self):
        pdu = SubmitSM(source_addr="123", destination_addr="456", short_message=b"\x00\xff")
        result = pdu_body(pdu)
        self.assertEqual("MTIz", result["SourceAddress"])
        self.assertEqual("NDU2", result["DestinationAddress"])
        self.assertEqual("AP8=", result["ShortMessage"])

    def test_submit_projects_target_bill_and_dlr_connector(self):
        user = User("user-1", Group("customers"), "alice", "secret")
        bill = SubmitSmBill(user)
        bill.bid = "bill-1"
        bill.setAmount("submit_sm", 0.25)
        bill.setAmount("submit_sm_resp", 0.75)
        bill.setAction("decrement_submit_sm_count", 1)
        pdu = SubmitSM(source_addr="123", destination_addr="456", short_message=b"hello")
        client = RecordingClient({"router.submit_sm": "message-1"})
        avatar = ClientManagerAvatar(client)

        result = avatar._submit(
            "user-1",
            "forced-cid",
            pickle.dumps(pdu, pickle.HIGHEST_PROTOCOL),
            bill,  # pickled=False callers pass the live bill object
            3,
            __import__("datetime").datetime(2026, 7, 29, 12, 34, 56, 123456),
            "httpapi",
            "https://example.test/dlr",
            3,
            "POST",
            "receipt-cid",
        )

        self.assertEqual("message-1", result)
        _, params = client.calls[0]
        self.assertEqual("forced-cid", params["connector_id"])
        self.assertEqual([pdu_wire(pdu)], params["pdu_wires"])
        self.assertEqual(3, params["priority"])
        self.assertEqual("2026-07-29 12:34:56.123456", params["validity_period"])
        self.assertEqual({
            "id": "bill-1",
            "submit_sm_amount": 0.25,
            "submit_sm_resp_amount": 0.75,
            "decrement_submit_sm_count": 1,
        }, params["bill"])
        self.assertEqual(params["bill"], bill_spec(pickle.dumps(bill, pickle.HIGHEST_PROTOCOL)))
        self.assertEqual("receipt-cid", params["dlr_connector"])

    def test_submit_preserves_ordered_linked_pdu_chain_and_rejects_cycles(self):
        first = SubmitSM(source_addr="123", destination_addr="456", short_message=b"part-1")
        second = SubmitSM(source_addr="123", destination_addr="456", short_message=b"part-2")
        first.nextPdu = second
        self.assertEqual([pdu_wire(first), pdu_wire(second)], pdu_wires(first))
        with self.assertRaisesRegex(ValueError, "exceeds 1"):
            pdu_wires(first, max_parts=1)
        second.nextPdu = first
        with self.assertRaisesRegex(ValueError, "cyclic"):
            pdu_wires(first)

    def test_delete_queues_architecture_error_is_exposed_as_false(self):
        avatar = ClientManagerAvatar(RejectingClient())
        self.assertFalse(avatar._safe(
            "client.connector.stop",
            {"id": "conn-1", "delete_queues": True},
        ))

    def test_router_calls_normalized_method_without_forwarding_pickle(self):
        group = Group("customers")
        raw = pickle.dumps(group, pickle.HIGHEST_PROTOCOL)
        client = RecordingClient()
        avatar = RouterAvatar(client)
        result = avatar._safe("router.group.add", {"id": group.gid, "spec": group_spec(group)})
        self.assertTrue(result)
        method, params = client.calls[0]
        self.assertEqual("router.group.add", method)
        self.assertNotIn(raw, repr(params).encode())

    def test_connector_details_restore_legacy_result_shape(self):
        client = RecordingClient({
            "client.connector.get": {
                "config": {"cid": "conn-1"},
                "desired_started": True,
                "observed": "BOUND_TRX",
            }
        })
        avatar = ClientManagerAvatar(client)
        self.assertEqual({
            "id": "conn-1",
            "session_state": "BOUND_TRX",
            "service_status": 1,
            "start_count": 0,
            "stop_count": 0,
        }, avatar._one_details("conn-1"))

    def test_native_connector_state_is_projected_to_frozen_session_name(self):
        self.assertEqual("BOUND_TX", ClientManagerAvatar._legacy_session_state({
            "config": {"bind": "transmitter"},
            "observed": "BOUND",
        }))
        self.assertEqual("NONE", ClientManagerAvatar._legacy_session_state({
            "config": {"bind": "transceiver"},
            "observed": "DISCONNECTED",
        }))

    def test_non_pb_origin_entities_are_reconstructed_for_legacy_lists(self):
        client = RecordingClient({
            "router.group.list": [{
                "id": "shared",
                "spec": {"gid": "shared", "disabled": True},
            }],
            "router.user.list": [{
                "id": "alice",
                "spec": {
                    "username": "alice",
                    "external_id": "user-1",
                    "group_id": "shared",
                    "password_md5": "5ebe2294ecd0e0f08eab7690d2a6ee69",
                    "balance": 3.5,
                    "submit_sm_count": 2,
                    "mt_credential": {"filter_destination_address": "^44"},
                    "smpps_credential": {"bind": False, "max_bindings": 1},
                },
            }],
            "router.mtroute.list": [{
                "order": 10,
                "spec": {
                    "connector_id": "smsc-a",
                    "rate": 0.2,
                    "filters": [{"type": "destination_addr", "pattern": "^44"}],
                },
            }],
            "router.moroute.list": [{
                "order": 20,
                "spec": {
                    "filter_connector_id": "smsc-in",
                    "filters": [{"type": "tag", "value": "gold"}],
                    "connector": {
                        "type": "http",
                        "cid": "webhook",
                        "url": "https://example.test/mo",
                        "method": "POST",
                    },
                },
            }],
            "router.mtinterceptor.list": [{
                "order": 30,
                "spec": {
                    "py_code": "routable.addTag('seen')",
                    "filters": [{"type": "tag", "value": "gold"}],
                },
            }],
        })
        avatar = RouterAvatar(client)

        groups = pickle.loads(avatar._group_get_all())
        self.assertEqual("shared", groups[0].gid)
        self.assertFalse(groups[0].enabled)

        users = pickle.loads(avatar._user_get_all(None))
        self.assertEqual("user-1", users[0].uid)
        self.assertEqual("shared", users[0].group.gid)
        self.assertEqual(3.5, users[0].mt_credential.quotas["balance"])
        self.assertFalse(users[0].smpps_credential.authorizations["bind"])

        mt_routes = pickle.loads(avatar._ordered_get_all("router.mtroute.list", "mt"))
        mo_routes = pickle.loads(avatar._ordered_get_all("router.moroute.list", "mo"))
        interceptors = pickle.loads(
            avatar._ordered_get_all("router.mtinterceptor.list", "mt", True)
        )
        self.assertIsInstance(mt_routes[0][10], StaticMTRoute)
        self.assertIsInstance(mo_routes[0][20], StaticMORoute)
        self.assertIsInstance(interceptors[0][30], StaticMTInterceptor)


if __name__ == "__main__":
    unittest.main()

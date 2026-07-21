#!/usr/bin/env python3
"""Security regression tests for golden fixture validation."""

from __future__ import annotations

import base64
import copy
import hashlib
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock

import verify_fixtures
from verify_fixtures import (
    validate_amqp,
    validate_billing_enforcement,
    validate_late_billing,
    validate_router_amqp_subscriptions,
    validate_common,
    validate_routing_filters,
    validate_routing_tables,
    validate_segmentation,
    validate_smpp,
    validate_smpp_client_pacing,
    validate_smpp_client_readiness,
    validate_smpp_client_response_publish,
)


ROOT = Path(__file__).resolve().parents[2]
AMQP_FIXTURE = ROOT / "compat/fixtures/amqp/baseline.json"
BILLING_ENFORCEMENT_FIXTURE = ROOT / "compat/fixtures/billing-enforcement/baseline.json"
LATE_BILLING_FIXTURE = ROOT / "compat/fixtures/late-billing/baseline.json"
ROUTER_AMQP_SUBSCRIPTIONS_FIXTURE = ROOT / "compat/fixtures/router-amqp-subscriptions/baseline.json"
HTTP_FIXTURE = ROOT / "compat/fixtures/http/baseline.json"
SEGMENTATION_FIXTURE = ROOT / "compat/fixtures/segmentation/baseline.json"
ROUTING_FILTER_FIXTURE = ROOT / "compat/fixtures/routing-filters/baseline.json"
ROUTING_TABLE_FIXTURE = ROOT / "compat/fixtures/routing-tables/baseline.json"
SMPP_FIXTURE = ROOT / "compat/fixtures/smpp/baseline.json"
SMPP_CLIENT_PACING_FIXTURE = ROOT / "compat/fixtures/smpp-client-pacing/baseline.json"
SMPP_CLIENT_READINESS_FIXTURE = ROOT / "compat/fixtures/smpp-client-readiness/baseline.json"
SMPP_CLIENT_RESPONSE_PUBLISH_FIXTURE = ROOT / "compat/fixtures/smpp-client-response-publish/baseline.json"


class AmqpFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(AMQP_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_broker_capture_is_valid(self) -> None:
        validate_amqp(self.document)

    def test_self_consistent_forged_payload_is_rejected(self) -> None:
        forged = copy.deepcopy(self.document)
        body = forged["cases"][0]["body"]
        payload = b"not-the-legacy-pickle"
        body["wire_base64"] = base64.b64encode(payload).decode("ascii")
        body["wire_sha256"] = hashlib.sha256(payload).hexdigest()
        body["pickle_protocol"] = None

        with self.assertRaisesRegex(AssertionError, "trusted case fingerprint"):
            validate_amqp(forged)

    def test_unknown_case_is_rejected(self) -> None:
        forged = copy.deepcopy(self.document)
        forged["cases"][0]["id"] = "forged"
        with self.assertRaisesRegex(AssertionError, "unexpected or missing case ids"):
            validate_amqp(forged)


class BillingEnforcementFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(BILLING_ENFORCEMENT_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_oracle_corpus_is_valid(self) -> None:
        validate_billing_enforcement(self.document)

    def test_self_consistent_post_state_edit_is_rejected(self) -> None:
        forged = copy.deepcopy(self.document)
        forged["cases"][0]["expected"]["balance_after"] = 1.5
        forged["cases_sha256"] = hashlib.sha256(
            json.dumps(forged["cases"], sort_keys=True, separators=(",", ":")).encode("utf-8")
        ).hexdigest()
        with self.assertRaisesRegex(AssertionError, "trusted corpus fingerprint"):
            validate_billing_enforcement(forged)


class LateBillingFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(LATE_BILLING_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_oracle_corpus_is_valid(self) -> None:
        validate_late_billing(self.document)

    def test_self_consistent_action_edit_is_rejected(self) -> None:
        forged = copy.deepcopy(self.document)
        forged["cases"][0]["expected"]["action"] = "ack"
        forged["cases_sha256"] = hashlib.sha256(
            json.dumps(forged["cases"], sort_keys=True, separators=(",", ":")).encode("utf-8")
        ).hexdigest()
        with self.assertRaisesRegex(AssertionError, "trusted corpus fingerprint"):
            validate_late_billing(forged)


class RouterAMQPSubscriptionsFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(ROUTER_AMQP_SUBSCRIPTIONS_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_oracle_corpus_is_valid(self) -> None:
        validate_router_amqp_subscriptions(self.document)

    def test_self_consistent_durability_edit_is_rejected(self) -> None:
        forged = copy.deepcopy(self.document)
        forged["cases"][0]["expected"]["operations"][0]["durable"] = True
        forged["cases_sha256"] = hashlib.sha256(
            json.dumps(forged["cases"], sort_keys=True, separators=(",", ":")).encode("utf-8")
        ).hexdigest()
        with self.assertRaisesRegex(AssertionError, "trusted corpus fingerprint"):
            validate_router_amqp_subscriptions(forged)


class SmppClientResponsePublishFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(SMPP_CLIENT_RESPONSE_PUBLISH_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_oracle_corpus_is_valid(self) -> None:
        validate_smpp_client_response_publish(self.document)

    def test_self_consistent_forged_response_body_is_rejected(self) -> None:
        forged = copy.deepcopy(self.document)
        publication = forged["cases"][1]["expected"]["publication"]
        payload = b"\x80\x02N."
        publication["body_base64"] = base64.b64encode(payload).decode("ascii")
        publication["body_sha256"] = hashlib.sha256(payload).hexdigest()
        with self.assertRaisesRegex(AssertionError, "trusted corpus fingerprint"):
            validate_smpp_client_response_publish(forged)


class SegmentationFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(SEGMENTATION_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_oracle_corpus_is_valid(self) -> None:
        validate_segmentation(self.document)

    def test_modified_corpus_is_rejected_before_structural_acceptance(self) -> None:
        forged = copy.deepcopy(self.document)
        forged["cases"][0]["source"] = "forged-but-self-consistent-source"
        with self.assertRaisesRegex(AssertionError, "trusted corpus fingerprint"):
            validate_segmentation(forged)


class SMPPFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(SMPP_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_oracle_corpus_is_valid(self) -> None:
        validate_smpp(self.document)

    def test_self_consistent_wire_edit_is_rejected(self) -> None:
        forged = copy.deepcopy(self.document)
        case = next(case for case in forged["cases"] if case["id"] == "enquire_link")
        wire = bytearray.fromhex(case["wire_hex"])
        wire[-1] = 3
        case["wire_hex"] = wire.hex()
        case["roundtrip_wire_hex"] = wire.hex()
        case["decoded"]["sequence_number"] = 3
        with self.assertRaisesRegex(AssertionError, "trusted corpus fingerprint"):
            validate_smpp(forged)


class SMPPClientPacingFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(SMPP_CLIENT_PACING_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_oracle_corpus_is_valid(self) -> None:
        validate_smpp_client_pacing(self.document)

    def test_modified_wait_is_rejected_before_structural_acceptance(self) -> None:
        forged = copy.deepcopy(self.document)
        case = next(case for case in forged["cases"] if case["id"] == "one_mps_fast")
        case["expected"]["wait_seconds"] = 0.123456
        with self.assertRaisesRegex(AssertionError, "trusted corpus fingerprint"):
            validate_smpp_client_pacing(forged)


class SMPPClientReadinessFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(SMPP_CLIENT_READINESS_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_oracle_corpus_is_valid(self) -> None:
        validate_smpp_client_readiness(self.document)

    def test_modified_action_is_rejected_before_structural_acceptance(self) -> None:
        forged = copy.deepcopy(self.document)
        forged["cases"][0]["expected"]["action"] = "requeue"
        with self.assertRaisesRegex(AssertionError, "trusted corpus fingerprint"):
            validate_smpp_client_readiness(forged)


class RoutingFilterFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(ROUTING_FILTER_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_oracle_corpus_is_valid(self) -> None:
        validate_routing_filters(self.document)

    def test_self_consistent_outcome_edit_is_rejected(self) -> None:
        forged = copy.deepcopy(self.document)
        forged["cases"][0]["expected"]["matched"] = False
        with self.assertRaisesRegex(AssertionError, "corpus fingerprint"):
            validate_routing_filters(forged)


class RoutingTableFixtureValidationTests(unittest.TestCase):
    def setUp(self) -> None:
        self.document = json.loads(ROUTING_TABLE_FIXTURE.read_text(encoding="utf-8"))

    def test_committed_oracle_corpus_is_valid(self) -> None:
        validate_routing_tables(self.document)

    def test_modified_selection_is_rejected(self) -> None:
        forged = copy.deepcopy(self.document)
        forged["cases"][2]["expected"]["selected"]["connector_id"] = "forged"
        with self.assertRaisesRegex(AssertionError, "corpus fingerprint"):
            validate_routing_tables(forged)


class FixtureInventoryValidationTests(unittest.TestCase):
    def test_missing_non_amqp_case_is_rejected(self) -> None:
        document = json.loads(HTTP_FIXTURE.read_text(encoding="utf-8"))
        document["cases"] = [case for case in document["cases"] if case["id"] != "send_missing_to"]
        with self.assertRaisesRegex(AssertionError, "unexpected or missing case ids"):
            validate_common("http", document)

    def test_modified_coverage_manifest_is_rejected(self) -> None:
        original = verify_fixtures.COVERAGE.read_text(encoding="utf-8")
        forged = original.replace("http,send_missing_to,H-002,full", "http,send_missing_to,ZZ-999,full")
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "coverage.csv"
            path.write_text(forged, encoding="utf-8")
            with mock.patch.object(verify_fixtures, "COVERAGE", path):
                with self.assertRaisesRegex(AssertionError, "approved manifest"):
                    verify_fixtures.main()


if __name__ == "__main__":
    unittest.main()

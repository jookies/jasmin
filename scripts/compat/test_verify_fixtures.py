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
from verify_fixtures import validate_amqp, validate_common, validate_segmentation


ROOT = Path(__file__).resolve().parents[2]
AMQP_FIXTURE = ROOT / "compat/fixtures/amqp/baseline.json"
HTTP_FIXTURE = ROOT / "compat/fixtures/http/baseline.json"
SEGMENTATION_FIXTURE = ROOT / "compat/fixtures/segmentation/baseline.json"


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


class FixtureInventoryValidationTests(unittest.TestCase):
    def test_missing_non_amqp_case_is_rejected(self) -> None:
        document = json.loads(HTTP_FIXTURE.read_text(encoding="utf-8"))
        document["cases"] = [case for case in document["cases"] if case["id"] != "rate_valid"]
        with self.assertRaisesRegex(AssertionError, "unexpected or missing case ids"):
            validate_common("http", document)

    def test_modified_coverage_manifest_is_rejected(self) -> None:
        original = verify_fixtures.COVERAGE.read_text(encoding="utf-8")
        forged = original.replace("http,rate_valid,H-010,partial", "http,rate_valid,ZZ-999,full")
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "coverage.csv"
            path.write_text(forged, encoding="utf-8")
            with mock.patch.object(verify_fixtures, "COVERAGE", path):
                with self.assertRaisesRegex(AssertionError, "approved manifest"):
                    verify_fixtures.main()


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Regression tests for the exact golden comparer."""

from __future__ import annotations

import unittest

from diff_golden import compare


class DifferentialComparerTests(unittest.TestCase):
    def test_identical_documents_match(self) -> None:
        document = {"cases": [{"id": "ping", "status": 200}]}
        self.assertEqual([], compare(document, document))

    def test_scalar_change_has_exact_pointer(self) -> None:
        expected = {"cases": [{"id": "ping", "status": 200}]}
        actual = {"cases": [{"id": "ping", "status": 503}]}
        self.assertEqual(
            [{"pointer": "/cases/0/status", "expected": 200, "actual": 503, "reason": "value"}],
            compare(expected, actual),
        )

    def test_missing_key_and_extra_item_are_detected(self) -> None:
        expected = {"headers": {"content-type": "text/plain"}, "segments": [1]}
        actual = {"headers": {}, "segments": [1, 2]}
        self.assertEqual(
            [
                {
                    "pointer": "/headers/content-type",
                    "expected": "text/plain",
                    "actual": "<absent>",
                    "reason": "missing",
                },
                {"pointer": "/segments", "expected": 1, "actual": 2, "reason": "length"},
                {"pointer": "/segments/1", "expected": "<absent>", "actual": 2, "reason": "extra"},
            ],
            compare(expected, actual),
        )


if __name__ == "__main__":
    unittest.main()

#!/usr/bin/env python3
"""Run named unittest modules and emit one machine-readable result record."""
from __future__ import annotations

import json
import sys
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))


class RecordingResult(unittest.TextTestResult):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self.test_names: list[str] = []

    def startTest(self, test: unittest.case.TestCase) -> None:
        self.test_names.append(test.id())
        super().startTest(test)


def main(argv: list[str]) -> int:
    if not argv:
        print("usage: run_python_unittest_json.py module [module ...]", file=sys.stderr)
        return 64
    suite = unittest.TestSuite()
    loader = unittest.defaultTestLoader
    for name in argv:
        suite.addTests(loader.loadTestsFromName(name))
    runner = unittest.TextTestRunner(stream=sys.stderr, verbosity=1, resultclass=RecordingResult)
    result = runner.run(suite)
    names = result.test_names if isinstance(result, RecordingResult) else []
    record = {
        "framework": "python-unittest",
        "tests": result.testsRun,
        "skipped": len(result.skipped),
        "failed": len(result.failures) + len(result.errors) + len(result.unexpectedSuccesses),
        "test_names": names,
    }
    print(json.dumps(record, sort_keys=True, separators=(",", ":")))
    return 0 if result.wasSuccessful() else 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))

#!/usr/bin/env python3
"""Exact structural comparer for Python-oracle and Go-produced fixture documents."""

from __future__ import annotations

import argparse
import json
from pathlib import Path


def compare(expected, actual, pointer=""):
    mismatches = []
    if type(expected) is not type(actual):
        return [{"pointer": pointer or "/", "expected": expected, "actual": actual, "reason": "type"}]
    if isinstance(expected, dict):
        for key in sorted(set(expected) | set(actual)):
            child = f"{pointer}/{key.replace('~', '~0').replace('/', '~1')}"
            if key not in expected:
                mismatches.append({"pointer": child, "expected": "<absent>", "actual": actual[key], "reason": "extra"})
            elif key not in actual:
                mismatches.append({"pointer": child, "expected": expected[key], "actual": "<absent>", "reason": "missing"})
            else:
                mismatches.extend(compare(expected[key], actual[key], child))
    elif isinstance(expected, list):
        if len(expected) != len(actual):
            mismatches.append({"pointer": pointer or "/", "expected": len(expected), "actual": len(actual), "reason": "length"})
        shared = min(len(expected), len(actual))
        for index in range(shared):
            mismatches.extend(compare(expected[index], actual[index], f"{pointer}/{index}"))
        for index in range(shared, len(expected)):
            mismatches.append({
                "pointer": f"{pointer}/{index}",
                "expected": expected[index],
                "actual": "<absent>",
                "reason": "missing",
            })
        for index in range(shared, len(actual)):
            mismatches.append({
                "pointer": f"{pointer}/{index}",
                "expected": "<absent>",
                "actual": actual[index],
                "reason": "extra",
            })
    elif expected != actual:
        mismatches.append({"pointer": pointer or "/", "expected": expected, "actual": actual, "reason": "value"})
    return mismatches


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("expected", type=Path)
    parser.add_argument("actual", type=Path)
    parser.add_argument("--json", action="store_true", dest="as_json")
    parser.add_argument("--max-mismatches", type=int, default=100)
    args = parser.parse_args()

    expected = json.loads(args.expected.read_text(encoding="utf-8"))
    actual = json.loads(args.actual.read_text(encoding="utf-8"))
    mismatches = compare(expected, actual)

    if args.as_json:
        print(json.dumps({"match": not mismatches, "mismatch_count": len(mismatches), "mismatches": mismatches[: args.max_mismatches]}, indent=2, sort_keys=True))
    elif mismatches:
        print(f"MISMATCH count={len(mismatches)}")
        for item in mismatches[: args.max_mismatches]:
            print(f"{item['pointer']}: {item['reason']} expected={item['expected']!r} actual={item['actual']!r}")
    else:
        print("MATCH")
    return 1 if mismatches else 0


if __name__ == "__main__":
    raise SystemExit(main())

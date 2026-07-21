#!/usr/bin/env python3
"""Validate the Wave 0 compatibility registry and optional candidate evidence."""
from __future__ import annotations

import argparse
from pathlib import Path

try:
    from scripts.compat.contract_registry import RegistryError, validate_repository
except ModuleNotFoundError:  # direct execution from repository root
    from contract_registry import RegistryError, validate_repository


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--evidence-dir", type=Path)
    parser.add_argument("--scope")
    parser.add_argument("--mode")
    args = parser.parse_args()
    if bool(args.scope) != bool(args.mode) or (args.scope and not args.evidence_dir):
        parser.error("--scope and --mode require each other and --evidence-dir")
    try:
        registry = validate_repository(args.repo.resolve(), args.evidence_dir, scope=args.scope, mode=args.mode)
    except (RegistryError, OSError, ValueError) as exc:
        print(f"contract registry: FAIL: {exc}")
        return 1
    counts = registry.status_counts
    rendered = ", ".join(f"{status}={counts.get(status, 0)}" for status in sorted(counts))
    print(f"contract registry: PASS: total={len(registry.rows)}, unfinished={len(registry.unfinished_ids)}; {rendered}")
    if args.evidence_dir is None:
        print("evidence: structural-only (macro closure is not claimed)")
    else:
        print(f"evidence: validated from {args.evidence_dir}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

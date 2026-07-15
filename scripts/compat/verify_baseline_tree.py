#!/usr/bin/env python3
"""Fail when the frozen Python oracle surface differs from the pinned baseline."""

from __future__ import annotations

import hashlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BASELINE_COMMIT = "0aac58e466d583d0f0436df7b8afa3dc96191263"
EXPECTED_FILE_COUNT = 201
EXPECTED_AGGREGATE_SHA256 = "8e7c1439068bfbbdef29a1bcc6b2c155db36a8763a1012ded059a05b9e6c87c6"


def oracle_files() -> list[Path]:
    files: list[Path] = []
    for directory in ("jasmin", "tests", "misc/config"):
        files.extend(
            path
            for path in (ROOT / directory).rglob("*")
            if path.is_file()
            and "__pycache__" not in path.parts
            and path.suffix not in {".pyc", ".pyo"}
        )
    for filename in ("requirements.txt", "requirements-test.txt", "pyproject.toml"):
        path = ROOT / filename
        if path.is_file():
            files.append(path)
    return sorted(set(files), key=lambda path: path.relative_to(ROOT).as_posix())


def aggregate(files: list[Path]) -> str:
    digest = hashlib.sha256()
    for path in files:
        relative = path.relative_to(ROOT).as_posix().encode("utf-8")
        file_digest = hashlib.sha256(path.read_bytes()).hexdigest().encode("ascii")
        digest.update(relative + b"\0" + file_digest + b"\n")
    return digest.hexdigest()


def main() -> int:
    files = oracle_files()
    actual = aggregate(files)
    if len(files) != EXPECTED_FILE_COUNT or actual != EXPECTED_AGGREGATE_SHA256:
        print(
            "oracle_tree=changed "
            f"baseline={BASELINE_COMMIT} "
            f"files={len(files)}/{EXPECTED_FILE_COUNT} "
            f"sha256={actual}/{EXPECTED_AGGREGATE_SHA256}"
        )
        return 1
    print(
        "oracle_tree=frozen "
        f"baseline={BASELINE_COMMIT} files={len(files)} sha256={actual}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

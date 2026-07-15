#!/usr/bin/env python3
"""Validate every golden document against its committed JSON Schema."""

from __future__ import annotations

import json
from pathlib import Path

from jsonschema import Draft202012Validator

ROOT = Path(__file__).resolve().parents[2]
PAIRS = {
    "http": (
        ROOT / "compat/fixtures/schema/http-golden.schema.json",
        ROOT / "compat/fixtures/http/baseline.json",
    ),
    "smpp": (
        ROOT / "compat/fixtures/schema/smpp-golden.schema.json",
        ROOT / "compat/fixtures/smpp/baseline.json",
    ),
    "amqp": (
        ROOT / "compat/fixtures/schema/amqp-content-golden.schema.json",
        ROOT / "compat/fixtures/amqp/baseline.json",
    ),
    "redis": (
        ROOT / "compat/fixtures/schema/redis-state-golden.schema.json",
        ROOT / "compat/fixtures/redis/baseline.json",
    ),
}


def main() -> int:
    for surface, (schema_path, fixture_path) in PAIRS.items():
        schema = json.loads(schema_path.read_text(encoding="utf-8"))
        document = json.loads(fixture_path.read_text(encoding="utf-8"))
        Draft202012Validator.check_schema(schema)
        errors = sorted(Draft202012Validator(schema).iter_errors(document), key=lambda error: list(error.path))
        if errors:
            for error in errors:
                pointer = "/" + "/".join(str(part) for part in error.absolute_path)
                print(f"{surface}{pointer}: {error.message}")
            return 1
    print("json_schemas=valid surfaces=http,smpp,amqp,redis")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

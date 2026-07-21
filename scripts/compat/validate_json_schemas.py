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
    "smpp-client-pacing": (
        ROOT / "compat/fixtures/schema/smpp-client-pacing-golden.schema.json",
        ROOT / "compat/fixtures/smpp-client-pacing/baseline.json",
    ),
    "smpp-client-readiness": (
        ROOT / "compat/fixtures/schema/smpp-client-readiness-golden.schema.json",
        ROOT / "compat/fixtures/smpp-client-readiness/baseline.json",
    ),
    "smpp-client-error-retry": (
        ROOT / "compat/fixtures/schema/smpp-client-error-retry-golden.schema.json",
        ROOT / "compat/fixtures/smpp-client-error-retry/baseline.json",
    ),
    "smpp-client-response-publish": (
        ROOT / "compat/fixtures/schema/smpp-client-response-publish-golden.schema.json",
        ROOT / "compat/fixtures/smpp-client-response-publish/baseline.json",
    ),
    "billing-enforcement": (
        ROOT / "compat/fixtures/schema/billing-enforcement-golden.schema.json",
        ROOT / "compat/fixtures/billing-enforcement/baseline.json",
    ),
    "late-billing": (
        ROOT / "compat/fixtures/schema/late-billing-golden.schema.json",
        ROOT / "compat/fixtures/late-billing/baseline.json",
    ),
    "router-amqp-subscriptions": (
        ROOT / "compat/fixtures/schema/router-amqp-subscriptions-golden.schema.json",
        ROOT / "compat/fixtures/router-amqp-subscriptions/baseline.json",
    ),
    "amqp": (
        ROOT / "compat/fixtures/schema/amqp-content-golden.schema.json",
        ROOT / "compat/fixtures/amqp/baseline.json",
    ),
    "redis": (
        ROOT / "compat/fixtures/schema/redis-state-golden.schema.json",
        ROOT / "compat/fixtures/redis/baseline.json",
    ),
    "segmentation": (
        ROOT / "compat/fixtures/schema/segmentation-golden.schema.json",
        ROOT / "compat/fixtures/segmentation/baseline.json",
    ),
    "routing-filters": (
        ROOT / "compat/fixtures/schema/routing-filter-golden.schema.json",
        ROOT / "compat/fixtures/routing-filters/baseline.json",
    ),
    "routing-tables": (
        ROOT / "compat/fixtures/schema/routing-table-golden.schema.json",
        ROOT / "compat/fixtures/routing-tables/baseline.json",
    ),
    "multi-connector-routes": (
        ROOT / "compat/fixtures/schema/multi-connector-route-golden.schema.json",
        ROOT / "compat/fixtures/multi-connector-routes/baseline.json",
    ),
}


def main() -> int:
    candidate_evidence_schema = ROOT / "spec/compatibility/CANDIDATE_EVIDENCE.schema.json"
    Draft202012Validator.check_schema(json.loads(candidate_evidence_schema.read_text(encoding="utf-8")))
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
    print(f"json_schemas=valid surfaces={','.join(PAIRS)},candidate-evidence")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

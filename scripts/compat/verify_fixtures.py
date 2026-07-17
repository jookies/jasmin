#!/usr/bin/env python3
"""Validate golden fixtures using only the Python standard library."""

from __future__ import annotations

import base64
import csv
import hashlib
import json
import pickletools
from datetime import datetime
from pathlib import Path

BASELINE = "0aac58e466d583d0f0436df7b8afa3dc96191263"
ROOT = Path(__file__).resolve().parents[2]
FIXTURES = {
    "http": ROOT / "compat/fixtures/http/baseline.json",
    "smpp": ROOT / "compat/fixtures/smpp/baseline.json",
    "smpp-client-pacing": ROOT / "compat/fixtures/smpp-client-pacing/baseline.json",
    "smpp-client-readiness": ROOT / "compat/fixtures/smpp-client-readiness/baseline.json",
    "amqp": ROOT / "compat/fixtures/amqp/baseline.json",
    "redis": ROOT / "compat/fixtures/redis/baseline.json",
    "segmentation": ROOT / "compat/fixtures/segmentation/baseline.json",
    "routing-filters": ROOT / "compat/fixtures/routing-filters/baseline.json",
    "routing-tables": ROOT / "compat/fixtures/routing-tables/baseline.json",
    "multi-connector-routes": ROOT / "compat/fixtures/multi-connector-routes/baseline.json",
}
COVERAGE = ROOT / "spec/compatibility/FIXTURE_COVERAGE.csv"
EXPECTED_CASE_IDS = {
    "http": {
        "ping",
        "rate_valid",
        "balance_unlimited",
        "send_missing_password",
        "send_bad_password",
        "send_no_live_connector",
        "rate_disabled_user",
        "balance_disabled_group",
        "send_json_bad_password",
    },
    "smpp": {
        "bind_transceiver",
        "submit_sm_ascii",
        "submit_sm_sar_part",
        "deliver_sm_mo",
        "submit_sm_resp_ok",
        "deliver_sm_dlr_message_payload",
        "submit_sm_unknown_vendor_tlv",
    },
    "smpp-client-pacing": {
        "default_config",
        "non_numeric_rejected",
        "unlimited_zero",
        "negative_disables_pacing",
        "first_message_no_wait",
        "first_message_large_interval_component",
        "first_message_large_interval_zero_component",
        "one_mps_same_timestamp",
        "one_mps_fast",
        "one_mps_boundary",
        "four_mps_fast",
        "fractional_two_point_five_mps",
        "half_even_interval_7812_microseconds",
        "half_even_interval_2_microseconds",
        "half_even_interval_zero_microseconds",
        "tiny_positive_large_interval_component",
        "large_interval_one_microsecond_elapsed",
        "large_interval_three_microseconds_elapsed",
        "tiny_positive_large_interval_one_microsecond_elapsed",
        "backward_clock_zero_interval",
        "half_mps_legacy_microseconds",
    },
    "smpp-client-readiness": {
        "expired_discard_precedes_disconnected",
        "expired_without_created_at_discards",
        "expiration_at_now_is_not_expired",
        "disconnected_fresh_delayed_requeue",
        "disconnected_boundary_requeues",
        "disconnected_over_age_discards",
        "disconnected_zero_delay_requeues",
        "unbound_fresh_delayed_requeue",
        "unbound_over_age_discards",
        "multi_day_age_uses_seconds_component",
        "future_created_at_wraps_seconds_component",
    },
    "amqp": {
        "submit_sm_httpapi",
        "submit_sm_resp",
        "dlr_lookup_submit_sm_resp",
        "dlr_http_thrower",
        "dlr_smpps_thrower",
        "bill_submit_sm_resp",
        "routed_deliver_sm_http",
    },
    "redis": {
        "http_dlr_request",
        "smpps_dlr_request",
        "smpp_to_queue_id",
        "multipart_deliver_sm_part",
    },
    "segmentation": {
        "gsm7_single_160",
        "gsm7_sar_161",
        "gsm7_udh_161",
        "invalid_dcs_255_fallback_sar_161",
        "gsm7_reference_rollover_sar_161",
        "gsm7_max_parts_two_truncates",
        "eight_bit_single_140",
        "eight_bit_sar_141",
        "eight_bit_udh_141",
        "binary_dcs4_sar_141",
        "ucs2_single_70_units",
        "ucs2_sar_71_units",
        "ucs2_udh_71_units",
        "ucs2_odd_byte_sar",
    },
    "routing-filters": {
        "transparent_mt", "transparent_mo",
        "connector_match", "connector_miss", "user_match", "user_miss", "group_match", "group_miss",
        "source_match", "source_miss", "source_utf8_replacement", "source_missing_key",
        "destination_match", "destination_miss", "destination_utf8_replacement", "destination_missing_key",
        "short_message_match", "short_message_precedes_payload", "message_payload_fallback",
        "message_content_missing", "short_message_utf8_replacement",
        "date_before", "date_start_inclusive", "date_inside", "date_end_inclusive", "date_after",
        "date_reversed_matches_nothing", "time_before", "time_start_inclusive", "time_inside",
        "time_end_inclusive", "time_after", "time_reversed_no_midnight_wrap",
        "tag_integer_normalized_match", "tag_string_match", "tag_miss",
    },
    "routing-tables": {
        "mt_descending_order", "mo_descending_order", "replace_same_order", "remove_existing", "remove_missing", "flush_table",
        "mt_first_match_user", "mt_first_match_destination", "mt_default_fallback", "mt_no_match",
        "mo_first_match_source", "mo_first_match_destination", "mo_default_fallback", "mo_no_match",
        "and_filters_match", "and_filters_short_circuit_miss", "negative_order_rejected",
        "nondefault_at_zero_rejected", "default_at_nonzero_rejected", "mt_wrong_connector_rejected",
        "mo_wrong_connector_rejected", "mt_visible_rate", "mo_rate_ignored",
    },
    "multi-connector-routes": {
        "random_mt_index_0", "random_mt_index_1", "random_mo_index_0", "random_mo_index_1",
        "random_mo_hybrid_allowed", "random_mt_filter_match", "random_mt_filter_miss", "random_empty_rejected",
        "failover_mt_sequence", "failover_mo_sequence", "failover_match_resets_sequence", "failover_get_connectors_order",
        "failover_mo_mixed_rejected", "failover_empty_rejected", "failover_mo_filter_match", "failover_mo_filter_miss",
    },
}
EXPECTED_COVERAGE_SHA256 = "9ac1bedba60b83e19d62c32163533d692089c5aee62a37fee7bfb31be11fa82e"
EXPECTED_SMPP_CLIENT_PACING_CASES_SHA256 = "ca2aaaf23cdaa0e5975639ad833013b146d5215d753d783b481fc64161df75e0"
EXPECTED_SMPP_CLIENT_READINESS_CASES_SHA256 = "4d811b89f63b005301a9dc3f4c7e3e7d45a1a0f6586f24b2f3429a988bea78a5"
EXPECTED_SEGMENTATION_CASES_SHA256 = "63be2a1a22afcebee9fc1da771be622c6a82e3adcaed82383dc20f49f24cc44f"
EXPECTED_ROUTING_FILTER_CORPUS_SHA256 = "424240347ce5c083d61be7bc6d7ea421e8a193cfeb9612466c8c0048c67b7ea0"
EXPECTED_ROUTING_TABLE_CASES_SHA256 = "1bf5aa6529429add1823d3b1ce29d6be3ffa7495b65f9a54203dc5ce330cb90d"
EXPECTED_MULTI_CONNECTOR_CASES_SHA256 = "4856bb235a509d3c1ac6593e1d57d9bc8856cc8023fb865942591cc7812cbff2"
EXPECTED_AMQP_CASE_SHA256 = {
    "submit_sm_httpapi": "e696cb539f3b187d99c368e6e69bc6db8a29e3e636bef8ec90d162adc2aa1706",
    "submit_sm_resp": "89768110d1c535cfd625a89aba82d0be827f9e5c1005de7ff30469b6cc300906",
    "dlr_lookup_submit_sm_resp": "0a60a46c7ba0c7fb8c259521b4d0d2e8dd30afbf0a1a4ac810476a00b171c089",
    "dlr_http_thrower": "0f4f4312b3865e1b0b96cd6b43ceabca70be5b61c06a7730e2342ce045eeb17c",
    "dlr_smpps_thrower": "9ed9c69d382ec371474c9deef4d5b965f6b17c96607722b172277a9b71c5fe1a",
    "bill_submit_sm_resp": "df3f4da44a9b4177350dfeb4fc01d54fe2d243265d14a8ad598cb32bb1c9b106",
    "routed_deliver_sm_http": "154cc6fe1f9f369e65c7422961f4f3cc01be98cf42052a6dcfac5a7de43de3dc",
}


def require(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def decode64(value: str, context: str) -> bytes:
    try:
        return base64.b64decode(value, validate=True)
    except Exception as exc:
        raise AssertionError(f"{context}: invalid base64: {exc}") from exc


def validate_common(surface: str, document: dict) -> None:
    require(document.get("schema_version") == 1, f"{surface}: schema_version")
    require(document.get("baseline_commit") == BASELINE, f"{surface}: baseline_commit")
    cases = document.get("cases")
    require(isinstance(cases, list) and len(cases) > 0, f"{surface}: cases must be non-empty")
    if not isinstance(cases, list):
        raise AssertionError(f"{surface}: cases must be a list")
    ids = [case.get("id") for case in cases]
    require(all(isinstance(case_id, str) and case_id for case_id in ids), f"{surface}: invalid case id")
    require(len(ids) == len(set(ids)), f"{surface}: duplicate case ids")
    require(set(ids) == EXPECTED_CASE_IDS[surface], f"{surface}: unexpected or missing case ids")


def validate_http(document: dict) -> None:
    for case in document["cases"]:
        response = case["response"]
        body = decode64(response["body_base64"], f"http/{case['id']}")
        if response["body_utf8"] is not None:
            require(body.decode("utf-8") == response["body_utf8"], f"http/{case['id']}: utf8 mismatch")
        require(100 <= response["status"] <= 599, f"http/{case['id']}: status")


def validate_smpp(document: dict) -> None:
    for case in document["cases"]:
        wire = bytes.fromhex(case["wire_hex"])
        require(len(wire) >= 16, f"smpp/{case['id']}: PDU shorter than header")
        require(int.from_bytes(wire[:4], "big") == len(wire), f"smpp/{case['id']}: command_length mismatch")
        if case["direction"] == "encode":
            require(case["roundtrip_wire_hex"] == case["wire_hex"], f"smpp/{case['id']}: encode roundtrip")
        if case.get("roundtrip_wire_hex") is None:
            require(bool(case.get("roundtrip_error")), f"smpp/{case['id']}: missing roundtrip error")


def validate_smpp_client_pacing(document: dict) -> None:
    cases_digest = hashlib.sha256(
        json.dumps(document["cases"], sort_keys=True, separators=(",", ":")).encode("utf-8")
    ).hexdigest()
    require(
        cases_digest == EXPECTED_SMPP_CLIENT_PACING_CASES_SHA256,
        "smpp-client-pacing: trusted corpus fingerprint",
    )
    require(
        document.get("source") == [
            "jasmin/protocols/smpp/configs.py:SMPPClientConfig",
            "jasmin/managers/listeners.py:SMPPClientSMListener.submit_sm_callback",
        ],
        "smpp-client-pacing: source boundary",
    )
    for case in document["cases"]:
        context = f"smpp-client-pacing/{case['id']}"
        expected = case["expected"]
        config = expected["config"]
        if config["error_type"] is None:
            require(config["error"] is None, f"{context}: successful config error")
            require(isinstance(config["value"], (int, float)), f"{context}: config value")
        else:
            require(config["error_type"] == "TypeMismatch", f"{context}: config error type")
            require(config["value"] is None, f"{context}: rejected config value")
        if isinstance(case["input"]["throughput"], (int, float)):
            require(isinstance(expected["wait_seconds"], (int, float)), f"{context}: wait")


def validate_smpp_client_readiness(document: dict) -> None:
    digest = hashlib.sha256(
        json.dumps(document["cases"], sort_keys=True, separators=(",", ":")).encode("utf-8")
    ).hexdigest()
    require(
        digest == EXPECTED_SMPP_CLIENT_READINESS_CASES_SHA256,
        "smpp-client-readiness: trusted corpus fingerprint",
    )
    require(
        document.get("source") == [
            "jasmin/managers/configs.py:SMPPClientSMListenerConfig",
            "jasmin/managers/listeners.py:SMPPClientSMListener.submit_sm_callback",
        ],
        "smpp-client-readiness: source boundary",
    )
    require(
        document.get("defaults") == {"max_age_seconds": 1200, "retry_delay_seconds": 30},
        "smpp-client-readiness: defaults",
    )
    for case in document["cases"]:
        context = f"smpp-client-readiness/{case['id']}"
        expected = case["expected"]
        require(expected["action"] in {"discard", "requeue"}, f"{context}: action")
        require(expected["retry_count"] == 1, f"{context}: retry post-state")
        if expected["action"] == "discard":
            require(expected["requeue_delay_seconds"] is None, f"{context}: discard delay")
        else:
            require(isinstance(expected["requeue_delay_seconds"], int), f"{context}: requeue delay")


def validate_amqp(document: dict) -> None:
    require(
        document.get("capture_transport") == "rabbitmq-publish-consume",
        "amqp: capture_transport",
    )
    require(
        {case["id"] for case in document["cases"]} == set(EXPECTED_AMQP_CASE_SHA256),
        "amqp: unexpected or missing case ids",
    )
    for case in document["cases"]:
        case_id = case["id"]
        case_digest = hashlib.sha256(
            json.dumps(case, sort_keys=True, separators=(",", ":")).encode("utf-8")
        ).hexdigest()
        require(
            case_digest == EXPECTED_AMQP_CASE_SHA256[case_id],
            f"amqp/{case_id}: trusted case fingerprint",
        )
        body = case["body"]
        payload = decode64(body["wire_base64"], f"amqp/{case_id}")
        require(hashlib.sha256(payload).hexdigest() == body["wire_sha256"], f"amqp/{case_id}: wire hash")
        if body["wire_kind"] == "text":
            require(payload.decode("utf-8") == body["text"], f"amqp/{case_id}: text mismatch")
            require(body["pickle_protocol"] is None, f"amqp/{case_id}: text marked as pickle")
        elif body["pickle_protocol"] is not None:
            require(len(payload) >= 2 and payload[0] == 0x80, f"amqp/{case_id}: invalid pickle prefix")
            require(payload[1] == body["pickle_protocol"], f"amqp/{case_id}: pickle protocol mismatch")
            try:
                opcodes = list(pickletools.genops(payload))
            except Exception as exc:
                raise AssertionError(f"amqp/{case_id}: malformed pickle opcode stream: {exc}") from exc
            require(bool(opcodes) and opcodes[-1][0].name == "STOP", f"amqp/{case_id}: pickle missing STOP")


def validate_redis(document: dict) -> None:
    for case in document["cases"]:
        require(case["redis_type"] == "hash", f"redis/{case['id']}: type")
        require(case["ttl_seconds"] > 0, f"redis/{case['id']}: ttl")
        require(case["fields"], f"redis/{case['id']}: empty fields")
        for field, value in case["fields"].items():
            if value.get("type") == "bytes":
                raw = decode64(value["base64"], f"redis/{case['id']}/{field}")
                require(raw.hex() == value["hex"], f"redis/{case['id']}/{field}: hex mismatch")


def validate_bytes(value: dict, context: str) -> bytes:
    raw = decode64(value["base64"], context)
    require(raw.hex() == value["hex"], f"{context}: hex mismatch")
    require(len(raw) == value["length"], f"{context}: length mismatch")
    require(hashlib.sha256(raw).hexdigest() == value["sha256"], f"{context}: sha256 mismatch")
    return raw


def segmentation_class(data_coding: int) -> tuple[int, int, int]:
    if data_coding in {3, 6, 7, 10}:
        return 8, 140, 134
    if data_coding in {2, 4, 5, 8, 9, 13, 14}:
        return 16, 70, 134
    return 7, 160, 153


def validate_segmentation(document: dict) -> None:
    cases_digest = hashlib.sha256(
        json.dumps(document["cases"], sort_keys=True, separators=(",", ":")).encode("utf-8")
    ).hexdigest()
    require(cases_digest == EXPECTED_SEGMENTATION_CASES_SHA256, "segmentation: trusted corpus fingerprint")
    for case in document["cases"]:
        context = f"segmentation/{case['id']}"
        request = case["input"]
        payload = validate_bytes(request["payload"], f"{context}/input")
        bits, single_limit, slice_bytes = segmentation_class(request["data_coding"])
        require(
            case["classification"] == {
                "bits": bits,
                "single_limit": single_limit,
                "multipart_payload_bytes": slice_bytes,
            },
            f"{context}: classification",
        )
        single_bytes = single_limit * 2 if bits == 16 else single_limit
        multipart = len(payload) > single_bytes
        expected_count = 1
        if multipart:
            expected_count = min((len(payload) + slice_bytes - 1) // slice_bytes, request["max_parts"])
        require(len(case["parts"]) == expected_count, f"{context}: part count")

        emitted = bytearray()
        if multipart:
            expected_reference = (request["initial_reference"] % 255) + 1
            require(case["emitted_reference"] == expected_reference, f"{context}: reference")
        else:
            expected_reference = 0
            require(case["emitted_reference"] is None, f"{context}: single reference")
        for index, part in enumerate(case["parts"], 1):
            part_context = f"{context}/part/{index}"
            require(part["sequence"] == index, f"{part_context}: sequence")
            raw = validate_bytes(part["payload"], f"{part_context}/payload")
            short_message = validate_bytes(part["short_message"], f"{part_context}/short_message")
            start = (index - 1) * slice_bytes if multipart else 0
            stop = index * slice_bytes if multipart else len(payload)
            require(raw == payload[start:stop], f"{part_context}: payload slice")
            emitted.extend(raw)
            if not multipart:
                require(part["sar"] is None and part["udh"] is None, f"{part_context}: single metadata")
                require(short_message == raw, f"{part_context}: single short_message")
            elif request["split_method"] == "sar":
                require(part["udh"] is None, f"{part_context}: unexpected UDH")
                require(
                    part["sar"] == {"reference": expected_reference, "total": expected_count, "sequence": index},
                    f"{part_context}: SAR",
                )
                require(short_message == raw, f"{part_context}: SAR short_message")
            else:
                require(part["sar"] is None, f"{part_context}: unexpected SAR")
                header = bytes((5, 0, 3, expected_reference, expected_count, index))
                require(part["udh"]["reference"] == expected_reference, f"{part_context}: UDH reference")
                require(part["udh"]["total"] == expected_count, f"{part_context}: UDH total")
                require(part["udh"]["sequence"] == index, f"{part_context}: UDH sequence")
                require(validate_bytes(part["udh"]["bytes"], f"{part_context}/udh") == header, f"{part_context}: UDH")
                require(short_message == header + raw, f"{part_context}: UDH short_message")

        require(bytes(emitted) == payload[: len(emitted)], f"{context}: emitted prefix")
        require(case["consumed_payload_bytes"] == len(emitted), f"{context}: consumed bytes")
        require(case["truncated"] == (len(emitted) != len(payload)), f"{context}: truncated flag")


def validate_compact_bytes(descriptor: dict, context: str) -> None:
    raw = base64.b64decode(descriptor["base64"], validate=True)
    require(len(raw) == descriptor["length"], f"{context}: length")
    require(hashlib.sha256(raw).hexdigest() == descriptor["sha256"], f"{context}: sha256")


def validate_routing_filters(document: dict) -> None:
    corpus = {"compatibility": document["compatibility"], "cases": document["cases"]}
    digest = hashlib.sha256(
        json.dumps(corpus, sort_keys=True, separators=(",", ":")).encode("utf-8")
    ).hexdigest()
    require(digest == EXPECTED_ROUTING_FILTER_CORPUS_SHA256, "routing-filters: corpus fingerprint")
    require(
        document["compatibility"] == {
            "connector": ["mo"],
            "date_interval": ["mt", "mo"],
            "destination_addr": ["mt", "mo"],
            "group": ["mt"],
            "short_message": ["mt", "mo"],
            "source_addr": ["mt", "mo"],
            "tag": ["mt", "mo"],
            "time_interval": ["mt", "mo"],
            "transparent": ["mt", "mo"],
            "user": ["mt"],
        },
        "routing-filters: compatibility map",
    )
    valid_types = set(document["compatibility"])
    for case in document["cases"]:
        context = f"routing-filters/{case['id']}"
        require(case["source"] == "jasmin/routing/Filters.py", f"{context}: source")
        require(case["filter"]["type"] in valid_types, f"{context}: filter type")
        route = case["routable"]
        require(route["direction"] in {"mt", "mo"}, f"{context}: direction")
        datetime.fromisoformat(route["timestamp"])
        require(all(isinstance(tag, str) for tag in route["tags"]), f"{context}: normalized tags")
        for field in ("source_addr", "destination_addr", "short_message", "message_payload"):
            if route[field] is not None:
                validate_compact_bytes(route[field], f"{context}/{field}")
        expected = case["expected"]
        if expected["error_type"] is None:
            require(isinstance(expected["matched"], bool), f"{context}: matched outcome")
        else:
            require(expected["matched"] is None, f"{context}: errored match must be null")
            require(expected["error_type"] == "KeyError", f"{context}: legacy error type")


def validate_routing_tables(document: dict) -> None:
    digest = hashlib.sha256(
        json.dumps(document["cases"], sort_keys=True, separators=(",", ":")).encode("utf-8")
    ).hexdigest()
    require(digest == EXPECTED_ROUTING_TABLE_CASES_SHA256, "routing-tables: corpus fingerprint")
    for case in document["cases"]:
        context = f"routing-tables/{case['id']}"
        require(case["source"] == "jasmin/routing/RoutingTables.py", f"{context}: source")
        require(case["direction"] in {"mt", "mo"}, f"{context}: direction")
        orders = case["expected"]["orders"]
        require(orders == sorted(orders, reverse=True), f"{context}: descending orders")
        require(len(orders) == len(set(orders)), f"{context}: unique orders")
        error_type = case["expected"]["error_type"]
        require(error_type in {None, "InvalidRoutingTableParameterError"}, f"{context}: error type")
        require(all(isinstance(value, bool) for value in case["expected"]["remove_results"]), f"{context}: remove results")


def validate_multi_connector_routes(document: dict) -> None:
    digest = hashlib.sha256(json.dumps(document["cases"], sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    require(digest == EXPECTED_MULTI_CONNECTOR_CASES_SHA256, "multi-connector-routes: corpus fingerprint")
    for case in document["cases"]:
        context = f"multi-connector-routes/{case['id']}"
        require(case["source"] == "jasmin/routing/Routes.py", f"{context}: source")
        require(case["input"]["policy"] in {"random", "failover"}, f"{context}: policy")
        require(case["input"]["direction"] in {"mt", "mo"}, f"{context}: direction")
        require(case["error_type"] in {None, "InvalidRouteParameterError"}, f"{context}: error")


def main() -> int:
    documents = {}
    for surface, path in FIXTURES.items():
        require(path.is_file(), f"missing fixture: {path}")
        document = json.loads(path.read_text(encoding="utf-8"))
        validate_common(surface, document)
        documents[surface] = document

    validate_http(documents["http"])
    validate_smpp(documents["smpp"])
    validate_smpp_client_pacing(documents["smpp-client-pacing"])
    validate_smpp_client_readiness(documents["smpp-client-readiness"])
    validate_amqp(documents["amqp"])
    validate_redis(documents["redis"])
    validate_segmentation(documents["segmentation"])
    validate_routing_filters(documents["routing-filters"])
    validate_routing_tables(documents["routing-tables"])
    validate_multi_connector_routes(documents["multi-connector-routes"])

    require(COVERAGE.is_file(), f"missing coverage map: {COVERAGE}")
    require(
        hashlib.sha256(COVERAGE.read_bytes()).hexdigest() == EXPECTED_COVERAGE_SHA256,
        "coverage map differs from approved manifest",
    )
    with COVERAGE.open(newline="", encoding="utf-8") as handle:
        rows = list(csv.DictReader(handle))
    mapped = {(row["surface"], row["case_id"]) for row in rows}
    expected = {
        (surface, case["id"])
        for surface, document in documents.items()
        for case in document["cases"]
    }
    require(len(mapped) == len(rows), "coverage map has duplicate surface/case_id rows")
    require(
        mapped == expected,
        f"coverage mismatch missing={sorted(expected - mapped)} extra={sorted(mapped - expected)}",
    )
    require(all(row["coverage"] in {"partial", "full"} for row in rows), "invalid coverage state")
    require(all(row["contract_ids"] for row in rows), "fixture without contract mapping")

    counts = ", ".join(f"{name}={len(doc['cases'])}" for name, doc in documents.items())
    print(f"fixtures=valid baseline={BASELINE} {counts}, coverage_rows={len(rows)}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

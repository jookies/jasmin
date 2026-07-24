#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
RUNNER="$ROOT/scripts/compat/run_baseline_tests.sh"

JASMIN_COMPAT_PROJECT="jasmin-go-capture-http-$$" "$RUNNER" python scripts/compat/capture_http_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-smpp-$$" "$RUNNER" python scripts/compat/capture_smpp_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-smpps-bind-state-$$" "$RUNNER" python scripts/compat/capture_smpps_bind_state_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-amqp-$$" "$RUNNER" python scripts/compat/capture_amqp_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-redis-$$" "$RUNNER" python scripts/compat/capture_redis_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-segmentation-$$" "$RUNNER" python scripts/compat/capture_segmentation_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-routing-filters-$$" "$RUNNER" python scripts/compat/capture_routing_filters_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-routing-tables-$$" "$RUNNER" python scripts/compat/capture_routing_tables_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-multi-connector-routes-$$" "$RUNNER" python scripts/compat/capture_multi_connector_routes_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-smpp-client-pacing-$$" "$RUNNER" python scripts/compat/capture_smpp_client_pacing_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-smpp-client-readiness-$$" "$RUNNER" python scripts/compat/capture_smpp_client_readiness_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-smpp-client-error-retry-$$" "$RUNNER" python scripts/compat/capture_smpp_client_error_retry_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-smpp-client-response-publish-$$" "$RUNNER" python scripts/compat/capture_smpp_client_response_publish_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-billing-enforcement-$$" "$RUNNER" python scripts/compat/capture_billing_enforcement_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-late-billing-$$" "$RUNNER" python scripts/compat/capture_late_billing_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-billing-persistence-$$" "$RUNNER" python scripts/compat/capture_billing_persistence_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-router-amqp-subscriptions-$$" "$RUNNER" python scripts/compat/capture_router_amqp_subscriptions_golden.py
python3 "$ROOT/scripts/compat/verify_fixtures.py"

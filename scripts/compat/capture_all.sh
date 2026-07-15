#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
RUNNER="$ROOT/scripts/compat/run_baseline_tests.sh"

JASMIN_COMPAT_PROJECT="jasmin-go-capture-http-$$" "$RUNNER" python scripts/compat/capture_http_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-smpp-$$" "$RUNNER" python scripts/compat/capture_smpp_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-amqp-$$" "$RUNNER" python scripts/compat/capture_amqp_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-redis-$$" "$RUNNER" python scripts/compat/capture_redis_golden.py
JASMIN_COMPAT_PROJECT="jasmin-go-capture-segmentation-$$" "$RUNNER" python scripts/compat/capture_segmentation_golden.py
python3 "$ROOT/scripts/compat/verify_fixtures.py"

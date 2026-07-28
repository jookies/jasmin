#!/usr/bin/env bash
# Print a stable hash of everything the admin UI bundle is built from.
#
# The CI guard used to rebuild the bundle and byte-compare it against the
# committed dist/. That is not a sound check: the minifier renames identifiers
# between environments (cW -> pW, nje -> eje), so an untouched bundle can
# produce a different file and fail the job, while a genuinely stale bundle can
# pass whenever the two environments happen to agree.
#
# Hashing the *inputs* instead is deterministic, and it is the question the job
# actually wants answered: was dist/ built from the source that is committed
# next to it?
set -euo pipefail

cd "$(dirname "$0")/.."

{
  # Sorted for a stable order across filesystems.
  find web/src -type f -print0 | sort -z | xargs -0 shasum -a 256
  shasum -a 256 web/package.json web/package-lock.json web/index.html \
                web/vite.config.ts web/tsconfig.json
} | shasum -a 256 | cut -d' ' -f1

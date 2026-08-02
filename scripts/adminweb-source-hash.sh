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

# Byte order, not locale order. `sort` collates by LC_COLLATE, so a developer
# machine on en_US.UTF-8 sorts case-insensitively (adapter.ts, AnalyticsPanel.tsx)
# while CI on C/POSIX sorts by byte (AnalyticsPanel.tsx, adapter.ts). The two
# then hash the same files in different orders and disagree about a bundle that
# is perfectly current — which is exactly the false failure this script replaced
# the byte-comparison to avoid.
export LC_ALL=C

cd "$(dirname "$0")/.."

{
  # Sorted for a stable order across filesystems *and* locales.
  find web/src -type f -print0 | sort -z | xargs -0 shasum -a 256
  shasum -a 256 web/package.json web/package-lock.json web/index.html \
                web/vite.config.ts web/tsconfig.json
} | shasum -a 256 | cut -d' ' -f1

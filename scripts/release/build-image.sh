#!/usr/bin/env bash
# Build (and optionally push) the multi-architecture Synevyr platform image.
#
#   scripts/release/build-image.sh --dry-run 0.1.0   # build + load host arch only
#   scripts/release/build-image.sh 0.1.0             # build linux/amd64+arm64, push
#
# Why multi-arch: an Apple Silicon Mac must pull linux/arm64 and an Ubuntu or
# WSL2 host linux/amd64. A single-arch image still *runs* on the other via
# emulation, but slowly and with an alarming warning on every start.
#
# The Dockerfile pins its build stage to $BUILDPLATFORM and cross-compiles, so
# both architectures compile natively on this machine; only the thin runtime
# layer is materialised per architecture.
#
# Flags:
#   --dry-run        build without pushing; --load the host architecture so the
#                    image can be smoke-tested locally before anything is
#                    published. Multi-arch cannot be --load'ed (the local image
#                    store holds one manifest), so a dry run is host-arch only.
#   --allow-dirty    permit a build from a dirty working tree. Off by default:
#                    the image records the commit in an OCI label, and a label
#                    that points at a commit not matching the built bytes is
#                    worse than no label.
#   --no-latest      push only the version tag, not :latest.
#   --platforms LIST override the default linux/amd64,linux/arm64.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${REPO_ROOT}"

IMAGE="${SYNEVYR_IMAGE:-aroksetx/synevyr-messaging-platform}"
PLATFORMS="linux/amd64,linux/arm64"
BUILDER="synevyr-release"
DRY_RUN=0
ALLOW_DIRTY=0
PUSH_LATEST=1
VERSION=""

info() { printf '\033[1;34m==>\033[0m %s\n' "$1"; }
warn() { printf '\033[1;33m!!\033[0m %s\n' "$1" >&2; }
fail() { printf '\033[1;31mxx\033[0m %s\n' "$1" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run)     DRY_RUN=1 ;;
    --allow-dirty) ALLOW_DIRTY=1 ;;
    --no-latest)   PUSH_LATEST=0 ;;
    --platforms)   shift; PLATFORMS="${1:?--platforms needs a value}" ;;
    -h|--help)     sed -n '2,26p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'; exit 0 ;;
    -*)            fail "unknown flag '$1'" ;;
    *)             VERSION="$1" ;;
  esac
  shift
done

[ -n "${VERSION}" ] || fail "usage: $0 [--dry-run] [--allow-dirty] [--no-latest] <version>"
command -v docker >/dev/null 2>&1 || fail "docker not found."
docker buildx version >/dev/null 2>&1 || fail "docker buildx not available."

REVISION="$(git rev-parse HEAD 2>/dev/null || echo unknown)"
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  if [ "${ALLOW_DIRTY}" -eq 1 ]; then
    warn "working tree is dirty; the revision label will not match the built bytes."
    REVISION="${REVISION}-dirty"
  elif [ "${DRY_RUN}" -eq 0 ]; then
    fail "working tree is dirty. Commit, stash, or pass --allow-dirty."
  else
    REVISION="${REVISION}-dirty"
  fi
fi
# Not Date.now() in a script for style reasons — the label must be the real
# build time, and reproducibility here is not a goal.
CREATED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

# The default "docker" driver cannot build more than one platform at a time.
# A docker-container builder can, and is reused across runs for its cache.
if [ "${DRY_RUN}" -eq 0 ]; then
  if ! docker buildx inspect "${BUILDER}" >/dev/null 2>&1; then
    info "creating buildx builder '${BUILDER}' (docker-container driver)"
    docker buildx create --name "${BUILDER}" --driver docker-container --bootstrap >/dev/null
  fi
  BUILDER_ARGS=(--builder "${BUILDER}" --platform "${PLATFORMS}" --push)
  TARGET_DESC="${PLATFORMS} -> push"
else
  # --load writes into the local image store, which holds a single manifest.
  BUILDER_ARGS=(--load)
  TARGET_DESC="host architecture -> local image store (no push)"
fi

TAGS=(--tag "${IMAGE}:${VERSION}")
if [ "${PUSH_LATEST}" -eq 1 ] && [ "${DRY_RUN}" -eq 0 ]; then
  TAGS+=(--tag "${IMAGE}:latest")
fi

info "image      ${IMAGE}:${VERSION}"
info "revision   ${REVISION}"
info "target     ${TARGET_DESC}"

docker buildx build \
  "${BUILDER_ARGS[@]}" \
  "${TAGS[@]}" \
  --file docker/Dockerfile.gateway \
  --build-arg "VERSION=${VERSION}" \
  --build-arg "REVISION=${REVISION}" \
  --build-arg "CREATED=${CREATED}" \
  .

if [ "${DRY_RUN}" -eq 1 ]; then
  info "built locally as ${IMAGE}:${VERSION} — nothing was pushed."
  info "smoke test it:  SYNEVYR_VERSION=${VERSION} docker compose -f docker-compose.release.yml up -d"
else
  info "pushed. Verify both architectures are present:"
  info "  docker buildx imagetools inspect ${IMAGE}:${VERSION}"
fi

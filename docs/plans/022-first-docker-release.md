# Plan 022 — First published Docker image (`aroksetx/synevyr-messaging-platform`)

- **Date:** 2026-07-30
- **Status:** done
- **Summary:** Publish the first multi-arch (`linux/amd64` + `linux/arm64`) platform image to Docker Hub as `aroksetx/synevyr-messaging-platform:0.1.0` (+ `:latest`), and ship a pull-only compose file so an operator on Ubuntu, macOS, or Windows can run the whole stack without a source checkout or a Go toolchain.
- **Related:** [`plans/016-release-readiness-and-python-deprecation.md`](016-release-readiness-and-python-deprecation.md), [`adr/001-admin-provisioning-sqlite.md`](../adr/001-admin-provisioning-sqlite.md), `docker-compose.prod.yml`, `configs/gateway.production.md`

## Context

Everything the platform needs to be deployed already exists in-repo — `docker/Dockerfile.gateway`, `docker/Dockerfile.fakesmsc`, `docker-compose.prod.yml`, `.env.example`, `scripts/deploy/setup.sh`, `deploy/BACKUP.md` — but all of it builds **from source**. There is no published artifact. `.github/workflows/docker.yml` is deliberately disabled because it still pointed at the legacy Python image and the `jookies/jasmin` namespace.

That leaves three gaps for a first release:

1. **No published image.** Anyone wanting to run the platform needs the repository, a Docker build, and (for `docker-compose.prod.yml`) a second local build for `bootstrap-smsc`.
2. **Single-architecture assumption.** The current Dockerfiles build with `FROM golang:1.26` and compile for the *target* platform. Under `buildx --platform linux/amd64,linux/arm64` on an Apple Silicon host, the amd64 leg runs the entire Go compile under QEMU emulation — minutes of wall clock for a build that is trivially cross-compilable (`CGO_ENABLED=0`, pure-Go SQLite per ADR-001).
3. **`bootstrap-smsc` has no published counterpart.** `docker-compose.prod.yml` hard-requires it (the `http+smppc` role blocks startup until every configured SMPPc connector binds), so a pull-only deployment is impossible while the simulator only exists as a local build.

Desired outcome: `docker compose -f docker-compose.release.yml up -d` on a clean Ubuntu / macOS / Windows box with Docker Desktop or Docker Engine, pulling published images only, coming up genuinely healthy.

## Approach

One Docker Hub repository, `aroksetx/synevyr-messaging-platform`, holding **all three** command binaries (`synevyr-gateway`, `synevyr-fake-smsc`, `synevyr-partner-sim`) with `ENTRYPOINT` defaulting to the gateway. The compose `bootstrap-smsc` service then runs the *same* image with an `entrypoint:` override instead of needing a second published repository. The two extra static binaries cost roughly 15 MB in a shared layer and remove the only remaining local-build requirement.

The build stage is pinned to the **build** platform (`--platform=$BUILDPLATFORM`) and cross-compiles via `GOOS`/`GOARCH` from the `TARGETPLATFORM` args BuildKit injects. Only the thin runtime stage is materialised per architecture. This is correct precisely because the tree is `CGO_ENABLED=0` end to end.

Key trade-offs accepted:

- **One repo, three binaries** over three repos: simpler release surface and a single tag to reason about, at the cost of a slightly larger image and a non-obvious `entrypoint:` override in compose (documented inline).
- **`0.1.0` version line** even though the repository already carries inherited upstream Jasmin tags `0.10.x` / `0.11.0`. Those tags describe the *Python* implementation this fork replaced; the Go platform starts its own line. Called out in `README.md` so the apparent regression is not read as one.
- **Pull-only compose as a separate file** (`docker-compose.release.yml`) rather than adding `pull_policy` to `docker-compose.prod.yml`. Compose v2 builds a service that declares `build:` whenever the image is absent locally, so a file that must never build cannot also carry a `build:` key. The duplication is deliberate.
- **Baked default config.** The image already ships `configs/gateway.example.json` at `/etc/synevyr/gateway.json`. The release compose mounts nothing by default and instead points the gateway at the production example config baked in at a second path, so a first boot needs no bind mount at all — bind mounts are the single most common Windows-specific failure (path translation, file-vs-directory, permissions).

## Steps

### Step 1: Cross-compiling, multi-binary, labelled gateway image

- **Files:** `docker/Dockerfile.gateway`
- **Changes:**
  - Build stage becomes `FROM --platform=$BUILDPLATFORM golang:1.26 AS build`, declaring `ARG TARGETOS`, `ARG TARGETARCH`, and compiling with `GOOS=$TARGETOS GOARCH=$TARGETARCH CGO_ENABLED=0`.
  - Build all three commands (`./cmd/synevyr-gateway`, `./cmd/synevyr-fake-smsc`, `./cmd/synevyr-partner-sim`) into `/out/`.
  - Copy all three into `/usr/local/bin/` in the runtime stage; `ENTRYPOINT` stays `/usr/local/bin/synevyr-gateway`.
  - Bake `configs/gateway.production.example.json` to `/etc/synevyr/gateway.production.example.json` alongside the existing dev default, so the release compose can select it without a bind mount.
  - Add OCI labels (`org.opencontainers.image.title/description/source/licenses/version/revision/created`) driven by `ARG VERSION`/`ARG REVISION`/`ARG CREATED`.
  - Add a `HEALTHCHECK` probing `http://127.0.0.1:1401/health` so the image is self-describing to any orchestrator, not only to our compose files.
  - Keep the `python:3.12-slim` runtime (interceptor runner only) — it is published for both amd64 and arm64, so it does not block multi-arch.
- **Verify:**
  `docker buildx build --platform linux/amd64,linux/arm64 -f docker/Dockerfile.gateway --build-arg VERSION=0.1.0 -t synevyr-test:multiarch . ` completes without QEMU-emulated Go compilation (build log shows one `go build` per target, run natively), and a local single-arch load passes `docker run --rm --entrypoint /usr/local/bin/synevyr-gateway synevyr-test:local --check-config --config /etc/synevyr/gateway.production.example.json`.

### Step 2: Line-ending guard for Windows checkouts

- **Files:** `.gitattributes` (new)
- **Changes:** Force `LF` for `*.sh`, `*.py`, `Dockerfile*`, `*.yml`, `*.json`, `*.go`. Without this, a Windows checkout with `core.autocrlf=true` produces CRLF shebang lines; `scripts/interceptor_runner.py` and `scripts/deploy/*.sh` then fail inside a Linux container with the classic `bad interpreter: no such file or directory`.
- **Verify:** `git check-attr text eol -- scripts/interceptor_runner.py docker/Dockerfile.gateway` reports `eol: lf` for both.

### Step 3: Pull-only release compose

- **Files:** `docker-compose.release.yml` (new)
- **Changes:** Self-contained single-node stack — `postgres:16-alpine`, `rabbitmq:3-management-alpine`, `redis:7-alpine`, `bootstrap-smsc` (the platform image with `entrypoint: /usr/local/bin/synevyr-fake-smsc`), and `gateway`. No `build:` keys anywhere. `image:` defaults to `aroksetx/synevyr-messaging-platform:${SYNEVYR_VERSION:-0.1.0}`. Same port policy as `docker-compose.prod.yml`: data plane (1401 / 8080 / 2775) public, admin web UI (8404), admin REST (8405) and jCli (8990) bound to `127.0.0.1` only. Named volumes only — no host bind mounts on the default path. `name: synevyr` pins the compose project so it cannot collide with the other three stacks' volumes.
- **Verify:** `docker compose -f docker-compose.release.yml config -q` parses clean, and `grep -c "build:" docker-compose.release.yml` returns 0.

### Step 4: Release environment template

- **Files:** `.env.release.example` (new)
- **Changes:** Only the variables `docker-compose.release.yml` actually consumes — the required secrets (`POSTGRES_PASSWORD`, `RABBITMQ_DEFAULT_PASS`, `ADMIN_TOKEN`, `ADMIN_WEB_PASSWORD`, `JCLI_PASSWORD`, `SMSC_PASSWORD`, `SMPPS_USER_PASSWORD`), the port overrides, and `SYNEVYR_VERSION`. Carries the SMPP-3.4 8-character password constraint note verbatim from `.env.example`, and cross-platform generation commands (`openssl` for Linux/macOS, PowerShell equivalent for Windows).
- **Verify:** every `${VAR}` referenced in `docker-compose.release.yml` appears in `.env.release.example`, checked by a `grep -o '\${[A-Z_]*' | sort -u` diff of the two files.

### Step 5: Release build/push script

- **Files:** `scripts/release/build-image.sh` (new)
- **Changes:** One script that creates/uses a `docker-container` buildx builder, stamps `VERSION`/`REVISION`/`CREATED` build args, builds `linux/amd64,linux/arm64`, and pushes both the version tag and `latest`. Supports `--dry-run` (build without `--push`, `--load` the host arch only) so the image can be smoke-tested before anything reaches Docker Hub. Refuses to push when the working tree is dirty unless `--allow-dirty` is passed.
- **Verify:** `bash scripts/release/build-image.sh --dry-run 0.1.0` produces a locally loaded image; `docker image inspect` shows the OCI labels populated.

### Step 6: Pre-push smoke test on the real artifact

- **Files:** none (runtime verification)
- **Changes:** Bring the release stack up against the locally built image, confirm the gateway reaches `healthy`, submit a message through the HTTP front door to the bundled `bootstrap-smsc`, and confirm the admin web UI serves.
- **Verify:**
  - `docker compose -f docker-compose.release.yml up -d` → `docker compose -f docker-compose.release.yml ps` shows `gateway` as `healthy`.
  - `curl "http://127.0.0.1:1401/send?username=…&password=…&to=…&content=hello"` returns a message id.
  - `curl -sI http://127.0.0.1:8404/` returns 200 and serves the SPA.
  - Teardown with `down -v` leaves no volumes.

### Step 7: Push to Docker Hub

- **Files:** none
- **Changes:** `bash scripts/release/build-image.sh 0.1.0` — multi-arch build, push `:0.1.0` and `:latest`.
- **Verify:** `docker buildx imagetools inspect aroksetx/synevyr-messaging-platform:0.1.0` lists both `linux/amd64` and `linux/arm64` manifests; `docker manifest inspect` on `:latest` returns the same digest.

### Step 8: Documentation

- **Files:** `README.md`, `docs/README.md`, `docs/worklog.md`
- **Changes:**
  - `README.md`: a "Run the published image" quickstart above the existing source-build Deployment section, with per-OS prerequisites (Docker Engine + Compose v2 on Ubuntu; Docker Desktop on macOS/Windows), the `0.1.0` versioning note versus the inherited `0.10.x`/`0.11.0` Python tags, and the file-table rows for the two new files.
  - `docs/README.md`: link plan 022.
  - `docs/worklog.md`: entry for the release.
- **Verify:** README quickstart commands are the exact ones executed in Step 6; no stale claim that images must be built locally.

## End-to-end verification

On a machine with no repository checkout, only the release compose file and env template:

```
curl -O https://raw.githubusercontent.com/pumpitspace/jasmin/master/docker-compose.release.yml
curl -o .env https://raw.githubusercontent.com/pumpitspace/jasmin/master/.env.release.example
# fill the required secrets
docker compose -f docker-compose.release.yml up -d
docker compose -f docker-compose.release.yml ps          # gateway healthy
curl http://127.0.0.1:1401/health                        # 200
```

Architecture coverage proven by `docker buildx imagetools inspect` showing both manifests, which is what makes an Apple Silicon Mac pull arm64 natively and an Ubuntu/WSL2 host pull amd64.

## Rollback

Docker Hub tags are mutable: a bad `:0.1.0` can be overwritten by rebuilding and re-pushing, and `:latest` can be re-pointed at any previously pushed digest with `docker buildx imagetools create -t …:latest …@sha256:…`. Nothing in the repository change is destructive — the new files are additive, and `docker/Dockerfile.gateway` reverts with a single `git revert`. `docker-compose.prod.yml`, `docker-compose.gateway.yml`, and `docker-compose.gateway-ha.yml` are untouched, so every existing source-build workflow keeps working.

## Risks

- **Public image, baked defaults.** The image ships an example config with `admin`/`jcliadmin` usernames and `env:`-referenced passwords. The passwords are *not* baked (they resolve from the environment at startup and the gateway refuses to start when unresolved), but the usernames and the port layout are public knowledge from the moment this is pushed. Mitigated by keeping admin surfaces loopback-only in the release compose and stating it in the README.
- **`latest` drift.** Pushing `:latest` on a first release means a future `0.2.0` silently changes what `latest` resolves to for anyone who pinned nothing. Documented; the release compose defaults to the pinned version, not `latest`.
- **arm64 is genuinely less exercised.** CI runs on `ubuntu-latest` (amd64). The arm64 leg is cross-compiled and only smoke-tested locally on Apple Silicon in Step 6. A pure-Go, `CGO_ENABLED=0` build makes an architecture-specific defect unlikely but not impossible.
- **`bootstrap-smsc` is not a real SMSC.** It accepts any bind and `ESME_ROK`s every submit. A first-boot stack looks fully healthy while delivering nothing to a real network. This is pre-existing behaviour (`configs/gateway.production.md`) but it becomes far more visible once a one-command quickstart exists — the README quickstart must say so at the point of first success, not in a footnote.
- **Docker Hub rate limits** apply to anonymous pulls of the base images (`postgres`, `rabbitmq`, `redis`) on the operator's side, not to ours. Noted in the README troubleshooting line.

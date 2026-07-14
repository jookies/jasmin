# Approved project decisions

Status: approved by project owner on 2026-07-14.
Baseline: Jasmin commit `0aac58e466d583d0f0436df7b8afa3dc96191263`.

## Product and compatibility

1. Working name: `jasmin-go`; final branding is deferred pending trademark review.
2. Rewrite the complete working Jasmin functionality in Go.
3. Compatibility model is hybrid:
   - public HTTP/REST responses and SMPP wire behavior are strict compatibility surfaces;
   - internal implementation may change when differential contracts remain green.
4. Preserve during parity: SMPP client/server, HTTP API, REST/batch, callbacks, routing, billing, DLR/MO, jCli, PB APIs, INI configuration, profile import, AMQP/Redis behavior and existing metrics.
5. Unsupported upstream stubs are documented, not presented as working parity features.
6. Migration is gradual by connector/user/CID; one side-effecting owner per partition.

## Runtime dependencies

1. RabbitMQ remains the parity broker.
2. Redis remains the parity backend for DLR correlation, TTL and transient multipart state.
3. Go must never decode untrusted Python pickle.
4. Temporary trusted Python bridges are allowed for PB/AMQP/profile/interceptor compatibility and for allowlisted Redis values during partitioned cutover.
5. Pickled Redis multipart state remains owned by Python or translated by the bridge; Go never decodes it directly.

## Persistence strategy

1. Domain and control-plane code depend on storage interfaces, not concrete databases.
2. Legacy file profiles remain importable through an offline Python exporter.
3. SQLite is the default for local development, tests and single-node control-plane deployments.
4. PostgreSQL is the production HA control-plane implementation.
5. A separate NoSQL database is not introduced without a measured access pattern that Redis/SQLite/PostgreSQL cannot satisfy.
6. Redis is not the source of truth for durable users/routes/billing revisions.

## Security and migration

1. Provide explicit `compat` and `secure` deployment profiles.
2. Legacy MD5 verification remains available during migration; successful authentication can rehash into Argon2id in modern storage.
3. Existing Python interceptors run in an isolated compatibility sidecar until a new plugin ABI is approved.
4. Callback egress restrictions are opt-in in compatibility mode and recommended/default in secure mode.

## Billing and future work

1. Compatibility billing must reproduce legacy decisions and visible rounding.
2. Improved billing is a separate fixed-point, idempotent ledger mode.
3. New routing, multi-tenancy, admin API/UI, analytics and plugin systems begin only after parity gates pass.

## Deployment

- Initial targets: Docker Compose and Kubernetes.
- Production architectures: Linux AMD64 and ARM64.
- Packages/systemd support are deferred until parity.
- Repository remains public unless the owner changes this decision.

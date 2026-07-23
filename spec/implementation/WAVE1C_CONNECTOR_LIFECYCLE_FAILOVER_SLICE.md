# Wave 1C — Connector Lifecycle and Failover Closure

## Goal

Close the bounded Task 1.5 production path from route selection through observed connector availability, ordered failover, SMPP reconnect controls, graceful unbind, TLS verification, and connector-channel QoS without changing RouterPB's frozen no-QoS topology.

## Scope

- Primary rows: `SC-003`, `SC-004`, `SC-007`, `S-005`–`S-007`, `A-010`.
- Dependencies only: `SC-005`, `SC-006`, `RR-008`, `O-003`.
- Baseline implementation candidate: `8a0b7bb1308fcc16797a16569e43b14c256546a6`.

## Oracle/test harness

1. Preserve the frozen Python oracle and existing fixture bytes.
2. Add production-path tests for route-owned ordered connector pools, observed-bound availability, aggregate-stable selection, persistence/reload, and deterministic exhaustion.
3. Add lifecycle tests proving connection-failure retry and established-connection-loss retry have independent enable flags and delays; disabled retry performs no second dial.
4. Cover typed control-PDU correlation, configured unbind grace, trusted/untrusted TLS, configurable prefetch placement, and concurrent SQLite route migration.

## Production implementation

1. Select one observed-bound connector before segmentation, charging, envelope construction, and durable admission; all parts share that connector.
2. Persist the ordered connector pool on each route and migrate legacy singular rows to `[primary]`.
3. Use independent connection-failure and connection-loss retry controls with legacy-compatible defaults.
4. Correlate control responses by sequence and expected command; use configured transaction timeout for graceful unbind.
5. Apply verified TLS on the SMSC dial path and configurable QoS on every connector AMQP consumer generation.

## Non-goals

- Exactly-once external SMSC delivery.
- Authoritative PostgreSQL control-plane storage and multi-instance reconciliation.
- Inbound DLR/MO execution.
- Promotion of matrix rows without complete fixture and production-flow evidence.

## Acceptance criteria

1. Production failover selection occurs before charging and durable CID-bound admission; unavailable connectors consume neither charge nor pacing slots.
2. Multipart submissions select one connector for the whole aggregate.
3. Independent retry flags are honored for initial/bind failure versus established connection loss, with cancellation-safe configured delays.
4. Graceful unbind, TLS verification, route-pool persistence/migration, and connector QoS tests pass under race where applicable.
5. Focused tests pass repeatedly; full Go, race, vet, build, Python fixture/schema/registry, frozen-fixture diff, secret scan, and workspace checks pass.
6. A stable exact candidate receives a Ralph code boundary audit with no unresolved High/Medium finding.
7. Plan plus correction are pushed to `go-rewrite`; exact-SHA GitHub Actions passes 4/4; local/tracking/remote SHA match and workspace is clean before the main orchestrator emits a LoopKey.

## Affected paths

- `internal/core/smppc/{config,connector,session}.go` and tests
- `internal/core/{submit_service,routingtable,routepolicy}` and tests
- `internal/app/{gateway,outbound}` and tests
- `internal/infra/storage/sqlite_routing.go` and tests
- `internal/transport/{amqpcompat,smppwire}` and tests
- `spec/compatibility/GO_MACRO_TESTS.csv`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

## Verification

- The pre-correction remote candidate `8a0b7bb1308fcc16797a16569e43b14c256546a6` passed exact-SHA GitHub Actions run `29969854699` with `4/4` jobs; the boundary audit then found three in-scope defects rather than treating that green run as closure.
- Corrected executable candidate: `c64baaec6065dfe759dd699e4a29547973780fec` (tree `11763dcc27e57b5d032b901e7ac5c004ebe5ee07`). It rejects pooled standalone routes without observed availability, forbids disabling SMSC TLS certificate verification, validates every route-pool connector against route direction, and honors independent connection-failure/loss retry flags with legacy default `true`.
- Focused reconnect and correction tests passed `20` times under `-race`; full `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...` passed.
- Contract registry remains `205 = 129 INVENTORIED + 55 GO-PARTIAL + 18 MATCH + 3 GO-COMPLETE`; fixture integrity, JSON Schemas, the `1,039`-test manifest, all `58` Python compatibility tests, frozen-fixture diff, and candidate-diff secret scan passed.
- The exact-candidate Ralph code council identified three High/Medium boundary defects. After one correction batch and invalidated-gate rerun, the focused final council returned `PASS` for the corrected SHA/tree.
- Publication, corrected exact-SHA `4/4` CI, final local/tracking/remote equality, clean-workspace verification, and the terminal LoopKey remain required.

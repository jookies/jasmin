# Phase 2.34 — SMPP Connector Pacing Integration

## Goal

Close the production-wiring subset deferred by Phase 2.33B by applying the already fixture-proven `Pacer` to every envelope accepted by the concrete `submit.sm.<CID>` connector consumer before expiry/readiness checks and SMPP submission. Preserve the fixture-supported pacing-before-terminal-checks order, context-cancellable shutdown, and one-attempt AMQP settlement ownership without claiming unimplemented pre-pacing pickle/property parity or promoting unproven throughput, retry, or lifecycle surfaces.

## Selection evidence

- `PHASE2_33B_SMPP_SESSION_RECOVERY_SLICE.md` explicitly names Pacer integration as Phase 2.34.
- `SMPP_MATRIX.md` keeps `SC-004` at `GO-PARTIAL` because AMQP ownership, socket submission, settlement, and response correlation are not connected to the fixture-proven pacing decision.
- `SMPPClientSMListener.submit_sm_callback` schedules the next queue read, updates retrial state, applies QoS pacing, updates `qos_last_submit_sm_at`, and only then validates type, expiry, connection, and bind readiness.
- `internal/core/smppc/pacer.go` already provides the frozen-oracle arithmetic, serialized admission, cancellation, and production clock; `Connector.runConsumer` currently proceeds directly from readiness to `Session.Submit` without invoking it.
- The concrete AMQP consumer already configures `prefetch_count=1`, so one unsettled submit remains the broker-side backpressure boundary.

## Scope

### Oracle/test harness

- Keep the frozen Python oracle and `capture_smpp_client_pacing_golden.py` behavior unchanged; reuse the existing trusted pacing corpus and generic no-skip production-Pacer differential harness.
- Regenerate the complete fixture set twice through the approved container contour and require exact byte identity plus zero committed-fixture diff.
- Add RED connector-consumer tests proving pacing placement before readiness, one pacing call per delivery, delayed second submission, disabled-throughput behavior, cancellation while waiting, and exact settlement ownership.
- Extend the full bind → consume → paced submit → correlated response integration test so it proves that the configured production pacer gates real SMPP writes while the response still owns final ACK.

### Production implementation

- Construct one `Pacer` from the connector's defensively cloned `EffectiveSubmitSMThroughput` in `NewConnector`; reject impossible pacing configuration before lifecycle startup.
- Store the pacer on the connector and invoke it after the Go transport has accepted a concrete envelope but before expiry parsing and `ReadinessPolicy.Decide`. Legacy reads `message-id` and unpickles the body before pacing; parity for malformed pre-pacing properties/pickle remains outside this slice.
- On pacing cancellation/error, make one explicit requeue settlement attempt for the already-delivered AMQP message and terminate that consumer invocation; do not pass settlement ownership to `Session.Submit`.
- Preserve `Session.Submit` as the sole settlement owner after successful pacing/readiness and preserve the concrete consumer's `prefetch_count=1` topology.
- Keep pacing state connector-local and shared across reconnects within the connector object's lifetime; configuration mutation/restart parity remains outside this slice.

### Documentation

- Keep `SC-004` at `GO-PARTIAL`, but update its scope note to include concrete AMQP-consumer and socket-submit pacing integration with cancellation and settlement evidence.
- Do not promote `SC-005`, `SC-006`, `SC-001`, `SC-002`, or `SC-007`; this slice does not add complete readiness retries, status retry timers, management updates, or failover.
- Recount every authoritative compatibility matrix before stable candidate identity.

## Non-goals

- Dynamic throughput updates, manager/PB/JCLI configuration lifecycle, or defining restart-required fields (`SC-001`, `SC-002`).
- Multiple in-flight window sizing, configurable AMQP prefetch, throughput statistics, fairness across connectors, or distributed rate limiting.
- Delayed readiness requeue timers or status-specific SMPP response retry execution (`SC-005`, `SC-006`).
- Semantic protocol-2 `SubmitSM` decoding, multipart socket submission, DLR/MO processing, graceful unbind, TLS, or failover routing.
- Malformed pre-pacing message-id/property/pickle parity and a claim of exact full callback event ordering.
- Correcting documented legacy whole-second `.microseconds` behavior or claiming full `SC-004` parity.
- Modifying the frozen Python source boundary or expanding its already trusted pacing corpus without a concrete uncovered behavior.

## Acceptance criteria

1. Existing frozen pacing capture remains byte-exact across two aggregate regenerations; verifier trusted-corpus fingerprint, schemas, Python verifier units, coverage, and `TEST_MANIFEST.csv --check` pass.
2. The generic no-skip Go differential harness continues to execute every pacing fixture through the production `Pacer`.
3. RED-to-GREEN connector tests prove pacing occurs once per received delivery and before expiry/readiness, including a delivery that is ultimately discarded but still advances pacing state.
4. Two accepted deliveries cannot produce SMPP submit writes faster than the configured production pacing decision; disabled throughput adds no wait.
5. Cancellation while pacing unblocks connector stop, performs one requeue settlement attempt for the owned delivery, performs no SMPP write, and does not advance pacing state.
6. After successful pacing, `Session.Submit` remains the only settlement owner: correlated success and each existing failure path make exactly one settlement attempt under the existing session contract; broker confirmation/redelivery is not claimed.
7. Focused connector/pacer tests pass 20 times, followed by full Go, race, vet, build, both pacing fuzz targets, Python verifier/schema/unit, `TEST_MANIFEST.csv --check`, fixture diff, secret scan, and workspace-contamination gates.
8. A stable candidate receives final Ralph code audit with no unresolved high-severity finding.
9. Implementation and final documentation are committed on top of the published plan commit, pushed to `go-rewrite`, and the exact SHA passes GitHub Actions 4/4; local/tracking/remote SHA match, the workspace is clean, and the main orchestrator computes the final LoopKey.

## Affected paths

- `spec/implementation/PHASE2_34_SMPP_CONNECTOR_PACING_INTEGRATION_SLICE.md`
- `internal/core/smppc/connector.go`
- `internal/core/smppc/connector_test.go`
- `internal/core/smppc/connector_pacing_internal_test.go` (new)
- `internal/core/smppc/connector_pacing_additional_test.go` (new)
- `internal/core/smppc/pacer.go` (only if an integration-safe test seam or error contract is required)
- `internal/core/smppc/pacer_test.go` (only for an invalidated pacing invariant)
- `spec/compatibility/SMPP_MATRIX.md`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

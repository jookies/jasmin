# DLR submit_sm_resp publication + always-declared topology + CI integration harness

- **Date:** 2026-07-26
- **Status:** active
- **Summary:** Publish `dlr.submit_sm_resp` from the Go smppc response path (MT-path audit GAP 3), made shippable by always declaring the DLR queue in the base topology and by running the gateway integration test in CI.
- **Related:** MT-path parity audit backlog (memory: `mt-path-parity-audit-backlog.md`, GAP 3); roadmap Macro 2.2 (AMQP consumers/publication).

## Context

The legacy `SMPPClientSMListener` publishes a `dlr.submit_sm_resp` message to DLRLookup on every final `submit_sm_resp` (`jasmin/managers/listeners.py`), which fires level-1 callbacks and writes the `smpp_msgid -> msgid` Redis mapping later receipt correlation depends on. The Go smppc response path (`DurableResponseLifecycle.Commit`) never produced this event — `EventDLRState` had zero producers — so for any connector cut over to the Go client, DLRs break entirely.

A pure producer cannot ship: the AMQP publisher is `mandatory: true` (`internal/transport/amqpcompat/client.go`), and the `dlr.submit_sm_resp` queue (`DLRLookup-<pid>` bound to `messaging`/`dlr.*`) is declared only when the optional in-process DLRLookup worker starts (`gateway/runtime.go` gates on `config.DLRLookup != nil`). Publishing where the queue is unbound returns the message and errors the outbox event, which then never dispatches — the outbox blocks. The gateway integration test that would catch this is env-gated (AMQP+Postgres) and does not run in go-rewrite CI, so a naive change merges green and breaks production.

Desired outcome: the DLR is published faithfully on every response, is always routable, and the behavior is actually verified in CI.

## Approach

Keep the already-built, envelope-verified producer. Make the DLR queue part of the base messaging topology so publishes are always routable regardless of whether the in-process DLRLookup consumer runs — mirroring Jasmin's model where the broker topology is fixed and DLRLookup is a separate daemon. Then close the verification gap by running the gateway integration test in CI (add rabbitmq + postgres services) and extending it to assert the DLR publication.

Key trade-off: always-declaring `DLRLookup-<pid>` means DLR messages accumulate in a durable queue if no DLRLookup consumes them. That is acceptable and matches legacy semantics (the queue exists on the broker; a daemon drains it), and is strictly better than an unroutable mandatory publish that blocks the outbox. The pid must match the consumer's (`"main"` default) — threaded from the same config.

## Steps

### Step 1: Producer (DONE — on branch `feat/mt-dlr-submit-resp-producer`)

- **Files:** `internal/core/smppc/dlr_publish.go` (new), `internal/core/smppc/response_publish.go` (Commit), `internal/core/smppc/dlr_publish_test.go` (new).
- **Changes:** `newDLRSubmitRespPublication(msgID, status, smscMessageID)` builds the envelope (routing `dlr.submit_sm_resp`, body = status name, `message-id` = msgid, headers `type=submit_sm_resp` and, for ESME_ROK only, `smpp_msgid` = `ToUpper`+`TrimLeft("0")`). `Commit` appends an unconditional `EventDLRState` event (every response), keyed `PartKey:15-dlr-submit-resp-NNNNNN`.
- **Verify:** DONE — PYTHON_PATH differential vs the real `managers/content.py` DLR (ROK + error) green; unit tests + full smppc suite green.

### Step 2: Always-declare the DLR queue in the base topology

- **Files:** `internal/app/outbound/runtime.go`, `internal/app/gateway/runtime.go`.
- **Changes:** Add `DLRLookupPID string` to `outbound.RuntimeDependencies`. In the outbound runtime, after the connector-submit-queue loop and before creating the publisher, call `topology.DeclareQueue(ctx, amqpcompat.DLRLookupQueue(pid), "messaging", amqpcompat.DLRLookupRoutingKey)` with `pid` from the dependency (fallback `"main"` if empty). In the gateway runtime, compute `pid := "main"; if config.DLRLookup != nil && config.DLRLookup.PID != "" { pid = config.DLRLookup.PID }` and pass it in `RuntimeDependencies`. This declaration is idempotent with `OpenDLRLookupSubscription` when DLRLookup is enabled (same queue/binding).
- **Verify:** `go build ./... && go vet ./...`; a unit test with a fake `topologyChannel` asserting the outbound runtime declares+binds `DLRLookup-main` to `messaging`/`dlr.*` (extend existing outbound topology tests if present, else add one against a recording fake). Confirm the pid matches `dlrlookup.Config.applyDefaults` (`"main"`).

### Step 3: Extend the gateway integration test to assert the DLR publication

- **Files:** `internal/app/gateway/runtime_integration_test.go`.
- **Changes:** Bind a temporary test queue to `messaging`/`dlr.submit_sm_resp` (or consume `DLRLookup-main`) before driving the submit. After the two `submit_sm_resp` responses, assert two `dlr.submit_sm_resp` deliveries whose `message-id` = the part msgid, body = `ESME_ROK`, and `smpp_msgid` header = the normalized SMSC id. Keep the existing `undispatched == 0` assertion (now also covers the DLR events dispatching).
- **Verify:** with `AMQP_URL`, `TEST_POSTGRES_DSN`, `PYTHON_PATH` set, `go test ./internal/app/gateway/ -run Integration` passes; without the always-declare (Step 2 reverted) it must fail on outbox block, proving the coupling is exercised.

### Step 4: Run the integration test in CI

- **Files:** `.github/workflows/go-rewrite-compat.yml`.
- **Changes:** Add `services: rabbitmq (3-management-alpine, 5672)` and `postgres (16-alpine, 5432, POSTGRES_PASSWORD)` to the `go-http-differential` job (or a dedicated `go-integration` job), plus a schema-init step for the submit_transaction tables, and export `AMQP_URL`, `TEST_POSTGRES_DSN`, `PYTHON_PATH` (the job already installs Python 3.12 + the pickle bridge). Keep the existing `go test ./...` step so gated tests now execute instead of skip.
- **Verify:** the job's log shows `TestRuntimeIntegration...` running (not `SKIP`) and passing; the three fast checks stay green.

## End-to-end verification

CI drives a real submit -> `submit_sm_resp` -> outbox dispatch -> `dlr.submit_sm_resp` publish -> test-queue consume, asserting `results==2`, `undispatched==0`, and two DLR envelopes matching the legacy content. Locally: build/vet/unit/PYTHON_PATH-differential green; the AMQP-routability leg is proven in CI only (no local broker). The prior WIP-block failure mode (unroutable mandatory publish) is now impossible because the queue is always bound.

## Rollback

All changes are additive and isolated to the branch. Revert the branch to drop the producer, the topology declaration, and the CI/test changes together. The always-declared queue is durable and harmless if left; deleting it on the broker is a manual `queue.delete DLRLookup-main` if ever needed.

## Risks

- **CI cost/time:** two new service containers per run. Mitigation: single job, alpine images; acceptable for the parity guarantee.
- **Queue accumulation without a consumer:** DLR messages pile up if nothing drains `DLRLookup-main`. Mitigation: matches legacy (a DLRLookup daemon drains it); durable, bounded by broker policy; a Go cutover always runs DLRLookup.
- **pid mismatch** between always-declare and consumer would split messages across queues. Mitigation: both derive from the same config with the same `"main"` default; covered by the Step 2 unit test and the Step 3 integration assertion.
- **Integration flakiness** against a real broker. Mitigation: existing test already polls with a deadline; reuse that pattern for the DLR assertion.

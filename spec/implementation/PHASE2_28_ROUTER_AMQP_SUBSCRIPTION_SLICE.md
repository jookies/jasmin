# Phase 2.28 — Macro 1.3d: Router AMQP subscription topology

## Goal

Freeze and implement the exact RouterPB AMQP subscription declaration boundary so
the Go router can create the legacy `deliver.sm.*` and late-billing consumers with
correct exchange, queue, binding, consumer-tag, manual-ack, and declaration
properties before a later process-lifecycle/recovery slice.

## Scope

- Capture the actual operation sequence executed by `RouterPB.addAmqpBroker`
  through constructor-compatible read-only test doubles, including resolved AMQP
  defaults rather than merely copying source literals.
- Add a frozen `router-amqp-subscriptions` fixture with the two exchange/queue/
  binding/consumer declarations in exact order.
- Replace the current unverified durable topology assumptions with a typed,
  channel-injected declaration executor that is unit-testable without RabbitMQ.
- Add a generic no-skip Go differential harness that executes every committed
  operation against a recording channel and compares all arguments and order.
- Preserve manual acknowledgement and the fixed legacy consumer tags
  `RouterPB-delivers` and `RouterPB-billrequests`.
- Keep A-001, A-004, A-009, and A-012 truthful: only the RouterPB declaration and
  initial-consume subset may move to or remain `GO-PARTIAL`.

## Non-goals

- Broker connection dialing, authentication, heartbeat, reconnect, topology
  recovery, consumer cancellation, or in-flight redelivery (A-012).
- QoS/prefetch, concurrent callback scheduling, or backpressure (A-010).
- A production process/container or service lifecycle wiring.
- Decoding or routing `deliver.sm.*` pickle bodies, MO processing, or thrower
  publication (A-004/A-005/A-013).
- Changing late-billing decisions, settlement behavior, malformed-delivery policy,
  deduplication, or durable ledger semantics (A-009/B-010).
- Claiming all Jasmin exchange/queue topology is complete; this slice covers only
  the two RouterPB subscriptions reached by `addAmqpBroker`.
- Migrating vhosts created by the earlier, unverified experimental Go helper,
  which declared the same names durable. No production process currently wires
  that helper; an experimental vhost must be recreated or explicitly migrated
  before adopting the frozen legacy non-durable declarations.
- Changing the frozen Python implementation.

## Legacy contract

Authoritative boundaries:

- `jasmin/routing/router.py:75-109`
- `jasmin/queues/factory.py:200-219`
- AMQP method defaults exercised by the capture doubles

Expected sequence:

1. Declare topic exchange `messaging` with legacy non-durable defaults.
2. Declare queue `RouterPB_deliver_sm_all` with legacy non-durable,
   non-exclusive, non-auto-delete defaults.
3. Bind it to `messaging` with routing key `deliver.sm.*`.
4. Start manual-ack consumer `RouterPB-delivers`.
5. Repeat for topic exchange `billing`, queue
   `RouterPB_bill_request_submit_sm_resp_all`, routing key
   `bill_request.submit_sm_resp.*`, and consumer tag
   `RouterPB-billrequests`.

## Acceptance criteria

1. The new fixture regenerates twice byte-for-byte and aggregate regeneration has
   zero diff; schema, trusted fingerprint, registry, coverage, and manifest gates
   include the new surface atomically.
2. The generic Go differential test executes every frozen operation with no skip
   and matches exact operation order and arguments.
3. Exchange and queue durability are false, consumers use manual acknowledgement,
   and both fixed consumer tags are preserved.
4. Context cancellation and every declaration/consume failure stop immediately,
   return an operation-specific wrapped error, and close the owned channel without
   executing later operations.
5. Focused tests pass 20 consecutive times, then full Go, race, vet, build, Python
   schema/integrity/unit, frozen-tree, secret, and workspace-contamination gates
   pass on one stable executable candidate.
6. A stable candidate identity receives a final Ralph code audit; supported
   findings are fixed and invalidated gates rerun.
7. Plan and implementation publish together; exact-SHA GitHub Actions reaches
   4 / 4, local/remote SHA match, workspace is clean, and the main orchestrator
   emits the final LoopKey.

## Affected paths

- `spec/implementation/PHASE2_28_ROUTER_AMQP_SUBSCRIPTION_SLICE.md`
- `scripts/compat/capture_router_amqp_subscriptions_golden.py`
- `scripts/compat/capture_all.sh`
- `compat/fixtures/router-amqp-subscriptions/baseline.json`
- `compat/fixtures/schema/router-amqp-subscriptions-golden.schema.json`
- `scripts/compat/verify_fixtures.py`
- `scripts/compat/test_verify_fixtures.py`
- `scripts/compat/validate_json_schemas.py`
- `spec/compatibility/FIXTURE_COVERAGE.csv`
- `internal/transport/amqpcompat/topology.go`
- `internal/transport/amqpcompat/topology_golden_test.go`
- `internal/transport/amqpcompat/topology_test.go`
- `spec/compatibility/AMQP_REDIS_MATRIX.md`
- `spec/implementation/MACRO_SLICE_ROADMAP.md`

# Architecture

How Synevyr is put together, and why. Read this before changing anything
structural, and before debugging something that crosses a component boundary.

Every claim here is traceable to code. Where a detail is likely to be doubted,
the file and line are cited so you can check rather than trust.

## Shape of the system

Synevyr is **one Go binary** (`cmd/synevyr-gateway`) that runs every component
in-process, coordinated through a message broker. There is no microservice mesh
to operate and no internal RPC to secure.

```
                    ┌─────────────────── synevyr-gateway (one process) ────────────────────┐
                    │                                                                       │
  HTTP /send ──────►│ httpcompat ──┐                                                        │
  REST /secure/* ──►│ restcompat ──┤                                                        │
                    │              ├─► submit_service ─► intercept ─► route ─► bill ─► AMQP ├──► submit.sm.<cid>
  ESME bind ───────►│ smpps ───────┘        │              │           │                    │         │
  (SMPP 3.4)        │                       │              │           │                    │         ▼
                    │                       │              │           │                    │      smppc
                    │                  interceptor     routingtable  billing                │    (connector)
                    │                   (Python)         + filters   + quotas               │         │
                    │                                                                       │         ▼
                    │                                                                       │    carrier SMSC
                    │                                                                       │
                    │  smppc ─► deliver.sm.<cid> ─► modispatch ─┬─► deliver_sm_thrower.http ─┼──► your webhook
  carrier ─────────►│  (deliver_sm / data_sm)                   └─► deliver_sm_thrower.smpps ┼──► bound ESME
                    │                                                                       │
                    │  submit_sm_resp ─► dlr.submit_sm_resp ─┐                              │
                    │  receipt        ─► dlr.deliver_sm ─────┴─► dlrlookup ─► dlr_thrower.* ┼──► callback / ESME
                    │                                                                       │
                    └───────────────────────────────────────────────────────────────────────┘

     PostgreSQL              RabbitMQ                       Redis
     durable submits,        exchange "messaging"           DLR correlation (dlr:<id>),
     billing, CDRs,          (topic) + "billing"            multipart reassembly
     REST batches, HA fence                                 (longDeliverSm:<cid>:<ref>:<dst>)
```

The three stores are the only hard dependencies. Everything else — routing,
billing, DLR correlation, the admin plane, the web console, jCli — is this
binary.

## Why a broker sits in the middle

A submit is accepted at the front door but delivered by a connector that may be
unbound, throttled, or reconnecting. Those two rates are unrelated, so something
has to absorb the difference.

The queue is that buffer, and it is also the **durability boundary in
combination with PostgreSQL**. The front door does not answer `Success` until
the submit is durably admitted, so a crash between acceptance and delivery does
not lose the message and does not charge twice — recovery consults the durable
submit ledger before routing or billing again.

Exchange and routing keys, from `internal/core/smppc/response_publish.go:15` and
`internal/core/smppc/deliver.go`:

| Routing key | Produced by | Consumed by |
|---|---|---|
| `submit.sm.<cid>` | submit service | that connector's SMPPc session |
| `deliver.sm.<cid>` | SMPPc, on inbound MO | `modispatch` (MO router) |
| `dlr.submit_sm_resp` | SMPPc, on `submit_sm_resp` | `dlrlookup` |
| `dlr.deliver_sm` | SMPPc, on an inbound receipt | `dlrlookup` |
| `dlr_thrower.http` / `.smpps` | `dlrlookup` | DLR thrower |
| `deliver_sm_thrower.http` / `.smpps` | `modispatch` | MO thrower |

The `billing` exchange carries late-billing intents separately, so a charge that
must be applied after the SMSC responds is not coupled to message delivery.

**Two hops for a receipt, not one.** `dlrlookup` exists because a receipt
arriving from a carrier identifies the message by the *SMSC's* id, not ours.
Correlation needs the mapping stored at submit time, so the raw receipt is
published first, correlated second, and only then thrown to the customer. That
is why a DLR problem is usually a *correlation* problem — see
[`docs/runbooks/dlrs-not-arriving.md`](runbooks/dlrs-not-arriving.md).

## Layers, and the rule that keeps them honest

```
cmd/                 process entry: flags, listeners, signals, shutdown
internal/app/        wiring: builds and owns components, holds no protocol logic
internal/core/       protocol and domain logic: SMPP, routing, billing, DLR
internal/transport/  codecs and clients: SMPP wire, AMQP, pickle, HTTP, Redis
internal/infra/      storage: PostgreSQL repositories
```

The rule: **dependencies point inward, and `core` never imports `app`.** Domain
logic stays testable without a broker, a database or a socket, which is why 39
packages of tests run with no external dependencies.

`internal/app/gateway/runtime.go` is the composition root. It is long on purpose
— all the wiring is in one readable place rather than scattered through
constructors. If you want to know what actually runs, read that file.

### Where the interesting logic lives

| Package | Responsibility |
|---|---|
| `core/smppc` | Outbound SMPP client: bind, reconnect with backoff, submit window, sequence allocation, inbound MO and receipt ingestion |
| `core/smpps` | Inbound SMPP server: bind auth and state machine, submit ingestion, `deliver_sm` push with a bounded window |
| `core/submit_service.go` | The MT pipeline: authenticate, intercept, segment, route, bill, admit |
| `core/routingtable`, `core/routingfilter` | Route selection and the filter engine |
| `core/billing` | Balances, quotas, early and late charging |
| `core/dlr` | Receipt correlation, level semantics, thrower consumers |
| `core/mo` | MO delivery to HTTP and reassembly |
| `core/segmentation` | Long-message splitting, UDH and SAR |
| `transport/smppwire` | SMPP 3.4 PDU codec |
| `transport/picklecompat` | AMQP payload codec (see below) |

## Two design decisions worth understanding

### The AMQP payload is a Python pickle, in Go

`transport/picklecompat` encodes queue payloads in Python's pickle format,
including class paths like `jasmin.routing.Bills`. There is no Python involved —
`transport/gopickle` implements the format natively.

This is deliberate and it is a **wire-format compatibility choice**: a Synevyr
gateway can share a broker with a legacy deployment during migration, and the
payloads stay readable by either side. The cost is that the queue format is odd
to look at and cannot be casually changed. If you are tempted to switch to JSON,
that decision belongs in an ADR, not a refactor.

### Interception shells out to Python

`core/interceptor` runs MO and MT hooks as a Python subprocess
(`scripts/interceptor_runner.py`) rather than in-process. That buys the Python
ecosystem for customer-written rules, at the cost of IPC on the message path and
a real security boundary question — the script is arbitrary code running as the
gateway user. See [`operations/security.md`](operations/security.md).

It is the only Python in the runtime, and it is optional: the message path is
pure Go.

## Storage model

**PostgreSQL** is the durability boundary. It holds the submit transaction
ledger (which makes recovery idempotent), billing balances and quotas, CDRs,
REST batch state, and the advisory lock that fences active-passive HA. Back this
up; the other two stores are recoverable state.

**Redis** holds correlation and reassembly state that is naturally expiring:
`dlr:<msgid>` for receipt correlation, and
`longDeliverSm:<cid>:<ref>:<destination>` for inbound multipart segments. Losing
Redis loses in-flight correlation, not accepted messages.

**RabbitMQ** holds queued work. Whether that survives a broker restart depends
on `amqp_durable_topology`, and there is a hazard worth knowing before a
migration: a durable and a non-durable declaration of the same queue cannot
coexist on one vhost — AMQP answers the mismatch with `PRECONDITION_FAILED`
(406). Use a separate vhost. `internal/transport/amqpcompat/topology.go:50`
documents this at the source.

## Concurrency model

One goroutine per SMPP session drives its read loop, so PDU handling within a
session is single-threaded and needs no locking for session state. Writes are
mutex-guarded because delivery pushes come from other goroutines.

Each connector consumes its own queue with a bounded prefetch, and the submit
window bounds outstanding `submit_sm` independently of prefetch — the two are
separate knobs because one long message occupies several window slots but one
prefetch slot. See [`operations/scaling.md`](operations/scaling.md).

## High availability

Active-passive, fenced by a PostgreSQL session advisory lock
([ADR-005](adr/005-active-passive-postgres-fencing.md)). Exactly one process
holds the lock and owns all side effects; standbys stay live but answer `/ready`
with 503 so a load balancer excludes them. Losing the lock closes every
admission listener before releasing, so two processes cannot both charge.

**Multi-active is explicitly unsupported.** In-memory quota state would
double-spend. That is a design boundary, not a missing feature.

## What the design does not yet prove

Being explicit, because architecture documents tend to read as claims of
soundness:

- **No real carrier link has been exercised.** Every SMPP peer so far has been a
  simulator or a third-party library.
- **Metrics instrumentation is incomplete** — some series are defined and never
  incremented. [`operations/monitoring.md`](operations/monitoring.md) says which.
- **No load or soak evidence**, so the concurrency and windowing choices above
  are reasoned rather than measured.
- **Failure behaviour is designed but not drilled** — broker, database or SMSC
  dying mid-submit has defined handling and no chaos test.

[`plans/017-smpp-production-readiness.md`](plans/017-smpp-production-readiness.md)
tracks each of these as a gate.

## Where to go next

- Integrating over HTTP: [`api/http.md`](api/http.md),
  [`api/callbacks.md`](api/callbacks.md)
- Integrating over SMPP: [`api/smpp.md`](api/smpp.md)
- Running it: [`operations/`](operations/)
- An incident right now: [`runbooks/`](runbooks/)
- Why something behaves oddly: [`reference/legacy-behaviours.md`](reference/legacy-behaviours.md)
- Unfamiliar terminology: [`glossary.md`](glossary.md)

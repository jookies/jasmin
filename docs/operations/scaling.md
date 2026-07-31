# Scaling and high availability

Synevyr's supported availability model is active-passive, not horizontally
active-active. Live balances and throughput cursors are process memory; two
unfenced active processes could independently spend the same persisted quota
(`docs/adr/005-active-passive-postgres-fencing.md:9`,
`internal/app/outbound/config.go:733`). Add a standby for recovery. Do not add
active send nodes to increase throughput.

There is no benchmark behind the defaults in this document. Sustained target
TPS and a 24-hour soak are both unfinished, and no real carrier link has been
run (`docs/plans/017-smpp-production-readiness.md:137`). Every number below is
a starting configuration or protocol limit from code, not an achieved capacity
claim.

## Throughput controls

There are four independent rate controls. A message can pass one and still be
slowed or rejected by another.

| Control | Scope | Default | Behavior and cost |
|---|---|---:|---|
| `users[].mt_credential.http_throughput` | One HTTP user in one process | Unlimited when omitted | Minimum spacing between accepted HTTP submits. There is no burst credit or queue: an early request is rejected. Zero or negative disables the limit (`internal/core/throughput/limiter.go:1`, `internal/app/outbound/throughput_gate.go:25`). |
| `users[].mt_credential.smpps_throughput` | One SMPPS user in one process | Unlimited when omitted | The same spacing algorithm, with an independent cursor keyed to the SMPPS ingress (`internal/app/outbound/throughput_gate.go:9`, `internal/app/outbound/config.go:796`). |
| `rest_api.http_throughput_per_worker` | The durable `/secure/sendbatch` worker | 8 tasks/s | Paces the serial batch worker. Zero removes the fixed pace. With `smart_qos` (default true), the worker changes its current rate by 10% as request duration worsens or improves (`internal/transport/restcompat/config.go:9`, `internal/transport/restcompat/batch.go:395`). |
| `connectors[].submit_sm_throughput` | One outbound SMSC connector | 1 PDU/s | Serial pacing before readiness and SMPP submission. A non-positive explicit value disables pacing (`internal/core/smppc/pacer.go:10`, `internal/core/smppc/pacer.go:55`, `internal/core/smppc/connector.go:648`). |

The per-user limiter remembers only the last accepted time. It is process-local
and resets on restart or failover (`internal/core/throughput/limiter.go:28`).
Clients should pace below the configured rate because this is not a smoothing
token bucket.

For each connector, two bounds control concurrency rather than rate:

* `prefetch_count` is the RabbitMQ unacknowledged-delivery allowance. It
  defaults to 1 and is applied as consumer QoS
  (`internal/core/smppc/config.go:271`,
  `internal/core/smppc/connector.go:107`).
* `window_size` bounds outstanding `submit_sm` PDUs waiting for responses. Zero
  inherits `prefetch_count`, and the resulting default is therefore 1
  (`internal/core/smppc/config.go:283`,
  `internal/core/smppc/session.go:136`).

Raising the window can hide SMSC round-trip latency, but also increases
in-flight memory and the number of ambiguous submissions during a disconnect.
Raising prefetch increases messages held by this process rather than ready in
RabbitMQ. Start with a window near expected TPS multiplied by measured
`submit_sm_resp` latency, never above the SMSC's permitted window, and measure
redelivery/unknown outcomes while increasing it. That is a capacity-planning
formula, not a tested recommendation.

Response and reconnect controls also affect recovery capacity:

* `res_to` bounds an outstanding SMPP response and defaults to 120 seconds;
  `pdu_to` defaults to 10 seconds, `bind_to` to 30 seconds, and
  `requeue_delay` to 120 seconds
  (`internal/core/smppc/config.go:196`).
* Connection-failure and connection-loss retry both default on. Their base
  delays default to 10 seconds, double to a 60-second cap, and receive up to
  20% downward jitter (`internal/core/smppc/config.go:234`,
  `internal/core/smppc/reconnect_backoff.go:17`).
* A bound session lasting at least the configured backoff maximum resets the
  exponential attempt count (`internal/core/smppc/connector.go:506`).

Shortening reconnect delay reduces recovery latency but can hammer an
unavailable SMSC and synchronize nodes. Lengthening it reduces pressure but
extends an outage. The shipped alert's three-minute unbound window assumes the
default ten-second base (`deploy/alerts.prometheus.yml:3`).

## Active-passive HA

With `ha` present, each process attempts a PostgreSQL session advisory lock
derived from `ha.namespace`. A contender stays alive and retries at
`standby_retry_seconds`; zero selects two seconds
(`internal/app/gateway/config.go:95`,
`internal/app/gateway/config.go:458`,
`internal/infra/storage/postgres_leader.go:65`). Leadership is acquired before
the mutable runtime, stores, workers, or active listeners are constructed
(`internal/app/gateway/runtime.go:73`).

While waiting, the process serves only standby `/live` (200) and `/ready`
(503). After it acquires the lock, that standby server closes and the ordinary
listeners start (`cmd/synevyr-gateway/main.go:59`,
`cmd/synevyr-gateway/main.go:73`). The shipped HAProxy checks the public
`/ready` every second, removes a node after two failed checks, and restores it
after one success. REST, admin web, jCli, and SMPPS backends all use the public
health port for their check (`configs/haproxy.gateway-ha.cfg:16`,
`configs/haproxy.gateway-ha.cfg:23`,
`configs/haproxy.gateway-ha.cfg:35`).

The lease uses a dedicated one-connection PostgreSQL pool and probes that
connection every two seconds with a two-second timeout
(`internal/infra/storage/postgres_leader.go:25`,
`internal/infra/storage/postgres_leader.go:127`,
`internal/infra/storage/postgres_leader.go:185`). If it is lost, the old active
immediately closes HTTP admission and then closes the runtime. It does not
gracefully drain across a lost fence, because that could overlap the promoted
node (`cmd/synevyr-gateway/main.go:170`). Normal shutdown releases leadership
only after message workers and stores stop
(`internal/app/gateway/runtime.go:798`).

Run the repository's drill on an isolated machine:

```console
$ docker compose -f docker-compose.gateway-ha.yml up -d --build
$ curl -fsS http://127.0.0.1:1401/ready
$ docker compose -f docker-compose.gateway-ha.yml stop gateway-a
$ curl --retry 20 --retry-delay 1 --retry-all-errors -fsS \
    http://127.0.0.1:1401/ready
```

The Compose file runs two identical gateway configs against shared PostgreSQL,
RabbitMQ, and Redis (`docker-compose.gateway-ha.yml:8`,
`docker-compose.gateway-ha.yml:91`). This is a development failover drill, not
evidence of a soak or carrier-safe cutover.

### State during promotion

| State | Shared or durable | Failover consequence |
|---|---|---|
| Submit transactions, outbox, CDRs | PostgreSQL | The promoted process migrates and recovers unresolved submit attempts before serving (`internal/app/gateway/runtime.go:99`, `internal/app/gateway/runtime.go:113`). |
| Balances and submit-count quotas | Live in memory, checkpointed to PostgreSQL | The standby reloads the latest durable values. The default flush cadence is 10 seconds, so a hard crash can refund mutations not yet flushed (`internal/core/billing/quota_persister.go:14`, `internal/app/outbound/runtime.go:231`). |
| REST batch tasks and callbacks | PostgreSQL | A process claims durable work from the shared store (`internal/app/outbound/runtime.go:212`, `internal/transport/restcompat/batch.go:395`). |
| Admin-managed entities and profiles | PostgreSQL in HA; SQLite otherwise | A promoted HA node replays the shared, namespace-isolated control plane (`internal/app/admin/store.go:96`, `internal/app/admin/store.go:120`). |
| DLR callback mappings and multipart MO parts | Redis when `dlr_lookup` is enabled | The promoted process uses the shared Redis URL; without it terminal DLR correlation and multipart persistence are unavailable (`internal/app/gateway/runtime.go:221`). |
| Pending queues | RabbitMQ | Survive a broker restart only when the topology is durable; see below. |
| Connector sockets, SMPPS binds, limiter cursors, metric counters, log writers, in-memory routes | Per process | Recreated or replayed. ESMEs and the carrier must reconnect; counters and user spacing reset (`internal/core/throughput/limiter.go:28`, `internal/core/stats/stats.go:1`). |

Multi-active remains unsupported even though much state is shared. PostgreSQL
quota persistence is not an atomic charge-admission ledger
(`docs/adr/005-active-passive-postgres-fencing.md:44`).

## RabbitMQ topology and durability

The gateway declares topic exchanges `messaging` and `billing`. Queues are
manual-ack, non-exclusive, and not auto-deleted
(`internal/transport/amqpcompat/topology.go:142`,
`internal/transport/amqpcompat/topology.go:155`,
`internal/transport/amqpcompat/topology.go:181`).

| Queue | Exchange / binding | Consumer purpose |
|---|---|---|
| `submit.sm.<cid>` | `messaging` / `submit.sm.<cid>` | One connector's MT submissions (`internal/transport/amqpcompat/topology.go:20`, `internal/core/smppc/connector.go:107`). |
| `RouterPB_bill_request_submit_sm_resp_all` | `billing` / `bill_request.submit_sm_resp.*` | Late submit-response billing (`internal/transport/amqpcompat/topology.go:15`, `internal/transport/amqpcompat/topology.go:120`). |
| `RouterPB_deliver_sm_all` | `messaging` / `deliver.sm.*` | MO/DLR ingress to the MO dispatcher (`internal/transport/amqpcompat/topology.go:12`, `internal/transport/amqpcompat/topology.go:336`). |
| `DLRLookup-<pid>` | `messaging` / `dlr.*` | Submit-response and terminal-receipt correlation (`internal/transport/amqpcompat/topology.go:24`, `internal/transport/amqpcompat/topology.go:391`). |
| `dlr_thrower` | `messaging` / `dlr_thrower.*` | HTTP or SMPPS DLR delivery (`internal/transport/amqpcompat/topology.go:195`, `internal/transport/amqpcompat/topology.go:219`). |
| `deliver_sm_thrower` | `messaging` / `deliver_sm_thrower.*` | HTTP or SMPPS MO delivery (`internal/transport/amqpcompat/topology.go:256`, `internal/transport/amqpcompat/topology.go:280`). |

All gateway publications are persistent, mandatory, and wait for broker
confirmation (`internal/transport/amqpcompat/client.go:18`,
`internal/transport/amqpcompat/client.go:50`). Persistent messages do **not**
survive a broker restart in a non-durable queue/exchange.

`amqp_durable_topology: false` is the Jasmin-compatible default. `true` makes
every topology declaration propagated by the gateway durable
(`internal/app/gateway/runtime.go:80`). The setting must be uniform for every
process using a RabbitMQ vhost. If an exchange or queue already exists with the
other durability, RabbitMQ closes the channel with
`PRECONDITION_FAILED` code 406; it does not convert the object
(`internal/transport/amqpcompat/topology.go:47`). To switch safely, use a fresh
vhost or drain/delete and recreate the old topology during an outage. Never
point the durable production example at a vhost still used by the non-durable
Python gateway (`docs/plans/017-smpp-production-readiness.md:163`).

## Capacity procedure

Because no load evidence exists, establish capacity for each deployment:

1. Start from the SMSC contract: allowed TPS, maximum outstanding window,
   expected response latency, bind count, and throttle statuses. Configure the
   connector pace no higher than the contracted TPS
   (`internal/core/smppc/config.go:103`).
2. Generate representative single and multipart MT, MO, DLR, billing, and
   interceptor traffic. Multipart sends consume more than one PDU and therefore
   more window/pacing capacity.
3. Read `synevyr_queue_depth` for per-queue backlog, and check RabbitMQ
   directly as well: the metric is polled every 15 s and counts READY messages
   only, so an unacknowledged backlog held by a stalled consumer is invisible in
   it (`internal/app/gateway/queuedepth.go`).
4. Increase `window_size` and then `prefetch_count` in small steps while
   recording SMPP latency, PostgreSQL latency, RabbitMQ growth, memory, CPU,
   failures, duplicates, and unknown-after-send records.
   `synevyr_submit_round_trip_seconds` is now recorded per connector and is
   usable as latency evidence; it measures the SMSC round trip only, not the
   front-door-to-receipt path.
5. For batch traffic, measure the serial worker independently. Raising its
   throughput cannot overcome the per-user gate or connector pace
   (`internal/transport/restcompat/batch.go:395`).
6. Run the target rate long enough to prove no queue growth, then complete the
   outstanding 24-hour soak and failover under load before calling the number a
   capacity limit (`docs/plans/017-smpp-production-readiness.md:137`).

### Known HA example mismatches

The HAProxy file contains a `pb_facade` frontend on 8998, but the gateway
configuration type has no PB-facade listener field
(`configs/haproxy.gateway-ha.cfg:48`,
`internal/app/gateway/config.go:29`). That frontend looks stale and was not
verified runnable.

The same drill config enables the admin API on 8405, but the HA Compose file
publishes only admin web 8404, and HAProxy has no 8405 frontend
(`configs/gateway.example.json:154`,
`docker-compose.gateway-ha.yml:102`,
`configs/haproxy.gateway-ha.cfg:38`). Therefore the documented
`/metrics/prometheus` listener is not host-accessible through that shipped HA
proxy. Treat both as deployment bugs to review; this documentation does not
change them.

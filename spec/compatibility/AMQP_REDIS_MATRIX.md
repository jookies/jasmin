# AMQP, Redis and persistence compatibility matrix

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.

## RabbitMQ topology and delivery semantics

| ID | Contract | Required fixture | Status |
|---|---|---|---|
| A-001 | exchanges | `messaging`, `billing`, type/durability/declaration properties | INVENTORIED |
| A-002 | submit route | `submit.sm.<CID>` queue/binding/properties/body | GO-PARTIAL |
| A-003 | submit response | optional `submit.sm.resp.<CID>` properties/body | GO-PARTIAL |
| A-004 | MO ingest | `deliver.sm.<CID>` and router wildcard binding | INVENTORIED |
| A-005 | MO throwers | `deliver_sm_thrower.http` / `.smpps` | GO-PARTIAL |
| A-006 | DLR lookup | every `dlr.*` routing key and payload/property set | GO-PARTIAL |
| A-007 | DLR throwers | `dlr_thrower.http` / `.smpps` | GO-PARTIAL |
| A-008 | billing | `bill_request.submit_sm_resp.<UID>` and amount/IDs | GO-PARTIAL |
| A-009 | ACK/reject | success/failure/retry/requeue timing per consumer | INVENTORIED |
| A-010 | QoS/prefetch | configured counts and concurrency effects | INVENTORIED |
| A-011 | expiry | message age/expiration and terminal handling | INVENTORIED |
| A-012 | reconnect | declarations, consumer recovery and in-flight delivery | INVENTORIED |
| A-013 | pickle bridge | allowlisted classes/fields, headers and round-trip fidelity | INVENTORIED |

## Redis

| ID | Key/state | Contract | Status |
|---|---|---|---|
| RD-001 | `dlr:<queue-msgid>` | fields, types, expiry, updates and deletion | GO-PARTIAL |
| RD-002 | `queue-msgid:<smsc-id>` | normalized key, `{msgid, connector_type}`, expiry and deletion | GO-PARTIAL |
| RD-003 | `longDeliverSm:<cid>:<ref>:<destination>` | hash fields/pickled parts, 300-second TTL and concatenation; in mixed mode the partition stays Python-owned or a trusted Python bridge translates the allowlisted structure—Go never decodes legacy pickle | GO-PARTIAL |
| RD-004 | missing/expired | DLR/MO behavior, ACK/reject/logging | INVENTORIED |
| RD-005 | duplicate/reordered | idempotency and terminal-state behavior | INVENTORIED |
| RD-006 | REST backend | DB index/config/result semantics used by Celery | INVENTORIED |

## Profile persistence

| ID | File/behavior | Contract | Status |
|---|---|---|---|
| P-001 | `PROFILE.smppccs` | connector configs and started state behavior | INVENTORIED |
| P-002 | `PROFILE.router-groups` | groups | INVENTORIED |
| P-003 | `PROFILE.router-users` | users, credentials, balances/counts | INVENTORIED |
| P-004 | `PROFILE.router-moroutes` | MO routes/filters/connectors | INVENTORIED |
| P-005 | `PROFILE.router-mtroutes` | MT routes/filters/rates/connectors | INVENTORIED |
| P-006 | `PROFILE.router-mointerceptors` | MO interceptors/scripts | INVENTORIED |
| P-007 | `PROFILE.router-mtinterceptors` | MT interceptors/scripts | INVENTORIED |
| P-008 | header/migration | release header, pickle protocol, class-path migrations | INVENTORIED |
| P-009 | autoload | `jcli-prod` startup and partial failure behavior | INVENTORIED |
| P-010 | periodic persist | quota mutation persistence timer and crash window | INVENTORIED |

## Improved storage boundary

- SQLite adapter: local/test/single-node revisions and audit.
- PostgreSQL adapter: production HA control plane.
- Redis: parity transient state, not durable configuration source of truth.
- NoSQL: deferred until a measured access pattern requires it.
- Legacy import: trusted offline Python exporter to canonical versioned data; Go never writes old pickle profiles.

`GO-PARTIAL` means only a fixture-proven subset is implemented. For AMQP rows this is envelope/routing only; it does not imply broker topology, ACK/retry, state-machine, or pickle-bridge parity. For Redis rows it is typed key/hash projection or opaque multipart metadata only; it does not imply live Redis commands, TTL lifecycle, deletion, assembly, or pickle ownership.

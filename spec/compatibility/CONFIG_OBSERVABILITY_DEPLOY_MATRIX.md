# Configuration, observability and deployment compatibility matrix

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Sources: `misc/config/*.cfg`, `jasmin/config/*`, `jasmin/bin/*`, stats/metrics tests and Compose files.

## Configuration

| ID | Area | Required inventory | Status |
|---|---|---|---|
| C-001 | `smpp-server` | every key, type, default, bind address/port and timer | INVENTORIED |
| C-002 | `smpp-server-pb` | endpoint, auth/digest and defaults | INVENTORIED |
| C-003 | `client-management` | PB endpoint, store, persistence and pickle protocol | INVENTORIED |
| C-004 | `service-smppclient` | logging/runtime defaults | INVENTORIED |
| C-005 | `sm-listener` | retry maps, DLR, QoS, multipart and logging | INVENTORIED |
| C-006 | `dlr` | queue/prefetch/retry/expiry/logging | INVENTORIED |
| C-007 | `amqp-broker` | endpoint/vhost/auth/TLS/reconnect/spec paths | INVENTORIED |
| C-008 | `redis-client` | endpoint/auth/dbid/pool/reconnect | INVENTORIED |
| C-009 | `http-api` | bind/port/access/logging and long-content mode | INVENTORIED |
| C-010 | `router` | PB/store/persistence/logging | INVENTORIED |
| C-011 | throwers | deliver/DLR retry, timeout, method and queue settings | INVENTORIED |
| C-012 | `jcli` | bind/port/auth/session/logging | INVENTORIED |
| C-013 | interceptor client/server | PB endpoints, timeouts and logging | INVENTORIED |
| C-014 | REST/Celery | broker/backend/upstream/auth cache/QoS/logging | INVENTORIED |
| C-015 | environment | uppercase override names, precedence, parsing and invalid values | INVENTORIED |
| C-016 | CLI flags | config path, enable/disable components, daemon roles and version output | INVENTORIED |

## Process and lifecycle

| ID | Process | Contract | Status |
|---|---|---|---|
| D-001 | `jasmind` | component startup order, optional roles, partial failure and shutdown order | INVENTORIED |
| D-002 | `interceptord` | standalone role, endpoint and shutdown | INVENTORIED |
| D-003 | `dlrd` | standalone DLR thrower role | INVENTORIED |
| D-004 | `dlrlookupd` | standalone lookup role | INVENTORIED |
| D-005 | `deliversmd` | standalone MO thrower role | INVENTORIED |
| D-006 | signals | TERM/INT handling, drain and exit codes | INVENTORIED |
| D-007 | ports | SMPP 2775, HTTP 1401, jCli 8990, Router PB 8988, SMPPc PB 8989, SMPPs PB 14000, interceptor PB 8987 | INVENTORIED |
| D-008 | Compose | RabbitMQ/Redis dependency, volumes, ports and startup expectations | INVENTORIED |
| D-009 | REST deployment | API/Celery workers, broker/backend and scheduling | INVENTORIED |

## Observability

| ID | Surface | Contract | Status |
|---|---|---|---|
| O-001 | `/metrics` | exact metric names, HELP/TYPE, labels and content type | INVENTORIED |
| O-002 | HTTP counters | increment points for request/success/error/auth/throughput | INVENTORIED |
| O-003 | SMPPc stats | connector state, binds, submit/response/MO/DLR counters and timestamps | INVENTORIED |
| O-004 | SMPPs stats | binds, sessions, submit/MO/DLR counters and timestamps | INVENTORIED |
| O-005 | user stats | connection/bind/message fields and reset behavior | INVENTORIED |
| O-006 | jCli stats | names, formatting and uptime/reset semantics | INVENTORIED |
| O-007 | logging | logger names, levels, rotation, privacy-sensitive fields and failure events | INVENTORIED |
| O-008 | restart | process-local counters reset; persistent state does not masquerade as legacy metric | INVENTORIED |

New OpenTelemetry and durable analytics must use separate names and cannot replace required legacy metrics before an approved deviation.

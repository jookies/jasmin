# Compatibility surface registry

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Statuses: `INVENTORIED`, `GO-PARTIAL`, `MATCH`, `GO-COMPLETE`, `APPROVED_DEVIATION`, `BLOCKED`.

| ID | Surface | Strictness | Oracle/source | Initial status |
|---|---|---|---|---|
| HTTP-SEND | Legacy `/send` GET/POST | byte/semantic | `jasmin/protocols/http/endpoints/send.py`; HTTP tests | INVENTORIED |
| HTTP-RATE | `/rate` | byte/semantic | HTTP endpoint/tests | INVENTORIED |
| HTTP-BALANCE | `/balance` | byte/semantic | HTTP endpoint/tests | INVENTORIED |
| HTTP-PING | `/ping` | byte-exact | `protocols/http/server.py` | INVENTORIED |
| HTTP-METRICS | `/metrics` and metric names | text contract | metrics tests/docs | INVENTORIED |
| REST | `/secure/*`, batch, scheduling | schema/semantic | `protocols/rest/*` | INVENTORIED |
| SMPPC | Outbound SMPP 3.4 client | wire/behavior | `protocols/smpp/*`, `managers/*` | INVENTORIED |
| SMPPS | Inbound SMPP 3.4 server | wire/behavior | `protocols/smpp/factory.py` and tests | INVENTORIED |
| ROUTING | MO/MT tables, routes, filters | decision-exact | `routing/*`; routing tests | INVENTORIED |
| INTERCEPT | EvalPy and interceptor scripts | behavior | `routing/Interceptors.py`, `interceptor/*` | INVENTORIED |
| BILLING | balance, sms_count, early/late charge | decision/visible values | `routing/Bills.py`, router tests | INVENTORIED |
| MO-CALLBACK | HTTP/SMPP MO delivery | payload/retry | `routing/throwers.py` | INVENTORIED |
| DLR | levels, correlation, callback/deliver | payload/state/retry | `managers/dlr.py`, listeners/tests | INVENTORIED |
| JCLI | Telnet commands/prompts/output | transcript | `protocols/cli/*` and tests | INVENTORIED |
| PB | Router/SMPPc/SMPPs remote methods | method/result/error | public proxy modules | INVENTORIED |
| CONFIG | INI/env/defaults/ports | schema/defaults | `misc/config/*.cfg`, `config/*` | INVENTORIED |
| PROFILE | persist/load/autoload/migration | effective-state | router/client managers | INVENTORIED |
| AMQP | topology, properties, ACK/requeue | topology/behavior | queues/managers/router | INVENTORIED |
| REDIS | DLR and multipart keys/TTL | key/state | managers/listeners/DLR | INVENTORIED |
| OBS | logs/stats/Prometheus | names/increments | stats modules/tests | INVENTORIED |
| DEPLOY | processes, ports, signals, health | operational | bin/config/compose | INVENTORIED |

## Completion rule

A parity release is blocked while any required row remains `INVENTORIED`,
`GO-PARTIAL` or `BLOCKED`. The finished set is exactly `MATCH`,
`GO-COMPLETE`, and `APPROVED_DEVIATION`. Every approved deviation must include
owner approval, migration notes, repository-relative approval evidence, a
rollback invariant, and a reproducible differential fixture.

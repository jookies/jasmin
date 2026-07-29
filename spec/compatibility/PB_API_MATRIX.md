# Perspective Broker API compatibility matrix

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Sources: `jasmin/routing/proxies.py`, `jasmin/managers/proxies.py`, `jasmin/protocols/smpp/proxies.py`, PB implementations and tests.

## Contract policy

Twisted PB is Python-specific and uses pickled values. During migration, a trusted Python facade will preserve PB connectivity and translate calls into a versioned Go control API. Go does not implement generic pickle or accept PB payloads directly.

| ID | Service | Contract inventory | Status |
|---|---|---|---|
| PB-001 | RouterPB auth/connect | endpoint, credentials/digest, reconnect and error type | GO-COMPLETE |
| PB-002 | groups | add/remove/get/list/enable/disable and serialized returns | GO-COMPLETE |
| PB-003 | users | add/remove/get/list/enable/disable, quotas and persistence state | GO-COMPLETE |
| PB-004 | MO/MT routes | add/remove/get/list/flush and pickle type fidelity | GO-COMPLETE |
| PB-005 | MO/MT interceptors | add/remove/get/list/flush and script serialization | GO-COMPLETE |
| PB-006 | Router persist/load | profile/scope, return/error and effective state | GO-COMPLETE |
| PB-007 | Router message consume | routing/billing worker-facing methods and callbacks | GO-COMPLETE |
| PB-008 | SMPPClientManager auth/connect | endpoint, credentials, reconnect and error type | GO-COMPLETE |
| PB-009 | connector CRUD | add/remove/list/get/config serialization | GO-COMPLETE |
| PB-010 | connector lifecycle | start/stop/status/stats and return types | GO-COMPLETE |
| PB-011 | submit | arguments, pickled PDU/content, DLR/billing fields and Deferred result | GO-COMPLETE |
| PB-012 | connector persist/load | profile, file behavior and effective state | GO-COMPLETE |
| PB-013 | SMPPServerPB | list/bound connections, unbind/ban and result/error | GO-COMPLETE |
| PB-014 | InterceptorPB | script execution input/output/error/status mutation | GO-COMPLETE |

`GO-COMPLETE` records functional completion through the trusted PB sidecar and
the normalized Go boundary. It is intentionally not `MATCH`: the PB macro
proves digest login, translation, mixed-origin reconstruction, normalized
dispatch, live profile rollback and data-plane method wiring, but does not yet
contain a complete frozen-oracle fixture corpus. Router worker-facing message
consumption in PB-007 is replaced by the native AMQP consumers; it is not
re-exported as a public sidecar method.

## Facade exit rule

PB facade removal requires an approved deviation, migration tool/documentation, proof that no required external client remains and an equivalent authenticated admin API.

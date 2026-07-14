# Perspective Broker API compatibility matrix

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Sources: `jasmin/routing/proxies.py`, `jasmin/managers/proxies.py`, `jasmin/protocols/smpp/proxies.py`, PB implementations and tests.

## Contract policy

Twisted PB is Python-specific and uses pickled values. During migration, a trusted Python facade will preserve PB connectivity and translate calls into a versioned Go control API. Go does not implement generic pickle or accept PB payloads directly.

| ID | Service | Contract inventory | Status |
|---|---|---|---|
| PB-001 | RouterPB auth/connect | endpoint, credentials/digest, reconnect and error type | INVENTORIED |
| PB-002 | groups | add/remove/get/list/enable/disable and serialized returns | INVENTORIED |
| PB-003 | users | add/remove/get/list/enable/disable, quotas and persistence state | INVENTORIED |
| PB-004 | MO/MT routes | add/remove/get/list/flush and pickle type fidelity | INVENTORIED |
| PB-005 | MO/MT interceptors | add/remove/get/list/flush and script serialization | INVENTORIED |
| PB-006 | Router persist/load | profile/scope, return/error and effective state | INVENTORIED |
| PB-007 | Router message consume | routing/billing worker-facing methods and callbacks | INVENTORIED |
| PB-008 | SMPPClientManager auth/connect | endpoint, credentials, reconnect and error type | INVENTORIED |
| PB-009 | connector CRUD | add/remove/list/get/config serialization | INVENTORIED |
| PB-010 | connector lifecycle | start/stop/status/stats and return types | INVENTORIED |
| PB-011 | submit | arguments, pickled PDU/content, DLR/billing fields and Deferred result | INVENTORIED |
| PB-012 | connector persist/load | profile, file behavior and effective state | INVENTORIED |
| PB-013 | SMPPServerPB | list/bound connections, unbind/ban and result/error | INVENTORIED |
| PB-014 | InterceptorPB | script execution input/output/error/status mutation | INVENTORIED |

## Facade exit rule

PB facade removal requires an approved deviation, migration tool/documentation, proof that no required external client remains and an equivalent authenticated admin API.

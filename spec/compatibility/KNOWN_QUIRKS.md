# Known legacy quirks

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.

These behaviors must not be silently corrected during parity.

| ID | Quirk | Source/evidence | Compatibility rule |
|---|---|---|---|
| Q-001 | `RandomRoundrobin*Route` uses random choice, not deterministic round-robin. | `jasmin/routing/Routes.py` | Preserve in legacy route type; add corrected strategy under a new type later. |
| Q-002 | Default route occupies order `0`; higher route order is evaluated first and same-order add replaces. | `RoutingTables.py`, routing tests | Reproduce decision order exactly. |
| Q-003 | `BestQualityMTRoute` is an unimplemented/skipped stub. | route source/tests | Do not count it as working parity. |
| Q-004 | Package metadata says `0.12`, runtime constants report `0.11.1`. | `pyproject.toml`, `jasmin/__init__.py` | Compatibility identity is commit SHA plus fixtures. |
| Q-005 | Successful MO/DLR HTTP callback requires status 200 and exact body `ACK/Jasmin`. | `routing/throwers.py` | Preserve exact ACK and retry behavior. |
| Q-006 | DLR SMSC IDs are uppercased, leading zeros stripped and may use configured decimal/hex conversion. | `managers/content.py`, `listeners.py` | Preserve mapping and missing/expired-key behavior. |
| Q-007 | MO multipart state uses `longDeliverSm:<cid>:<ref>:<destination>` with fixed TTL 300 seconds. | `managers/listeners.py` | Preserve key/TTL in compat adapter. |
| Q-008 | Route prices and balances use Python float. | `routing/Bills.py`, route tests | Fixed-point is allowed only after a differential rounding corpus proves identical decisions/output. |
| Q-009 | Profiles and many PB/AMQP bodies use Python pickle. | router/managers/content | Only trusted Python bridge may decode; Go never does. |
| Q-010 | Startup can continue after some component failures. | `jasmin/bin/jasmind.py` | Record exact readiness behavior; secure mode may fail fast as a documented mode difference. |
| Q-011 | Stats are process-local and reset on restart. | stats modules/docs | Preserve legacy metrics while adding separate durable telemetry. |
| Q-012 | GET `/send` remains supported for backward compatibility. | HTTP send endpoint | Do not remove during parity. |
| Q-013 | Unlimited balance/count can be represented as `None` internally and `ND` in API output. | credentials/HTTP tests | Preserve type and formatting at edges. |
| Q-014 | Billing may split charge between enqueue and successful `submit_sm_resp`. | router/listeners | Differential tests must cover redelivery and exactly-once delta. |

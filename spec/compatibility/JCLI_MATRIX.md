# jCli compatibility matrix

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Oracle: `jasmin/protocols/cli/*`, CLI tests and management documentation.

| ID | Manager/command | Required transcript coverage | Status |
|---|---|---|---|
| J-001 | connection/auth | banner, prompt, username/password success/failure, timeout, quit | INVENTORIED |
| J-002 | help/completion | help text, unknown command, tab completion | INVENTORIED |
| J-003 | `group` | list/add/remove/enable/disable and validation/errors | INVENTORIED |
| J-004 | `user` | list/add/update/remove/show/enable/disable | INVENTORIED |
| J-005 | user credentials | every HTTP/SMPP authorization, filter, default and quota field | INVENTORIED |
| J-006 | user SMPP control | unbind/ban and session effects | INVENTORIED |
| J-007 | `filter` | all filter types, MO/MT restrictions, regex/date/time/tag/eval | INVENTORIED |
| J-008 | `morouter` | list/add/remove/show/flush; all working route types | INVENTORIED |
| J-009 | `mtrouter` | list/add/remove/show/flush; rates and connectors | INVENTORIED |
| J-010 | `mointerceptor` | list/add/remove/show/flush and script references | INVENTORIED |
| J-011 | `mtinterceptor` | list/add/remove/show/flush and script references | INVENTORIED |
| J-012 | `smppccm` | list/add/update/remove/show/start/stop and field prompts/defaults | INVENTORIED |
| J-013 | `httpccm` | list/add/remove/show and method/base URL | INVENTORIED |
| J-014 | `stats` | users/user, SMPPc(s), SMPPs, HTTP and exact names/format | INVENTORIED |
| J-015 | `persist` | default/profile/scope, success/failure, file set | INVENTORIED |
| J-016 | `load` | default/profile/scope, missing/corrupt/versioned files | INVENTORIED |
| J-017 | autoload | `jcli-prod` startup behavior | INVENTORIED |
| J-018 | interactive sessions | start/save/abort, prompt ordering, invalid key/value | INVENTORIED |

## Strictness

Compatibility fixtures preserve prompt bytes, line ordering, spacing where scripts may parse output, success/error text and effective control-plane mutation. A future CLI may be added, but the compatibility facade cannot silently change these transcripts.

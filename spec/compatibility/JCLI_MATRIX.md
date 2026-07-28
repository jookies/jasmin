# jCli compatibility matrix

Baseline: `0aac58e466d583d0f0436df7b8afa3dc96191263`.
Oracle: `jasmin/protocols/cli/*`, CLI tests and management documentation.

The **Admin-plane equivalent** column records where the same *capability* already
exists in the Go rewrite (JSON API + web UI). It is deliberately separate from
**Status**, which tracks jCli *transcript* parity only: a capability being
reachable through the API does not make the telnet transcript match. Retiring
the telnet console is the question the first column answers; implementing it is
the question the second one answers.

| ID | Manager/command | Required transcript coverage | Admin-plane equivalent | Status |
|---|---|---|---|---|
| J-001 | connection/auth | banner, prompt, username/password success/failure, timeout, quit | session cookie + CSRF (`/api/login`); **Go console implemented** (`internal/app/jcli`) | INVENTORIED (impl, fixtures pending) |
| J-002 | help/completion | help text, unknown command, tab completion | n/a (UI navigation); **Go console: help + unknown-command done, tab completion not implemented** | INVENTORIED (impl, fixtures pending) |
| J-003 | `group` | list/add/remove/enable/disable and validation/errors | **MISSING** — plan 012 Step 6 | INVENTORIED |
| J-004 | `user` | list/add/update/remove/show/enable/disable | `/api/users` + Users page | INVENTORIED |
| J-005 | user credentials | every HTTP/SMPP authorization, filter, default and quota field | **PARTIAL** — balance/quota/early-decrement only; per-authorization fields not exposed | INVENTORIED |
| J-006 | user SMPP control | unbind/ban and session effects | **MISSING** — no session control surface | INVENTORIED |
| J-007 | `filter` | all filter types, MO/MT restrictions, regex/date/time/tag/eval | inline filters on each route/interceptor — deviation **D-001** | INVENTORIED |
| J-008 | `morouter` | list/add/remove/show/flush; all working route types | `/api/mo-routes` + MO Routes page | INVENTORIED |
| J-009 | `mtrouter` | list/add/remove/show/flush; rates and connectors | `/api/routes` + MT Routes page | INVENTORIED |
| J-010 | `mointerceptor` | list/add/remove/show/flush and script references | `/api/interceptors` (direction `mo`), opt-in | INVENTORIED |
| J-011 | `mtinterceptor` | list/add/remove/show/flush and script references | `/api/interceptors` (direction `mt`), opt-in | INVENTORIED |
| J-012 | `smppccm` | list/add/update/remove/show/start/stop and field prompts/defaults | `/api/connectors` + Connectors page (incl. start/stop) | INVENTORIED |
| J-013 | `httpccm` | list/add/remove/show and method/base URL | inline in the MO route destination (no standalone entity) | INVENTORIED |
| J-014 | `stats` | users/user, SMPPc(s), SMPPs, HTTP and exact names/format | `/metrics` + dashboard health; **not** the jCli field set | INVENTORIED |
| J-015 | `persist` | default/profile/scope, success/failure, file set | obsolete by design — deviation **D-002** | INVENTORIED |
| J-016 | `load` | default/profile/scope, missing/corrupt/versioned files | obsolete by design — deviation **D-002** | INVENTORIED |
| J-017 | autoload | `jcli-prod` startup behavior | `LoadAndApply` at boot | INVENTORIED |
| J-018 | interactive sessions | start/save/abort, prompt ordering, invalid key/value | n/a (forms) | INVENTORIED |

**Go console status (2026-07-28).** `internal/app/jcli` implements the session,
auth, `help`/`quit` and read-only `list`/`show` for `smppccm`, `mtrouter`,
`morouter` and `user`. No row has moved off `INVENTORIED`, deliberately: the
literals were transcribed from the frozen source rather than captured from a
running oracle, so byte-parity is asserted, not proven. Rows move to `MATCH`
only once plan 013 Step 1 (the transcript-capture harness) can run, which needs
the frozen Python stack. Mutating verbs and interactive sessions are not
implemented — they report an explicit "not implemented" rather than appearing to
succeed.

Admin-plane gaps as of 2026-07-27: **J-003** (groups), **J-005** (per-authorization
user fields), **J-006** (SMPP session control). Everything else is reachable
without the telnet console. SMPPs bind accounts have no jCli row — in legacy they
are user credentials (J-005) — and are managed at `/api/smpps-users`.

## Strictness

Compatibility fixtures preserve prompt bytes, line ordering, spacing where scripts may parse output, success/error text and effective control-plane mutation. A future CLI may be added, but the compatibility facade cannot silently change these transcripts.

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
| J-001 | connection/auth | banner, prompt, username/password success/failure, timeout, quit | session cookie + CSRF (`/api/login`); **Go console implemented** (`internal/app/jcli`) | **MATCH** — `J-001-auth-success`, `J-001-auth-failure` |
| J-002 | help/completion | help text, unknown command, tab completion | n/a (UI navigation) | **MATCH** — `J-002-help`, `J-002-help-commands`, `J-002-completion` |
| J-003 | `group` | list/add/remove/enable/disable and validation/errors | `admin.GroupService` (plan 012 Step 6, landed) | **MATCH** — `J-003-group` |
| J-004 | `user` | list/add/update/remove/show/enable/disable | `/api/users` + Users page | **MATCH** — `J-004-user` |
| J-005 | user credentials | every HTTP/SMPP authorization, filter, default and quota field | `outbound.UserConfig.MTCredential` + `SMPPSCredential` (mirrored to the bind account) | **MATCH** — `J-005-user-credentials`, `J-005-user-invalid` (console surface; **HTTP-path enforcement is still missing**, see below) |
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

**Go console status (2026-07-27, evening).** Plan 013 Step 1 is no longer
blocked: `.venv-oracle` built from the repo's own `requirements.txt` imports the
whole frozen stack, and `scripts/compat/capture_jcli_transcript.py` records real
transcripts into `fixtures/jcli/`. `TestOracleTranscripts` replays every one of
them and compares bytes per step.

Five rows now carry `MATCH` on fixture-backed evidence — the first surface in
this project to earn that status. What the recording changed: the console is a
Twisted *telnet terminal*, so it opens with IAC negotiation and ESC c / ESC [ 4 h,
every line ends with `\r\r\r\n` (four bytes), input is echoed, and it never
emits `Username: ` on connect. The previous Go implementation got all of that
wrong while claiming Steps 2–3 were done — which is precisely what "asserted,
not proven" was warning about.

Still unimplemented, and still answering with an explicit "not implemented"
rather than appearing to succeed: `smppccm`/`mtrouter`/`morouter` mutating
verbs, `filter`, `httpccm`, `mointerceptor`, `mtinterceptor`, `stats`,
`persist`/`load`, and `--smpp-unbind`/`--smpp-ban`.

**J-005 caveat.** The console surface matches, but the HTTP front door does not
yet *enforce* per-user authorizations, value filters or the default source
address (`internal/core/mtcredential.ValidateSend` exists, is unit-tested, and
is never called from the HTTP path). Disabled users and disabled groups ARE now
refused at authentication.

Admin-plane gaps as of 2026-07-27: **J-003** (groups), **J-005** (per-authorization
user fields), **J-006** (SMPP session control). Everything else is reachable
without the telnet console. SMPPs bind accounts have no jCli row — in legacy they
are user credentials (J-005) — and are managed at `/api/smpps-users`.

## Strictness

Compatibility fixtures preserve prompt bytes, line ordering, spacing where scripts may parse output, success/error text and effective control-plane mutation. A future CLI may be added, but the compatibility facade cannot silently change these transcripts.

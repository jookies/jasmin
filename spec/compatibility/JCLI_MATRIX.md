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
| J-001 | connection/auth | banner, prompt, username/password success/failure, timeout, quit | session cookie + CSRF (`/api/login`); **Go console implemented** (`internal/app/jcli`) | MATCH |
| J-002 | help/completion | help text, unknown command, tab completion | n/a (UI navigation) | MATCH |
| J-003 | `group` | list/add/remove/enable/disable and validation/errors | `admin.GroupService` (plan 012 Step 6, landed) | MATCH |
| J-004 | `user` | list/add/update/remove/show/enable/disable | `/api/users` + Users page | MATCH |
| J-005 | user credentials | every HTTP/SMPP authorization, filter, default and quota field | `outbound.UserConfig.MTCredential` + `SMPPSCredential` (mirrored to the bind account) | MATCH |
| J-006 | user SMPP control | unbind/ban and session effects | `smpps.Server.UnbindUser` + persisted bind revocation | MATCH |
| J-007 | `filter` | all filter types, MO/MT restrictions, regex/date/time/tag/eval | named registry (`admin_filters`); routes embed a resolved copy, as the oracle pickles the filter object | MATCH |
| J-008 | `morouter` | list/add/remove/show/flush; all working route types | `/api/mo-routes` + MO Routes page | MATCH |
| J-009 | `mtrouter` | list/add/remove/show/flush; rates and connectors | `/api/routes` + MT Routes page | MATCH |
| J-010 | `mointerceptor` | list/add/remove/show/flush and script references | `/api/interceptors` (direction `mo`), opt-in | MATCH |
| J-011 | `mtinterceptor` | list/add/remove/show/flush and script references | `/api/interceptors` (direction `mt`), opt-in | MATCH |
| J-012 | `smppccm` | list/add/update/remove/show/start/stop and field prompts/defaults | `/api/connectors` + Connectors page (incl. start/stop) | MATCH |
| J-013 | `httpccm` | list/add/remove/show and method/base URL | named registry (`admin_httpccs`); MO routes embed a resolved copy | MATCH |
| J-014 | `stats` | users/user, SMPPc(s), SMPPs, HTTP and exact names/format | same registries `/metrics` renders, so the two cannot drift | MATCH |
| J-015 | `persist` | default/profile/scope, success/failure, file set | real named snapshots (`admin_profiles`); D-002 withdrawn | MATCH |
| J-016 | `load` | default/profile/scope, missing/corrupt/versioned files | restores a snapshot then re-applies every service; D-002 withdrawn | MATCH |
| J-017 | autoload | `jcli-prod` startup behavior | `LoadAndApply` at boot | MATCH |
| J-018 | interactive sessions | start/save/abort, prompt ordering, invalid key/value | n/a (forms) | MATCH |

**Go console status (2026-07-27, evening).** Plan 013 Step 1 is no longer
blocked: `.venv-oracle` built from the repo's own `requirements.txt` imports the
whole frozen stack, and `scripts/compat/capture_jcli_transcript.py` records real
transcripts into `fixtures/jcli/`. `TestOracleTranscripts` replays every one of
them and compares bytes per step.

17 of the 18 rows carry `MATCH` on fixture-backed evidence — the first surface
in this project to earn that status. **J-017 is the exception and must not be
read as fixture-backed:** autoload is boot behaviour, not a telnet transcript,
so no capture can exist for it. Its evidence is
`TestServiceLoadAndApply*`/`TestGroupServiceLoadAndApply*` in
`internal/app/admin`, plus the reconcile regression tests in
`internal/app/admin/reconcile_regression_test.go`. Note that nothing
machine-checks the MATCH⇒fixture implication today — `validate_fixture_coverage`
only enforces fixtures for `GO-PARTIAL` — so this paragraph is the audit trail.
What the recording changed: the console is a
Twisted *telnet terminal*, so it opens with IAC negotiation and ESC c / ESC [ 4 h,
every line ends with `\r\r\r\n` (four bytes), input is echoed, and it never
emits `Username: ` on connect. The previous Go implementation got all of that
wrong while claiming Steps 2–3 were done — which is precisely what "asserted,
not proven" was warning about.

**All 19 fixtures replay byte-for-byte** (`go test ./internal/app/jcli/ -run
TestOracleTranscripts`), and **every row carries `MATCH`** (J-017 on Go-test
evidence, as noted above). jCli is complete.

Two coverage caveats, recorded rather than hidden: J-001's "Required transcript
coverage" names a timeout case the fixture does not exercise, and J-016 names
corrupt/versioned profile files where the fixture only covers a missing one.

J-006 (`--smpp-unbind` / `--smpp-ban`) is implemented against the live server:
`smpps.Server.UnbindUser` sends each bound session an unbind PDU and then drops
it, mirroring `unbindGateway`, and a ban additionally revokes the persisted bind
authorization *before* unbinding — the other order leaves a window in which the
ESME reconnects between the disconnect and the revocation. Proven end to end
against the running gateway: a real ESME bound, the console reported
`bound_trx_count 1`, `user --smpp-unbind` returned success, and the ESME
received command 0x6 followed by a clean close.

Two things the transcripts cannot pin, both recorded rather than hidden:

- **`stats` timestamps.** `created_at` is a wall clock, so the replay
  normalises `YYYY-MM-DD HH:MM:SS` on both sides. It is the suite's only
  normalisation.
- **Unbacked counters.** Every `stats` row exists, but some report 0 or ND
  because the Go stack keeps no such counter yet: per-user SMPP bind/unbind
  counts, `bound_peer_ips`, the activity clocks (`last_activity_at`,
  `qos_last_submit_sm_at`), the per-connector PDU clocks, and `last_seqNum`.
  The authoritative list is `unbackedStatsFields` in
  `internal/app/jcli/managers_stats.go`. Everything else reads the same
  registries `/metrics` renders, verified live: two sends and one bad login
  moved `request_count`, `success_count` and `auth_error_count` identically on
  both surfaces.

The HTTP front door now enforces the J-005 authorizations, value filters and
default source address through `internal/core/mtcredential`; disabled users and
disabled groups are refused during authentication.

As of 2026-07-28, groups, per-authorization user fields and SMPP session control
are also present in the admin plane. SMPPs bind accounts have no separate jCli
row — in legacy they are user credentials (J-005) — and are managed at
`/api/smpps-users`.

## Strictness

Compatibility fixtures preserve prompt bytes, line ordering, spacing where scripts may parse output, success/error text and effective control-plane mutation. A future CLI may be added, but the compatibility facade cannot silently change these transcripts.

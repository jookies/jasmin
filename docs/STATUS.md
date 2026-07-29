# Go Rewrite — Status: what's ready, what's left

- **Date:** 2026-07-29
- **Branch:** `go-rewrite`
- **One-line:** The core message gateway and functional items 11–21 are implemented for the supported single-node and active-passive topologies. Native pickle is Go-only, while supported legacy interceptor scripts intentionally remain in the isolated Python worker until the last migration step.

This is a living overview. The authoritative detail lives in `docs/plans/NNN-*.md` and `docs/worklog.md`; where a plan's own `Status:` field lags reality it is noted under **Housekeeping** below.

---

## Ready — functionally implemented on `go-rewrite`

Oracle evidence is stated per item. Functional implementation alone does not
promote a compatibility-registry row.

### Messaging core
- **MT send path (HTTP + SMPPs → SMSC).** Front-door parse/validate, envelope build, `SubmitSM` encode, priority/validity/schedule, service_type/protocol_id/TON-NPI, empty-param handling. Byte-for-byte proven against the real legacy send path (`update_submit_sm_pdu` + `preSubmitSm` + `PDUEncoder`) for default and non-default connectors. — plans 003, 009 (#87); MT-path parity audit (#46–#52)
- **Multipart / long messages.** SAR and UDH segmentation, per-part encode, reassembly. — plan 002; verified live in the gateway E2E (2-part SAR submit)
- **MO receive path (SMSC → HTTP / SMPPs).** `deliver_sm` + `data_sm` decode (distinct SMPP 3.4 codec, byte-parity), long-MO reassembly (`MultipartStore`), routing to HTTP and SMPPs destinations. — #83, #85
- **DLR (delivery receipts).** Levels 1/2/3, submit_sm_resp correlation, terminal-reject reporting; level-2 connector quirk preserved. — plan 001; #76
- **Routing & filters.** MT and MO route tables; filter engine (source/destination/short_message/tag/date/time). Note: legacy `re.match` is anchored — use `.*X` for substring. — #77, #89
- **Interception.** MT hook (pre-encode) and MO hook (reject / mutate, on whole and reassembled messages). The isolated legacy-script worker now respawns after request cancellation, broken pipe, EOF, or a script terminating the child, so one bad interceptor cannot disable interception until restart. — #81, #88
- **SMPP bind roles.** transmitter / receiver / transceiver; receiver excluded from MT routing and submit consumption. — #82
- **SMPPs submit fidelity.** A bound ESME's mandatory `submit_sm` fields, every standard optional accepted by the frozen SubmitSM decoder, vendor TLVs, binary coding-zero octets and pre-segmented UDH/SAR payloads survive forwarding. Credential source defaults are applied at the legacy point without mutating the session-owned request. Unsupported or command-invalid known TLVs are rejected instead of silently disappearing.
- **Per-user QoS throughput ceiling.** `http_throughput` / `smpps_throughput` are now enforced at both front doors (`internal/core/throughput`), separately per ingress. They were previously accepted by jCli, stored, and rendered back in the user list while nothing applied them — an operator could rate-limit a customer, see the limit reported, and have it do nothing. HTTP answers 403 `Error "User throughput exceeded"` and increments `throughput_error_count`; SMPPs answers `ESME_RTHROTTLED` without dropping the bind. Legacy's quirks are preserved and recorded as Q-022.

### Platform
- **Native Go pickle codec — the Python bridge is retired.** All 10 AMQP encode/decode actions run natively (`internal/transport/gopickle` + `picklecompat.NativeCodec`), including the full DataCoding surface (DEFAULT + RAW + GSM message-class, all 256 bytes), schedule/validity times, and custom TLVs. `pickle_codec` defaults to `native`; the gateway runs E2E with no Python subprocess; `pickle_bridge.py` + `smpp-pdu3` + the `jasmin/` tree are dropped from `docker/Dockerfile.gateway`. — plan 010 (#93–#100, #101)
- **Admin data plane.** Connectors, routes, users managed programmatically with a live-mutable directory over pure-Go SQLite (modernc, ADR-001); changes apply without restart. — #78 / #79 / #80
- **Admin web UI.** A React SPA (Refine + Ant Design) embedded via `go:embed` over a session-authenticated Go BFF (`internal/app/adminweb`), served on its own listener (`admin.web_listen_address`): health and traffic operations, config-aware connectors, MT/MO routes, groups, full user credentials, SMPPs bind/session actions, saved filter/HTTP-destination libraries, exact message status, balance/rate/test-submit diagnostics, named configuration profiles, and opt-in interceptors. No token in the browser; no Node in the runtime image. — plans 011/012, ADR-002
- **Live-mutable admin plane.** MO routes, MT/MO interceptors, SMPPs bind users and groups joined connectors/routes/users as runtime-managed entities: each rebuilds its live state, reserves config-owned identities, and cannot half-apply. User/group edits retain the existing live billing objects; failed SQLite writes restore the exact pre-edit spent balance/count, credentials, group and provisioning baseline while concurrent charges wait. Interceptor editing is opt-in (`admin.allow_interceptor_editing`) because the scripts are arbitrary Python on the gateway host. Group/admin completion is present in the current worktree and still needs clean-candidate evidence. — plan 012
- **jCli management console.** Every frozen command is implemented over the shared admin core; all 19 captured fixtures replay byte-for-byte and all 18 matrix rows are `MATCH`. Named `persist`/`load` profiles are real SQLite snapshots. — plan 013
- **Logging.** The SMS-MT audit line is byte-complete (config fields, TLVs, multipart) to `messages.log` with rotation. Named rotating component loggers are now wired for SMPPc lifecycle, SMPPs, router, HTTP API/access, DLRLookup, AMQP factory and both throwers; components sharing `messages.log` share one synchronized rotating writer. Strict line-level oracle promotion remains evidence work, not an implementation gap. — plans 004/006 (#54–#62)
- **Prod hardening.** Opt-in durable AMQP topology (broker-restart drill 10/10), `/health` (postgres/amqp/connectors), inbound HTTPS + SMPPS-over-TLS, `env:` / `file:` secret refs. — plan 007 (#66–#73)
- **Roadmap #18 CDR — functionally complete.** One content-free durable CDR per submit part plus immutable deduplicated admission/result, actual late-billing and final-DLR events. Operator-configurable currency/retention, bounded pruning, versioned cursor JSONL/CSV export, reconciliation alerts and fail-closed role/audit enforcement are implemented in PostgreSQL and exercised by the production runtime. No compatibility row is promoted because frozen Python has no CDR oracle. — ADR-006
- **Roadmap #19 REST — functionally complete.** Authenticated JSON `/secure/send`, `/secure/balance`, `/secure/rate` and `/secure/sendbatch` delegate to the proven HTTP pipeline. Expanded immediate/scheduled work and terminal callbacks are admitted to PostgreSQL before acknowledgement, recovered through leased claims after restart and bounded/retried under configurable QoS/backpressure. Durable rows retain a credential digest rather than the password; stable per-task message IDs prevent a recovered, already-admitted submit from charging/publishing twice. The optional standalone listener supplies the REST JSON `/ping` without changing legacy `/ping`. No R-001–R-009 row is promoted without frozen macro evidence. — ADR-007
- **Roadmap #20 PB — functionally complete.** A trusted Twisted sidecar terminates the four frozen digest-authenticated PB services and projects every method family through a bearer-authenticated private Go seam. Groups, users, routes, interceptors, profiles, connector lifecycle, SMPP-server operations and script execution retain their frozen result shapes. Manager submits preserve the forced connector, bill/bill ID, DLR connector, priority, expiration and every ordered `nextPdu` part without rerouting or double charging. The focused PB macro is green; rows are `GO-COMPLETE`, not `MATCH`, until the full frozen fixture corpus exists. — `docs/pb-facade.md`
- **Roadmap #21 HA — functionally complete for active-passive.** A namespaced PostgreSQL session advisory lock fences exactly one active gateway while in-process standbys remain live and unready. The shared, namespace-isolated PostgreSQL admin store survives promotion; leadership loss closes every admission listener before release. The executable two-node HAProxy deployment was drilled through pre/post-failover MT sends, readiness transitions, shared PB-created state and rejoined-standby behavior. Multi-active is explicitly unsupported by ADR-005 rather than an incomplete mode. — ADR-005

---

## Left — remaining to reach full parity + prod cutover

Ordered by the current roadmap.

1. **Commercial deployment policy/proof.** Charging, durable quotas, deleted-principal quota GC and the complete CDR lifecycle are implemented. Operators still must select settlement currency/retention and execute the release-grade prepaid/postpaid/group oracle E2E; those are deployment decisions/evidence, not missing CDR code. The Python oracle has no CDR feature, so CDR tests are greenfield production contracts rather than parity rows.
2. **Compatibility closure.** The structurally valid registry has 205 contracts: 37 `MATCH`, 17 `GO-COMPLETE`, 60 `GO-PARTIAL`, 91 `INVENTORIED`. Release A has 58 unique required contracts, only 8 finished. Of 39 macro scope/mode rows, 18 still have no executable command. Functional completeness is therefore not yet a formal cutover claim.

   Two qualifiers, both verified 2026-07-29 and both load-bearing for planning. First, the 50 unfinished Release A rows are overwhelmingly **missing differential evidence, not missing code** — spot-checking `INVENTORIED` rows found working implementations for every one checked. Second, Release A draws only from SMPP (19), AMQP/Redis (15), HTTP (14) and Routing/Billing (10): **jCli and PB contribute zero rows**, so their 18/18 and 14/14 completions do not move the cutover number, and the 0/33 `CONFIG_OBSERVABILITY_DEPLOY` matrix is outside Release A entirely. Effort belongs on the four contributing matrices. See plan 016.
3. **Native interceptor decision.** Interception still shells to `scripts/interceptor_runner.py` (stdlib-only). Porting or deliberately replacing that contract removes Python from the *gateway* image; it is not required to keep the frozen Python tree as the compatibility oracle.

   **It does not make the deployment Python-free.** The PB compatibility surface (roadmap #20) is a Python/Twisted sidecar that installs the entire legacy `jasmin/` tree and runs it as a deployed service (`docker/Dockerfile.pbfacade`, ports 8987/8988/8989/14000). Per `docs/pb-facade.md`, pickle/jelly has no safe byte-compatible generic Go decoder, so this is an architectural commitment rather than a migration remnant: **as long as the PB API is offered, Python runs in production.** This costs nothing at cutover — PB contributes zero rows to Release A — but "deprecate Python Jasmin" therefore means retiring the legacy *gateway*, not removing Python. See plan 016.
4. **Formal cutover proof.** A clean, attested candidate plus side-by-side shadow, partitioned canary, soak and rollback drill is still missing. The decision and executable gates are in plan 015.

### Python Jasmin deprecation decision

- **Feature-freeze the legacy Python implementation:** allowed now.
- **Deprecate the Python deployment:** not ready. Earliest responsible planning window is 2026-10-20 through 2026-12-01, conditional on every applicable plan 015 gate passing.
- **End support/remove the legacy deployment:** no earlier than January–February 2027, after at least one stable Go release and 30–60 days of rollback retention.
- **Delete the Python source/oracle:** not authorized by deployment deprecation; retain it as a compatibility test asset.

### Newly found (2026-07-27 through 2026-07-29)
- ~~**Shared groups could be persisted from different moments in one batch.**~~ — **fixed.** Dirty users are snapshotted first, then each affected live group is snapshotted once in deterministic key order immediately before the atomic write.
- ~~**A non-quota admin edit could regrant spent money, and a failed store write could restore a provisioned grant instead of the exact spent state.**~~ — **fixed.** In-place user/group reprovision transactions serialize concurrent charges and commit or restore every mutable billing and directory field exactly.
- ~~**The admin diagnostic requested DLR level 2.**~~ — **fixed.** It now requests level 3, exercising SMSC submit acknowledgement and terminal delivery receipt correlation.
- ~~**SMPPs forwarding could drop legal SubmitSM fields/TLVs or corrupt binary/UDH payloads.**~~ — **fixed.** Mandatory fields, the full frozen supported optional set, vendor TLVs and raw octets are retained through the native codec; frozen-encoder differential and end-to-end component regressions cover the path.
- ~~**A cancelled or crashed Python interceptor worker stayed dead.**~~ — **fixed.** Cancellation, EOF, broken-pipe and explicit child-crash tests prove lazy respawn on the next request.
- ~~**The HTTP front door enforces no user credentials.**~~ — **fixed.** It checked the password and nothing more; `internal/core/mtcredential` (the unit-tested port of Jasmin's `HttpAPICredentialValidator`) had zero callers. `/send` now enforces per-user authorizations, value filters and the default source address, with the oracle's exact rejection text and status 400. Disabled users and disabled groups are refused at authentication. Verified live: a destination outside a user's `dst_addr` filter and a user with `http_send False` are both refused, while a matching destination still sends.
- **Connector defaults diverged from legacy, including on the wire** — fixed. A connector provisioned without explicit addressing emitted source TON/NPI 0/0 and destination 0/0 where legacy emits NATIONAL/ISDN and INTERNATIONAL/ISDN, so its `submit_sm` bytes differed from the Python gateway's. `trx_to`, `res_to` and `pdu_red_to` were wrong too, and `enquire_link` ran off the PDU read timer. Pinned by `TestConnectorDefaultsMatchTheFrozenConfig`.
- **A static MO route required a `filter_connector_id`** — fixed. Legacy's `StaticMORoute` matches on its filter list alone, so the Go constraint made a legal legacy route unexpressible. An empty value now means "any inbound connector".
- ~~**The `adminweb-bundle-freshness` CI job is flaky by construction.**~~ — **fixed.** It rebuilt the bundle and byte-compared, but the minifier renames identifiers between environments, so an untouched bundle could fail while a stale one passed whenever the environments happened to agree. The bundle now carries `bundle.sourcehash`, stamped by `npm run build`; CI recomputes it from `web/` and compares. Deterministic, and it needs no Node.

### Nice-to-have / cleanup
- **Retire the `pickle_codec: "bridge"` option** once native has soaked in production (the bridge script is already out of the image).
- ~~CI guard for the embedded UI bundle~~ — **done**: the `adminweb-bundle-freshness` job rebuilds `internal/app/adminweb/dist` and fails on a diff. Added after the bundle went stale once in practice.

---

## Housekeeping — plan statuses that lag reality

These plans read `active` but their work has effectively landed; reconcile when touched:
- `001-dlr-submit-resp-publication.md`, `002-multipart-longsubmitsm.md` — the DLR and multipart paths are live and E2E-verified.
- `008-macro2-mo-dlr-admin.md` — MO / DLR / admin all delivered (#74–#85).
- `005-logging-message-path.md` (draft) / `004`, `006` (active) — functional logger rollout is done; strict oracle evidence and operational attestation remain under plan 015 G1/G3.
- `012-admin-plane-full-coverage.md` now reflects that groups are implemented in the current worktree; clean-candidate and commercial evidence remain separate plan 015 gates.

## Cross-references
- Plans: `docs/plans/`; plan 015 is the authoritative Python-deprecation gate, and plan 016 is the corrected release roadmap (CI integrity, the four real code gaps, and where Release A effort actually belongs).
- Session history: `docs/worklog.md` (newest on top).
- ADR-001: pure-Go SQLite for the admin store. ADR-002: React SPA + Go BFF. ADR-005: active-passive PostgreSQL fencing. ADR-006: durable CDR lifecycle. ADR-007: durable REST batch execution.
- Frozen contracts: `spec/compatibility/` matrices.

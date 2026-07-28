# Go Rewrite — Status: what's ready, what's left

- **Date:** 2026-07-27
- **Branch:** `go-rewrite`
- **One-line:** The core message gateway (MT send, MO receive, routing, DLR, interception) is functionally complete and Go-only on the hot path, the Python pickle bridge is retired, and the admin web UI is in. Remaining work is the billing engine, the jCli console, the last logging lines, and the formal prod cutover gate.

This is a living overview. The authoritative detail lives in `docs/plans/NNN-*.md` and `docs/worklog.md`; where a plan's own `Status:` field lags reality it is noted under **Housekeeping** below.

---

## Ready — done and merged on `go-rewrite`

Every item is differential-tested against the frozen Python oracle unless stated otherwise.

### Messaging core
- **MT send path (HTTP + SMPPs → SMSC).** Front-door parse/validate, envelope build, `SubmitSM` encode, priority/validity/schedule, service_type/protocol_id/TON-NPI, empty-param handling. Byte-for-byte proven against the real legacy send path (`update_submit_sm_pdu` + `preSubmitSm` + `PDUEncoder`) for default and non-default connectors. — plans 003, 009 (#87); MT-path parity audit (#46–#52)
- **Multipart / long messages.** SAR and UDH segmentation, per-part encode, reassembly. — plan 002; verified live in the gateway E2E (2-part SAR submit)
- **MO receive path (SMSC → HTTP / SMPPs).** `deliver_sm` + `data_sm` decode (distinct SMPP 3.4 codec, byte-parity), long-MO reassembly (`MultipartStore`), routing to HTTP and SMPPs destinations. — #83, #85
- **DLR (delivery receipts).** Levels 1/2/3, submit_sm_resp correlation, terminal-reject reporting; level-2 connector quirk preserved. — plan 001; #76
- **Routing & filters.** MT and MO route tables; filter engine (source/destination/short_message/tag/date/time). Note: legacy `re.match` is anchored — use `.*X` for substring. — #77, #89
- **Interception.** MT hook (pre-encode) and MO hook (reject / mutate, on whole and reassembled messages). — #81, #88
- **SMPP bind roles.** transmitter / receiver / transceiver; receiver excluded from MT routing and submit consumption. — #82

### Platform
- **Native Go pickle codec — the Python bridge is retired.** All 10 AMQP encode/decode actions run natively (`internal/transport/gopickle` + `picklecompat.NativeCodec`), including the full DataCoding surface (DEFAULT + RAW + GSM message-class, all 256 bytes), schedule/validity times, and custom TLVs. `pickle_codec` defaults to `native`; the gateway runs E2E with no Python subprocess; `pickle_bridge.py` + `smpp-pdu3` + the `jasmin/` tree are dropped from `docker/Dockerfile.gateway`. — plan 010 (#93–#100, #101)
- **Admin data plane.** Connectors, routes, users managed programmatically with a live-mutable directory over pure-Go SQLite (modernc, ADR-001); changes apply without restart. — #78 / #79 / #80
- **Admin web UI.** A React SPA (Refine + Ant Design) embedded via `go:embed` over a session-authenticated Go BFF (`internal/app/adminweb`), served on its own listener (`admin.web_listen_address`): dashboard health, connectors (incl. start/stop), MT routes, MO routes, users, SMPPs bind accounts, and (opt-in) interceptors. No token in the browser; no Node in the runtime image. — plan 011, ADR-002
- **Live-mutable admin plane.** MO routes, MT/MO interceptors and SMPPs bind users joined connectors/routes/users as runtime-managed entities: each rebuilds its live table and swaps it atomically, reserves config-owned identities, and cannot half-apply. Interceptor editing is opt-in (`admin.allow_interceptor_editing`) because the scripts are arbitrary Python on the gateway host. — plan 012 Steps 1–5
- **Logging.** The SMS-MT audit line is byte-complete (config fields, TLVs, multipart) to `messages.log` with rotation, strict byte-parity with legacy. — plans 004/006 (#54–#62)
- **Prod hardening.** Opt-in durable AMQP topology (broker-restart drill 10/10), `/health` (postgres/amqp/connectors), inbound HTTPS + SMPPS-over-TLS, `env:` / `file:` secret refs. — plan 007 (#66–#73)

---

## Left — remaining to reach full parity + prod cutover

Ordered by the current roadmap.

1. **Billing (#1) — NEXT, and further along than previously recorded.** An earlier revision of this file said the engine was "unbuilt"; the code says otherwise. Already working: route rate × parts charged at submit (`internal/core/submit_service.go:319`, `AuthorizeAndApplyCalculatedSubmit`), early-decrement percent, a late-billing consumer with a durable ledger, balance and `submit_sm_count` quota enforcement, persistence, and golden/enforcement tests. **Genuinely missing:** CDRs (no call-detail records anywhere in the tree), end-to-end prepaid/postpaid validation against the oracle, and **groups** — see plan 012 Step 6, since group balances are part of the charge-precedence contract.
2. **jCli management console — DONE.** `internal/app/jcli` implements **every** command the frozen console has, and **all 19 captured transcripts replay byte-for-byte**. All 19 matrix rows are `MATCH` — the first surface in the project to reach that state, and on recorded evidence rather than assertion. Both open decisions resolved: named filter and HTTP-connector registries live in the admin store while routes still embed a resolved copy (so ADR-003/D-001 stands unchanged), and `persist`/`load` became real named snapshots (`admin_profiles`), which withdrew D-002.
3. **Native interceptor runner.** Interception still shells to `scripts/interceptor_runner.py` (stdlib-only). Porting it to Go removes Python from the image entirely; until then the gateway image keeps a Python base for this one contract.
4. **Logging rollout remainder.** Submit-lifecycle lines (requeue / reject) and the other components' loggers (smpp.server, throwers, router). — plans 004/005/006 (partial)
5. **Formal cutover gate.** The documented go/no-go validation (side-by-side vs Python Jasmin, replay, sign-off) that authorizes replacing the Python deployment. Flagged out-of-scope in plan 007; not yet built.

### Newly found (2026-07-27/28)
- **The HTTP front door enforces no user credentials.** It checks the password and nothing more. `internal/core/mtcredential` — the port of Jasmin's `HttpAPICredentialValidator`, unit-tested — is wired only for SMPPs binds, so per-user authorizations, value filters and the default source address are not applied to `/send`. `HTTP_MATRIX` rows H-004 and H-005 claim `MATCH`; that does not hold for those branches. **Fixed so far:** disabled users and disabled groups are now refused at authentication.
- **Connector defaults diverged from legacy, including on the wire** — fixed. A connector provisioned without explicit addressing emitted source TON/NPI 0/0 and destination 0/0 where legacy emits NATIONAL/ISDN and INTERNATIONAL/ISDN, so its `submit_sm` bytes differed from the Python gateway's. `trx_to`, `res_to` and `pdu_red_to` were wrong too, and `enquire_link` ran off the PDU read timer. Pinned by `TestConnectorDefaultsMatchTheFrozenConfig`.
- **A static MO route required a `filter_connector_id`** — fixed. Legacy's `StaticMORoute` matches on its filter list alone, so the Go constraint made a legal legacy route unexpressible. An empty value now means "any inbound connector".
- **The `adminweb-bundle-freshness` CI job is flaky by construction.** Two builds of identical `web/src` produce bundles differing in minifier-generated identifier names, so a byte comparison against a rebuilt `dist` can fail spuriously. Replace it with a source-hash marker.

### Nice-to-have / cleanup
- **Retire the `pickle_codec: "bridge"` option** once native has soaked in production (the bridge script is already out of the image).
- ~~CI guard for the embedded UI bundle~~ — **done**: the `adminweb-bundle-freshness` job rebuilds `internal/app/adminweb/dist` and fails on a diff. Added after the bundle went stale once in practice.

---

## Housekeeping — plan statuses that lag reality

These plans read `active` but their work has effectively landed; reconcile when touched:
- `001-dlr-submit-resp-publication.md`, `002-multipart-longsubmitsm.md` — the DLR and multipart paths are live and E2E-verified.
- `008-macro2-mo-dlr-admin.md` — MO / DLR / admin all delivered (#74–#85).
- `005-logging-message-path.md` (draft) / `004`, `006` (active) — foundation done; see **Left #5** for the remainder.

## Cross-references
- Plans: `docs/plans/` (003, 007, 009, 010, 011 are `done`).
- Session history: `docs/worklog.md` (newest on top).
- ADR-001: pure-Go SQLite for the admin store. ADR-002: React SPA + Go BFF for the admin web UI.
- Frozen contracts: `spec/compatibility/` matrices.

# Macro 2/3 — MO inbound, terminal DLR, filter routing, admin, interception

- **Date:** 2026-07-26
- **Status:** active
- **Summary:** Implement the five remaining functional gaps blocking "the whole app is rewritten": (1) `deliver_sm` ingestion in the SMPP client, (2) MO routing + terminal DLR (L2/L3), (3) filter-based routing config, (4) an admin/provisioning slice, (5) interception. Frozen-oracle discipline throughout: every byte that crosses AMQP or a log file is differential-tested against the legacy implementation.
- **Related:** docs/plans/007 (done — deployability/shadow), `jasmin/managers/listeners.py` (`deliver_sm_event_post_interception` — the frozen contract), `jasmin/managers/content.py` (`DeliverSmContent`, `DLR`), `jasmin/protocols/smpp/operations.py` (`isDeliveryReceipt`), memory `prod-testing-readiness`.

## Contract notes (from the frozen tree)

- **Receipt classification** (`isDeliveryReceipt`): TLV path first (`receipted_message_id`+`message_state` → `id` + mapped `stat`), then `short_message` regex fields (`id/sub/dlvrd/submit date/done date/stat/err/text`; TLV `id`/`stat` win); `sub`/`dlvrd`/`err` zero-pad to 3; it is a DLR iff `id` AND `stat` resolved. Defaults `ND`/empty text.
- **DLR publish** (receipt path): exchange `messaging`, routing key `dlr.deliver_sm`, body = `stat` string, `message-id` = `code_dlr_msgid(pdu)` (per-connector `dlr_msg_id_bases`: 0 verbatim/`upper().lstrip('0')`, 1 dec→hex, 2 hex→dec), headers `type=deliver_sm`, `cid`, `dlr_<k>` for every receipt field.
- **MO publish**: routing key `deliver.sm.<cid>`, body = pickled `RoutableDeliverSm(pdu, Connector(cid))` protocol 2, `message-id` = `randomUniqueId('deliver_sm', None, cid, None)`, headers `try-count:0, connector-id, concatenated, will_be_concatenated`; SMS-MO audit line; long-message parts (SAR or UDH `\x05\x00\x03`) go to Redis `longDeliverSm:<cid>:<ref>:<dst>` for reassembly before a concatenated re-publish.

## Steps

**Step 1 — `deliver_sm` ingestion + receipt path (PR A).**
- **Files:** `internal/core/smppc/session.go` (reader dispatch: handle `deliver_sm`/`data_sm`, respond `_resp` after processing), new `internal/core/smppc/receipt.go` (isDeliveryReceipt port + `code_dlr_msgid`), `internal/core/smppc/config.go` (`dlr_msg_id_bases`), DLR envelope builder + publish on the connector's AMQP path.
- **Verify:** differential test — oracle venv runs the legacy `isDeliveryReceipt`/receipt fixtures vs the Go port over a corpus (TLV-only, text-only, both, padding, non-DLR); DLR envelope headers/body golden vs legacy `DLR` content; live e2e: fake SMSC pushes a receipt-shaped `deliver_sm`, gateway acks ROK and `dlr.deliver_sm` lands with parity headers.

**Step 2 — MO publish (PR B).**
- **Files:** `scripts/pickle_bridge.py` (`encode_routable_deliver_sm` action: params+cid → pickled `RoutableDeliverSm`), `internal/transport/picklecompat` (encoder wrapper), session MO branch (publish + SMS-MO line via the sm-listener logger).
- **Verify:** pickle byte-differential (bridge output vs legacy `DeliverSmContent` body for the same PDU), SMS-MO line byte-parity test, e2e MO from fake SMSC → `deliver.sm.<cid>` queue.

**Step 3 — MO router + thrower e2e (PR C) [item 2 first half].**
- **Files:** `internal/core/router` (consume `RouterSubscriptions.DeliverSM`, replace the Reject stub), MO route config (`mo_routes[]`: default/static + http connector defs), bridge `encode_routed_deliver_sm` (router → `deliver_sm_thrower.*` payload the existing mothrower already decodes), smpps destination via `smppsdelivery.MOSink`.
- **Verify:** e2e — simulator-injected MO reaches an HTTP callback (mothrower) and an smpps-bound receiver.

**Step 4 — terminal DLR L2/L3 (PR D) [item 2 second half].**
- **Files:** `internal/app/dlrlookup` (handle `dlr.deliver_sm`: Redis receipt-msgid→submit-msgid mapping, then `dlr_thrower.*` publish; exact gap depends on current L1-only dispatch), Redis mapping written on submit path if missing.
- **Verify:** e2e — submit with `dlr-level=2`, simulator sends receipt, HTTP DLR callback receives DELIVRD; parity of DLRContentForHttpapi headers.

**Step 5 — long-message MO reassembly (PR E).**
- **Files:** session MO branch (SAR/UDH detection per contract), Redis `longDeliverSm` store + concat re-publish.
- **Verify:** differential on part detection; e2e 2-part MO reassembles.

**Step 6 — filter routing config (item 3).**
- **Files:** `internal/app/outbound/config.go` (`RouteConfig` filter fields), `internal/core/routingfilter` wiring for MT + MO (user/group/source/destination/content per legacy semantics), validation.
- **Verify:** golden routing-decision tests vs legacy fixtures; e2e: two users, content-filtered routes select different connectors.

**Step 7 — admin/provisioning slice (item 4).**
- **Files:** new `internal/app/admin` (authenticated HTTP admin API: users/routes/connectors CRUD), durable persistence (Postgres), live application via existing managers. jCli byte-parity console is explicitly out of scope of this step and stays on the registry as unfinished contract rows.
- **Verify:** CRUD e2e with restart-survival; live connector add binds without reboot.

**Step 8 — interception (item 5).**
- **Files:** new `internal/app/interceptor` runner executing the user's Python scripts (bridge-style subprocess, same script semantics: routable in scope, smpp_status/http_status error returns, pickled-routable success), MO/MT hook points pre-routing (session MO path, outbound submit path), per-route interceptor config.
- **Verify:** differential vs `interceptord` on a corpus of scripts (pass-through, mutate, reject); e2e MO intercept mutates destination.

## End-to-end verification

The compose stack gains an MO/DLR drill: submit with dlr-level 2 → receipt → HTTP DLR callback; simulator-pushed MO → HTTP MO callback + smpps receiver; filtered routes select per user/content; admin CRUD survives restart; an interceptor script mutates an MO. Full suite + differentials green per PR; 3-fast-checks merge gate unchanged.

## Rollback

Each PR is additive behind config (MO routes, dlr level, filters, admin listener, interceptor blocks all optional). The bridge remains the pickle boundary; no wire-format changes.

## Risks

- **Pickle parity on the MO edge** — legacy router/throwers may consume Go-published MO content during shadow; byte-differential the pickled routable, not just fields.
- **Receipt classification drift** — regex/padding subtleties (`stat` 7 chars, TLV precedence) are silent-drop material if wrong; corpus-test hard.
- **Redis reassembly semantics** — key shapes and concat order must match legacy or mixed-version operation corrupts long MO.
- **Interception keeps Python** — user scripts are Python by contract; the runner isolates but does not remove that dependency.

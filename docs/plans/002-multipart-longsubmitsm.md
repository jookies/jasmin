# Multipart submit_sm — LongSubmitSm port (chained-pickle fan-out)

- **Date:** 2026-07-26
- **Status:** active
- **Summary:** Make the Go smppc send all parts of a legacy chained SubmitSM (nextPdu) and aggregate their responses into one durable outcome (MT-path audit GAP 2), matching Jasmin's LongSubmitSmTransaction.
- **Related:** MT-path parity audit backlog (memory, GAP 2); DLR publication plan `docs/plans/001-dlr-submit-resp-publication.md` (the aggregated response feeds one DLR).

## Context

A long message published by the legacy front door is one pickled `SubmitSM` carrying a `nextPdu` chain of N parts (confirmed: a 300-char GSM7 message → 2 parts, default SAR split, each with `sar_msg_ref_num`/`sar_total_segments`/`sar_segment_seqnum`). The legacy client (`doSendRequest`) sends **all N** wire PDUs, each in its own `OutboundTransaction`, grouped into one `LongSubmitSmTransaction` keyed by `sar_msg_ref_num` (or the UDH ref); it completes when all N respond and reports **one** aggregated response upstream → one msgid, one DLR.

The Go bridge (`scripts/pickle_bridge.py:decode_submit_sm`) projects only `obj.params` (part 1) and drops `nextPdu`, and the Go session (`internal/core/smppc/session.go:Submit`) is strictly one-message → one body → one PDU → one `pending[seq]` → one `Commit`. So a chained pickle is sent **truncated to its first segment** — customer-visible content loss and billing collected for parts never sent. This only affects the legacy-publisher → Go-smppc edge (the Go-native path already splits at HTTP ingestion via `segmentation.Segment`, one part per queue message).

Desired outcome: on the cutover edge, a chained pickle sends all N parts and produces one aggregated durable outcome and one DLR, byte-faithful to the legacy client.

## Approach

Port `LongSubmitSmTransaction`: keep **one** durable part/msgid per queue message, but the session sends N PDUs (one per chain part, each its own seqNum) and tracks them under a shared chain tracker. Each part's submit_sm_resp — ROK or error — counts toward completion; when the last arrives, the session Commits **one** outcome using the **last-arriving** part's status and SMSC message-id (the legacy quirk at protocol.py:238), settles the delivery once, and (via `docs/plans/001`) publishes one DLR.

Trade-off vs. the "re-expand into N parts" alternative (rejected): this preserves legacy parity — one msgid, one DLR, the last-arriving-response aggregation — at the cost of new send/response bookkeeping in the session. A non-chained submit keeps the exact current single-PDU path.

Key quirk to record (KNOWN_QUIRKS): the aggregated response is the **last-arriving** part's response, not part N or a status merge — order-dependent when parts are answered out of order.

## Steps

### Step 1: Bridge projects the whole chain

- **Files:** `scripts/pickle_bridge.py` (`decode_submit_sm`).
- **Changes:** Walk `obj` and its `nextPdu` chain; project each node's params with the existing per-part logic (SAR TLVs 0x020c/0e/0f, message_payload, data_coding via `_data_coding_value`, times, custom_tlvs). Return `{"parts": [body, ...]}` (length 1 for a non-chained submit) instead of a bare body. Keep the SAR completeness/consistency checks per part.
- **UDH `more_messages_to_send` (done, follow-up):** projected via the legacy `MoreMessagesToSendEncoder` to its 1-byte wire value (MORE_MESSAGES→1 on non-final parts, NO_MORE_MESSAGES→0 last), with a `smppwire` `MoreMessagesToSend` field decoded/re-encoded byte-identically (verified by the codec optional-differential and a UDH chain differential). Both SAR and UDH multipart now send every part faithfully.
- **Verify:** `python -m py_compile scripts/pickle_bridge.py`; a PYTHON_PATH differential (Step 5) builds a 300-char SAR message and a UDH message via `SMPPOperationFactory` and asserts the projected part count, per-part SAR/UDH fields, and 0x0426 presence match the chain the legacy `doSendRequest` would send.

### Step 2: Decoder returns the chain

- **Files:** `internal/transport/picklecompat/submit_decoder.go` (add `0x0426` to the optional-TLV allowlist and the `SubmitSMBody.Optional` shape; introduce `type SubmitSMChainPart struct { Body smppwire.SubmitSMBody; CustomTLVs []tlv.TLV }`), and a new `DecodeSubmitSMChain(ctx, data) ([]SubmitSMChainPart, error)` that unmarshals `parts` and runs the existing per-part validation/TLV projection. Keep `DecodeSubmitSM` as a thin wrapper returning `parts[0]` for any remaining single-body callers/tests.
- **Verify:** `go build/vet ./...`; existing `submit_decoder_test.go` stays green (single-part path); a new unit test asserts a 2-part fixture decodes to 2 bodies with correct SAR seqnums.

### Step 3: Session sends N PDUs under one durable part

- **Files:** `internal/core/smppc/session.go` (`Submit`, `pendingRequest`, the response path `handleSubmitResponse`/`takePending` → `Commit`), likely a new `internal/core/smppc/chain.go`.
- **Changes:** In `Submit`, call `DecodeSubmitSMChain`. For a single part, keep the current path unchanged. For N>1: one `durablePartKey` and one `BeginAttempt` as today, then loop the parts — encode each with its own `nextSequenceLocked()` seq, register a `pendingRequest` per seq that shares one `*submitChain{ partKey, delivery, attempt, envelope, remaining: N, done: false, lastStatus, lastSMSCID, mu }`, and write each frame. The delivery is settled and the response Committed **once**, when `remaining` reaches 0. A write failure or per-part timeout fails the whole chain (settle delivery failure / mark unknown) — mirroring `cancelLongSubmitSmTransactions`.
- **Changes (response path):** when a submit_sm_resp arrives for a chained pending, record its status/SMSC-id as `last*`, decrement `remaining`; if `remaining>0` return without settling; if `remaining==0`, run the existing single `Commit` with the last-arriving status + SMSC-id and settle the delivery once. Non-chained pendings are unchanged.
- **Verify:** `go build/vet ./...`, `go test -race ./internal/core/smppc/...`; a new session unit test drives a 2-part chain against a fake connection and asserts 2 PDUs written (distinct seqNums), exactly one `Commit`, and that its status/SMSC-id is the second-arriving response's.

### Step 4: Record the quirk

- **Files:** `spec/compatibility/KNOWN_QUIRKS.md`.
- **Changes:** Add a quirk: multipart aggregated response/DLR uses the last-arriving part's submit_sm_resp (status + SMSC message-id), order-dependent (protocol.py:238). Reference this plan.
- **Verify:** doc-only; linked from the plan.

### Step 5: Differential + integration verification

- **Files:** `internal/transport/picklecompat/submit_decoder_chain_differential_test.go` (new), `internal/app/gateway/runtime_integration_test.go` (extend).
- **Changes (done):** PYTHON_PATH differential (`submit_decoder_chain_differential_test.go`): build a 300-char SAR message via `SMPPOperationFactory`, pickle protocol 2, feed `DecodeSubmitSMChain`; assert the projected part count, per-part SAR ref/total/seqnum and short_message bytes equal the legacy `nextPdu` chain. Plus a session unit test (`session_chain_test.go`) driving a 2-part chain against a fake connection: both parts sent with distinct seqnums, the message settles exactly once (only on the last response), and (via the last-arriving pdu) the last response wins.
- **Integration (deferred):** a full end-to-end gateway test (publish a raw chained pickle → N wire PDUs at the fake SMSC → one aggregated response + one DLR) needs a chained-pickle producer in the test harness; deferred. The bridge/decoder chain differential plus the session send-N/settle-once/last-wins unit test cover the mechanism; the single-part durable path is unchanged and still covered by the CI integration harness.
- **Verify:** differential + unit tests green; full `go test ./...` shows no new failures (only pre-existing local py3.9 env artifacts).

## End-to-end verification

CI: a legacy-shaped chained pickle → N wire PDUs at the fake SMSC → one durable result, one submit response, one DLR. Locally: build/vet/race + the chain differential (CI Python). Regression guard: single-part submits keep the exact current path (existing decoder/session/storage tests unchanged, incl. the outbox event-order test from GAP 3).

## Rollback

Additive and branch-isolated. `DecodeSubmitSM` remains for single-body callers; reverting the branch drops the chain path with no schema or topology change.

## Risks

- **Durable model coupling:** N pendings share one attempt/partKey — a partial failure (one part errors/times out) must fail the whole part deterministically, matching `cancelLongSubmitSmTransactions`. Mitigation: chain fails on first write error/timeout; unit tests cover partial failure and the settle-once invariant.
- **Response ordering:** the last-arriving-response aggregation is order-dependent (the quirk). Mitigation: recorded in KNOWN_QUIRKS; the differential fixes an order and the session test asserts the second-arriving response wins.
- **Bridge output-shape change** (`{"parts":[...]}`): only `DecodeSubmitSM(Chain)` consumes it. Mitigation: wrapper preserves the single-body API; full `go test ./...` sweep before push (lesson from GAP 3's outbox event-order test).
- **Billing:** legacy bills per part; the Go billing path currently bills per queue message — confirm whether multipart billing needs N× before claiming billing parity (may be out of scope for this slice; flag if so).

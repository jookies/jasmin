# Correctness audit: termination-plane parity, usage reporting, and interceptor safety

- **Date:** 2026-08-02
- **Status:** active
- **Summary:** Twelve verified defects found by a multi-agent audit, most of them one theme — the local termination plane is a second-class citizen that skips billing settlement, the submit transaction, multipart part-keying, and every report. All twelve are now fixed on `fix/audit-findings-004`; the interceptor sandbox remains a deployment decision.

## Resolution status

All twelve findings are fixed with regression tests, on branch
`fix/audit-findings-004`. Each fix names its finding in its commit message.

| Finding | Fix |
| --- | --- |
| F-01 late billing never settles | Terminated parts raise the same late-billing intent a carrier acceptance raises. **Behaviour change** — see below. |
| F-02 multipart part key | `cdrPartKey` derives the key correctly; every segment marks its own part. |
| F-03 Accepted always 0 | `TerminatedLocally` is its own counter in both backends. |
| F-04 columns don't sum | Submission counters partition Parts exactly. |
| F-05 pending never drains | `NoReceiptExpected` split out; delivery counters partition Parts. |
| F-06 ledger stays PENDING | Terminated parts run BeginAttempt/CommitResponse. |
| F-07 silent truncation | Always logged with dropped-byte count; `long_content_reject_over_max` to refuse. |
| F-08 interceptor wedge | Per-script deadline (`DefaultScriptTimeout`, 30s). |
| F-09 terminal rows look in-flight | `TERMINATED_LOCALLY` badged terminal and filterable; "pending" only when a receipt is expected. |
| F-10 bogus spool filters | `dlq` replaces `failed`/`dead`. |
| F-11 reconciliation goes red | Retention prunes the submit ledger too, skipping parts with undispatched outbox events. |
| F-12 unknown ceiling reads unlimited | `group_live_error` renders "not readable". |

Three problems were caught by running the result on a live gateway rather than
by the test suite, which is the main lesson of this round:

1. `CommitResult` projects a successful result as `SMSC_ACCEPTED`
   unconditionally, so the new settlement relabelled every terminated message as
   carrier-accepted — undoing F-03. `Result.LocalTermination` now selects the
   right projection.
2. `FINAL_RESULT_STATE_MISMATCH` required a SUCCESS result's CDR to be
   `SMSC_ACCEPTED`, so once terminated parts committed results, a healthy
   gateway reported reconciliation mismatches. Both states are successes.
3. The new dispatch logging exposed a **pre-existing** infinite loop: the
   `late-bill-amount` header is on every envelope carrying `"0"` when nothing is
   deferred, so a zero-value intent was raised for ordinary traffic, refused by
   the ledger, and requeued forever — instantly and silently before this work.
   Intents are now raised only above zero, and one whose CDR has already settled
   is discarded (`cdr.ErrLateBillingSettled`) instead of retried.

Final state on the dev gateway: no errors or warnings at all, 13 connectors
bound, and a terminated submit recording `TERMINATED_LOCALLY` with ledger
`RESULT_COMMITTED`, no spurious intent, and clean reconciliation.

**Behaviour change to announce (F-01).** A deployment that both split-bills
(`early_decrement_balance_percent` on a rated route) *and* routes to a
termination connector will now collect the deferred half it previously quoted
and dropped. Deployments with `late_amount` of zero — every unrated or
fully-prepaid route — are unaffected. This resolves open question 1 in favour of
charging, on the grounds that the operator's own configuration asks for it and
local termination is a confirmed delivery; reverse it by setting
`early_decrement_balance_percent` to 100 on the affected routes.

## Problem

The billing Usage-statements console showed a customer row that cannot be true:

| Messages | Parts | Delivered | Delivery pending | Accepted | Rejected | Charged |
| --- | --- | --- | --- | --- | --- | --- |
| 65,823 | 65,823 | 64,154 | 1,669 | 0 | 0 | 0.0000 |

64,154 messages were delivered while 0 were ever accepted. Investigating that
contradiction opened a much larger seam: traffic that terminates on this gateway
(rather than being relayed to a carrier) is handled correctly by the message path
but is invisible or wrong nearly everywhere else — billing settlement, the submit
transaction, multipart part-keying, reconciliation, and every operator-facing
screen.

That matters commercially. On a termination-heavy deployment the quoted
`submit_sm_resp` portion of every split-billed message is **never charged**, and
multipart terminated messages **never send their terminal DLRs**.

This document records every finding with evidence so the fixes can be scheduled
independently. Nothing here is fixed yet.

### How these were found and how far to trust them

Six risk-dimension finder agents swept the codebase, and each top finding was
re-checked by an adversarial verifier agent instructed to refute it. Findings
marked **CONFIRMED** were verified against the actual code (several also against
live database rows in the dev stack); those marked **UNVERIFIED** are single-pass
finder output that survived no second opinion — treat them as leads, not facts.
One earlier finding (an MO-thrower `Connectors[0]` panic) was correctly refuted
and is deliberately absent.

## Goals

- Make locally-terminated traffic a first-class citizen: billed, reconciled, and
  reported exactly like relayed traffic, or explicitly and visibly waived.
- Make the usage statement arithmetically self-consistent: no row may show more
  delivered than accepted, and the columns must account for every part.
- Stop silent money and message loss: no truncated-and-billed messages, no
  permanently unsettled billing intents, no dropped terminal DLRs.
- Bound the interceptor runtime so one bad script cannot wedge the pipeline.

## Non-goals

- Fixing the previous audit's separate backlog (idle-close siblings, pre-auth
  OOM, unbounded ledger growth). Those are tracked separately; only the items
  re-confirmed here are restated.
- Redesigning the billing model or the CDR schema wholesale. Each fix below is
  intended to be independently shippable.
- Byte-parity work against the legacy Python stack.

## Requirements

### Functional

Ordered by production impact. `F-01` and `F-02` lose money and messages today.

#### F-01 — Late billing never settles for terminated traffic (CRITICAL, CONFIRMED)

`internal/infra/storage/postgres_cdr.go:14-19` stamps `billing_outcome='PENDING'`
on admission whenever `late_amount > 0`, for every connector type, because
`internal/core/submit_service.go:568-574` quotes
`LateAmount = perPartBill.SubmitSmRespAmount` with no connector-type check. The
only code that ever settles that PENDING is the late-billing intent published by
the SMPP client on a real carrier acceptance
(`internal/core/smppc/response_publish.go:160`). The termination plane never
publishes one.

Consequence for any user with `early_decrement_balance_percent` set: the early
half is debited at submit, the late half is quoted, the message terminates and is
receipted — and the late half is **never charged**. The row stays `PENDING`
forever, so it is also never prunable, and it inflates
"Quoted, not yet applied" on statements indefinitely.

Fix: on the `TERMINATED_LOCALLY` transition (in `MarkCDRTerminated` or the spool
`WithAcceptance` hook), either publish the same idempotent late-billing outbox
intent the smppc success path creates, or explicitly waive it
(`LATE_BILLING_REJECTED` / `actual_late_amount=0`) in the same transaction. Also
extend the `ACCEPTED_LATE_INTENT_MISSING` reconciliation predicate to cover
`state='TERMINATED_LOCALLY'`.

#### F-02 — Multipart terminated messages never leave ADMITTED; terminal DLRs dropped (HIGH, CONFIRMED)

`internal/app/gateway/termination.go:273` builds the CDR part key as
`fmt.Sprintf("%s/%06d", messageID, 1)`. That is correct only for a single-part
submit. For a multipart submit the envelope message id **already** carries the
part suffix — `internal/app/outbound/submit_envelope_builder.go:141-143` sets
`messageID = "<aggregate>/<%06d seq>"` when `len(request.Parts) > 1` — so the
acceptance key becomes `"<uuid>/000002/000001"`, a `cdr_id` that does not exist.

`MarkCDRTerminated` therefore matches no row, the CDR parts stay in `ADMITTED`,
and every synthesized terminal receipt is refused by the final-DLR guard,
requeued to `MaxRetries`, and dropped. **The partner never receives terminal DLRs
for any concatenated message terminated locally.**

The in-code comment at `termination.go:265-270` states the assumption ("A
terminated message is spooled once as an assembled whole, so it settles part 1")
— the assumption is simply not true once the envelope id is suffixed.

Fix: pass `msg.MessageID` through unchanged when it already contains a `/%06d`
suffix and append `/000001` only to a bare aggregate id; call the acceptance hook
for each segment row from `Spool.RecordReceiptOnly`; and make acceptance failure
in `Spool.Record` non-fatal (log + metric, explicit `cdr.ErrNotFound` handling)
so a committed spool row can never requeue-loop.

#### F-03 — `Accepted` is structurally 0 for terminated traffic (HIGH, CONFIRMED)

This is the reported screenshot. `SummarizeCDRs` computes
`Accepted = count(*) FILTER (WHERE state='SMSC_ACCEPTED')`
(`internal/infra/storage/postgres_cdr.go:325`, identically at
`internal/infra/storage/sqlite_cdr.go:397`), but `cdr_records.state` is a
**mutable current-state column** overwritten on every event
(`postgres_cdr.go:62`, `UPDATE cdr_records SET state=$1 ... WHERE cdr_id=$7`).

`TERMINATED_LOCALLY` is a deliberately distinct state — `internal/core/cdr/model.go:38-47`
explains at length why it must not reuse `SMSC_ACCEPTED` — so locally-terminated
traffic is Delivered but never Accepted. Both storage backends share the defect
identically, so this is a consistent bug rather than a Postgres/SQLite divergence.

Note the correct source of truth already exists and is unused: `cdr_events` is
append-only and idempotent (`event_key` + `ON CONFLICT DO NOTHING`), so "was this
ever accepted" is answerable exactly.

Fix: count acceptance from `cdr_events.kind`, or count both `SMSC_ACCEPTED` and
`TERMINATED_LOCALLY` and surface them as separate columns ("carrier accepted" vs
"terminated here"). Apply the same change to both backends.

#### F-04 — `Accepted + Rejected` does not account for all parts (HIGH, CONFIRMED)

Five of the seven states — `ADMITTED`, `RETRY_PENDING`, `UNKNOWN_AFTER_SEND`,
`TERMINAL_TIMEOUT`, `TERMINATED_LOCALLY` — are counted in neither the Accepted
nor the Rejected filter, yet the console renders those columns beside Parts as
though they partition the population. An operator cannot reconcile the row, and
messages stuck in a non-terminal state are indistinguishable from messages that
never existed.

Fix: either add an explicit "in flight / other" column so the columns sum to
Parts, or render Accepted/Rejected as a breakdown that visibly does not total.

#### F-05 — "Delivery pending" is an unbounded bucket that can never drain (HIGH, CONFIRMED)

`DeliveryPending = count(delivery_state IS NULL OR delivery_state='')`
(`postgres_cdr.go:329`). There is **no `dlr_requested` / `registered_delivery`
column anywhere in `cdr_records`** — verified against the live schema — so the
gateway cannot distinguish:

- awaiting a receipt that was actually requested,
- no receipt will ever come because none was requested,
- terminated locally with no receipt recorded,
- failed before send.

All four are reported as "Delivery pending" forever. The reported 1,669 is this
bucket. The six possible `delivery_state` values are otherwise exhaustively
covered by the three buckets, so this is the only gap — but it is permanent.

Fix: persist whether a receipt was requested at admission, and count only those
as pending; report the rest as "no receipt expected".

#### F-06 — Termination plane bypasses the submit transaction (HIGH, CONFIRMED)

Admission writes a `submit_parts` row in state `PENDING` for every part
(`internal/infra/storage/postgres_submittransaction.go:129`), and only the smppc
path advances it (`BeginAttempt` / `MarkAttemptSent` / `CommitResult`). The
termination connector consumes the same `submit.sm.<cid>` queue but never calls
them (`internal/core/termination/manager.go:297`).

So `/api/message-status` reports `PENDING` **forever** for every message ever
routed over a termination connector, directly contradicting the billing console,
which shows the same message as `TERMINATED_LOCALLY` with a delivered receipt.

Fix: commit a submit result for the part in the same moment as
`MarkCDRTerminated` — a synthetic `ResultSuccess` carrying the synthesized SMSC
message id, or a dedicated terminal result kind.

#### F-07 — Over-long messages are silently truncated and billed as delivered (MEDIUM, CONFIRMED)

`segmentation.Segment` clamps the part count to `MaxParts`
(`internal/core/segmentation/segmentation.go:196-198`), **discards every payload
byte past the limit**, and records the loss by setting `truncated=true` (`:250-257`).
`Result.Truncated()` is defined at `:279` and has **zero non-test callers** in the
entire repository — verified by grep. `internal/core/submit_service.go:395`
consumes only `Parts()`.

A customer submitting an 1,800-character GSM-7 message with the default
`long_content_max_parts=5` has ~1,035 characters silently dropped, receives a
success message-id, and is billed for 5 parts. Neither the HTTP nor the REST
layer rejects over-length content below the 1 MB cap.

Fix: check `segmented.Truncated()` after the `Segment` call and reject with a
content-too-long error (config-gated reject-vs-truncate if legacy parity demands
truncation), surfacing truncation in the API response. At absolute minimum, log a
warning with the dropped-byte count.

#### F-08 — A single hung interceptor script wedges the whole pipeline (MEDIUM, CONFIRMED)

`internal/transport/pyintercept/runner.go:187` takes a single process-wide mutex
for every `Run()` and round-trips one shared subprocess, with **no execution
deadline of its own** — `decode()` (`:246-259`) aborts only on caller-context
cancellation. The HTTP submit path passes `r.Context()`
(`internal/transport/httpcompat/handler.go:309`), and `net/http`'s `WriteTimeout`
does **not** cancel a running handler's context.

An operator-installed script that infinite-loops therefore holds the mutex
forever, and all MT/MO interception blocks behind it permanently.

Fix: wrap each `Run()` in its own `context.WithTimeout` (a configurable
per-script budget) so a hung script is killed via the existing kill-and-respawn
path regardless of the caller context; longer term replace the single
mutex+subprocess with a small worker pool to remove head-of-line blocking.

#### F-09 — Usage console renders terminal records as in-flight (MEDIUM, CONFIRMED)

`web/src/pages/billing/usage.tsx:351-353` renders `{value || "pending"}` and
`deliveryTone` (`:96-106`) maps empty/undefined to the in-progress tone. Any
record with an empty `delivery_state` shows a blue "pending" badge — including
`SMSC_REJECTED` parts that were refused and will never deliver. An operator
drilling into the 1,669 pending parts concludes receipts are merely delayed.

`TERMINATED_LOCALLY` is also missing from `stateTone` (`:81-94`) and from the
submission-state filter (`:270-277`), which lists only six of the seven states —
so on a termination gateway the filter cannot select the state most rows are in.

Fix: add `TERMINATED_LOCALLY` to the tone map (terminal/positive) and the filter
options; show "pending" only when a receipt is genuinely expected and render "—"
otherwise. Rebuild the committed `web/dist` per project convention.

#### F-10 — Spool page offers delivery filters the backend rejects (MEDIUM, CONFIRMED)

`web/src/pages/messages/list.tsx:225-230` offers `pending / delivered / failed /
dead`, but `internal/core/msgspool/store.go:53-55` defines exactly `pending /
delivered / dlq`, and `Service.Search` returns `ErrInvalidInput` → HTTP 400 for
anything else. Selecting "failed" or "dead" replaces the listing with an error
state, and **there is no way to list dead-lettered messages at all**; `dlq` rows
also render in a neutral tone.

Fix: replace the two bogus options with `{ value: "dlq", label: "dead-lettered" }`
and map `dlq` to a negative tone. Frontend-only change.

#### F-11 — Reconciliation goes permanently red after the first prune (MEDIUM, UNVERIFIED)

`PruneCDRs` deletes only from `cdr_events` and `cdr_records`
(`postgres_cdr.go:401-405`, SQLite twin `sqlite_cdr.go:494-505`). Nothing ever
deletes `submit_parts` / `submit_results` / `submit_billing_intents`. The
`SUBMIT_PART_WITHOUT_CDR` check joins the two, so after the first routine prune it
reports one issue per pruned row and `Healthy()` stays false forever — training
operators to ignore a red reconciliation card, which then hides a genuine
integrity failure later.

Related and separately observed: locally-terminated messages have **no**
`submit_results` rows at all (verified in the dev database), so several
reconciliation checks silently skip that entire traffic class rather than
false-alarming on it. Terminated traffic is effectively unreconciled.

Fix: make the two lifecycles agree — prune the submit-transaction rows for each
pruned `cdr_id` in the same transaction, or scope the check to the retention
window.

#### F-12 — Unreadable group quota renders as "unlimited" (MEDIUM, UNVERIFIED)

`internal/app/adminweb/handlers_billing.go:122-127` sets the
`group_remaining_*` fields only when the live group-quota lookup returns
`found=true`; on a miss it omits them, and the UI maps the absent field to
"unlimited". A group whose quota failed to re-apply to the live directory is
therefore displayed as having **no ceiling** — the most permissive possible
reading of an unknown state, on a money screen.

Fix: add an explicit `group_quota_readable` field and render "not readable",
mirroring the pattern the per-user balance column already uses.

### Non-functional

- **Interceptor sandboxing (MEDIUM, UNVERIFIED).** `scripts/interceptor_runner.py:80`
  executes operator-supplied Python via `eval(compile(...), glo)` with real
  `__builtins__`; `_SAFE_MODULES` only *adds* modules and restricts nothing, so a
  script has `__import__`, `open()`, `os`, `subprocess` and sockets. This is
  acceptable only while interceptor authoring is restricted to fully trusted
  operators. If a semi-trusted integrator can ever hold an admin token with
  `allow_interceptor_editing`, it is a straightforward host compromise, and
  restricted builtins are not a sufficient fix — it needs a separate
  low-privilege process with seccomp/namespaces.
- Fixes to `SummarizeCDRs` must be applied to **both** the Postgres and SQLite
  implementations; they are maintained as independent SQL twins and already
  drifted once (missing `COALESCE` on the SQLite money sums).
- Any change to the reported columns is customer-visible on invoices; statement
  arithmetic changes should be announced rather than shipped silently.

## User flows

**Flow that exposed this (operator, today):** open Billing → Usage statements for
a window → read a per-customer row → the row is arithmetically impossible
(delivered > accepted, accepted + rejected ≠ parts) → drill into Usage records →
terminal rows are badged blue "pending" and the state filter cannot select them.

**Flow that silently loses money (customer, today):** a split-billed user submits
over a termination route → the early half is debited → the message terminates and
is receipted → the late half stays `PENDING` forever and is never charged, while
statements show it under "Quoted, not yet applied".

**Flow that silently loses messages (partner, today):** a partner submits a
2-segment long SMS over a termination connector with registered delivery → both
CDR parts stay `ADMITTED` because the part key is malformed → both terminal
receipts are refused, requeued, and dropped → the partner never learns the
outcome.

**Target flow after the fixes:** every part of every message, relayed or
terminated, advances through the same states, settles its billing, commits a
submit result, reconciles, and appears with a truthful label on both screens.

## Open questions

1. **Should terminated traffic be charged the late (`submit_sm_resp`) portion at
   all?** Delivery is confirmed locally, which argues yes. The alternative is an
   explicit waiver. This is a commercial decision, not a technical one, and it
   gates `F-01`. Owner: product/billing.
2. **Should `Accepted` merge `TERMINATED_LOCALLY` into one column or show it
   separately?** Merging makes the row add up; splitting preserves the model's
   deliberate distinction. Recommendation: split, since `model.go:38-47` argues
   the two facts are genuinely different. Owner: whoever takes `F-03`.
3. **Is truncation or rejection the legacy-parity behaviour for over-length
   content (`F-07`)?** The Python stack's behaviour needs checking before
   choosing; a config gate may be required either way. Owner: rewrite parity.
4. **Who may author interceptors in the target deployment model?** The answer
   decides whether the sandbox gap is acceptable-as-documented or a blocker.
   Owner: security/ops.

## Coverage gaps

Not examined in this round, and therefore not clean — only unaudited: the
`submit_sm` pack path for integer truncation; `internal/state/rediscompat` key and
TTL correctness; the `fmt.Sprintf`-built SQL in `sqlite_interceptor.go`,
`sqlite_routing.go` and `internal/app/admin/*_specs.go` (a finder judged the
interpolated values to be trusted identifiers, but that was never independently
verified); admin-surface parity across jCli / admin REST / web / config file; and
the React admin UI beyond the two pages named above. The audit was also cut short
of its planned verification depth, so `F-11`, `F-12` and the interceptor sandbox
item carry single-pass confidence only.

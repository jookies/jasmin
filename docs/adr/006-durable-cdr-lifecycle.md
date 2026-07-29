# ADR-006 — Durable content-free CDR lifecycle

- **Date:** 2026-07-29
- **Status:** active, phase 1 implemented
- **Scope:** successful durable MT admission through the SMSC response; DLR
  terminal delivery, export/retention automation and reconciliation reports
  remain cutover blockers.

## Context

The Go gateway charged submits and durably tracked every logical part, socket
attempt, SMSC response, late-billing intent and AMQP side effect, but it emitted
no commercial record. The frozen Python tree has no CDR implementation, so
there is no byte-for-byte oracle to copy or compatibility row to promote.

The submit transaction repository is the only correct write boundary. A
separate best-effort CDR consumer could lose an event after the account was
charged, while a synchronous external exporter could make commercial traffic
depend on an analytics system.

## Decision

Store a current `cdr_records` projection and an immutable `cdr_events` trail in
the same PostgreSQL transaction as submit admission, attempt uncertainty and
SMSC result commitment. SQLite implements the same schema only for local and
unit tests; the production runtime continues to require PostgreSQL.

### Identity and deduplication

One CDR represents one generated submit part:

- `cdr_id = <aggregate-message-id>/<six-digit-part-number>`;
- aggregate `message_id`, `part_number` and `part_count` make multipart
  reconciliation explicit;
- the admission event key is `cdr:<cdr-id>:admitted`;
- an ambiguous attempt event key is
  `cdr:<cdr-id>:attempt:<attempt-id>:unknown`;
- a response event key is `cdr:<cdr-id>:attempt:<attempt-id>:result`.

Every key is a primary key. A replay can update neither the immutable trail nor
the current projection twice. The existing result uniqueness and transaction
lock remain the first fence; the CDR key is a second commercial dedupe fence.

### Lifecycle

Phase 1 records:

| State | Terminal | Meaning |
|---|---:|---|
| `ADMITTED` | No | Charged as applicable and committed to the durable submit/outbox transaction |
| `UNKNOWN_AFTER_SEND` | No | The write may have reached the SMSC; a new attempt is fenced and separately identified |
| `RETRY_PENDING` | No | SMSC response matched the configured retry policy |
| `SMSC_ACCEPTED` | Yes for submission | SMSC accepted the part and returned an opaque message id |
| `SMSC_REJECTED` | Yes | Non-retryable SMSC rejection |
| `TERMINAL_TIMEOUT` | Yes | A terminal timeout result was explicitly committed |

`SMSC_ACCEPTED` is a terminal **submission** outcome, not proof of handset
delivery. A later phase must append the correlated final DLR without replacing
the accepted event.

### Identifiers and commercial fields

Each record contains:

- stable external user id and stable legacy group gid when present;
- route id as the MT routing-table slot (`mt:<order>`), selected connector id,
  ingress (`httpapi` or `smppsapi`), aggregate/part ids and bill id;
- per-part route rate, early amount, late amount, billing mode and currency;
- attempt id, SMPP status and opaque SMSC message id after a result.

Existing Jasmin rates are unitless. Currency is therefore ISO 4217 `XXX`
("no currency") until an operator-owned settlement currency becomes explicit;
silently labelling historical route units as USD/EUR would be incorrect.

Billing modes are derived, never caller supplied:

- `FREE`: early and late amounts are zero;
- `PREPAID`: all value is charged at admission;
- `POSTPAID`: value is eligible only after SMSC acceptance;
- `SPLIT`: an early charge plus an acceptance-time remainder.

The late amount is the quoted/idempotent billing intent. Actual late application
remains owned by `submit_billing_intents` and its billing ledger; reconciliation
must join by the part's stable `:20-late-billing` event key.

### Privacy

CDRs never store message content, source/destination addresses, passwords,
bind data, callback URLs, raw PDUs, custom TLVs or vendor TLVs. Commercial
identifiers and opaque SMSC message ids are still operational data and must
follow database access controls and audit policy.

### Durability, retry, retention, export and reconciliation

- Durability and retry use the production PostgreSQL submit transaction and its
  existing idempotent retry fences. CDR failure aborts the enclosing durable
  admission/result transaction.
- No automatic deletion is enabled in phase 1. This is safer than silently
  deleting billable history before legal/finance owners approve a retention
  period. A retention decision and tested pruner remain required before
  production cutover.
- No exporter is enabled in phase 1. The normalized tables are the source of
  truth; a cursor-based JSONL/CSV export with checkpointing and schema version
  remains required before settlement use.
- Reconciliation must compare admitted parts to submit results, accepted
  late-amount rows to the billing application ledger, and final DLRs once that
  phase exists. Missing/duplicate joins are alerts, never silent correction.

## Billing compatibility mapping

No registry status changes: CDRs are greenfield evidence, not Python parity.
The commercial record consumes the already-defined behavior as follows:

| Billing row | CDR dependency / required fixture |
|---|---|
| B-001 route rate | rated and unrated route expose the selected per-part rate |
| B-002 multipart | one aggregate produces one CDR per generated part with exact count/order |
| B-003 unlimited | zero amounts produce `FREE` without inventing a charge |
| B-004 insufficient balance | rejected authorization produces no admitted CDR |
| B-005 insufficient count | rejected authorization produces no admitted CDR |
| B-006 early decrement | admission amount equals the early debit |
| B-007 late decrement | accepted result links the quoted late amount to the durable billing intent |
| B-008 float order | stored binary64 rate/amounts retain the proven calculation order |
| B-009 persistence | quota persistence is independent; CDR admission is transaction-durable |
| B-010 redelivery | repeated admission/attempt/result keys create one event each |
| B-011 HTTP/SMPP parity | equivalent ingress produces equal commercial fields except ingress identity |

The minimum fixture set is: rated and unrated single-part admission; finite and
unlimited quota; multipart; early percentages 1/50/100; success, rejection,
retry then success, ambiguous send then redelivery; duplicate admission/result;
and equivalent HTTP/SMPPs submits. Oracle fixtures prove the underlying billing
values only—there is no legacy CDR output to label `MATCH`.

## Consequences and remaining work

Phase 1 gives every successfully admitted MT part a durable, deduplicated,
content-free commercial lifecycle through SMSC acceptance/rejection. It does
not yet make roadmap item 18 complete. Required follow-ups are:

1. append correlated final DLR state and delivery timestamps;
2. record/settle the actual late-billing application outcome;
3. operator approval for currency and retention, followed by a tested pruner;
4. versioned cursor export and reconciliation/alert jobs;
5. read/export authorization and audit logging;
6. CDRs for pre-admission front-door rejections only if commercial owners
   decide rejected traffic belongs in the settlement record.

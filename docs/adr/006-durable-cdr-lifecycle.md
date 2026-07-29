# ADR-006 — Durable content-free CDR lifecycle

- **Date:** 2026-07-29
- **Status:** accepted, implemented
- **Scope:** durable MT admission, SMSC response, late-billing application,
  correlated final DLR, retention, versioned export, reconciliation and audited
  role-based access.

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

The submission projection records:

| State | Terminal | Meaning |
|---|---:|---|
| `ADMITTED` | No | Charged as applicable and committed to the durable submit/outbox transaction |
| `UNKNOWN_AFTER_SEND` | No | The write may have reached the SMSC; a new attempt is fenced and separately identified |
| `RETRY_PENDING` | No | SMSC response matched the configured retry policy |
| `SMSC_ACCEPTED` | Yes for submission | SMSC accepted the part and returned an opaque message id |
| `SMSC_REJECTED` | Yes | Non-retryable SMSC rejection |
| `TERMINAL_TIMEOUT` | Yes | A terminal timeout result was explicitly committed |

`SMSC_ACCEPTED` is a terminal **submission** outcome, not proof of handset
delivery. A correlated final DLR appends the stable
`cdr:<cdr-id>:dlr:final` event and fills the separate delivery projection
(`DELIVERED`, `EXPIRED`, `DELETED`, `UNDELIVERABLE` or `REJECTED`) without
replacing the accepted submission event. The SMSC done timestamp is retained
when valid and the gateway receipt timestamp is always retained. The event is
written before the Redis correlation record is removed, so a PostgreSQL
failure remains retryable.

### Identifiers and commercial fields

Each record contains:

- stable external user id and stable legacy group gid when present;
- route id as the MT routing-table slot (`mt:<order>`), selected connector id,
  ingress (`httpapi` or `smppsapi`), aggregate/part ids and bill id;
- per-part route rate, early amount, late amount, billing mode and currency;
- attempt id, SMPP status and opaque SMSC message id after a result.

Existing Jasmin rates are unitless. `outbound.cdr_currency` is therefore
operator-configurable for new records and defaults to ISO 4217 `XXX` ("no
currency"). It accepts only three uppercase letters. Changing it never
relabels historical records.

Billing modes are derived, never caller supplied:

- `FREE`: early and late amounts are zero;
- `PREPAID`: all value is charged at admission;
- `POSTPAID`: value is eligible only after SMSC acceptance;
- `SPLIT`: an early charge plus an acceptance-time remainder.

The late amount is the quoted/idempotent billing intent. Actual application is
committed with the billing ledger update and records `APPLIED` plus the actual
amount, or `REJECTED` plus zero actual amount, using the part's stable
`:20-late-billing` event key. A replay cannot move an already-final billing
outcome to a contradictory state.

### Privacy

CDRs never store message content, source/destination addresses, passwords,
bind data, callback URLs, raw PDUs, custom TLVs or vendor TLVs. Commercial
identifiers and opaque SMSC message ids are still operational data and must
follow database access controls and audit policy.

### Durability, retry, retention, export and reconciliation

- Durability and retry use the production PostgreSQL submit transaction and its
  existing idempotent retry fences. CDR failure aborts the enclosing durable
  admission/result transaction.
- `cdr_retention_days` is the operator-owned retention policy. Zero (the safe
  default) disables deletion; a positive value runs bounded batches
  (`cdr_retention_batch_size`, default 1000). Only terminal records whose
  billing outcome is no longer pending are eligible. Access audit rows are not
  deleted with CDR data.
- Export is schema-versioned JSONL or CSV. The opaque base64url cursor contains
  version, admitted timestamp and CDR id; ordering is stable and every page is
  independently checkpointable. Filters are limited to commercial identity
  and time—content fields do not exist in the projection.
- Reconciliation compares CDR/submit-part existence, final submit results,
  accepted late intents, billing ledger/application projection and final-DLR
  event/projection consistency. It emits named nonzero issue counts as alerts
  and never silently corrects commercial data.
- The management boundary requires `cdr_reader`, `cdr_exporter` or
  `cdr_operator`. Every allowed or denied read/export/maintenance request is
  written to `cdr_access_audit`; access fails closed if its audit cannot be
  persisted.

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

## Consequences

Roadmap item 18 is functionally complete. Every successfully admitted part has
a durable, deduplicated, content-free lifecycle through submission, actual
late-billing outcome and final DLR when requested/received. Reconciliation and
bounded retention run in the production outbound runtime; management
transports consume the authorization/audit-enforcing CDR service.

Pre-admission rejections intentionally do not produce CDRs: this record is a
commercial *admission* ledger, while authentication, validation, routing and
insufficient-quota refusals remain operational/security events. Operators must
still choose their settlement currency and retention duration; leaving the
defaults (`XXX`, retention disabled) is an explicit safe policy, not missing
functionality.

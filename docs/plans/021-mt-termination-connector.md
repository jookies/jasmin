# MT termination connector: absorb the fake SMSC and the queue tap

- **Date:** 2026-07-30
- **Status:** active
- **Summary:** Give the gateway a connector type that terminates MT traffic locally — decoding and reassembling the message, deciding the receipt from an activation-window gate, delivering the content to a downstream application, and synthesizing the DLR — so `dlr-smpp-python` and `smsget-jasmin-sms-queues` can be decommissioned.
- **Related:** [que.md](../../que.md) and [que2.md](../../que2.md) (the answers this plan is built on), [009-core-gateway-completeness.md](009-core-gateway-completeness.md), [018-admin-plane-and-onboarding.md](018-admin-plane-and-onboarding.md), [../adr/006-durable-cdr-lifecycle.md](../adr/006-durable-cdr-lifecycle.md), [../operations/running-the-platform.md](../operations/running-the-platform.md)

## Context

Two Python services sit beside the gateway in production, and both exist for the same
structural reason.

`internal/core/routingtable/table.go:249`:

```go
func connectorAllowed(d routingfilter.Direction, t ConnectorType) bool {
	if d == routingfilter.MT {
		return t == SMPPC          // MT can ONLY go to an outbound SMPP client connector
	}
	return t == HTTP || t == SMPPS // MO can go to HTTP
}
```

Partners bind to this platform and `submit_sm`; the platform is the **destination**, not a
sender. But a terminating MT message can only be handed to an outbound SMPP connector, so:

- **`dlr-smpp-python`** (`fake_smsc.py`, 518 lines) is a fake upstream SMSC that Jasmin binds
  to. It answers `submit_sm_resp` immediately, then after `dlr_delay` (default 5 s) plus
  `dlr_jitter` (default 2 s) sends a receipt whose status comes from a Redis lookup of
  `dlr:block:{digits}`. The key's presence means **an activation window is open for that
  number**, so every message arriving on it during the window is `DELIVRD`; absence is
  `REJECTD`. Each decision is appended to a capped Redis stream, `dlr:audit`.
- **`smsget-jasmin-sms-queues`** taps `submit.sm.#` on RabbitMQ, unpickles the PDU, decodes
  10+ encodings, reassembles multipart (UDH/SAR plus a time-window stitch for plain-split
  messages), and writes to PostgreSQL, which `smsget-api-gateway` reads. It is the only
  writer of message content in the platform, because `internal/core/cdr/model.go` stores
  metadata only, by design.

Three facts shape this plan:

1. **The verdict is per-number, not per-message.** It reports whether an activation window is
   open, not whether this particular message was liked. Two messages on the same number in
   the same window must produce the same answer, so caching the lookup is a correctness
   property, not an optimization.
2. **The verdict does not depend on downstream delivery succeeding.** Content delivery to
   `smsget-api-gateway` is a durability problem (retry, DLQ, replay), not a receipt problem.
3. **One partner today, three planned.** Today's setup is single-tenant by construction: one
   fake SMSC, one broker tap, one Redis keyspace, no per-partner attribution beyond what is
   in the PDU. Everything the gateway already does per-user — credentials, filters,
   throughput quota, CDR, the byte-complete `SMS-MT` audit line — is what makes absorbing
   these two services worth more than porting them.

## Approach

Add a third MT-capable connector type — a **termination connector** — that consumes the same
per-connector submit queue an SMPP client connector consumes
(`amqpcompat.ConnectorSubmitQueue(cid)`, `internal/core/smppc/connector.go:122`). Everything
upstream of that queue is untouched: routing, filters, interception, segmentation, early
billing, CDR admission and the MT audit line already ran before the message was enqueued.
Only the consumer differs.

Two per-connector settings, because throughput at 10 msg/s and at 1000 msg/s want different
shapes and the answer to que2 Q1 was "we need options":

- **Verdict source** — `redis-window` (today's `dlr:block:{digits}` lookup, with a short
  in-process TTL cache), `http-gate` (POST to a policy endpoint, cached the same way),
  `http-inline` (the delivery POST's response body carries the verdict — per-message veto,
  and your app is on the SMPP hot path), or `static` (always accept; useful for tests).

  **`redis-window` is the only mode with production parity.** The legacy fake SMSC never
  consults the downstream application for a status: an open rental window means `DELIVRD`,
  full stop. `http-gate` and `http-inline` are new capability, not migration, and neither is
  in the first phase.
- **Delivery sink** — `http-push` (queued POST with retries and a DLQ), `pull` (your app
  fetches from the spool over a cursor API), `both`, or `none`.

The receipt is synthesized through the **existing** DLR forward path
(`internal/core/dlr/thrower_publish.go`, `internal/app/dlrthrower`), not through new
receipt-building code, so what the partner sees is produced by the same code that forwards a
real carrier receipt today.

Message content is kept in a **spool, not an archive**: decoded text plus raw bytes,
retained 24 h (que2 Q4), pruned by the existing CDR maintenance loop, masked in the console
behind an audited reveal. The spool is what makes `http-push` failures recoverable and what
answers a partner dispute; it is deliberately not a message warehouse, because OTP bodies are
the highest-value content this platform handles.

**Deliberately not doing:** exposing an AMQP/Redis fan-out for the downstream app (que2 Q4,
third idea). It re-creates the coupling this plan removes — the app consuming gateway
internals. If push and pull prove insufficient at volume, it comes back as a *published*
exchange contract with its own ADR, not as a tap.

## Decisions this plan implements

| Question | Decision |
| --- | --- |
| que2 Q1 | Verdict source and delivery sink are per-connector settings; default `redis-window` + `http-push`. `http-inline` is the opt-in per-message-veto mode. |
| que2 Q2 | `submit_sm_resp` stays `ESME_ROK`; rejection is reported by receipt, as today. Synchronous rejection is a per-connector flag, off by default. |
| que2 Q3 | Receipt delay + jitter kept, per-connector, defaults 5 s / 2 s (the script's defaults). |
| que2 Q4 | Spool, 24 h retention. Push primary, cursor-based pull secondary. No broker sink. |
| que2 Q5 | Console shows message text, masked by default, reveal writes an audit row. |
| que2 Q6 | Decoder and plain-split stitch both ported; stitch is per-connector opt-in. |
| que2 Q7 | All three adjacent gaps in scope: DLQ, inert metrics, emulator receipt bug. |
| que2 Q8 | A destination that normalizes to nothing is rejected explicitly and counted, never silently gated. |
| que2 Q9 | Retirement of both Python services is in scope: dual-run, cutover, decommission. |
| que3 Q1 | Connector type is `term`, console label "Termination (local delivery)". The type names where traffic stops; the sink setting names how the app receives it, so a pull-only deployment does not carry an `http` in its type name. |
| que3 Q2 | The pull API rides the existing REST listener — no new port — and authenticates with a stored, read-only, **scoped** consumer token, never the admin token. Scope is a first-class field from day one, so "show this consumer only some messages" is later a new rule type, not a new auth model. |
| que3 Q4 | Partners are asked to bind to the new gateway rather than being migrated in place. The partner onboarding next week goes on the new path **first** if it is ready in time — a new relationship has no established behaviour to preserve — and the current live partner moves after it has run clean. |
| que3 Q5 | The delivery payload is defined here (see "Delivery contract"). JSON, not the legacy form-encoded + `ACK/Jasmin` shape. |
| que3 Q6 | Load target is 200 msg/s sustained, ~20× current peak, not the 1000 msg/s in the first draft. |
| que3 Q7 | Build a thin vertical slice first — see "Sequencing". |
| que3 Q8 | The legacy path takes no status from the downstream app; `redis-window` is therefore the only parity mode. |

**Parity note that contradicts an earlier recommendation:** `fake_smsc.py:221-231` **fails
open** on a Redis error — it answers `DELIVRD` rather than rejecting legitimate traffic on an
infrastructure blip, and flags the decision with `redis_ok=0`. That behaviour is preserved as
the default for `redis-window`, with the bypass counted and recorded in the decision trail. It
applies to the *gate being unreachable*, not to the downstream app being unreachable — with
`http-inline`, an app timeout is a reject, because there is no other answer available.

## Delivery contract

There is no standard for SMS ingest webhooks; this follows the de-facto conventions
(Twilio/Vonage) rather than the legacy Jasmin MO shape, which is form-encoded with an
`ACK/Jasmin` body. Both remain selectable per connector for compatibility, but JSON is the
default for a new integration.

```
POST <endpoint>
Content-Type: application/json
X-Synevyr-Message-Id: 019428c1-...        idempotency key — retries reuse it
X-Synevyr-Attempt: 2                       1-based delivery attempt
X-Synevyr-Signature: sha256=<hex>          HMAC-SHA256 of the raw body, per-connector secret
X-Synevyr-Timestamp: 1785412800            signed alongside the body; reject old ones

{
  "message_id":  "019428c1-...",           gateway id, same value the receipt carries
  "connector":   "partner-a-term",
  "partner":     "partner-a",              the submitting user
  "from":        "NETFLIX",
  "to":          "380671234567",           normalized, digits only
  "text":        "ПРОВЕРОЧНЫЙ КОД 63125",  decoded, parts already joined
  "raw_hex":     "041f0420...",            pre-decode bytes, all parts concatenated
  "dcs":         8,
  "encoding":    "ucs2",                   which codec actually matched
  "parts":       2,
  "received_at": "2026-07-30T18:22:41.113Z",
  "verdict":     "delivrd"                 what the partner was told, or will be
}
```

- **Success** is any `2xx`. A body is only read in `http-inline` mode, where
  `{"accept": false, "stat": "UNDELIV", "err": "001"}` overrides the verdict.
- **Idempotency:** the same `message_id` may arrive more than once (retry after a timeout
  that actually succeeded). The receiver must treat it as the dedupe key.
- **Signature:** HMAC over the raw body plus timestamp, so the app can verify the caller is
  this gateway without an IP allowlist. Rejecting an unsigned or stale request is the app's
  choice; the gateway always sends it.
- `raw_hex` is included deliberately: when the app and the gateway disagree about what a
  message said, the argument is only settleable with the pre-decode bytes.

## Sequencing

Build a thin vertical slice before broadening (que3 Q7). The step numbers below are stable;
the phases say when each runs.

- **Phase A — one message, end to end.** Steps 1, 2, 3, 5, 6 (`redis-window` only), 7, 8, 9,
  12, and the Step 14 harness restricted to that corpus. No stitch, no console, no pull API,
  no `http-gate`/`http-inline`. Done when a partner ESME submits and receives a receipt whose
  verdict came from Redis and whose content reached the app, proven byte-identical to the
  legacy path.
- **Phase B — parity and operability.** Steps 4 (stitch), 10 (pull API), 11 (metrics,
  decision trail, emulator fixes), 13 (admin plane and console), and the rest of Step 14.
- **Phase C — retirement.** Step 15, per partner.

**Migration is by re-binding, not by repointing a route.** The legacy stack is a separate
deployment (Python Jasmin + `fake_smsc.py` + the queue tap); the partner is simply asked to
bind to the new gateway with new credentials. One partner is on exactly one path at a time,
the old stack keeps serving whoever has not moved, and rollback is the partner binding back
to the old endpoint. No in-place route flip, no shared broker, no dual-write.

The consequence worth stating: there is **no natural production side-by-side**, so the
offline differential harness (Step 14) is the only proof that the new path behaves like the
old one. It becomes more important under this migration, not less.

## Steps

### Step 1: Record the architectural decision — DONE

- **Files:** `docs/adr/008-mt-termination-connector.md` (new), plus
  `docs/operations/termination-safety.md` for the operator-facing version of the
  limitations the ADR's Consequences section names.
- **Changes:** Record the choice of a new connector type over the alternatives: teaching MT
  routing to accept the existing `http` connector type (rejected — MO HTTP delivery has
  different semantics, no receipt synthesis, no verdict), keeping the fake SMSC and only
  porting the decoder (rejected — leaves a second deployment and a single-tenant Redis
  keyspace for three partners), and an interceptor-only implementation (rejected — an
  interceptor can reject a submit but cannot deliver content or synthesize a receipt).
  Record the spool-not-archive decision and its retention rationale.
- **Verify:** ADR renders, is linked from `docs/README.md`, and its "Alternatives" section
  names the three rejected options with the reason each lost.

### Step 2: Register the connector type

- **Files:** `internal/core/routingtable/table.go`, `internal/core/routingtable/table_test.go`
- **Changes:** Add `TERM ConnectorType = "term"`. Extend `connectorAllowed` so MT permits
  `SMPPC` **or** `TERM`; MO permits `HTTP` or `SMPPS` unchanged. Extend the validation switch
  at `table.go:244`. Leave `table.go:74`'s SMPPC-specific handling alone and add an explicit
  case for `TERM` rather than widening the existing one, so a future reader sees two distinct
  MT paths.
- **Verify:** `go test ./internal/core/routingtable/...` including a new case asserting an MO
  route to a `term` connector is still refused and an MT route to it is accepted.

### Step 3: Port the decoder

- **Files:** `internal/core/msgcontent/decode.go`, `internal/core/msgcontent/decode_test.go`,
  `internal/core/msgcontent/testdata/vectors.json` (all new)
- **Changes:** Port `app/decoder.py`: DCS-driven decoding with detection fallback, GSM 7-bit,
  UTF-16BE/LE, Latin-1, Cyrillic restoration for 7-bit-stripped text, Hebrew, Japanese,
  Korean including the UCS-2 60-byte null heuristic, and the mixed brand-name token handling.
  Preserve the ordering constraint: 8-bit codecs run **before** charset detection.
- **Verify:** Extract the Python suite's inputs and expected outputs into `vectors.json` and
  run both implementations over it; the Go table test must pass every vector. A vector the
  Python side fails is recorded as a known-bad in the file rather than silently dropped.

  The vectors are only evidence for as long as they still agree with the oracle, so the
  comparison is a script rather than a one-time exercise:
  `scripts/differential/decode_oracle.py` re-runs every vector through the real
  `app/decoder.py` and exits non-zero on drift (`--update` regenerates them). It reproduces
  `extract_sms_text`'s body rather than calling it, because that function returns only the
  text and discards the encoding label the vectors also pin. It cannot run in CI — the
  Python service is not there — so it is a local gate before touching the decoder.
  Last run: 77/77 match.

**Correction, found while porting and confirmed by running the legacy decoder directly:**
`"@>25@>G=K9 :>4 63125"` does **not** decode to `"ПРОВЕРОЧНЫЙ КОД 63125"`. The real output is
`"РОВЕРОЧНЫЙ КОД ЖГБВЕ"` — the leading `П` is unrecoverable (nothing in the input encodes it)
and, far worse, **the OTP digits are destroyed**: the 7-bit-stripped path ORs `0x80` onto
every byte in `0x21–0x7F`, so `63125` becomes `ЖГБВЕ`. The mixed decoder has a digit-run rule
that would have saved them, but its suspicion gate needs two characters in `0x70–0x7E` and an
all-uppercase Cyrillic message has none. `decoder.py:120-135`'s own comment states the
intended output keeps the digits, and no Python test asserts either value, so this is an
unintended defect rather than a quirk anyone chose.

It matters because it corrupts exactly the message shape this platform exists to carry. Which
traffic reaches that path depends on the partner's data coding — UCS-2 bodies decode
correctly — so the size of the problem is answerable from the encoding distribution in the
existing message table. The Go port reproduces the legacy behaviour today, deliberately, so
the differential starts clean; the fix is a decision recorded in Open questions below.

### Step 4: Multipart reassembly and the plain-split stitch

- **Files:** `internal/core/msgcontent/stitch.go`, `internal/core/msgcontent/stitch_test.go`
  (new); reuse `internal/state/rediscompat` for part storage
- **Changes:** UDH/SAR reassembly reuses the existing `MultipartStore` path
  (`internal/state/rediscompat`) rather than a second implementation. Port `app/stitch.py` as
  a separate, **per-connector opt-in** layer with a configurable window (default: off).
- **Verify:** `go test ./internal/core/msgcontent/...` with the ported `test_stitch.py`,
  `test_udh_concat.py` and `test_reassembly.py` cases, plus a new case proving that with the
  stitch disabled two same-sender messages inside the window stay separate — the failure mode
  that makes this per-connector rather than global.

**Correction, found in review of the first implementation: reassembly joins content, not
receipts.** The first cut returned early on an incomplete segment and produced one delivery
receipt for the whole concatenated message. That is wrong. Under SMPP 3.4 each segment is
submitted as its own `submit_sm`, is answered with its own `message_id` in `submit_sm_resp`,
and carries its own `registered_delivery` flag; a delivery receipt references a `message_id`.
A partner that registers delivery on every segment is asking for a receipt per segment, and
that is what the legacy `fake_smsc.py` has always sent them — it receipts every `submit_sm`
unconditionally. Collapsing N receipts into 1 would have cut the partner's receipt count
silently and left their reconciliation permanently short, with nothing in either system to
explain the gap.

The two things are now separate:

- **Receipts belong to segments.** Every segment — complete or not — decides the verdict,
  publishes its own accept leg under its own queue message id, and durably schedules its own
  receipt. The activation gate is still consulted once per message, not once per segment: the
  verdict cache is keyed by normalized destination, which is also what stops the segments of
  one message disagreeing about whether the window was open.
- **Content belongs to the message.** The assembled message is spooled with its joined text
  and delivered downstream exactly once, on the segment that completes the group.

A segment that has not completed its group is persisted as a **receipt-only spool row**
(`message_spool.receipt_only`, migration `0007_message_spool_receipt_only.sql`): the verdict,
the addresses and when the receipt is owed, and no content — the fragment's bytes are half a
message and the assembled row already holds the whole of it. Those rows are excluded from
`msgspool.Search` by default, so a pull consumer is never handed a fragment it would store as
a blank message.

One gap is knowingly left open and is not this correction's to close: the **plain-split
stitch** still emits one receipt per released group rather than one per `submit_sm`. It is
off by default and enabled per connector against a partner known to split messages, and
closing it means giving a released group an identity of its own rather than reusing its last
chunk's message id — a change to what a stitched message *is*.

### Step 5: The termination connector worker

- **Files:** `internal/core/termination/connector.go`, `config.go`, `connector_test.go` (new);
  `internal/core/smppc/manager.go` (registration seam) or a sibling manager if the type
  boundary is cleaner
- **Changes:** Consume `amqpcompat.ConnectorSubmitQueue(cid)` with the same durable-topology
  and prefetch settings the SMPP client connector uses. Per message: decode (Step 3),
  reassemble/stitch (Step 4), obtain the verdict (Step 6), write the spool row (Step 8),
  deliver (Step 9), schedule the receipt (Step 7). Acknowledge the AMQP delivery only after
  the spool row commits, mirroring the Python service's "ACK only after successful save".
  Report availability through the same interface routing consults, so a stopped termination
  connector removes its routes from selection exactly as a stopped SMPP connector does.
- **Verify:** Integration test on the real broker: enqueue a submit for a `term` connector,
  assert one spool row, one delivery attempt and one receipt; kill the worker between decode
  and spool commit and assert redelivery produces exactly one row. For a three-segment
  submit, assert three accept legs and three receipts but one content row and one delivery
  (see the Step 4 correction).

### Step 6: Verdict sources

- **Files:** `internal/core/termination/verdict.go`, `verdict_redis.go`, `verdict_http.go`,
  `verdict_test.go` (new)
- **Changes:** `Source` interface returning `{Accept bool, Stat string, Err string, Reason
  string, GateBypassed bool}`. `redis-window` reads `dlr:block:{digits}` with the script's key
  normalization (`fake_smsc.py:81`, digits only, no leading `+`) and a short TTL cache keyed
  by normalized destination; **fail-open on a Redis error**, counted and flagged.
  `http-gate` POSTs `{to, from, connector}` and accepts the same response shape as
  `http-inline`, so an app can move between modes without changing its handler.
  `http-inline` takes the verdict from the delivery response. `static` always accepts.
  The response may name the SMPP status explicitly — `{"accept": false, "stat": "UNDELIV",
  "err": "001"}` — so custom status logic lives in your app, with the interceptor seam
  (`internal/core/interceptor`) as the escape hatch for arbitrary per-partner rules.
- **Verify:** Unit tests per source including the fail-open path; a cache test proving two
  messages to the same number inside one window issue one lookup and receive identical
  verdicts.

### Step 7: Receipt synthesis, delay and jitter

- **Files:** `internal/core/termination/receipt.go` (new),
  `internal/core/dlr/thrower_publish.go` (reuse), `internal/app/dlrthrower/service.go` (reuse)
- **Changes:** Build a `dlr.Forward` and publish it through `ForwardPublisher.PublishDLR`
  after `delay + rand(jitter)`, so the partner receives a receipt built by the same code path
  as a carrier receipt. Counters derive from the status — `DELIVRD` → `dlvrd:001 err:000`,
  `REJECTD` → `dlvrd:000 err:008` (matching `fake_smsc.py:50-53`) — never hardcoded. The
  pending-receipt timer must be durable: a gateway restart during the 5–7 s window must still
  emit the receipt, so pending receipts are persisted with the spool row and recovered on
  startup rather than living only in a goroutine.
- **Verify:** End-to-end test with a real SMPP partner session asserting receipt bytes; a
  restart test that stops the gateway 2 s after submit and asserts the receipt still arrives
  after recovery; an assertion that `stat:REJECTD` never appears with `dlvrd:001`.

### Step 8: The content spool

- **Files:** `internal/core/msgspool/store.go`, `postgres.go`, `store_test.go` (new);
  `internal/storage/postgres/migrations/00NN_message_spool.sql` (new); `internal/app/outbound/runtime.go`
  (maintenance loop registration, near `runCDRMaintenance` at line 488)
- **Changes:** Table keyed by gateway message ID and part number, carrying partner/user,
  connector, source and destination, decoded text, raw bytes, DCS, part count, verdict and
  reason, delivery state, attempt count and timestamps. Retention default 24 h, pruned by the
  existing CDR maintenance loop and configurable through the same settings surface as CDR
  retention (`SetCDRRetention`, `internal/app/admin/settings_service.go`). Reads go through a
  service that records an access-audit row, reusing the `cdr_access_audit` pattern.
- **Verify:** Migration applies and rolls back on a live PostgreSQL; a retention test proving
  rows older than the window are pruned in batches; an audit test proving every read of
  message text writes an audit row naming the actor.

### Step 9: Delivery sink, retries and DLQ

- **Files:** `internal/core/termination/deliver_http.go` (new), `internal/core/mo/http_thrower.go`
  (pattern reference), `internal/app/termthrower/service.go` (new)
- **Changes:** Queued POST of the decoded message with bounded retries and backoff. Failure
  after the final attempt moves the message to a **dead-letter queue** and marks the spool row
  `dlq`, instead of the current thrower behaviour of purging after three retries. Acknowledge
  contract configurable per connector: `2xx` alone, or the legacy `ACK/Jasmin` body
  (`internal/core/mo/http_thrower.go:122`), defaulting to `2xx` for a new integration.
- **Verify:** Test with a downstream returning 500 for N attempts then 200 — exactly one
  successful delivery, no duplicate spool rows; a test asserting a permanently failing
  delivery lands in the DLQ and is replayable, with nothing purged.

### Step 10: Cursor-based pull API with scoped read tokens — DONE

- **Files:** `internal/core/msgspool/{scope,consumer,pull_api}.go` and their tests (new);
  `internal/infra/storage/migrations/0008_message_consumers.sql`,
  `postgres_msgconsumers.go`, `sqlite_msgconsumers.go`, `msgconsumers_test.go` (new);
  `internal/transport/restcompat/messages.go` (new) plus the mount in `handler.go`;
  `internal/app/outbound/runtime.go` (`RuntimeDependencies.MessagePull`);
  `internal/app/gateway/{termination,runtime}.go` (build the consumer service, fill the
  late-bound slot); `internal/app/admin/{message_consumer_service,handlers_message_consumers}.go`
  and `internal/app/adminweb/handlers_message_consumers.go` (new).

  Numbered 0008 to leave 0007 for the concurrent per-segment receipt work; the migration
  files carry no version table and are applied by name, so the gap costs nothing.
- **Changes:** `GET /messages?after=<cursor>&limit=<n>` on the existing REST listener,
  returning messages plus `next_cursor`. Time filtering is a convenience parameter, **not**
  the paging mechanism.

  **Mounted on both REST views through a resolver, not a handler.** The REST listener is
  built by the outbound runtime and the spool is built afterwards by the termination plane —
  it needs that runtime's publisher — so a captured handler would always be nil.
  `restcompat.WithMessagePull(func() http.Handler)` is resolved per request; before the plane
  exists, and forever on a gateway with no termination section, the path answers 404. The
  path sits beside `/secure/*` rather than inside it because it authenticates a consumer
  token with Bearer, and `/secure/`'s catch-all demands a Jasmin user's Basic credential
  before it will even answer 404.

  Authentication is a stored **consumer** record, not a static config token: id, label,
  hashed token (never stored in clear, like the durable REST batch credentials), created/last
  used, revoked flag, and a `scope` object. Scope in the first implementation carries
  `connectors: [...]` and `include_text: bool`; the shape is designed so later rule types —
  destination prefix, source address, partner, maximum lookback, metadata-only — are added as
  fields without changing the token model or the endpoint.

  **Scope is compiled into the SQL predicate**, never applied by filtering rows after the
  query. Post-filtering leaks through the cursor: a consumer would page past rows it cannot
  see and receive short or empty pages that are indistinguishable from "no new messages",
  and the row count would still reveal traffic it is not entitled to know about.

  A consumer with `include_text: false` receives metadata and no `text`/`raw_hex` — the field
  is absent rather than null, so a client cannot mistake redaction for an empty message.
  Every read writes an audit row naming the consumer id, the scope applied and the row count.

  **Three choices worth recording.** *Scope is fail-closed*: an empty connector list denies
  everything rather than admitting everything, which inverts `Query`'s
  zero-value-means-no-filter convention — so "all connectors" is not expressible today and
  arrives later as an explicit field somebody has to write down. `ConsumerService.Page`
  re-checks the compiled predicate is non-empty before it reads, so the fail-open branch is
  unreachable even if a future scope field changes how `Compile` builds the list. *An
  out-of-scope `?connector=` is 403, not an empty page*: on this API an empty page means "no
  new messages", and a consumer that could not tell the two apart would wait forever.
  *`IncludeReceiptOnly` is spelled out at the pull path* rather than left to the zero value,
  so the per-segment receipt rows from Step 4 cannot reach a consumer that would store a
  fragment as the message.

  Consumers live in the spool's database, not the admin control plane: a scope names
  connectors whose rows are in that database, and a credential that could outlive or diverge
  from the spool it authorizes reads against is one nobody can reason about. The scope is one
  JSON document rather than a column per rule, so the anticipated rule types are additive and
  an older gateway reading a newer row ignores what it does not know.
- **Verify:** done. `go build ./...` and `go vet ./...` clean;
  `go test -race ./internal/...` passes with and without `TEST_POSTGRES_DSN`. The storage
  suite runs every property against **both** backends from one test body (SQLite
  unconditionally, PostgreSQL 16 in a throwaway container): a retried delivery handed back by
  a cursor that had passed both its sequence and its `received_at`; a consumer scoped to
  connector A seeing only A rows across interleaved traffic, in dense pages (3 pages for 5
  rows at limit 2 — a post-filtered scope would need more) and with the audit row counts
  summing to A's rows alone; a receipt-only row never returned; content masked in the SQL
  projection; one audit row per read, recorded as a reveal. Unknown, malformed and revoked
  tokens all get an identical 401 and each writes an audit row — the revoked one against its
  real consumer id. `include_text:false` omits `text`/`raw_hex` as JSON keys, asserted on the
  wire, while an empty message with `include_text:true` still renders `"text":""`.

### Step 11: Metrics, decision trail and the emulator fixes — DONE

- **Files:** the registry is `internal/core/stats/prometheus.go`, not the
  `internal/observability/metrics.go` this plan first named — that package does not exist,
  and a second registry beside the one the admin listener already serves would have been a
  second surface to scrape. Call sites: `internal/core/smppc/session.go` (submit),
  `internal/core/dlr/correlation.go` and `lookup_consumer.go` (DLR),
  `internal/app/gateway/queuedepth.go` and `internal/transport/amqpcompat/depth.go` (queue
  depth), `internal/core/termination/{connector,runner,metrics}.go` and
  `internal/app/gateway/termination.go` (termination). Trail:
  `internal/core/termination/trail.go`. Emulators: `cmd/synevyr-fake-smsc/`,
  `scripts/interop/smsc_probe.py`.
- **Changes:** The three inert recorders have real call sites. Termination counters added:
  verdicts by outcome per connector, gate bypasses, delivery attempts/failures/dead-letters,
  and three polled gauges (spool rows, dead-letter depth, receipts overdue). Both emulators
  derive `dlvrd`/`err` from `stat`, matching `fake_smsc.py`'s table; `synevyr-fake-smsc`
  gained `-dlr-auto`, `-dlr-stat`, `-dlr-delay`, `-dlr-jitter` and `-submit-status`, all
  defaulting to the previous behaviour.
- **The decision trail is a query, not a table.** The spool row already carries verdict,
  reason, `gate_bypassed`, partner, normalized destination and both receipt timestamps —
  every field `dlr:audit` held except `registered_delivery` and the pre-normalization
  destination, both of which are in the MT audit line for the same message id. A second
  table would have been a copy of data written in the same transaction that could disagree
  with it, plus one more place holding per-message OTP evidence. Two filters were added to
  the spool query instead (`verdict_stat`, `gate_bypassed`), compiled into SQL rather than
  applied to fetched rows, and `termination.DecisionTrail` projects rows into the legacy
  field set including its three-valued outcome (`delivrd`/`rejectd`/`delivrd_failopen`).
- **Correction found while verifying.** Counting every `ErrDLRMapNotFound` as a correlation
  failure made `synevyr_dlr_total{outcome="correlation_failure"}` fire on completely healthy
  traffic: the response path publishes `dlr.submit_sm_resp` for *every* submit, so the
  majority — which requested no receipt — find no DLR record. That would have put
  `SynevyrDLRCorrelationFailures` permanently in alarm, replacing an inert metric with a
  lying one. A missing map is now counted only on the `deliver_sm` leg, and only once its
  retry budget is spent.
- **Verify:** done on the live compose stack. Queue depth moved 0 → 39 → 0 across a paused
  SMSC; `synevyr_submit_total` attempt/success and the round-trip histogram moved with it;
  `synevyr_dlr_total` recorded level-1 and level-2 delivered forwards, and exactly one
  `correlation_failure` for an orphan receipt while five plain sends recorded none. A
  temporary `term` connector proved verdicts (`rejectd`, then `delivrd`), the gate-bypass
  counter (1, with Redis stopped), spool rows 1 → 2 → 3, and receipts-overdue 1 → 0 on
  recovery. Emulator receipt on the wire: `stat:UNDELIV dlvrd:000 err:008`, with the
  `DELIVRD` bytes unchanged.

### Step 12: Destination normalization

- **Files:** `internal/core/termination/verdict_redis.go`, `internal/core/routingtable` (front-door
  validation), `internal/app/smppssubmit/handler.go`
- **Changes:** Normalize the destination once, at the front door. A destination that
  normalizes to empty is rejected explicitly and counted — never turned into a lookup of
  `dlr:block:undefined`, which is the current silent failure that makes those OTPs always
  `REJECTD`.
- **Verify:** Test submitting a malformed destination and asserting an explicit rejection plus
  a counter increment, and that no gate lookup was attempted.

### Step 13: Admin plane and console

- **Files:** `internal/app/admin/service.go` and connector handlers, `internal/app/jcli/*`
  (connector verbs), `web/src/pages/connectors/*`, `web/src/pages/messages/*` (new),
  `internal/app/adminweb/handlers_messages.go`
- **Changes:** CRUD for termination connectors on the admin API, jCli and the console
  (verdict source, delivery sink, endpoints, delay/jitter, stitch window, ack contract).
  New "Messages (last 24 h)" console screen: search by destination, sender, connector, time
  and verdict; row shows time, partner, from, to, parts, encoding, verdict, delivery state;
  the detail drawer shows decoded text **masked by default** with an audited Reveal, plus raw
  hex, DCS/UDH, which codec matched, verdict reason, delivery attempts, DLQ state and a
  Replay action.
- **Verify:** Rebuild `web/dist` (the committed bundle is served via `go:embed` — an unrebuilt
  bundle silently serves the old UI), then exercise the screen against a live stack:
  send a message, find it by destination, reveal the text, confirm the audit row names the
  session user, replay a DLQ'd message and see it delivered.

### Step 14: Differential run against both Python services

- **Files:** `test/integration/termination_differential_test.go`, `scripts/differential/` (new)
- **Changes:** Harness that submits a corpus (every encoding vector, multipart, plain-split,
  active and inactive windows, malformed destinations) simultaneously to the legacy path
  (Jasmin + `fake_smsc.py` + the queue tap) and the new termination connector, comparing
  receipt bytes, decoded text, part assembly and the row the downstream app receives.

  This splits in two, and only the first half is done. **Content differential:**
  `scripts/differential/decode_oracle.py` plus the 77 vectors, which needs nothing running.
  **Flow differential:** receipt bytes, verdict timing and the delivered row, which needs the
  whole legacy stack up beside the new one — that is the part still outstanding, and it is
  the one that gates a partner moving over. The partner side of both is driven by
  `cmd/synevyr-partner-sim`.
- **Verify:** Zero differences on the corpus, or each difference explicitly classified as a
  fixed bug with a note — the same discipline used for the pickle-codec differentials.

### Step 15: Stand up the new deployment, onboard partners, decommission later

- **Files:** `docs/operations/new-deployment.md` (new), `docs/operations/partner-acceptance-tests.md` (new),
  `docs/plans/015-python-jasmin-deprecation-gate.md` (extend when decommission is actually scheduled).
  **Nothing in the legacy stack is modified** — no instrumentation, no staging writes, no change to
  `fake_smsc.py` or the queue tap. It runs exactly as it does today.
- **Changes:** The gateway is built from scratch on a new virtual machine, beside the legacy stack
  rather than replacing anything. A partner is given bind credentials for it and connects; nobody is
  migrated in place, so there is no cutover step and nothing to roll back — a partner who has not been
  given credentials is simply not using it.

  Before the partner's real traffic, the deployment is proven against
  `docs/operations/partner-acceptance-tests.md`: the message path, content and encoding, the failure
  cases (downstream down, Redis down, gateway restarted mid-flight), the partner's own client
  behaviour, and the commercial surface. Sections A–C need only the partner simulator; D needs the
  partner; E needs a day of real traffic.
- **Decommissioning is explicitly deferred.** The legacy stack keeps serving whoever has not moved,
  and switching it off is a separate decision taken when every partner is on the new deployment and
  the spool has shown a full retention cycle without an unexplained gap — never on a date.
- **Verify:** the acceptance matrix passes on the real deployment, with the partner's real client for
  section D.

## End-to-end verification

The partner side of every step below is driven by `cmd/synevyr-partner-sim`
(`scripts/dev.sh partner`), which binds as an ESME, submits, and prints each receipt with the
latency it arrived at. It is a development tool only: it refuses a public target address
without `--allow-remote`, it is foreground and short-lived, and `docker/Dockerfile.gateway`
builds only `./cmd/synevyr-gateway`, so it cannot reach a deployed environment.

On an isolated compose stack with a real partner ESME, PostgreSQL, RabbitMQ and Redis:

1. Provision a partner user, a `term` connector (verdict `redis-window`, sink `http-push`) and
   an MT route to it.
2. With no activation key set, submit a message: the partner receives `submit_sm_resp` OK and,
   5–7 s later, a receipt with `stat:REJECTD dlvrd:000 err:008`. The spool holds the decoded
   message; the CDR shows the early charge.
3. Set `dlr:block:{number}`, submit twice: both receipts are `DELIVRD`, the gate was consulted
   once, and the downstream app received both messages with correct text.
4. Submit a 3-part Cyrillic message: one delivery with reassembled text matching the Python
   decoder's output byte for byte.
5. Stop the downstream app, submit: receipt still `DELIVRD` (the window is open), delivery
   retries, then lands in the DLQ; replay from the console delivers it once.
6. Stop Redis, submit: `DELIVRD` by fail-open, the bypass counter increments and the decision
   trail records `gate_bypassed`.
7. Restart the gateway 2 s after a submit: the pending receipt still arrives.
8. Poll the pull API by cursor across all of the above and confirm every message appears
   exactly once, including the replayed one.

## Rollback

Every step is additive, and the legacy stack is never modified. The termination connector is a
new type on a separate deployment — no existing route, connector or Python service changes
behaviour at any point. Rollback for a partner is binding back to the old endpoint, which is
still running and still configured for them. The spool migration is reversible and its table
is read by no other subsystem. The only irreversible action in the whole plan is switching the
legacy stack off, and that is gated on every partner having moved, not on a date.

## Risks

- **A receipt cannot be un-sent.** If the verdict source is wrong, the partner has already been
  told. Mitigated by fail-open on gate errors, by the differential run, and by keeping
  `http-inline` (where an app outage becomes a reject) off by default.
- **Spool growth under a downstream outage.** 24 h retention bounds it, but a 1000 msg/s
  partner with a day-long outage is ~86 M rows. Mitigated by the DLQ depth metric and an alarm
  threshold set during Step 11 — this is exactly why the inert metrics are in scope.
- **OTP content at rest.** A second copy of message bodies is a second breach surface.
  Mitigated by short retention, masked-by-default display, audited reveal, and no console
  export of message text (unlike CDRs, which do export).
- **The stitch heuristic joining unrelated messages.** Time-window stitching across three
  partners on shared numbers can merge two messages. Mitigated by making it per-connector and
  off by default.
- **Throughput assumptions.** Current peak is ~10 msg/s; the target is 200 msg/s sustained,
  reached by keeping the hot path a cached KV lookup. That number is untested — Step 14
  includes a load run before the second cutover, not after. Higher targets are a cache-sizing
  exercise, not a redesign, which is why 200 is enough to prove now.
- **Cutovers are per-partner.** Each partner has its own ESME behaviour. The partner
  onboarding next week is cut over first, on the reasoning that a new relationship has no
  established expectations to violate; the checklist is revised before the live partner
  follows.

## Conformance correction: one receipt per segment

The first implementation of Step 4 produced **one receipt per assembled message**.
That is wrong, and it was found by asking the partner-facing question rather than by
a test: under SMPP 3.4 each segment of a long message is its own `submit_sm`, gets
its own `message_id`, and carries its own `registered_delivery`, so a partner that
requests a receipt on every segment is entitled to one per segment — which is
exactly what `fake_smsc.py` has always sent. Collapsing to one would have dropped
their receipt count from N to 1 per long message and left their reconciliation
permanently short, silently.

Receipts and content are now separate concerns: every segment gets its own accept
leg and its own receipt under its own message id, and the assembled message is
spooled with content and delivered downstream exactly once. Segment rows carry no
text or raw bytes and are excluded from every read except the decision trail's
explicit `IncludeSegmentReceipts`.

**Still outstanding, same defect class:** the plain-split stitch emits one receipt
per released group rather than one per `submit_sm`. Closing it means giving a
stitched group an identity of its own instead of reusing its last chunk's message
id, which changes what a stitched message *is*. It is off by default and per
connector.

## Open questions

All questions from `que.md`, `que2.md` and `que3.md` are answered and folded into the
Decisions table above. Two remain deliberately undecided, and neither blocks Phase A:

- **Whether `http-gate`/`http-inline` are ever wanted.** They are designed for but not built,
  because the legacy path takes no status from the downstream app. If the accept/reject rule
  outgrows a Redis key, this is where it goes.
- **Whether the spool ever becomes the system of record.** `smsget-api-gateway` keeps its own
  table; the spool is a 24 h safety net. Reversing that is a retention change, not a
  redesign.
- ~~**Whether to keep destroying OTP digits for parity.**~~ **Decided 2026-07-30: fixed in Go.**
  `msgcontent.Options.PreserveOTPDigits`, on in `termination.DefaultDecodeOptions()`, off in
  `Options{}` so the differential harness stays byte-exact and this appears there as the one
  intended difference. Recorded as [D-004](../reference/deviations.md). The Python service is
  unchanged and still mangles, so while both run the same message decodes differently
  depending on which path carried it — which is an argument for not running both for long.
- **Whether dead-lettered rows may outlive the content window.** Prune does not exempt `dlq`
  rows, which is what makes 24 h actually bound spool growth during an outage — but a
  message dead-lettered 25 h ago is no longer replayable. Redacting content on prune while
  keeping the row and its failure reason would give operators the history without holding OTP
  bodies; that is a Step 9 schema decision, not a config change.

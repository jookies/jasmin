# ADR-008 — A connector type that terminates MT traffic locally

- **Date:** 2026-07-30
- **Status:** accepted
- **Summary:** MT traffic this platform is the destination for is handled by a new connector type that decodes, decides, delivers and synthesizes the receipt itself, replacing a fake SMSC and a broker tap.
- **Related:** [plan 021](../plans/021-mt-termination-connector.md), [ADR-006](006-durable-cdr-lifecycle.md), [deviations D-004](../reference/deviations.md), [termination-safety.md](../operations/termination-safety.md)

## Context

Partners bind to this platform and `submit_sm`; the platform is the **destination**
(it rents numbers and terminates the traffic), not a sender. But routing only ever
allowed MT to reach an outbound SMPP client connector:

```go
// internal/core/routingtable/table.go, before this decision
if d == routingfilter.MT {
    return t == SMPPC
}
return t == HTTP || t == SMPPS
```

Two Python services existed to work around that one line. `dlr-smpp-python` was a
fake SMSC the gateway could bind to, answering `DELIVRD`/`REJECTD` from a Redis
activation window. `smsget-jasmin-sms-queues` tapped the broker's `submit.sm.#`,
unpickled the PDU, decoded ten-plus encodings, and wrote the content to
PostgreSQL — because the CDR model deliberately stores no message body.

They were the same missing feature split in half, and they could disagree: the
fake SMSC could report a message delivered while the tap failed to persist it, or
the reverse. Both were single-tenant by construction — one fake SMSC, one tap, one
Redis keyspace — at the point where the business was going from one partner to
three.

## Decision

Add a connector type, `term`, that terminates MT traffic on this platform. It
consumes the same per-connector submit queue an SMPP client connector consumes, so
everything upstream — routing, filters, interception, segmentation, early billing,
CDR admission, the MT audit line — is unchanged and untouched. Only the consumer
differs.

Two settings are per connector, because 10 msg/s and 1000 msg/s want different
shapes: the **verdict source** (today `redis-window`, the parity mode, or
`static`) and the **delivery sink** (a signed JSON push, a pull API, both, or
neither).

The receipt is emitted through the **existing** DLR path — the connector
publishes the same `dlr.submit_sm_resp` and deliver-receipt legs a real carrier
causes, using the same content constructors — so what the partner receives is
produced by the same code as a carrier receipt rather than by a second
implementation of the same contract.

Message content is kept in a **spool, not an archive**: decoded text plus raw
bytes, 24 h retention, pruned by the same machinery as CDRs, masked in the console
behind an audited reveal.

## Alternatives considered

**Teach MT routing to accept the existing `http` connector type.** Rejected: the
MO HTTP path has different semantics — no verdict, no receipt synthesis, no spool,
a different callback contract — so it would have meant one type with two
behaviours selected by direction. The failure mode of that is a config where half
the fields silently do nothing, which this project treats as worse than a missing
feature.

**Keep both Python services and port only the decoder.** Rejected: it leaves a
second deployment, a pickle tap on internal broker topology, and a single-tenant
Redis keyspace, at the moment a second and third partner arrive. It also keeps the
two systems that can disagree with each other.

**Implement the verdict in an interceptor.** Rejected: an interceptor can reject a
submit, but it cannot deliver content downstream, cannot synthesize a receipt, and
has no durable state for a delayed one. It remains the right seam for *custom*
verdict logic layered on top of this connector.

**Fuse the verdict and the delivery into one call** (the app's HTTP response
carries accept/reject). Rejected as the default, kept as a designed-but-unbuilt
mode: it puts the downstream application on the partner's submit path, where one
GC pause becomes a REJECTD storm, and a receipt cannot be un-sent. It also does
not match the legacy behaviour, where the verdict is a property of the number's
rental window and never consulted the application at all.

**Archive message content rather than spool it.** Rejected: the downstream
application already stores what it needs, and OTP bodies are the highest-value
content this platform handles. The spool exists to make a failed delivery
recoverable and a dispute answerable, which is a job that finishes in hours.

## Consequences

- MT now has two terminating paths. `routingtable` and `routepolicy` accept both,
  and a route declares which through `connector_type`; empty still means `smppc`,
  so every route persisted before this decision loads unchanged.
- The receipt and the downstream delivery are **independent promises**. A partner
  can be told `DELIVRD` while our application is down. That is inherited
  behaviour, it is now written down, and the guardrails are in
  [termination-safety.md](../operations/termination-safety.md).
- The gateway now stores message content, reversing a deliberate no-content design
  in the CDR model. It is bounded by retention, masked by default, and every read
  of a body writes an audit row.
- A concatenated message is refused until the stitch lands, rather than delivered
  as fragments. Refusing is worse for availability and better for truth; it is a
  Phase A limitation, not the intended end state.
- The decoder deliberately diverges from the legacy one in exactly one place
  ([D-004](../reference/deviations.md)) — it does not destroy OTP digits.
- Both Python services can be decommissioned once every partner has moved. They
  are unmodified in the meantime; migration is by re-binding a partner to the new
  gateway, not by an in-place cutover.

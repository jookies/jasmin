# Plan 023 — One connector for both directions

- **Date:** 2026-07-30
- **Status:** draft
- **Summary:** Let a termination connector receive **MO** traffic as well as MT, selected by a direction flag at creation, so a downstream application has one endpoint contract, one durable spool, one pull API and one console page for both directions — and MO stops silently dropping messages.
- **Related:** [plan 021](021-mt-termination-connector.md), [ADR-008](../adr/008-mt-termination-connector.md), [api/messages.md](../api/messages.md), [learning-path.md](../learning-path.md)

## Context

The platform currently has two unrelated ways to deliver a message to a
customer's HTTP endpoint, and operators cannot tell them apart. That confusion
is not a documentation failure — it is a real asymmetry in the design.

| | MO webhook | Termination connector |
| --- | --- | --- |
| Fed by | inbound `deliver_sm` from a carrier | MT routing, from a partner's submit |
| Queue | `deliver_sm_thrower.*` | `submit.sm.<cid>` |
| Configured as | a library entry + an MO route that copies it | one object carrying its own endpoint |
| Endpoint contract | 2xx **and** body exactly `ACK/Jasmin` | 2xx (json format) |
| Durability | **none** | spool, retries, dead letter |
| Pull API | no | yes |
| Operator view | none | Messages page |

The durability row is the important one. `internal/core/mo/thrower_consumer.go:313`:

```go
func (c *ThrowerConsumer) retryOrPurge(messageID string, delivery *amqpcompat.Delivery) {
	if c.retrials[messageID] > c.cfg.MaxRetries {
		...
		_ = delivery.Reject(false)   // no requeue: discarded
```

Four attempts, thirty seconds apart, then the message is gone. The retry counter
(`c.retrials`) and the timer (`time.AfterFunc`) are both in process memory, so a
restart mid-retry loses it too. There is no store, no dead-letter, no console
page and no API that can tell an operator what was lost.

For OTP delivery reports that is survivable. For **inbound STOP / opt-out
messages it is not**: losing an opt-out is a compliance failure, and the shipped
dev config already carries an MO interceptor matching `.*STOP`, which says
somebody expects that traffic.

Meanwhile the termination connector already has everything MO needs — durable
spool, bounded retry with backoff, dead-letter with content retained, a scoped
pull API, an audited operator view — and none of it is MT-specific.

## Approach

Give `termination.ConnectorConfig` a **direction** (`mt` — today's behaviour and
the default — `mo`, or `both`), and teach the MO dispatcher to publish toward a
termination connector as a third destination type.

The seam already exists. `internal/app/modispatch/service.go:301` chooses a
routing key from the route's connector type:

```go
routingKey := "deliver_sm_thrower.http"
if route.Connector.Type == "smpps" {
	routingKey = "deliver_sm_thrower.smpps"
}
```

A third case is a small, well-shaped addition rather than a new mechanism.

**The legacy MO HTTP webhook path stays exactly as it is.** Existing
integrations depend on the `ACK/Jasmin` contract, and this is inherited Jasmin
behaviour under the compatibility matrix. This plan is additive: a new
destination type for MO routes, never a change to the old one.

Key trade-offs:

- **One object, two directions** rather than making MO durable on its own. A
  separate "durable MO sink" would fix the loss but keep two things to learn,
  two endpoints to configure and two APIs to integrate. The operator-facing
  concern really is identical — *here is my endpoint, deliver to it reliably* —
  so the object should be too.
- **Verdict and receipt synthesis become inert for MO**, explicitly rather than
  silently. A verdict gate decides what receipt the *partner* sees; on the MO
  side there is no partner waiting for one. Configuring a verdict source on an
  `mo`-only connector must be a validation error, not a field that is quietly
  ignored.
- **`both` shares one spool and one endpoint**, distinguished by a `direction`
  column on the row and in the payload. An application that wants them separate
  creates two connectors; the platform should not force that on one that does not.

## Open question for the owner

**Retention pulls in opposite directions for the two.** MT content is OTP text —
the 24h window is a breach-surface control, and longer is worse. MO content may
be an opt-out, where deleting it after 24h destroys the evidence that someone
opted out. These are genuinely opposed, and the current single
`retention_hours` cannot serve both.

Options: a per-direction retention, a per-connector override, or keeping one
window and accepting that opt-out evidence must be captured downstream within
it. **This needs a decision before Step 3 is implemented** — it changes the
schema, not just a default.

## Steps

### Step 1: Direction on the connector

- **Files:** `internal/core/termination/config.go`, `internal/core/termination/config_test.go`
- **Changes:** Add `Direction` (`mt` default, `mo`, `both`) to `ConnectorConfig`. Validation: a connector whose direction excludes MT must not carry a verdict source; one whose direction excludes MO must not carry MO-only settings. Absent direction means `mt`, so every existing config keeps its meaning.
- **Verify:** table-driven tests for each direction × field combination; `--check-config` accepts every existing shipped config unchanged.

### Step 2: MO routes may target a termination connector

- **Files:** `internal/app/modispatch/service.go`, `internal/app/modispatch/service_test.go`
- **Changes:** Accept `Connector.Type == "term"` with a `cid` alongside `http` and `smpps` (`service.go:162`). Publish to `deliver_sm_thrower.term` (`service.go:301`). Reject at validation time a route naming a connector that does not exist or whose direction excludes MO — a route that can never deliver must not be creatable.
- **Verify:** a routed MO with a `term` destination publishes to the new routing key and does **not** reach the HTTP thrower; an `mt`-only connector is refused with a clear message.

### Step 3: The connector consumes MO and spools it

- **Files:** `internal/core/termination/manager.go`, `internal/core/termination/connector.go`, `internal/core/msgspool/store.go`, `internal/infra/storage/migrations/0009_message_spool_direction.sql` (new), both store implementations
- **Changes:** A connector whose direction includes MO opens a second consumer on the MO stream. Decoded MO rows are spooled with `direction = 'mo'`; MT rows get `'mt'` (backfilled for existing rows). Verdict gate and receipt synthesis are skipped for MO rows — no partner is owed a receipt.
- **Verify:** an injected MO lands in the spool with `direction: mo`; no receipt is published for it; an MT message on the same connector still gets its receipt.

### Step 4: Delivery, pull and console carry direction

- **Files:** `internal/core/termination/payload.go`, `internal/core/msgspool/pull_api.go`, `internal/app/adminweb/handlers_messages.go`, `web/src/pages/messages/list.tsx`
- **Changes:** `direction` in the push payload and the pull response; `?direction=` filter on both the pull API and `/api/messages`; a direction column and filter on the Messages page.
- **Verify:** push payload carries it; `?direction=mo` returns only inbound; the console filters correctly.

### Step 5: Admin surfaces

- **Files:** `internal/app/admin/handlers_termination.go`, `internal/app/adminweb/handlers_termination.go`, `web/src/pages/termination-connectors/form.tsx`, `internal/app/jcli/managers_routes.go`
- **Changes:** Direction selector on the connector form with the constraint explained inline; MO routes gain "termination connector" as a destination; jCli `morouter` accepts it.
- **Verify:** a connector created in the console with direction `both`, wired to an MO route, receives an injected MO end to end.

### Step 6: Documentation

- **Files:** `docs/api/messages.md`, `docs/learning-path.md`, `docs/getting-started.md`, a new ADR, `web/src/pages/education-lessons.tsx`
- **Changes:** Document the direction flag, when to use the legacy MO webhook instead, and the durability difference. An ADR recording why one object serves both directions.
- **Verify:** the "which one do I need" question is answerable from one page.

## End-to-end verification

On the dev stack: create one termination connector with direction `both` and an
endpoint; point an MT route and an MO route at it; submit a message and inject an
MO; both arrive at the endpoint, both appear in the spool with the right
direction, both are fetchable from one pull token — and stopping the endpoint
leaves **both** dead-lettered with content intact rather than the MO vanishing.

## Rollback

Additive throughout. Direction defaults to `mt`, so an unset field is today's
behaviour; the legacy MO webhook path is untouched and remains the default for
MO routes. The migration adds a nullable column. Reverting the code leaves rows
carrying a column nothing reads.

## Risks

- **Two consumers on one connector** is new concurrency in the connector
  lifecycle. Start, stop and reconnect must handle a partially-failed pair, and a
  connector that consumes MT but not MO must not report itself CONSUMING.
- **The spool becomes a mixed store.** Every existing query that assumed MT must
  be audited for a direction predicate; a missed one silently shows a partner
  another direction's traffic. The scope model is the guard, and its tests must
  grow direction cases.
- **Retention** — see the open question above. Shipping before it is decided
  means picking a default that is wrong for one direction.
- **Opt-out traffic becomes durable content.** Making MO durable is the point,
  but it also means STOP messages now persist where they previously evaporated.
  That is better for compliance and worse for breach surface; the security review
  in [operations/security.md](../operations/security.md) needs updating.

# Admin plane: truthful observability, offboarding, billing visibility, onboarding

- **Date:** 2026-07-30
- **Status:** active
- **Summary:** Close the gaps the admin-surface audit found, in order of what hurts an operator soonest: stop showing unmeasured values as zero, make offboarding complete, expose the CDR data that already exists, then make the onboarding wizard real.
- **Related:** [admin-surface-audit.md](../admin-surface-audit.md) (the evidence), [014-partner-onboarding.md](014-partner-onboarding.md) (superseded by step 4 below), [017-smpp-production-readiness.md](017-smpp-production-readiness.md), [../operations/monitoring.md](../operations/monitoring.md)

## The finding that shapes the order

The audit graded 36 capabilities across jCli, the admin REST API and the web
console. The headline is not a missing feature — the web console is broader than
either other surface — it is that **several panels present unmeasured values as
zero**.

That distinction drives the whole plan. A missing feature is visible: the
operator looks for it, does not find it, and works around it. A panel captioned
"Live process counters" showing `0` for a connector carrying traffic is worse,
because the operator believes it. They will conclude a customer sent nothing,
or that a connector is idle, and act on that.

So: correct the lies first, add capability second.

## Step 1 — Stop presenting unmeasured values as zero

### 1a. Per-connector SMPPc counters — DONE

`SMPPcRegistry` was created in the gateway runtime and handed to jCli, adminweb
and the outbound runtime, all readers. Nothing wrote to it. The connector and
session now count bind, disconnect, submit request/accept/throttle/other-failure,
inbound `deliver_sm` and `data_sm`, and `enquire_link`.

Verified on a running stack rather than only in tests: before traffic the
connector reported `connected=1 bound=1 submit=0`; after three sends,
`submit_sm_request_count=3` and `submit_sm_count=3`, with `/api/stats` — the
endpoint the console renders — returning the same values.

`disconnected_count` is counted at the single point a session ends. An earlier
placement in `Stop()` was removed rather than kept: `Stop()` cancels the session
context and lands in the same teardown, so two sites would have double-counted
every graceful shutdown. An over-counting metric is as useless as one that never
moves.

### 1b. jCli per-user statistics are fabricated — TODO

Every value `stats --user` prints is a literal zero, dash or `ND`
(`internal/app/jcli/managers_stats.go:81-143`). An operator debugging one
customer's complaint gets a screen of plausible nothing.

The cheapest honest fix is the smallest: `bound_connections_count` can be served
today from `smpps.BindManager.CountByType`, which already tracks it and which the
PB facade already served. For the traffic counters there is no per-user source
yet, so those must either be **wired to a real per-user registry** or **rendered
as an explicit "not measured" marker** rather than `0`. Do not leave them as
zero. Note this makes jCli row `J-014` — currently `MATCH` on transcript shape —
overstated; demote it when the values change.

### 1c. Per-connector and per-session clocks — TODO

`connected_at`, `bound_at`, `last_received_pdu_at`, `last_seqNum_at` and friends
all render `ND`. They need a timestamp recorded at the same points the counters
are now incremented. Small, and it is what an operator uses to answer "when did
this connector last do anything?".

## Step 2 — Make offboarding complete

Deleting a user removes the MT account and prunes its quota
(`internal/app/admin/user_service.go:183-192`) and does **not** touch a matching
SMPPs bind account. Confirmed severity, because it matters for how urgent this is:

- The deleted customer's bind credentials **still authenticate**. They connect,
  occupy a `max_bindings` slot, and appear as a live session in the console.
- They **cannot send**: `ResolveCredential` fails for the missing MT user and the
  submit is answered `ESME_RSYSERR`
  (`internal/app/smppssubmit/handler.go:63-66`).

So it is not a traffic or revenue leak. It is incomplete offboarding with a
misleading failure mode: the customer sees a **server error**, not "your account
was closed", and will reasonably open a support ticket saying the gateway is
broken.

**User deletion — DONE.** `UserService` now cascades to the SMPPs bind account and
drops any session already bound with it. Both halves were necessary: removing the
account alone only prevents *new* binds, because authentication is resolved at
bind time and an established session survives
(`internal/app/smppsserver/directory.go:109`). A receiver bind left standing for a
deleted customer can still be selected as an MO or receipt destination, which is
the one path by which this could leak traffic rather than merely confuse.

The bind account is removed before the MT user, chosen for the failure case: the
two removals are separate transactions, so if the second fails the customer can
send over HTTP but not bind, and a retry converges. The reverse order would leave
working bind credentials for a deleted account.

**Group deletion cascade and suspension — TODO.** Group deletion has the same gap.
Suspension matters more than deletion in practice and already half exists: the
console's Ban action and jCli `user --smpp-ban` disable the account *and* unbind
sessions. What is missing is one reversible "suspend this customer" that covers
both the HTTP and SMPP paths together.

## Step 3 — Expose the CDR data that already exists — DONE (plan 019)

Delivered as [019-billing-console.md](019-billing-console.md), which took the
scope further than described below: as well as CDR search, detail, event history
and export, it added SQL-side rated usage summaries, a live-versus-granted
balance view, a read-only billing settings card with the reconcile and prune
actions, and it fixed the destination-blind rate quote found along the way.

The original framing follows, unchanged.

`internal/core/cdr/operations.go` implements read, filter, event history and a
versioned cursor JSONL/CSV export. **No surface reaches any of it** — not jCli,
not the REST API, not the console. The only way to answer "what did this customer
send last month" is direct SQL against PostgreSQL.

This is the largest capability gap for running a business, and the work is
plumbing rather than design: the query layer is built and tested. Add an
authenticated read/export route on the admin listener, then a console page with
filters (user, connector, date range, final state) and an export button.

Once CDRs are reachable, rated usage statements become possible; that is the
natural follow-on and where invoicing integration would attach.

## Step 4 — Make the onboarding wizard real — DONE (plan 019 step 8)

Delivered. `POST /api/onboarding/partners` provisions group, user, bind account,
connector and route together and rolls back what it created on failure;
credentials are generated and shown once; the prototype notices are gone. Both
design points below were carried forward: passwords are generated rather than
typed, and the three directions each provision a different resource set.

One defect worth recording, because only a live run found it: the admin services
underneath are upserts, so the first implementation answered 201 to a re-run and
silently rotated an existing customer's password. Onboarding now refuses any
identity that already exists, and `TestOnboardingRefusesAnExistingPartnerRatherThanOverwritingIt`
pins it.

The original framing follows, unchanged.

`web/src/pages/partner-onboarding.tsx` is 672 lines of frontend-only prototype.
Four steps (Partner → Connection → Traffic → Review) producing in-memory state.
It labels itself honestly: "This button still creates only local mock state".

Plan 014 froze the backend deliberately, and **the reason it gave no longer
holds**. It wanted provisioning to be "one auditable, retry-safe operation
instead of asking the operator to create users, connectors, routes and
credentials independently" and could not have that because the admin plane was
incomplete. It is not anymore: `UserService`, `GroupService`, `Service`
(connectors), `RouteService`, `MORouteService` and `SMPPsUserService` all exist
and all take the same `*Store`, which supports transactions.

So the wizard can now provision for real, atomically — which is the difference
between a wizard that half-fails and leaves orphaned resources, and one that
cannot.

Two design points to carry forward from the prototype, both correct:

- It **deliberately omits the password field**, noting that production onboarding
  should collect credentials through a secret manager rather than a web form. The
  real implementation should generate and display-once, not accept-by-typing.
- Its three directions (partner binds to us / we bind to them / both) are the
  right decomposition, because they need different resources.

Remember the two traps an onboarding flow must not walk into, both documented
inherited behaviours: a throughput quota of `0` means **unlimited**, not blocked
(Q-022); and an SMPP bind password is limited to **8 characters** by SMPP 3.4, so
a generated secret longer than that cannot bind at all.

## Step 5 — Correct the semantics the UI misstates

Smaller, but each is a UI that promises something the runtime does not do:

- The route form offers **random** connector selection; the runtime selects the
  first available in order (`internal/app/outbound/runtime.go:1036-1047` versus
  `web/src/pages/routes/form.tsx:39-44`). Either implement random or stop
  offering it.
- jCli MO failover/random routes silently keep only the first destination
  (`internal/app/jcli/managers_routes.go:289-303`).
- jCli profile restore omits standalone SMPPs accounts
  (`internal/app/jcli/managers_persist.go:103-124`), so a restore silently loses
  bind users.

## Deliberately not now

**Operator roles and a change audit.** Today any admin token or console session
can do anything, and nothing records who changed what. That matters for a team
and for disputes, and it is a larger design than this plan. It belongs in its own
ADR, with the durable audit trail decided before the RBAC model.

**Invoicing.** Depends on step 3 landing first. Rated statements from CDRs are
the prerequisite; billing-system integration is a separate project.

## Order, and why

1. **Step 1** — an operator acting on false data does damage. Cheapest and most
   urgent.
2. **Step 2** — offboarding is a routine business action that currently leaves a
   half-open door and generates a support ticket blaming us.
3. **Step 3** — the biggest capability gap, and the code already exists.
4. **Step 4** — high value, larger, and safest once 1–3 make the plane
   trustworthy.
5. **Step 5** — correctness polish; do it opportunistically while in each file.

Each step must land with the evidence discipline the rest of the project uses: a
test that fails first, and for anything an operator reads, verification against a
running stack rather than a unit test alone. Step 1a was verified that way, and
that is what caught the double-count.

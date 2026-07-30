# Billing console: configuration, live balances, rated usage

- **Date:** 2026-07-30
- **Status:** active
- **Summary:** Give the web admin a real billing section — provisioned-versus-live balances, the CDR data that already exists but no surface reaches, rated usage summaries, and the global billing settings that are config-file-only today.
- **Related:** [018-admin-plane-and-onboarding.md](018-admin-plane-and-onboarding.md) (this is its Step 3, expanded), [../admin-surface-audit.md](../admin-surface-audit.md), [../adr/004-durable-billing-quota-store.md](../adr/004-durable-billing-quota-store.md), [../adr/006-durable-cdr-lifecycle.md](../adr/006-durable-cdr-lifecycle.md)

## Context

Three different things are called "billing" in this codebase, and the console
only exposes the first:

1. **Provisioned configuration** — the grants and prices an operator sets:
   per-user `balance` / `submit_sm_count` / `early_decrement_balance_percent`,
   per-group shared ceilings, per-MT-route `rate`. Editable today in
   `web/src/pages/users/form.tsx`, `groups/index.tsx`, `routes/form.tsx`.
2. **Live account state** — what a customer has left right now, held by
   `billing.Manager` and flushed to the durable quota store. Reachable only one
   user at a time through the Operations balance tool
   (`internal/app/adminweb/handlers_tools.go:16`).
3. **Rated usage history** — what was actually charged. `cdr_records` /
   `cdr_events` hold a complete rated model with an authorization and audit
   boundary and a cursor export (`internal/core/cdr/operations.go`).
   `Runtime.CDRService()` has **no caller** in adminweb; `Deps` has no CDR field.
   The only way to answer "what did this customer send last month" is SQL.

Two things are worse than missing, because an operator believes them:

- `internal/app/adminweb/handlers_users.go:64` projects `cfg.Balance` — the
  **provisioned grant** from the stored `UserConfig` — into a column labelled
  "Balance". After a day of traffic it reads `100` while the customer has `3.42`.
- `runtimeDirectory.Rate` ignores the destination entirely and returns
  `directory.defaultRate`, a value snapshotted from the boot config. So the
  console's Rate tool — and the customer-visible HTTP `/rate` endpoint — quote
  one price for every destination and keep quoting the boot-time price after an
  operator changes routes at runtime. Legacy Jasmin resolves the real matching
  route. Worse than it first looks: `defaultRate` is set by
  `if entry.Order > bestOrder` in `buildRoutes`, so it is the **highest-order**
  route's rate, not the default route's. A customer whose traffic falls through
  to a cheap order-0 default was being quoted the expensive filtered route's
  price. Pinned by `TestTheOldFallbackRateWasTheHighestOrderRouteNotTheDefaultRoute`.

## Approach

Add a **Billing** section to the console over new `/api/billing/*` BFF routes,
built on the existing core services rather than a new billing subsystem. Order
the work so the misleading numbers are corrected before new surface is added,
which is the discipline plan 018 set.

Key decisions and their trade-offs:

- **Live values are read, never cached.** The accounts view calls
  `BalanceReader` per user on request. It is an in-process mutex read; at admin
  page scale a stale cache would be a worse trade.
- **Aggregation happens in SQL, not in the BFF.** Summing a month of CDRs by
  paging them into Go works at pilot volume and collapses at carrier volume, so
  `SummarizeCDRs` is added to the repository interface and implemented in both
  backends, grouped by `(user_id, currency)`.
- **`Principal.Subject` is the session username.** `cdr.Service` writes an
  access-audit row for every read and export; passing the logged-in operator
  makes that audit real. This is audit fidelity, not RBAC — every console
  session still has the same power. Real roles stay deferred (018).
- **Charged means applied, not quoted.** Summaries report `early_amount +
  actual_late_amount` as charged and show quoted-but-unapplied late money
  separately, because `late_amount` is an intent that `BillingOutcome` may have
  left `PENDING` or `REJECTED`.
- **No fabricated money tiles.** The three billing Prometheus recorders have no
  call sites and `charging_error_count` is inert; nothing in this plan renders
  them as zero.

## Steps

### Step 1: destination-aware rate quotes

- **Files:** `internal/app/outbound/config.go`, `internal/app/outbound/runtime.go`, `internal/app/outbound/runtime_integration_test.go` (or a new focused test)
- **Changes:** give `runtimeDirectory` the live `*routingtable.AtomicTable`; in
  `Rate`, build the same `routingfilter.Routable` the submit path builds
  (direction MT, resolved UID/GID, destination address) and return the selected
  route's `Rate()`. Keep `defaultRate` only as the no-match answer so the
  endpoint's error surface does not change.
- **Verify:** a test with two routes (default rate `0.5`, a filtered route rate
  `2.0`) asserts the quote follows the destination, and that a route added at
  runtime through `ApplyAdminRoutes` changes the quote.

### Step 2: live balances beside the provisioned grant

- **Files:** `internal/app/adminweb/handlers_users.go`, `internal/app/adminweb/handlers_billing.go` (new), `internal/app/adminweb/server.go`, `internal/app/outbound/runtime.go`, `internal/app/gateway/runtime.go`, `web/src/pages/users/list.tsx`, `web/src/pages/billing/accounts.tsx` (new)
- **Changes:** add read-only `live_balance` / `live_submit_sm_count` to the user
  resource, sourced from `BalanceReader`; add `GET /api/billing/accounts`
  returning user, group, granted and remaining balance/quota, early-decrement
  percent and the derived billing mode; expose group live quota through a new
  `Runtime.GroupQuota` seam. Rename the users-list column to "Granted" and add
  "Remaining".
- **Verify:** `go test ./internal/app/adminweb/...` with a stub reader proving
  the two values are distinct in the payload; in the running stack, send a
  message and watch remaining fall while granted does not.

### Step 3: CDR read, detail, events and export

- **Files:** `internal/core/cdr/operations.go`, `internal/core/cdr/operations_test.go` (new), `internal/app/adminweb/handlers_billing.go`, `internal/app/adminweb/server.go`, `internal/app/gateway/runtime.go`, `web/src/pages/billing/usage.tsx` (new)
- **Changes:** add `Service.Events` (authorized + audited, currently only on the
  raw repository); wire `CDRService` into `Deps`; add
  `GET /api/billing/cdrs` (user + time window, cursor-paged),
  `GET /api/billing/cdrs/{id}`, `GET /api/billing/cdrs/{id}/events` and
  `GET /api/billing/export` (CSV/JSONL passthrough with `Content-Disposition`).
  The page filters by user and date range server-side and by state/connector
  within the fetched page, labelled as such.
- **Verify:** `go test ./internal/core/cdr/... ./internal/app/adminweb/...`;
  against a running stack, send a message and find its parts, event timeline and
  export row.

### Step 4: rated usage summary

- **Files:** `internal/core/cdr/operations.go`, `internal/infra/storage/sqlite_cdr.go`, `internal/infra/storage/postgres_cdr.go`, `internal/infra/storage/sqlite_cdr_summary_test.go` (new), `internal/app/adminweb/handlers_billing.go`, `web/src/pages/billing/statements.tsx` (new)
- **Changes:** add `SummarizeCDRs` to `OperationsRepository` with both
  implementations grouped by `(user_id, currency)` over the existing
  `cdr_records_user_time` index; add `Service.Summarize`; add
  `GET /api/billing/summary`; render a per-customer statement table with parts,
  distinct messages, accepted/rejected, delivered/undelivered/pending, charged
  early, charged late, total charged and quoted-late-pending.
- **Verify:** the storage test seeds records with mixed billing outcomes and
  asserts the aggregate; the totals must equal the sum of the same window's
  export rows (asserted in the test, not by eye).

### Step 5: billing settings, reconcile, prune

- **Files:** `internal/app/adminweb/handlers_billing.go`, `internal/app/adminweb/server.go`, `internal/app/gateway/runtime.go`, `web/src/pages/billing/settings.tsx` (new)
- **Changes:** `GET /api/billing/settings` returns currency, retention days and
  batch, maintenance interval and quota-persist interval, all read-only with an
  explicit "config file, restart to change" note and a warning while currency is
  `XXX`; `POST /api/billing/reconcile` returns the issue report;
  `POST /api/billing/prune` runs retention behind a typed confirmation and is
  refused with a clear message when retention is disabled.
- **Verify:** handler tests for the disabled-retention refusal and the report
  shape; the settings card matches the deployed config file.

### Step 6: navigation, bundle, docs

- **Files:** `web/src/App.tsx`, `web/src/components/AppShell.tsx` (if the menu
  needs a group), `internal/app/adminweb/dist/**`, `internal/app/adminweb/bundle.sourcehash`, `docs/admin-surface-audit.md`, `docs/STATUS.md`
- **Changes:** register the four billing pages under one menu group; rebuild the
  embedded bundle; update the audit rows that this plan turns from Absent into
  Full.
- **Verify:** `cd web && npm run build` restamps `bundle.sourcehash`;
  `go build ./...` embeds it; the CI `adminweb-bundle-freshness` job recomputes
  the hash from `web/` and must agree.

### Step 7: the same commercial data on the admin REST API

Decided after steps 1–6 landed, when the question "is this web-only?" was asked
directly. It is, and `/admin` is the surface that should not be.

**Why REST and not jCli.** `/admin` is the token-authenticated automation
surface — scheduled usage pulls, exports into an accounting system, a billing
job that reads balances — and the audit records it as Absent for every
statistics and CDR row. It carries no frozen-transcript obligation, so new
routes cost nothing conceptually.

jCli is deliberately excluded. Its one distinguishing property in this project
is that it replays the legacy console byte-for-byte: 19 captured fixtures, 18/18
matrix rows `MATCH`. Billing verbs have no Python counterpart, so they would be
the first jCli commands with no oracle, and the claim would become "byte-stable
except for the parts we invented". A line-oriented telnet transcript is also the
wrong medium for cursor-paged wide rated rows and file export. The real jCli gap
is that `stats --user` prints fabricated zeros (plan 018, step 1b) — a
correctness fix worth more there than a new feature.

- **Files:** `internal/app/admin/handler.go`, `internal/app/admin/handlers_billing.go` (new), `internal/app/admin/handlers_billing_test.go` (new), `internal/app/gateway/runtime.go`, `docs/api/` (admin API reference if one exists)
- **Changes:** add a functional option — `admin.WithBilling(...)`, following the
  existing `restcompat.WithBatchContext` precedent rather than breaking
  `NewHandler`'s signature — carrying the `*cdr.Service`, a `core.BalanceReader`
  and an optional config-account lister. Register `/admin/cdrs`,
  `/admin/cdrs/{id}`, `/admin/cdrs/{id}/events`, `/admin/cdrs/export`,
  `/admin/billing/summary` and `/admin/billing/accounts`, all bearer-gated by the
  existing `auth` wrapper and all absent when the option is not supplied. Route
  parsing is prefix-based like the rest of this mux, which handles the slash
  inside a CDR id natively (`/admin/cdrs/msg-1/000001/events`) as well as the
  percent-encoded form.
- **Audit subject:** `admin-api`. The admin plane has one shared token, so there
  is no per-caller identity to record; writing anything more specific would put a
  fabricated actor into `cdr_access_audit`. The console keeps its real per-operator
  attribution, and the two are distinguishable in the table.
- **Deliberately not included:** the accounts projection reads the stored user
  spec through a local field subset rather than importing `outbound` into
  `admin`, which would be a new dependency edge for four fields. Config-owned
  accounts arrive through the injected lister supplied by the gateway.
- **Verify:** handler tests for auth (401 without the token, on every new route),
  the disabled surface (404 when the option is absent), the slashed-id parse, and
  the export byte passthrough; then the same live-stack checks as step 3 against
  the admin listener with a bearer token.

### Step 8: partner onboarding provisions for real

Added when the console's onboarding screen was rechecked and confirmed to be
what its own banner said: 672 lines with no API call, creating nothing.

- **Files:** `internal/app/adminweb/handlers_onboarding.go` (new), `internal/app/adminweb/handlers_onboarding_test.go` (new), `internal/app/adminweb/server.go`, `web/src/pages/partner-onboarding.tsx`, `web/src/index.css`
- **Changes:** `POST /api/onboarding/partners` provisions a billing group, gateway
  user, SMPPs bind account, SMPPc connector and MT route in dependency order,
  recording each one so a later failure undoes them in reverse. The admin
  services each own their own transaction, so this is compensating rollback
  rather than one database transaction; a rollback that itself fails is reported
  with the exact orphans rather than swallowed. Credentials are generated
  server-side and returned once — the bind password at eight characters, because
  SMPP 3.4 caps it there and a longer one cannot bind. The wizard calls it, shows
  what was created, hands the credentials over once, and its prototype notices
  are gone.
- **Refuses rather than adopts.** The underlying services are upserts. The first
  implementation therefore answered `201` to a re-run of an existing partner code
  and silently rotated that customer's password and overwrote their balance,
  group and throughput. Found by running it twice against a live gateway, not by
  the unit tests, which each started from an empty store. Onboarding now refuses
  any identity that already exists and names the collision.
- **Verify:** `go test ./internal/app/adminweb/ -run TestOnboarding`; against a
  running stack, provision a partner, then authenticate as the generated user,
  quote its route, start its connector and send through it.

## End-to-end verification

On a running stack (`docker compose` gateway + postgres + rabbitmq):

1. Send a message through `/send` for a user with a balance and a rated route.
2. Billing → Accounts shows remaining below granted for that user.
3. Billing → Usage lists the part, its event timeline reaches `SMSC_ACCEPTED`
   (and `FINAL_DLR` once the receipt lands), and the export contains the row.
4. Billing → Statements shows one message, one part and the charged amount, and
   the amount equals granted minus remaining for a single-route prepaid user.
5. Operations → Rate quotes the filtered route's price for a destination that
   matches it, and the default price for one that does not.

## Rollback

Every step is additive. Reverting the commit removes the routes and pages; no
schema migration is introduced (the summary is a query over existing tables) and
no stored data changes shape. The only behaviour change to an existing surface is
the `/rate` fix in Step 1, which can be reverted independently.

## Risks

- **`/rate` is a customer-visible contract.** Its response shape is unchanged and
  the goldens stub the reader, but any consumer that depended on always receiving
  the default rate will now see the real one. That is the point, and it is a
  correction, not a regression.
- **Summary queries scan a user's window.** The `(user_id, admitted_at, cdr_id)`
  index covers the intended filters; an unfiltered all-users summary over a long
  window is deliberately capped by a required time range.
- **Audit rows grow.** Every console CDR read writes to `cdr_access_audit`.
  That table has no retention policy of its own; watch it before opening the page
  to automation.
- **Currency is global.** Summaries group by currency to stay correct if it is
  ever changed mid-window, but per-route or per-customer pricing currency remains
  out of scope.

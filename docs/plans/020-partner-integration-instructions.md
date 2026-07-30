# Partner integration instructions, generated per account

- **Date:** 2026-07-30
- **Status:** active
- **Summary:** Generate, from live gateway state, a per-account "how to send us traffic" pack covering every ingress this deployment actually has enabled, constrained by that account's real authorizations, value filters, throughput and balance — plus the reverse artifact for an outbound SMPP connector.
- **Related:** [019-billing-console.md](019-billing-console.md) (Step 8 provisions the partner; this is what you hand them afterwards), [018-admin-plane-and-onboarding.md](018-admin-plane-and-onboarding.md), [../api/http.md](../api/http.md), [../api/rest.md](../api/rest.md), [../api/smpp.md](../api/smpp.md), [../api/callbacks.md](../api/callbacks.md)

## Context

Plan 019 Step 8 made onboarding real: the console now creates a group, a gateway
user, a bind account, a connector and a route, and hands over the two generated
passwords once. What it does not produce is the thing the partner actually needs
next — the integration pack. Today an operator writes that by hand, from memory,
per customer.

Writing it by hand is where it goes wrong, because the answer is per-account and
this codebase enforces far more per-account state than a URL:

- **Which ingress exists at all is deployment state.** The HTTP front door lives
  on `outbound.listen_address`, the standalone JSON REST view only exists when
  `rest_api.listen_address` is set (`internal/transport/restcompat/config.go`),
  and the customer-facing SMPP server only exists when `smpps` is present
  (`internal/app/gateway/config.go:57`). Telling a partner to bind SMPP to a
  deployment with no `smpps` block is an immediate support ticket.
- **What the account may do is per-user credential state.** `http_send`,
  `http_long_content`, `set_dlr_level`, `http_set_dlr_method`,
  `set_source_address`, `set_priority`, `set_validity_period`, `set_hex_content`,
  `set_schedule_delivery_time`, `http_balance`, `http_rate`, `smpps_send`
  (`internal/core/mtcredential/credential.go:16`) each turn a documented
  parameter into an HTTP 400 with a specific sentence
  (`internal/transport/httpcompat/credentials.go:47`). Handing a customer the
  generic API doc when their credential forbids `from` produces exactly one
  outcome.
- **What values are admissible is per-user filter state.**
  `filter_destination_address`, `filter_source_address`, `filter_priority`,
  `filter_validity_period`, `filter_content` are Go RE2 patterns matched with
  Python `re.match` semantics — anchored at the start only
  (`internal/core/mtcredential/validate.go:151`). `^33` means "destinations must
  begin with 33", and a partner who is not told this discovers it as
  `Value filter failed for user [x] (destination_address filter mismatch).`
- **The delivery-receipt contract is the single most common integration
  failure.** A callback succeeds only on HTTP 2xx *and* a body that trims to
  exactly `ACK/Jasmin` (`docs/api/callbacks.md:12`,
  `internal/core/dlr/http_thrower.go:117`). `200 OK` with an empty body is a
  failure and retries four times, then purges.

Two facts constrain any implementation:

1. **The gateway does not know its own public address.** Every listener binds
   `0.0.0.0` in the shipped configs, and deployments port-map (this repo's own
   compose maps 1401 → 11401). Printing `0.0.0.0:1401` as a partner-reachable
   endpoint is a lie.
2. **Passwords are unrecoverable and must stay that way.** HTTP passwords are
   stored as `password_sha256` (`internal/app/outbound/config.go:105`), bind
   passwords as an MD5 verifier (`internal/app/smppsserver/directory.go:18`), and
   every console resource blanks the field before serialising. Onboarding shows
   the generated secret exactly once and nothing can retrieve it later.

## Approach

One BFF endpoint family, `GET /api/integration/*`, returning **structured JSON**,
rendered by one reusable drawer component. No prose paragraphs are built in Go;
the backend emits facts, short factual labels and ready-to-run command strings,
and the UI owns all framing copy and styling.

Key decisions and their trade-offs:

- **Billing → Accounts and Gateway users share one endpoint.** Both rows are the
  same identity: a `outbound.users[]` entry keyed by username
  (`internal/app/adminweb/handlers_billing.go:83` collects exactly what
  `listUsers` collects). Two buttons, one `GET /api/integration/users/{username}`,
  one component. A second endpoint would guarantee the two screens eventually
  disagree about what a customer may do.
- **The public address is handled twice over, and never guessed.** A new
  optional top-level `public_hostname` in the gateway config names the host
  partners use. When it is unset the payload carries the literal placeholder
  `YOUR-GATEWAY-HOST` and `authority_is_placeholder: true`, the UI shows a
  substitution banner, and an operator field rewrites the authority in every
  example before copy or download. The field accepts `host:port` because port
  mapping is normal and the listener port is not necessarily the published one.
  The raw bind address is still shown, labelled as the *bind* address, so an
  operator can diagnose exposure — it is never used to build a URL.
- **Availability is computed, not assumed.** Each channel carries
  `available: bool` plus a `blocked_by` list naming exactly what to fix
  ("`smpps_send` is not authorised", "no SMPP bind account exists for this
  username", "this deployment has no SMPP server configured"). An unavailable
  channel is rendered as a blocked card, not omitted — "you cannot use SMPP,
  because X" is a better answer to a partner than silence.
- **Effective authorizations, resolved the way the runtime resolves them.**
  `buildMTCredential` (`internal/app/outbound/config.go:829`) starts from
  `mtcredential.New(true)` and overlays only non-nil pointers, so an omitted flag
  is `true` (except `http_bulk`, always `false` on a fresh credential). The
  handler reproduces that resolution rather than reading the stored pointers
  directly, so the guide states what the front door will actually enforce.
- **`http_bulk` is reported as inert, because it is.** No validator consults
  `AuthHTTPBulk` anywhere in the tree — only jCli renders it. `/secure/sendbatch`
  availability is instead governed by `http_send` **and `http_balance`**, because
  `authenticateBatch` authenticates a batch by calling `/balance`
  (`internal/transport/restcompat/handler.go:342`), which is credential-gated
  (`internal/transport/httpcompat/handler.go:205`). A customer with
  `http_balance: false` gets `Authentication failed for user: x` on every batch
  and no indication why. This is stated in the pack.
- **The callback contract is a first-class section, not a footnote.** It carries
  the exact required body, a copyable minimal receiver response, the level table,
  and the level-2 trap (a failed submit produces *no* callback at all).
- **Balance and quota come from the live reader, not the grant.** The pack
  reuses `liveQuota` (`internal/app/adminweb/handlers_billing.go:136`) so it
  reports what is left, and says so — plan 019 already established that the grant
  in a column labelled "balance" is the console's most misleading number.
- **Connectors get a different artifact, deliberately.** See Step 5.

### Deliberately left out

- **No new npm dependency.** Copy uses `navigator.clipboard` with a
  `document.execCommand` fallback; download uses a `Blob` + object URL. No
  syntax highlighter, no markdown renderer, no QR code.
- **No PDF or branded export.** Plain text and clipboard only. A styled document
  is a marketing artifact with a different owner and a different review path.
- **No emailing or link-sharing of the pack.** It contains an account's
  commercial position (balance, throughput, filters). It leaves the console only
  when an operator deliberately copies or downloads it.
- **No password material, ever, and no "resend credentials" affordance.** The
  pack references the credential handed over separately. Adding a rotate-and-show
  button here would put credential minting behind an innocuous-looking
  "instructions" button.
- **No changes to `docs/api/*`.** The pack cites those pages; they are already
  accurate and are the long-form reference the pack links to conceptually.
- **No MO route creation.** The pack *reports* whether inbound routing exists for
  this account and links to the MO routes page; it does not provision one.
- **jCli is not given an equivalent command.** The artifact is a multi-section
  document with copyable snippets; a telnet line protocol is the wrong medium,
  and plan 019 Step 7 already recorded that reasoning for billing.

## Steps

### Step 1: teach the console what the deployment publishes

- **Files:** `internal/app/gateway/config.go`, `internal/app/gateway/runtime.go`,
  `internal/app/adminweb/server.go`
- **Changes:**
  - `config.go`: add optional top-level `PublicHostname string
    \`json:"public_hostname,omitempty"\`` — the host partners use to reach the
    customer-facing listeners. Host only, no port: each ingress keeps its own
    configured port, and port mapping is handled by the console's substitution
    field. `LoadConfig` uses `DisallowUnknownFields`, so this is additive and no
    existing config file breaks.
  - `server.go`: add exactly one field to `Deps` —
    `Ingress func() IngressSnapshot` — where `IngressSnapshot` is declared in the
    new `handlers_integration.go`, not here, to keep this edit to one line plus
    the route registration in Step 2. A nil `Ingress` means "the gateway did not
    report its listeners": every channel then reports its endpoint as unknown
    rather than fabricating one.
  - `runtime.go`: populate `Ingress` from `config.Outbound.ListenAddress`,
    `config.REST.ListenAddress`, `config.SMPPS`, `config.HTTPS` and
    `config.PublicHostname` inside the existing `adminweb.Deps{...}` literal.
- **Verify:** `go build ./...`; `go test ./internal/app/gateway/... -count=1`
  (the example-config test parses `configs/gateway.example.json` and must still
  pass with the new field absent).

### Step 2: the integration BFF handler

- **Files:** `internal/app/adminweb/handlers_integration.go` (new),
  `internal/app/adminweb/server.go` (two route lines)
- **Changes:** `GET /api/integration/users/{username}` returns:
  - `subject`: username, external id, group, managed_by, disabled, group_disabled.
  - `position`: granted/remaining balance and submit count via `liveQuota`,
    billing mode via `billingModeFor`, `live_error` when the live read fails
    (blank, never zero), HTTP and SMPPs throughput ceilings (nil = unlimited).
  - `endpoints`: one entry per ingress — `id`, `protocol`, `enabled`,
    `scheme`, `authority`, `authority_is_placeholder`, `bind_address`, `port`,
    `tls`. The HTTP entry is enabled when `outbound.listen_address` is set; the
    REST-daemon entry only when `rest_api.listen_address` is set (with a note
    that `/secure/*` is also on the main listener); the SMPP entry only when
    `smpps` exists.
  - `channels`: `http_send`, `rest_send`, `rest_sendbatch`, `smpp_bind`,
    `balance_rate`. Each carries `available`, `blocked_by []string`,
    `parameters []parameter` (only the ones this account may set), and
    `examples []example` (`{id, title, command}`) with the account's real
    username and a destination that satisfies `filter_destination_address`.
  - `authorizations`: every key with `{key, label, allowed, effect}` where
    `effect` is the exact rejection sentence from
    `internal/transport/httpcompat/credentials.go:47` for the denied ones.
  - `filters`: every value filter with `{key, pattern, constrains, meaning}`;
    `constrains` is false when the pattern still equals the literal the validator
    skips on (`.*`, `^[0-3]$`) — reproducing `applyFilter`'s skip rule, including
    the `validity_period` quirk where the skip literal is `.*` and not that
    filter's own default, so the default validity filter always applies.
  - `default_source_address` and its two behaviours: substituted only when `from`
    is absent on HTTP, or absent *or empty* on SMPP
    (`internal/core/mtcredential/submit.go:73`).
  - `callbacks`: the required ack body, the accepted status range, the level
    table, retry policy, and the field lists — as data.
  - `inbound`: whether an SMPP bind account exists, the bind account's
    `ip_whitelist` and `max_bindings`, and the MO routes whose destination is
    this account's SMPPs system id.
  - `warnings []string`: account disabled, group disabled, exhausted balance or
    quota, no bind account while SMPP is enabled, `http_send` denied, wide-open
    `ip_whitelist`, unknown public host.
  - **Destination example selection:** when `filter_destination_address`
    constrains, the example destination is derived from the pattern's literal
    prefix (`^33` → `33...`) so the emitted `curl` is not one the account's own
    filter would reject. When no literal prefix can be recovered, the example
    carries the pattern and a `substitute` note instead of a fake number.
  - **No password field exists on any struct in this file.** The account's
    credential is referenced as "the password handed over at onboarding".
- **Verify:** `internal/app/adminweb/handlers_integration_test.go` (Step 4).

### Step 3: the reusable UI component and the two buttons

- **Files:** `web/src/components/IntegrationGuide.tsx` (new),
  `web/src/pages/billing/accounts.tsx`, `web/src/pages/users/list.tsx`,
  `web/src/index.css` (appended block only)
- **Changes:**
  - `IntegrationGuide.tsx` exports `IntegrationGuideButton` (the trigger, safe to
    drop into a table actions cell) and the drawer itself. It follows
    `ConfigDetail.tsx`: Ant Design `Drawer` + `Descriptions` + `Collapse`,
    `destroyOnClose`, fetched with `useCustom` from `@refinedev/core` like
    `billing/accounts.tsx`.
  - Sections: substitution banner (public address field), account position,
    per-channel cards with copy buttons, authorizations table, value filters,
    delivery receipts, inbound, warnings.
  - The public-address field rewrites every example by replacing the payload's
    `authority` token; when the authority is a placeholder the copy button stays
    enabled but the banner is a warning, not an info.
  - Copy: `navigator.clipboard.writeText` with a hidden-textarea
    `document.execCommand("copy")` fallback (the console is served over plain
    HTTP on a loopback listener in most deployments, where the async clipboard
    API is unavailable outside a secure context).
  - Download: `Blob` + `URL.createObjectURL` producing
    `integration-<username>.txt`, rendered from the same payload by a pure
    function so the file and the screen cannot drift.
  - `accounts.tsx`: a new right-aligned "Integration" column. The row `onClick`
    opens the billing drawer, so the button must `stopPropagation`.
  - `users/list.tsx`: the button joins the existing actions `Space`, and is shown
    for config-owned users too (they can send; they just cannot be edited).
  - `index.css`: appended `.integration-guide*` rules only, using the existing
    custom properties.
- **Verify:** `cd web && ./node_modules/.bin/tsc --noEmit`.

### Step 4: handler tests

- **Files:** `internal/app/adminweb/handlers_integration_test.go` (new)
- **Changes:** using `newWebFixture`:
  - A permissive user: every channel that the fixture's ingress reports is
    available; the `curl` example contains the real username.
  - A restricted user (`set_source_address:false`, `set_dlr_level:false`,
    `filter_destination_address:"^33"`): the source and DLR-level parameters are
    absent from the HTTP channel's parameter list, the authorizations carry the
    exact rejection sentences, and the example destination begins `33`.
  - `http_balance:false`: `rest_sendbatch` is blocked and names `http_balance`.
  - No `smpps` ingress: the SMPP channel is present, unavailable, and blocked by
    "this deployment has no SMPP server".
  - `smpps` ingress but no bind account: blocked by the missing bind account.
  - **No password material:** create a user with password `pw-integration`,
    create a bind account with password `bindpw42`, then assert neither string
    (nor the SHA-256 of the first) appears anywhere in the raw response body.
  - Placeholder host: with no `public_hostname`, `authority_is_placeholder` is
    true and the examples contain `YOUR-GATEWAY-HOST`.
  - Unknown username → 404.
- **Verify:** `go test ./internal/app/adminweb/... -count=1`.

### Step 5: the connector artifact — decision and scope

**Decision: build it, as a deliberately different document.**

An SMPP connector is us dialling out to them, so "how to connect to us" is
meaningless. The reverse artifact — what to request from the carrier — earns its
place for one reason that is specific to this codebase rather than to SMPP in
general: `smppc.Config` carries about fifteen wire-affecting fields
(`src_ton`/`src_npi`, `dst_ton`/`dst_npi`, `service_type`, `address_range`,
`bind`, `submit_sm_throughput`, `dlr_msg_id_bases`, `tls_enabled`, the three
timers), and the onboarding wizard sets **none** of them
(`internal/app/adminweb/handlers_onboarding.go:272` builds a connector from host,
port, system id, password, bind mode and TLS only). Every unset field silently
becomes zero, and zero TON/NPI is a real value that a carrier may reject or
mis-route. A brief that prints "we will send with `src_ton=0`, `src_npi=0`,
`service_type` empty — confirm or correct" turns a silent default into an
explicit question, and it is generated from the connector record rather than
invented.

What it is **not**: it is not a mirror of the account pack, and it does not
pretend to know the carrier's throughput, DLR conventions or test MSISDN. Those
are emitted as an explicit *request* list with blank answers, because the gateway
genuinely does not know them and printing a guess would be the same failure mode
as printing a bind address as a public endpoint.

- **Files:** `internal/app/adminweb/handlers_integration.go`,
  `internal/app/adminweb/server.go` (one route line),
  `web/src/components/IntegrationGuide.tsx`,
  `web/src/pages/connectors/list.tsx`
- **Changes:** `GET /api/integration/connectors/{cid}` returns `link` (host,
  port, system id, bind mode, TLS, timers, throughput — **never the password**),
  `we_will_send` (the PDU-shaping fields with a `is_default` flag on each), and
  `request_from_carrier` (an ordered checklist: peer host/port/system id/password
  for our bind, permitted source IPs of ours to whitelist, expected source and
  destination TON/NPI, `service_type`, throughput ceiling and window size, DLR
  delivery mechanism and `dlr_msg_id_bases` convention, a test destination, and
  the escalation contact). The same drawer renders it in `kind="connector"` mode.
- **Verify:** handler test asserting the connector password never appears and
  that unset PDU fields are flagged `is_default`; `tsc --noEmit`.

### Step 6: bundle and docs

- **Files:** `internal/app/adminweb/dist/**`, `internal/app/adminweb/bundle.sourcehash`,
  `docs/admin-surface-audit.md`
- **Changes:** rebuild the embedded bundle so the new component ships.
- **Note:** the bundle rebuild is **out of scope for this change set** and is
  owned elsewhere in the current session; `dist/` and `bundle.sourcehash` are
  deliberately untouched here. The CI `adminweb-bundle-freshness` job will fail
  until that rebuild lands, which is the intended signal.
- **Verify:** `cd web && npm run build` restamps the hash; `go build ./...`
  embeds it.

## End-to-end verification

On an isolated stack (`docker compose -p jasmin-integration -f
docker-compose.gateway.yml -f <override> up -d --build`, admin UI on 18404,
sendsms mapped to host 11401):

1. `GET /api/integration/users/smppuser` through an authenticated session
   returns a pack whose HTTP channel is available and whose SMPP channel is
   blocked — that config has no `smpps.users[]` entry for `smppuser`.
2. Take the generated `curl` verbatim, substitute the authority for the mapped
   host port exactly as the console's substitution field does, and run it. It
   must return `Success "<uuid>"`. If it does not, the instructions are wrong.
3. Re-run with a destination the account's `filter_destination_address` rejects
   and confirm the rejection sentence matches the one the pack printed.
4. Confirm the raw response body contains no password material.

## Rollback

Entirely additive: one new handler file, one new component, two route lines, one
`Deps` field, one optional config field, two table buttons. Reverting removes the
buttons and the endpoints; no stored data changes shape and no existing response
shape is modified. `public_hostname` is optional, so reverting it cannot break a
config that never set it.

## Risks

- **The pack is only as honest as the deployment's config.** If an operator sets
  `public_hostname` to something unreachable, the pack confidently prints it. The
  UI substitution field and the "verify this yourself" banner are the mitigation;
  there is no way for the gateway to validate its own reachability from outside.
- **Commercial data in an exportable document.** The pack carries balance,
  throughput and filters. It is session-authenticated and leaves the browser only
  on an explicit copy or download, but an operator can now put a customer's
  commercial position into a text file in one click. Deliberate: that position is
  what the customer needs to understand their own limits.
- **Drift between the pack and the front door.** Anything that changes
  `mtcredential`'s rules without changing this handler makes the pack lie. The
  handler mirrors `buildMTCredential`'s default resolution and `applyFilter`'s
  skip rule rather than approximating them, and the tests assert the exact
  rejection sentences, so a change to those strings breaks a test rather than a
  customer.
- **`http_bulk` is reported as inert.** If it is ever wired to a real check, this
  handler must be updated in the same change or it will understate a restriction.

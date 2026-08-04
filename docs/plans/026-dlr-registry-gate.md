# 026 — Per-user DLR registry gate

- **Date:** 2026-08-03
- **Status:** done (extended after review: per-user scoped credentials and window ownership)
- **Summary:** Make the terminal DLR a submitting user receives depend on whether the destination MSISDN is in a short-lived Redis registry, switchable per user, with a write API and a live admin view.

## Problem

A partner binds as SMPP user `demoesme` and submits traffic. For that user we want the
terminal delivery receipt to be decided by an allowlist rather than by whatever the
upstream eventually says:

- destination present in the registry → success receipt (`DELIVRD` / `000`)
- destination absent → reject receipt (`REJECTD` / `008`)

Registry entries live at most 15 minutes. Multiple messages to the same registered
number inside one window all get the success receipt (the user's worked example is four
SMS to `380930242105`). Numbers arrive through an API; operators need to see what is
currently in the registry.

## Key finding — most of this already exists

`internal/core/termination/verdict_redis.go` already implements exactly these
semantics as the **activation window**:

- key `dlr:block:{digits}` (`termination.DefaultActivationKeyPrefix`, `verdict.go:71`)
- presence → `DELIVRD`/`000`, absence → `REJECTD`/`008` (`verdict_redis.go:35-38`)
- `NormalizeDestination` (`normalize.go`) is the key form: trim, drop one leading `+`, digits only, ≤ 20
- `windowCache` gives single-flight + a short reuse TTL so a burst of OTP retries to one
  number is one lookup and cannot straddle the window's expiry (`verdict_redis.go:107-113`)
- unreachable Redis **fails open to DELIVRD** rather than rejecting live traffic

What is missing is only: (a) it is bound to *termination connectors*, not to submitting
users; (b) nothing in this repo *writes* the keys — the comment at `verdict_redis.go:18`
says they come from "a different system entirely"; (c) there is no way to list them.

So this plan does **not** invent a parallel mechanism. It reuses the same keyspace,
the same normalization and the same verdict vocabulary, and adds the three missing
pieces.

## Design

### Decide at submit, apply at receipt

The verdict is computed **at submit time** (the window can expire between submit and
receipt, and the user's description checks the registry when the message arrives) and
stamped onto the `dlr:<msgid>` Redis record that the submit path already writes. The
correlator applies it when the terminal receipt arrives.

```
submit ──► registry lookup ──► stamp gate_stat/gate_err on dlr:<msgid>
                                        │
upstream deliver_sm receipt ──► correlator ──► CDR gets the REAL status
                                        └────► Forward gets the STAMPED status
```

Routing is untouched: the message still goes upstream exactly as today. Only the
`stat:`/`err:` the submitting partner is told changes.

### Truthfulness boundary (load-bearing)

The override is applied **after** the CDR hook in `OnDeliverReceipt`
(`correlation.go:340-352`), so `cdr.RecordFinalDLR` keeps the real upstream status.
Every override also emits a log line carrying both values. Internal records stay
truthful even when the partner-facing receipt is overridden; that separation is the
reason this is safe to ship and must not be "simplified" away.

No Prometheus series was added: the legacy `/metrics` text format is parity-frozen
(`docs/specs/003-metrics-coverage.md`), so overrides are observable through the
structured log only. A series behind a non-legacy registry is a reasonable follow-up.

### Why not the other injection points

| Option | Rejected because |
|---|---|
| Synthesize both DLR legs like `termination.SMSCLeg` | Needs a durable timer, and a real upstream receipt would still arrive → duplicate DLRs needing suppression. |
| Wrap the `dlr.Publisher` seam | The correlator has no username for `httpapi` records, and the level-1/level-2 logic would have to be duplicated. |
| Reject the submit on a miss | Gives the partner an `ESME_RSUBMITFAIL` on `submit_sm_resp`, not the reject *receipt* that was asked for. |

### Registry storage

Value keys stay **plain strings** so `GET` works. This is not cosmetic: the existing
gate reads with `GET` and treats a `WRONGTYPE` error as *fail-open DELIVRD*
(`verdict_redis.go:94-104`), so storing a hash here would silently accept everything.
The JSON body is metadata only; the existing gate and the legacy `fake_smsc.py`
(`get(key) is not None`) only test presence.

A sorted-set index `<prefix>index` scored by expiry-unix makes the admin list one
bounded read instead of a keyspace `SCAN` (the `dlr:` keyspace also holds 24h DLR
records, so `SCAN MATCH` would walk far more than the registry). Redis TTL does not
remove ZSET members, so the list prunes with `ZREMRANGEBYSCORE` on read. Entries
written by another system land in the keyspace but not the index, so the UI says the
list covers entries added through this API.

## Steps

### 1. `internal/core/dlrgate` — new package

New files: `registry.go`, `policy.go`, `gate.go`, plus tests.

- `Registry` over a locally-declared narrow Redis interface (the precedent and its
  rationale are at `verdict_redis.go:13-24` — do not widen `rediscompat`, whose `Key`
  type models Jasmin's own keyspace).
  - `Add(ctx, msisdn, ttl, Meta) (Entry, error)` — `SET <prefix><digits> <json> EX ttl`
    + `ZADD <prefix>index <expiresUnix> <digits>`. TTL clamped to `MaxTTL = 15m`,
    zero means `DefaultTTL = 15m`.
  - `List(ctx, limit)` — `ZREMRANGEBYSCORE(index, 0, now)`, then
    `ZRANGEBYSCORE(index, now, +inf) WITHSCORES`, then `MGET` for metadata.
  - `Remove(ctx, msisdn)` — `DEL` + `ZREM`.
  - Keys built with `termination.NormalizeDestination` so they are the same keys the
    existing gate reads.
- `Policy` — per-user `{Enabled, KeyPrefix, HitStatus, HitError, MissStatus, MissError}`
  with defaults `DELIVRD/000` and `REJECTD/008`; `Validate` rejects a status the DLR
  plane would drop (`dlr.ValidMessageStatus`).
- `Gate.Decide(ctx, username, destination) (Verdict, bool)` — resolves the policy,
  returns `(stat, err)`; reuses the existing single-flight window cache behaviour
  (fail-open = the hit status, matching `verdict_redis.go`).

**Verify:** `go test ./internal/core/dlrgate/...` with miniredis (`miniredis.RunT`,
`mr.FastForward` for expiry) covering: add/list/remove, TTL clamp at 15m, expired
entries pruned out of the list, normalization (`+380…` == `380…`), fail-open on a dead
Redis, and that a value written by `Add` is readable by
`termination.newRedisWindowSource` (direct cross-package assertion).

### 2. `internal/state/rediscompat/state.go` — optional gate fields

Add `GateStatus`/`GateError` to `HTTPDLRRequest` and `SMPPSDLRRequest`; emit
`gate_stat`/`gate_err` fields **only when non-empty** so existing records are
byte-identical.

**Verify:** `go test ./internal/state/rediscompat/...` — `TestGoldenRedisState` must
still pass unchanged (its four fixture cases set no gate fields), plus a new case
asserting the fields appear when set.

### 3. `internal/core/dlr/request_store.go` — carry the fields through

Add `GateStatus`/`GateError` to `dlr.HTTPDLRRequest` / `dlr.SMPPSDLRRequest` and pass
them to the record constructors.

**Verify:** `go test ./internal/core/dlr/...`.

### 4. `internal/core/submit_service.go` — decide and stamp

- Add `DLRGate` to `SubmitServiceDependencies` (nil disables; nil is the default so
  every existing caller is unaffected).
- Before the two record writes (`:536`, `:563`), call
  `DLRGate.Decide(ctx, request.Username, request.Destination)` once and put the result
  into both request structs.

**Verify:** `go test ./internal/core/...`; new table test asserting the stamped fields
appear on the stored record for a gated user and are absent for a non-gated one.

### 5. `internal/core/dlr/correlation.go` — apply the override

In `OnDeliverReceipt`, after the CDR block (`:352`) and before the `connectorType`
switch (`:353`), replace `ev.Status`/`ev.Err` from `dlr["gate_stat"]`/`dlr["gate_err"]`
when present. It must be before the switch because `onDeliverSMPPS` decides *whether to
forward at all* from `isSuccessState(ev.Status)` against `rd_receipt` (`:400-405`) — an
ESME that asked for failure-only receipts must see an overridden `REJECTD`.

Add a log line recording real vs reported status (no new Prometheus series — see above).

**Verify:** `go test ./internal/core/dlr/...`; new tests for HTTP and SMPPs targets
covering hit-override, miss-override, no-gate passthrough, CDR keeping the real status,
and the failure-only `rd_receipt` case.

### 6. `internal/app/outbound/config.go` — per-user policy

Follow the `HTTPThroughput`/`SMPPSThroughput` template exactly: the policy rides
`outbound.UserConfig` (so it reaches the bearer `/admin` API for free — that handler
passes the spec through opaquely, `admin/handler.go:341`) and is enforced from a
parallel map on `runtimeDirectory`, **not** from `mtcredential.Credential`, which is
parity-frozen.

- `DLRGateConfig` struct + `UserConfig.DLRGate` field
- `runtimeDirectory.dlrGate map[string]dlrgate.Policy` written in `installUser`,
  `beginReplaceUser` and `userDirectoryReplacement.Commit`
- `ResolveDLRGatePolicy(username)` accessor modelled on `ThroughputQuota`

**Verify:** `go test ./internal/app/outbound/...`; a test that a live user replacement
swaps the policy and that an absent block means disabled.

### 7. `internal/app/gateway/runtime.go` — wiring

Build the `Registry` and `Gate` from the existing `dlr_lookup.redis_url` client
(`dlrstore.go:229`), inject the gate into the submit dependencies and the registry into
both admin surfaces. Nil registry ⇒ endpoints 404, per the house convention
(`adminweb/server.go:97-101`).

**Verify:** `go build ./...`; `go test ./internal/app/gateway/...`.

### 8. Admin APIs

- Bearer `/admin/dlr-registry` (GET list, POST add, DELETE by msisdn) — the
  machine-facing API for adding numbers.
- Session `/api/dlr-registry` (same three) for the console.

**Verify:** `go test ./internal/app/admin/... ./internal/app/adminweb/...` using the
`newWebFixture`/`f.do` harness in `adminweb/handlers_test.go`.

### 9. Web console

- New panel on the **Live operations** page (`web/src/pages/operations.tsx`), matching
  the user's preference. That page is already the live/ephemeral surface and polls on a
  5 s `refetchInterval`; the SPA has no `Tabs` precedent, so a `<section
  className="dashboard-panel operations-panel">` block is the in-house shape.
- Registry table (number, added by, added at, expires in) + add form + per-row delete.
- DLR-gate fields on the user form (`web/src/pages/users/form.tsx`).

**Verify:** `cd web && npm run build` (runs `tsc` strict + `vite build` + the source
hash stamp), commit `internal/app/adminweb/dist/` **and**
`internal/app/adminweb/bundle.sourcehash` — CI job `adminweb-bundle-freshness`
(`.github/workflows/ci.yml:100`) fails on a stale bundle.

### 10. Docs

`docs/operations/configuration.md` (the per-user block), `docs/api/` (the registry
endpoints), and a `docs/reference/deviations.md` note that this is fork-local.

**Verify:** `go build ./... && go test ./...`, then `gofmt -l`.

## Out of scope for this plan

- Blocking the upstream send on a miss (saving termination cost). The knob is
  deliberately absent: dropping a message after billing and CDR admission would need
  the termination plane's transactional treatment, and the user described a DLR-level
  behaviour. Revisit as a follow-up.
- Overriding the level-1 (`submit_sm_resp`) receipt. Level 1 means "the SMSC accepted
  it", which the gate has no opinion about; only the terminal receipt is gated.
- Listing registry entries written by systems other than this API (would need a
  keyspace `SCAN`).

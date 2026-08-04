# The DLR registry gate

**What it does:** for a chosen user, the *terminal* delivery receipt they are told
depends on whether the destination number is in a short-lived registry, instead of
on what the upstream carrier reported.

- destination **in** the registry → the hit receipt (default `DELIVRD` / `000`)
- destination **not** in the registry → the miss receipt (default `REJECTD` / `008`)

It is off for every user until an operator switches it on.

## When you would use it

A partner submits OTP or transactional traffic and you only want to confirm delivery
for numbers a legitimate flow actually asked for. Your application opens a short
window for a number at the moment it requests an OTP; traffic to anything else is
reported back as rejected. That is an artificially-inflated-traffic control: a
sender that fabricates destinations never gets a success receipt for them.

It is also the straightforward way to give a test or demo account deterministic,
scriptable receipts.

## What it does **not** change

This is the important part, and it is enforced in code rather than by convention.

| | |
|---|---|
| **Routing** | Untouched. The message is still sent to the upstream connector exactly as it would be without the gate. |
| **Billing** | Untouched. A miss is still charged, because it was still sent. |
| **The CDR** | Keeps the **real** upstream status. The override is applied after `cdr.RecordFinalDLR`, so the commercial record and the partner-facing receipt disagree on purpose whenever the gate fires. |
| **The level-1 receipt** | Untouched. A `submit_sm_resp` callback means "the SMSC accepted it", which the registry has no opinion about. |
| **Message content** | Untouched. |

Because a hit reports success without anyone having observed a delivery, **every
override is logged** with both values:

```
INFO DLR registry gate overrode a terminal receipt msgid=... \
     upstream_status=UNDELIV upstream_error=011 \
     reported_status=DELIVRD reported_error=000
```

If you bill partners from delivery receipts, understand that the receipt and the CDR
can now differ, and that the CDR is the truthful one.

## Turning it on for a user

**Console:** Access → Users → edit the user → **DLR registry gate**. Switch it on and
pick the hit/miss statuses from the dropdowns. Once enabled, the section shows the
exact URL and JSON body your backend should call.

**Config or admin API** — the block rides the user spec, so `PUT /admin/users/<name>`
carries it with no extra endpoint. `key_id` and `token_sha256` are managed by the
console; set them yourself only if you are provisioning a credential you minted
elsewhere:

```json
{
  "username": "demoesme",
  "dlr_gate": {
    "enabled": true,
    "hit_status": "DELIVRD",
    "hit_error": "000",
    "miss_status": "REJECTD",
    "miss_error": "008"
  }
}
```

Statuses must be ones the DLR plane can publish — `DELIVRD`, `ACCEPTD`, `REJECTD`,
`UNDELIV`, `EXPIRED`, `DELETED`, `UNKNOWN`. An unusable value fails the user write
rather than silently dropping the receipt hours later.

## Managing the registry

Each gated user gets **their own endpoint and their own token**, minted when you
enable the gate. The token is scoped to that endpoint: it cannot submit traffic,
read messages, or administer the gateway, so it is safe to hand to the partner
whose traffic the gate is judging.

The console shows the credential once, immediately after you save. Copy it then —
only a SHA-256 proof is stored, so nothing can reprint it. To replace a lost one,
switch on **Rotate token** and save; the old URL and token stop working at once.

The endpoint is served on the **public front door**, the same listener as `/send`.

```bash
curl -X POST https://<gateway-host>/dlr-registry/<key-id> \
  -H 'Authorization: Bearer <this-user-token>' \
  -H 'Content-Type: application/json' \
  -d '{"msisdn": "380930242105", "ttl_seconds": 900, "note": "otp login"}'
```

| Field | Required | Meaning |
|---|---|---|
| `msisdn` | yes | Digits, optionally with a leading `+`. `+380930242105` and `380930242105` are the same entry. |
| `ttl_seconds` | no | How long the window stays open. **Capped at 900 (15 minutes)**; a larger value is clamped, not rejected. Omitted means the full 15 minutes. |
| `note` | no | Free text, shown beside the entry in the console. |

Other verbs on the same URL:

```
GET    /dlr-registry/<key-id>                  list this user's open windows
DELETE /dlr-registry/<key-id>/380930242105     close one early
```

The key id in the path is not a secret — it identifies whose endpoint is being
called, which is what makes an access log useful — but it is unguessable, so a
partner's endpoint cannot be found by enumeration and the URL does not disclose
the username. Authorization is the bearer token alone, compared in constant time.
A bad id, a bad token and a disabled gate all return the same `401`, so a caller
cannot probe which key ids exist.

### Isolation

A window opened with a user's token counts **only for that user**. Another gated
partner submitting to the same number gets the miss receipt, and cannot list or
close a window that is not theirs — `DELETE` on someone else's window is a `404`,
the same answer as one that does not exist.

### Operator-opened windows

The admin API still exists for the operator's own use, on the bearer admin token:

```
GET    /admin/dlr-registry                  list every live window
POST   /admin/dlr-registry                  open one
GET    /admin/dlr-registry/380930242105     one entry
DELETE /admin/dlr-registry/380930242105     close one, whoever opened it
```

A window opened here has **no owner** and therefore counts for *every* gated user
— which is what you want for a test or a manual override, and is also why entries
written directly into Redis by another system (the legacy gateway's own activation
windows) keep working unchanged. Pass `"owner": "demoesme"` to scope one to a
single user instead.

The console mirrors the admin verbs on `/api/dlr-registry` under session auth, and
lists live entries on the **Live operations** page with a countdown to expiry.

## How it works

The verdict is decided **when the message is submitted**, not when the receipt
arrives, and is stamped onto the `dlr:<msgid>` record the submit path already writes.
The correlator applies it when the terminal receipt comes back.

```
submit ──► registry lookup ──► stamp gate_stat/gate_err on dlr:<msgid>
                                        │
upstream deliver_sm receipt ──► correlator ──► CDR gets the REAL status
                                        └────► the partner gets the STAMPED status
```

Deciding at submit is deliberate: the window is minutes long and a receipt can arrive
after it closes. Deciding late would report a miss for a message that was submitted
while the window was open, and two messages sent seconds apart could disagree.

It also means several messages to one registered number inside one window all get the
same receipt, which is the normal case for OTP retries.

### It shares a keyspace with the termination plane

The registry is the **same activation window** the termination connectors' `redis-window`
verdict source already reads: Redis keys `dlr:block:<digits>`, presence meaning
delivered and absence meaning rejected. Entries you add here are honoured by both.

Two consequences worth knowing:

- The key prefix says `block`, which is legacy naming from the Python fake SMSC. It is
  a *time window*, not a blocklist. **Presence means confirm, absence means reject.**
- Values must stay plain strings. The termination gate reads them with `GET` and treats
  a `WRONGTYPE` error as a fail-open success, so storing a hash under one of these keys
  would silently confirm everything.

### Failure behaviour

An unreadable Redis **fails open to the hit receipt**, matching the termination gate.
The reasoning is that an infrastructure blip must not turn into a wave of rejections
for traffic that was fine. Those decisions are logged with the reason
`registry unreadable, failed open`.

A destination that is not a number (an alphanumeric or empty address) is a miss, with
the reason `invalid destination` — the gate never builds a lookup key from garbage.

## Limitations

- **A miss is still sent and still billed.** The gate is receipt-level only. Not
  sending unregistered traffic at all would save termination cost, but dropping a
  message after billing and CDR admission needs transactional treatment that is not
  built.
- **One Redis namespace, isolated by ownership rather than by keyspace.** Every
  window lives under `dlr:block:<digits>`, so two users cannot hold a window on
  the *same* number at the same time — the second write replaces the first, and
  the number then belongs to whoever wrote last. Isolation is enforced on the
  read side (a window counts only for its owner), not by separate keyspaces.
- **The console lists only entries added through this API.** Keys written directly
  into Redis by another system still gate traffic correctly but do not appear in the
  list, which is indexed rather than scanned.
- **No Prometheus series.** The legacy `/metrics` text format is parity-frozen, so
  overrides are observable through the structured log only.

## Related

- `docs/operations/configuration.md` — the `dlr_gate` user block, field by field.
- `docs/reference/deviations.md` D-007 — why this fork has a behaviour upstream Jasmin
  does not, and the boundaries it is held to.
- `docs/plans/026-dlr-registry-gate.md` — the design and the options rejected.
- `docs/operations/termination-safety.md` — the activation window as the termination
  plane uses it.

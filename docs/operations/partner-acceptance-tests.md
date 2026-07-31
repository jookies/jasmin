# Partner acceptance tests: prove it before real traffic

- **Date:** 2026-07-30
- **Status:** active
- **Summary:** The scenarios to run on a new deployment before a partner's real traffic reaches it, each with what to do, what must happen, and what it would mean if it does not.
- **Related:** [new-deployment.md](new-deployment.md), [termination-safety.md](termination-safety.md), [plan 021](../plans/021-mt-termination-connector.md), [monitoring.md](monitoring.md)

## How to run these

The partner side is `cmd/synevyr-partner-sim` — it binds as an ESME, submits, and
prints every receipt with the latency it arrived at:

```console
scripts/dev.sh partner --count 3 --destination 380671234567 --text "code 63125"
# or, against a real deployment:
go run ./cmd/synevyr-partner-sim --addr <gateway>:2775 \
    --system-id partner-a --password <password> \
    --count 3 --destination 380671234567
```

It refuses a public address without `--allow-remote`, because it submits real,
billable traffic.

The downstream side is whatever receives the delivery POST. For these tests, point
the connector at a throwaway endpoint you control and can stop at will — several
scenarios depend on being able to break it.

Run them in order: each one assumes the previous passed.

## A. The message path works at all

| # | Do this | Must happen | If it does not |
|---|---|---|---|
| A1 | Set the activation key for the destination, submit one message | `submit_sm_resp` OK, then `DELIVRD` 5–7 s later. One row in the spool. One POST to your endpoint carrying the decoded text. | Nothing else below is meaningful. Check the connector is `CONSUMING` and the route points at it. |
| A2 | Clear the activation key, submit again | `REJECTD` with `err:008`, `dlvrd:000`. Still a spool row and still a POST — a rejected message is delivered to you; the receipt is about the rental window, not about us. | If it says DELIVRD, the gate is not being consulted — check `verdict.source` is `redis-window`, not `static`. |
| A3 | Submit two messages to the same number inside one window | Both DELIVRD, and **exactly one** Redis lookup between them. | Two lookups means the verdict cache is off; two *different* answers means it is broken, which is worse — the same number in the same window must get the same answer. |

## B. The content is right

| # | Do this | Must happen | If it does not |
|---|---|---|---|
| B1 | Submit Cyrillic text with an OTP code, in whatever encoding your partner really uses | The POST carries the correct text **and the correct digits**. | Digits turned into letters means `PreserveOTPDigits` is off — see [D-004](../reference/deviations.md). This is the single most damaging failure on this list. |
| B2 | Submit a long message that splits into 3 segments | **One** POST with the whole joined text. **Three** receipts, one per segment. | One receipt means the per-segment fix regressed. Three POSTs means reassembly is off — check `redis_url` is configured. |
| B3 | Submit a message in each encoding the partner sends (UCS-2, GSM, Latin) | Text matches what they sent, byte for byte. Compare against what the legacy stack produced for the same input. | Any difference other than the OTP-digit fix is a bug, not a deviation. |
| B4 | Submit an emoji or any character outside the BMP, split across segments | The character survives. | A broken character at a segment boundary means something decoded before joining. |

## C. Things break (the important half)

| # | Do this | Must happen | If it does not |
|---|---|---|---|
| C1 | Stop your endpoint, submit | Receipt still `DELIVRD` — the window was open. Delivery retries, then the message lands in the dead-letter queue. **Nothing is lost.** | A message that vanishes here is the failure this whole design exists to prevent. |
| C2 | Restart your endpoint, replay from the console | The message arrives, exactly once. | |
| C3 | Stop Redis, submit | Everything answered `DELIVRD`, the gate-bypass counter increments, and every affected row is flagged. | Silence here is the dangerous case: a bypass that is not counted is a period where you accepted traffic blind and cannot say which. |
| C4 | Stop the gateway 2 s after a submit, restart it | The receipt still arrives after recovery. | A receipt owed by a committed row must survive the process. If it does not, partners silently wait forever. |
| C5 | Kill the gateway between the POST and its acknowledgement | Your endpoint receives the message twice, with the **same** `X-Synevyr-Message-Id`. | This is expected and unavoidable. What matters is that your app dedupes on that header — this test is really a test of your app. |
| C6 | Submit to a malformed destination (`undefined`, letters, empty) | Rejected explicitly, with a reason, and **no Redis lookup at all**. | A lookup on garbage is the `dlr:block:undefined` bug that made those OTPs always fail in the old stack. |

## D. The partner's own behaviour

| # | Do this | Must happen | If it does not |
|---|---|---|---|
| D1 | Have the partner bind with their real client, not the simulator | Bind succeeds; their client stays bound through `enquire_link`. | Bind failures are almost always credentials or IP allowlist, not protocol. |
| D2 | Ask them to send the exact message shapes they send in production | Everything in section B holds for their real traffic. | |
| D3 | Confirm whether they set `registered_delivery` on every segment or only the last | Their expectation matches what they get: one receipt per segment that requests one. | If they expect one receipt per message, tell us — that is not what SMPP says, but it is what their stack may be built on. |
| D4 | Have them disconnect mid-traffic and reconnect | Messages submitted before the drop are unaffected; receipts arrive after they return. | |
| D5 | Let them run for a day at real volume | Spool depth, dead-letter depth and gate bypasses all where [termination-safety.md](termination-safety.md) says they should be. | |

## E. The commercial and operational surface

| # | Do this | Must happen | If it does not |
|---|---|---|---|
| E1 | Check the CDR for every message above | One record per message, with the right partner, rate and currency. | Attribution is the reason for absorbing the old single-tenant services; an empty partner field means it is not working. |
| E2 | Compare the balance before and after a batch | The charge matches the rate times the parts. Note that a `REJECTD` message **is still charged** — inherited behaviour, and a commercial conversation, not a bug. | |
| E3 | Fetch the same messages through the pull API with a scoped token | Only that connector's messages, no fragments, cursor advances, every read audited. | A fragment or another connector's message appearing here is a scope leak — stop and report it. |
| E4 | Reveal a message body in the console | It works, and it writes an audit row naming who did it. | These are one-time passcodes; an unaudited reveal is the problem. |
| E5 | Restart the gateway with traffic in flight | Nothing lost, nothing duplicated beyond C5's known case. | |

## What "passed" means

Every row in A, B and C behaving as described, on the real deployment, with the
partner's real client for D. Sections A–C can be run by one person in an hour with
the simulator; D needs the partner; E needs a day of real traffic to be meaningful.

Record the results somewhere durable — a run of these tests is the evidence that a
later change did not break something, and the next person to touch this will want
to know what "working" looked like on day one.

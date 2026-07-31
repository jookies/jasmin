# Round 2 — decisions before I write the plan

Same format: answer under each question. "Agree" is enough where you agree.
Round 1 answers are in `que.md`.

---

## What I now understand (correct me in Q0 if wrong)

- Partners bind to your Jasmin and submit; you **terminate** the message.
- The content goes to `smsget-api-gateway`, which is your own system.
- `dlr-smpp-python` = fake SMSC that decides `DELIVRD`/`REJECTD` from a Redis key.
- `smsget-jasmin-sms-queues` = the only thing that decodes, stitches and delivers content.
- **1 partner today, 3 planned, same logic.** This is the strongest argument for absorbing
  both: today's setup is single-tenant by construction (one fake SMSC, one broker tap, one
  Redis keyspace). Native connectors give each partner its own credentials, filters,
  throughput quota, CDR, audit line, delivery endpoint and gate policy.

**Q0. Anything wrong above?**

**Answer:**
- DLR logic woks in a way that we are checking if number exist in Redis, if yes, that's mean we have activation window
and during this activation window session, all messages we are reciving on that number should be marked as delivered. 
smsget-jasmin-sms-queues - you described correctly. Regarding multi tenants, yep, we need support in in best possible way


---

## 1. How your app is consulted (you asked for variations)

- **A. One call.** Gateway POSTs the decoded message to `smsget-api-gateway`; the response
  body carries `accept`/`reject`; the gateway synthesizes the DLR from it. Delivery and
  verdict are the same event, so they can never disagree. Cost: your app sits on the hot
  path with a hard timeout.
- **B. Split.** A cheap gate (Redis key as today, or a small cached HTTP call) decides the
  DLR; a separate webhook with retries delivers the content. Closest to today, hot path
  stays fast. Cost: keeps today's failure mode — gate says `DELIVRD`, content never arrives.
- **C. Gate only.** Port just the Redis gate; your app keeps reading Postgres and the Python
  tap stays. Smallest change, but you keep a pickle tap and a second deployment.

**My recommendation: A**, with a ~2 s timeout, **fail-closed** by default (fail-open
configurable per connector). Your Q6 answer is what makes A coherent: if the app is down we
genuinely cannot deliver, so `REJECTD` is the truthful receipt — B would lie.

**Q1. A, B or C? And is fail-closed right?**

**Answer:**
What if we will have 1000+messages a second? Just assking, for now it's max 10 a per second, but nobody know what could be later. we need options.


---

## 2. How the partner learns a rejection (what Q7 meant)

SMPP tells the partner twice: `submit_sm_resp` immediately (accepted, or an error status),
then the receipt later. Today you always answer OK and reverse it 5–7 s later with
`REJECTD`.

The alternative is refusing at submit time with a real SMPP error — instant, one PDU instead
of two, unambiguous. Real improvement, but the risk is partner-side: many ESMEs treat a
submit error as retryable and re-send in a loop, or count it differently in their SLA.

**My recommendation:** keep today's behaviour as the default; add synchronous rejection as a
**per-connector flag** you can enable for a partner after agreeing it with them.

**Q2. Agree?**
let's jave what we have maybe, only if you don't wanna improve it.


**Answer:**
 i gave answer in wrong place)

---

## 3. Receipt delay (what Q8 meant)

Today the receipt is deliberately held 5–7 s so it looks like a real carrier delivering to a
handset. An instant receipt is visibly synthetic and some partners' monitoring flags that.

**My recommendation:** keep delay + jitter, configurable per connector, default 5–7 s.

**Q3. Agree? Any partner you'd want at 0?**

**Answer:**
no, let's have with proper delays to not trigger any false security alerts.

---

## 4. Message content — spool, not archive (the Q9 sync)

Under option A the content goes straight to `smsget-api-gateway`, which already stores it —
so the gateway does not need an archive. But it does need a **spool**: when the app is down
and we answer `REJECTD`, today's Python DB is the only reason that message isn't gone
forever.

**My recommendation:** decoded text + raw bytes kept until accepted, plus a short window for
replay and partner disputes, reusing the existing CDR retention/prune and access-audit
machinery, not shown in the console by default.

Security note: OTP bodies are the highest-value content you handle — a second copy is a
second breach surface, which is why I'd bound this in hours, not months. If you later want a
real archive it becomes a retention setting, not a redesign.

**Q4. Spool instead of archive — agree? And what retention: 24 h, 72 h, longer?**

**Answer:**
I think best 24 hrs it's more then enoght. And mayber we can do in this case next type of connection, synchronisation?
- post as you proposed
- second will some kind of our system url, example: POST /message-steam?time=[seconds], and it should accept Barear token to allow us get messages. as response we will give an object list with message history from spoof. Will work by concept current time - [seconds]. It will be windw we need to get data,
and because we a saving message only 24 hr, it will be fine, correct?
- one more opion, we can use redis or rabbit mq to exchange bigger message chunks as we are doing now for example.
What do you thinkg?


**Q5. Should the web console be able to show message text at all (masked / full / never)?**

**Answer:**
yep why not, especially because we will have for 24 hrs.
Give idea what and how we can show theem.

---

## 5. What to port from script 2

- **Decoder** — ~200 lines, your Python tests become the oracle vectors. Gotcha: 8-bit codecs
  must run *before* charset detection or Cyrillic restoration breaks.
- **Plain-split stitch** — keep it, but **per-connector opt-in with a configurable window**.
  It's a time-window heuristic, so with 3 partners on shared numbers a global setting can
  join two unrelated messages.

**Q6. Agree on both, especially the stitch being per-connector rather than always on?**

**Answer:**
yep, both should be, maybe you wanna add somethign else?


---

## 6. Adjacent gaps (what Q14 meant) — three separate things

1. **DLQ.** Today when delivery fails after 3 retries the message is **purged**, no trace.
   The fail-closed design above needs that parking lot to exist.
2. **Metrics.** `synevyr_queue_depth` and `synevyr_dlr_total` have no callers, yet two
   runbooks tell operators to grep them — the documented recovery check silently reports
   nothing.
3. **Emulator bug.** Both Go fake SMSCs hardcode `dlvrd:001 err:000` even for `UNDELIV`,
   producing self-contradictory receipts.

To your actual point — yes, this **is** how your DLR side improves: DLR generation becomes
native to the gateway and `dlr-smpp-python` disappears entirely.

**Q7. All three in scope, or only the DLQ?**

**Answer:**
We can do 3 of them, but what Emulator bug is did not get, explain before final decision. ANd how we will ablte infect custome logic 
according to wich we will send different statuses
How can implement this?

---

## 7. `dlr:block:undefined`

Under option A the verdict call carries the real MSISDN, so the `undefined` class stops
happening at the source. The gateway should reject a malformed destination loudly instead of
silently gating nothing. The fix inside `smsget-api-gateway` then becomes cleanup, not urgent.

**Q8. Agree, or do you want it fixed in `smsget-api-gateway` as part of this work?**

**Answer:**
If there is nothing, we should reject it that's it.

---

## 8. Scope of the plan

**Q9. Should the plan cover retiring both Python services (dual-run, cutover, decommission),
or only building the gateway side and leaving retirement for later?**

**Answer:**
Yes, if we can inject and build this features inside our Synvir project, would love decomisssion old code.


**Q10. Anything else you want in the plan that I haven't raised?**

**Answer:**
I think you will rise this question during analysis of my answers.

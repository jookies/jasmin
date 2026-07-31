# Round 3 — open questions from plan 021

Plan: `docs/plans/021-mt-termination-connector.md` (status: draft).
Answer under each question; "you decide" is fine.

---

**1.** Connector type name. Draft uses `term`. It lands in stored config, jCli output and the
admin API, so changing it later is a migration. Alternatives: `local`, `app`, `terminate`,
or something you'd rather see in the console.

**Answer:**
can you explain diferences or declare them 


**2.** Should the pull API live on the existing admin listener, or its own port with its own
token scope? Own listener is cleaner for a partner-facing consumer but is more moving parts.

**Answer:**
what is better? I dont think to be honest that it should be with own port as separated app.


**3.** After cutover, does `smsget-api-gateway` keep its own message table, or does it treat
the gateway spool as the system of record? The plan assumes it keeps its table and the spool
is a 24 h safety net — the opposite answer changes the retention decision.

**Answer:**
It does not metter, they wanna get messages, and thets there own proplem and erotic desire what they are doing with it.


**4.** Which partner do we cut over first? The plan recommends the smallest one alone, then
revising the checklist before the other two.

**Answer:**
currently we have one active parner, another will be next week


**5.** Is the delivery payload shape mine to design, or does `smsget-api-gateway` already have
an endpoint contract I should match? If it exists, point me at it.

**Answer:**
You can decide what you wanna do, maybe there is some protocol standarts or best practices.


**6.** Load target. The plan designs for 1000 msg/s against today's ~10 msg/s. Is 1000 the
right number to prove before the third partner, or lower/higher?

**Answer:**
It was example from my head.

**7.** Step order. The plan runs decoder (3) → stitch (4) → connector (5) → verdict (6) →
receipt (7) → spool (8) → delivery (9). Do you want the whole thing built before any cutover,
or a thin vertical slice first — one partner, one connector, no console — to prove the shape?

**Answer:**
Give advice what is best, I did lost a little,)

**8.** Anything in the plan you want changed before I start? (Read `docs/plans/021-mt-termination-connector.md`;
the Decisions table near the top is the fastest review.)

**Answer:**
Just to clarify dlr-smpp legacy app, does not get any statuses from 3d party apps, by  default if renting windows is active for number, we are sending recived by defalt.

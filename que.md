# Questions — `dlr-smpp-python` + `smsget-jasmin-sms-queues`

Answer under each question. Free text, short is fine, "you decide" is a valid answer.

---

**1.** Do partners bind to you and submit (you terminate the message), or does the OTP
arrive from a real carrier as inbound MO?

**Answer:**
We a currently working by schema, we created credentials for partners, as i understood parners binds to us.
We are reciving messages from upstream (PARTNER) and processing them and sending to another our system.
PARTNER -> OUR JASCMIN |
                       | -> messages gous tou our dlr generation script
                       | -> another script extracts messages from rebit and send to our main system db


**2.** Is the 3rd-party app your own `smsget-api-gateway`, or a genuinely external system?

**Answer:**
yep for now it's smsget-api-gateway, we are sending messages content to this system 


**3.** Is anything else still using these two scripts (other readers of that Postgres
table, other deployments binding to the fake SMSC)?

**Answer:**
this scripts were creted on 2 purpose:
1) send DLRs status according to specific scenario (besides send dlrs)
2) Extract message content, without any issues with encoding, (message in general) and transfer this data to 3d party system

**4.** Is the accept/reject decision still just the Redis key lookup, or has it grown?

**Answer:**
What you can see it's our production version


**5.** Should the gateway make one call to your app (message + verdict in the response),
or keep a fast gate separate from the delivery webhook?

**Answer:**
Give me variations.


**6.** If your app is down or times out — accept or reject?

**Answer:**
If app down, we need does not metter we should take latest messages.Maybe if it's down, its down we dont care, can not deliver nothing... Happenes


**7.** Should the partner learn a rejection from the DLR (as today) or from the
`submit_sm_resp` at submit time?

**Answer:**
Did not get, what was working before it was ok for them. If thre is any improbments tell me.


**8.** Keep the 5–7 s delay + jitter on the receipt, or send it as soon as the verdict is
known?

**Answer:**
Did not catch you!


**9.** Should the gateway store message content (it deliberately stores none today)?

**Answer:**
Good question, i don't know let's sync about it.. 


**10.** If yes — for how long, and should the web console show message text?

**Answer:**
I assume we can get it after #9


**11.** Port the encoding/decoder logic to Go? (Cyrillic restoration, brand tokens, Korean
UCS-2 heuristic)

**Answer:**
Ofcource if we can let's do it! Or I am wrong?


**12.** Port the temporal stitch for plain-split SMS, or was it a workaround for one bad
partner?

**Answer:**
Yep it was one of functionality that necessery! for our 3d part application, maybe in a future some apps wont needed, tell em what do you think.


**13.** Do you want a written plan now, discussion only, or the smallest useful piece built
immediately?

**Answer:**
We should write a plan

**14.** Include the adjacent gaps — DLQ for purged messages, the inert queue/DLR metrics
two runbooks tell operators to grep, the fake-SMSC receipt bugs?

**Answer:**
My point if we need it. Maybe we can somehow improve dlr that we have in our syste, Or i did not catch you?

**15.** `dlr:block:undefined` (bug in `smsget-api-gateway`, those numbers never get gated) —
fix here, track separately, or already handled?

**Answer:**
I think depends froum previous question, answers and direction we cwill go.

**16.** Anything I got wrong about how these scripts work?

**Answer:**
Just summanise what we were trying to achive!
- we were using jasmin to start reciving message from our partner
- fake-SMSC to send them dlrs, according to specific conditions
- smsget-jasmin-sms-queues - we were using to fix encoding of messages, combine splited messages in a single, and deliver final message with metta information to our 3d party system for next business steps
- FYI: beofore we had 1 parner(provider), now we are plaing to have 3 different one, but logic will be the same.



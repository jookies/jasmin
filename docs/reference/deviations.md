# Where this gateway deliberately differs from the reference

Preserved from `spec/compatibility/DEVIATIONS.md` before the Python reference
implementation was removed. The reference commit was
`0aac58e466d583d0f0436df7b8afa3dc96191263`; `jasmin/...` paths are provenance
only.

These were originally framed as deviations needing sign-off against a parity
gate. That gate is gone: `docs/plans/017-smpp-production-readiness.md` made SMPP
3.4 conformance and independent interop the standard, and the reference is no
longer authoritative. So these are now simply **design decisions with their
rationale recorded** — which is what they should have been.

Later divergences not in this file, decided while hardening for production:

- **Reconnect backoff.** The reference retries at a fixed interval forever, which
  hammers a recovering SMSC. We back off exponentially with jitter to a cap.
- **Bounded delivery window.** The reference has no server-side outstanding-request
  limit, so an ESME that never acknowledges is invisible. We bound it and time out.
- **MO interception on segments and the whole.** The reference intercepts each
  arriving PDU and never the reassembled whole. We do both: intercepting only the
  whole let a reject be bypassed after segments had already shipped.
- **Liberal inbound optional parameters.** The reference rejects some malformed
  optional TLVs. On a carrier-originated PDU we skip the parameter and keep the
  message — a failed decode there becomes a reconnect loop and an MO outage.
- **Separate admin listener.** `/admin/` shared the public send port; it now has
  its own.

## D-001 — Filters are inline, not named reusable objects

- **Matrix rows:** J-007 (`filter`), and the filter portions of J-008/J-009/J-010/J-011.
- **Legacy:** jCli creates filters as first-class objects with an fid (`filter -a`);
  routes and interceptors reference them by fid.
- **New:** filters are embedded in the route or interceptor that uses them
  (`outbound.RouteConfig.Filters`, `modispatch.RouteConfig.Filters`,
  `outbound.InterceptorConfig.Filters`). There is no filter registry.
- **Reason:** see [ADR-003](../../docs/adr/003-filter-model.md). Inline avoids a
  second identity space and dangling-reference handling; nothing in the current
  deployment reuses a filter across routes.
- **Impact:** editing a repeated filter means editing each copy. No security
  impact — the same filter engine and matching semantics apply either way.
- **Migration:** a jCli-era configuration expands each referenced filter into the
  referencing route. Rollback is to build the registry (the stored specs remain
  valid, since inline filters are a subset).
- **Proving test:** the routing and interception filter tests
  (`internal/app/outbound/filters_test.go`, `internal/core/routingfilter`) cover
  matching semantics; the deviation is representational, not behavioural.
- **Owner approval:** _pending_.

## D-002 — WITHDRAWN (2026-07-28): `persist` / `load` are implemented

Originally recorded as "obsolete by design", on the reasoning that the Go admin
plane persists every mutation as it applies it, so there is nothing to flush.
That is true of `persist` without a profile — and it misses what the commands
are actually for. `persist -p known-good` / `load -p known-good` is how an
operator keeps a configuration to roll back to, and no amount of
apply-then-persist provides that.

`admin.ProfileService` now implements named snapshots: `persist -p NAME` writes
every admin table under that name, `load -p NAME` restores them in one
transaction and re-applies every service. J-015, J-016 and J-017 are `MATCH`.

The original text is kept below for the record.

### Original record

## D-002 (original) — `persist` / `load` have no semantic role

- **Matrix rows:** J-015 (`persist`), J-016 (`load`), J-017 (autoload).
- **Legacy:** jCli mutations live in memory until `persist` writes a profile to
  disk; `load` reads one back, and `jcli-prod` autoloads at startup.
- **New:** every admin mutation applies live **and** is written to SQLite in the
  same operation (apply-first-then-persist), and is re-applied at boot by
  `LoadAndApply`. There is no unsaved state to persist and no profile to load.
- **Reason:** the store is the source of truth; a separate save step can only
  introduce a window where live state and persisted state disagree.
- **Impact:** operationally safer (no lost changes on restart). A script ending
  in `persist` must keep working — see plan 013 Step 7, which proposes accepting
  `persist` as a no-op returning the oracle's success text, while `load` with an
  explicit profile returns a clear error rather than silently doing nothing.
- **Migration:** drop `persist` from provisioning scripts (harmless if left).
  Rollback is not applicable — this follows from the storage design.
- **Proving test:** restart-survival is covered by each service's `LoadAndApply`
  path and verified live (admin connectors, MT/MO routes, interceptors, users).
- **Owner approval:** _pending_.

## D-003 — `smppccm -s` prints the bind password, matching the oracle

**Surface:** JCLI (J-012) · **Status:** accepted, pending owner sign-off

The frozen console prints a connector's bind password in clear in `smppccm -s`
(see `fixtures/jcli/J-012-smppccm.jsonl`). An earlier Go implementation redacted
it. Byte-parity and redaction are mutually exclusive, and the Go console now
matches the oracle.

**Why match rather than redact:** the console is reachable only after
authentication and is already a full privilege boundary — it mints user
credentials and starts connectors — so redacting one field buys little, while
any script that reads `password` from a `smppccm -s` transcript breaks silently
if the field disappears.

**Risk accepted:** the password appears in any terminal scrollback, session
recording or log that captures console output.

**Rollback invariant:** redacting the field again is a one-line change in
`connectorFieldValue` (`internal/app/jcli/managers_smppccm.go`), and it makes
J-012 fail — which is the point: the deviation cannot be taken silently.

**Revisit when:** the console grows a per-field redaction mode, or an operator
requires secret-free transcripts for compliance.

## D-004 — OTP digits are preserved when restoring 7-bit-stripped Cyrillic

- **Affected rows:** none in the compatibility matrix. This is a divergence from
  `smsget-jasmin-sms-queues/app/decoder.py`, the Python message-content service
  the termination connector replaces (see
  [plan 021](../plans/021-mt-termination-connector.md)), not from Jasmin.
- **Legacy:** the whole-message restoration in `_try_decode_7bit_stripped` ORs
  `0x80` onto every byte in `0x21-0x7F`. Digits are in that range, so an OTP code
  is rewritten as Cyrillic letters. Evidence, produced by running the real
  decoder rather than by reading it:

  ```
  >>> decode_with_auto_detection('@>25@>G=K9 :>4 63125'.encode('latin-1'))
  ('РОВЕРОЧНЫЙ КОД ЖГБВЕ', 'iso-8859-5-7bit')
  ```

  `63125` became `ЖГБВЕ`. The token-wise restoration beside it
  (`_try_decode_7bit_stripped_mixed`) already keeps digit runs intact, but it is
  only reachable through `_is_suspicious_mixed_ascii`, which requires two
  characters in `0x70-0x7E` — an all-uppercase Cyrillic message has none. So the
  correct code exists and never runs. `decoder.py:120-135`'s own comment states
  the intended output keeps the digits, and no Python test asserts either value:
  this was a defect, not a decision.
- **New:** `msgcontent.Options.PreserveOTPDigits` routes a stripped body that
  carries an OTP-shaped digit run through the token-wise restoration, which
  recovers the Cyrillic words and leaves the code alone. The same body now decodes
  to `РОВЕРОЧНЫЙ КОД 63125`. (The leading `П` is not recoverable by either
  implementation — nothing in the input encodes it.)
- **Reason:** the platform exists to deliver one-time codes. Reproducing a defect
  that corrupts the code, in the name of parity with a service being retired,
  would be faithfulness to the wrong thing.
- **Scope of the change (revised 2026-07-30 after review):** a digit token is
  protected only when it is at least 80% digits and either
  - has a run of **five** or more consecutive digits, or
  - has a run of four and the message contains a code anchor — `КОД`, `ПАРОЛЬ`,
    `ПИН`, `CODE`, `PIN`, `OTP` — matched against the *restored* text, so `:>4`
    has already become `КОД` by the time it is looked for.

  A body with nothing protected takes exactly the path it took before the option
  existed; that is decided before a decode path is chosen, not after, so the
  output of messages the fix is not for cannot change.
- **Why the bar moved.** The first version protected any four-digit run, which is
  wrong, and an adversarial review reproduced it against the oracle: `ДЕДА`
  strips to `4540` and `БЕДА` to `1540`, so ordinary words became numbers and the
  fix was *worse than the defect* on messages carrying no code at all —
  "ПРИВЕЗЛИ 4540 ДОМОЙ". No Russian word of five letters is spelled only from
  `А-Й`, which is what makes five safe unconditionally; four needs the message to
  say it is a code. Both counterexamples are now regression tests
  (`TestPreserveOTPDigitsDoesNotEatCyrillicWords`).
- **Second defect found in the same review:** the token-wise decoder accepts its
  result only when at least half the non-whitespace characters came out Cyrillic,
  and the preserved digits were counted against that ratio — so
  `ПРОВЕРОЧНЫЙ КОД 1111222233334444` was rejected at 46.7%, fell through to the
  whole-message path, and had its code destroyed anyway, silently, with the option
  on. Protected tokens are now excluded from the denominator: the ratio asks
  whether restoration produced Cyrillic, and a token that was deliberately not
  restored is not evidence either way. Regression test
  `TestPreserveOTPDigitsProtectsLongCodes`.
- **Residual ambiguity, accepted:** ISO-8859-5 maps `А-Й` onto `'0'-'9'`, so a
  genuine Cyrillic word can still arrive looking like digits. A five-letter word
  drawn only from those ten letters, or a four-letter one in a message that also
  contains an anchor word, would be left as digits. That residue is much smaller
  than the defect being fixed, but it is not zero, and it cannot be made zero:
  the two readings are the same bytes.
- **Impact:** no security impact. Operationally, messages that previously reached
  `smsget-api-gateway` with a mangled code will now carry the real one, so any
  downstream logic that pattern-matched the mangled form must be checked. The
  Python service is unchanged and still mangles: while both run, the same message
  decodes differently depending on which path carried it.
- **Migration:** production wiring uses `termination.DefaultDecodeOptions()`, which
  turns the option on. `msgcontent.Options{}` remains exact legacy parity and is
  what the Step 14 byte-differential harness must use, so this shows up there as
  the single intended difference.
- **Rollback:** clear `PreserveOTPDigits` in `termination.DefaultDecodeOptions`.
  One line, no schema or wire implications.
- **Tests proving the boundary:** `TestPreserveOTPDigitsRecoversTheCode` asserts
  both behaviours — the legacy answer and the fixed one — so neither can drift
  silently, joined by the three regression tests named above. The 77 vectors in
  `testdata/vectors.json` continue to run with `Options{}` and still match the
  Python oracle byte for byte; `scripts/differential/decode_oracle.py` re-checks
  that against the live decoder and is the gate before touching this code.
- **Owner approval:** requested 2026-07-30, pending.

## Required record format

Every future deviation must include:

- ID and affected compatibility matrix rows;
- legacy behavior and evidence;
- new behavior and reason;
- security/operational impact;
- migration and rollback instructions;
- owner approval date;
- differential fixture/test proving the boundary.

Security hardening is delivered through an explicit `secure` profile until the owner approves changing compatibility defaults.

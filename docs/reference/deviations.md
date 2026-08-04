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

## D-005 — jCli grows a `msgconsumer` command the legacy console does not have

- **Affected matrix rows:** J-002 (`help`, tab completion). No other jCli row
  changes, and no wire-protocol row changes at all.
- **Legacy behavior and evidence:** legacy Jasmin 0.11.1 registers exactly twelve
  commands, and bare `help` plus tab completion print that list verbatim. The
  recordings are `spec/compatibility/fixtures/jcli/J-002-help.jsonl` and
  `J-002-completion.jsonl`, captured from the Python console by
  `scripts/compat/capture_jcli_transcript.py`.
- **New behavior and reason:** a thirteenth command, `msgconsumer`, manages the
  read tokens a downstream application uses on the message pull API. It is
  listed in `help` and offered by completion like any other command, so bare
  `help` output now differs from legacy by one line. The reason is that legacy
  Jasmin has no message spool at all, so there is nothing here to be compatible
  *with* — and a hidden verb, the alternative that would have preserved the
  fixtures byte-for-byte, is a credential-management surface an operator can
  only find by being told it exists.
- **Security/operational impact:** the command creates and revokes credentials,
  so it is confined to the jCli listener, which is already a privilege boundary
  bound to loopback. The token secret is printed once, on creation; nothing can
  reprint it, because the service stores a SHA-256 proof rather than the secret.
  `msgconsumer -l` and `-s` therefore never render a token. Operational impact is
  limited to any tooling that parses bare `help` output — none is known in this
  repository.
- **Migration and rollback:** rollback is removing `"msgconsumer"` from
  `commandOrder`, `commandDocs` and `commandTable` in
  `internal/app/jcli/dispatch.go` and restoring the two fixtures from git
  history. No schema, no wire format, no stored state is involved: the tokens
  themselves are managed identically by the web console and the admin API, both
  of which predate this command.
- **Differential fixture/test proving the boundary:** the two J-002 fixtures were
  re-recorded and their provenance header now reads `FORK BASELINE (not an
  oracle recording)` with a `fork_note` naming this deviation, so neither can be
  mistaken for legacy evidence again. Every other jCli fixture remains an
  untouched byte-for-byte legacy recording, and `TestOracleTranscripts` still
  gates all of them.
- **Owner approval:** approved 2026-07-30 (chosen over the hidden-command and
  no-command alternatives when both were put to the owner explicitly).

## D-006 — a Latin body is never run through Cyrillic restoration

- **Affected matrix rows:** the `iso-8859-5-7bit-mixed` decode path. No wire
  format, no encode path, no other decoder.
- **Legacy behavior and evidence:** the mixed 7-bit-stripped decoder decided a
  token was "real ASCII, keep it" only if the token contained a character in
  `'p'`–`'~'`, and otherwise restored it as stripped Cyrillic. Absence of the
  letters p–z was treated as evidence FOR Cyrillic. It is not: most words in
  most Latin-script languages contain no letter from p to z.
- **New behavior and reason:** restoration now requires positive evidence. A
  body whose ASCII reading already contains two or more purely-alphabetic words
  is Latin text and is left alone. The discriminator is what stripping actually
  produces — ISO-8859-5 Cyrillic strips into `'0'`–`'?'`, `'A'`–`'O'`,
  `'P'`–`'`'` and `'a'`–`'o'`, so a stripped Cyrillic word lands on punctuation
  or mid-word capitals (`Код` → `:^T`, `подтверждения` → `_^TbRU\`VTU]Xo`),
  while a real Latin word is purely alphabetic.
- **Security/operational impact:** this was **destroying one-time passcodes**.
  Found in a live spool: an Indonesian WhatsApp message,
  `<#> Kode WhatsApp: 812-128 / Jangan bagikan kode ini dengan orang lain`, every
  byte printable ASCII with none above 0x7F, was stored and delivered as
  `М#О Ыяфх WhatsApp: ИБВ-БВИ …` — the passcode `812-128` rendered `ИБВ-БВИ`.
  `PreserveOTPDigits` (D-004) did not save it: its policy requires a digit run of
  five, or four when an anchor word is present, and `812-128` is two runs of
  three — while the anchor `Kode` had itself already been mangled, so no anchor
  could match. Wire bytes to a carrier were never affected (this is
  decode-for-storage), and `raw_hex` preserves the original, so already-spooled
  messages are recoverable.
- **Migration and rollback:** rollback is deleting `looksLikeRealLatinText` and
  its two call sites in `internal/core/msgcontent/decode.go`. No schema, no
  stored state, no wire implication. Messages spooled while the defect was live
  keep their mangled `text`; re-decode from `raw_hex` to recover them.
- **Differential fixture/test proving the boundary:**
  `internal/core/msgcontent/latin_false_positive_test.go` asserts both
  directions — the exact Indonesian body decodes intact under both option sets,
  and a genuinely stripped `Ваш код подтверждения 12345` still restores. The 77
  vectors in `testdata/vectors.json` are unchanged and still pass.
- **Owner approval:** reported by the owner from a production-shaped spool,
  2026-07-31.
- **Follow-up, same day (D-006b).** An independent audit found the gate guarded
  only two of the three restoration paths: it lived inside the two mixed
  helpers, so a body it refused fell through into `tryDecode7BitStripped` —
  whole-message restoration, the most destructive of the three. `YOUR CODE 12345
  <#>` (Android SMS Retriever; ALL-CAPS plus `<`/`>` trips the uppercase gate)
  was still rendered `йЯев УЯФХ БВГДЕ МЃО` with both D-004 and D-006 active. The
  gate now guards the whole block and is proportional (alphabetic words at least
  half of all tokens) so genuine stripped Cyrillic carrying a brand name still
  restores. Two further passcode shapes were unprotected on every path and now
  are: grouped codes with a separator (`123-456`, `12-34-56`) and alphanumeric
  codes (`A1B2C3`). The all-digit shape **requires a separator**, because
  Cyrillic А-Й strip onto `0`-`9` and the word `БЕДА` is indistinguishable from
  `1540` — a regression the existing suite caught immediately when the rule was
  looser. A space-separated `123 456` tokenises into two bare three-digit tokens
  and remains unprotected; closing that needs cross-token lookahead. Finally the
  mixed path now honours `PreserveOTPDigits`, without which protecting a code
  pushed the Cyrillic ratio under the acceptance bar and rejected the whole
  restore — protecting the passcode must not be what breaks the message.

## Known decoder limitations, not fixed (2026-07-31)

An audit after D-006/D-006b found further defects in the decode path. They are
recorded here rather than fixed, deliberately: none of them destroys a passcode
(that class is closed), all are plausibly faithful ports of the Python oracle,
and this path has already produced two regressions in one session when changed
(`БЕДА` protected as the code `1540`; CP1251 Cyrillic claimed as Hangul). Each
needs a conscious deviation decision, with the repro below as its starting point.

- **UCS-2 carrying no ASCII at all is not detected.** UTF-16 is found by counting
  NUL bytes, and Arabic (U+06xx), Han, Thai (U+0Exx), Hebrew (U+05xx) and
  Cyrillic (U+04xx) never produce one. `您的验证码是` is claimed by cp1251 and
  delivered as mojibake, while `您的验证码812128` survives because the ASCII
  digits supply the NULs. A script-dominance rule fixes these and breaks a
  legacy vector, because CP1251 Cyrillic read pairwise *is* valid Hangul. The
  real answer is probably a per-connector encoding hint, not a better heuristic.
- **The Thai and Arabic restoration arms are unreachable.** `decode.go:565` tries
  ISO-8859-5 first; strict ISO-8859-5 cannot fail, so Cyrillic claims every
  suspicious body and the `iso885911`/`iso88596` arms below it never run.
- **Spec-compliant ISO-8859-5 under DCS 0x06 loses to CP1251 when the text is
  all lowercase.** Every ISO-8859-5 lowercase byte also decodes to a Cyrillic
  letter under CP1251, the scores tie, and CP1251 is tried first: `привет`
  becomes `ЯаШТХв`. One uppercase letter breaks the tie.
- **Latin-1 accents under DCS 0 are deleted rather than decoded.**
  `Tu código: 123456` → `Tu cdigo: 123456`. Digits survive; the label is
  `utf-8-lossy` and does not say a character was dropped.
- **Plain-split reassembly can mix encodings across chunks.** Chunks are decoded
  independently before `Ingest`, so one half of a stripped-Cyrillic message can
  restore while the other is delivered raw, and the same bytes decoded as one
  body give a third answer.
- **UDH/SAR reassembly uses the DCS of whichever segment completed the group**
  (`assembler.go:378`), not the first. Mixed-DCS senders decode
  nondeterministically across redelivery orders.

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

## D-007 — a per-user DLR registry gate can override the terminal receipt

- **Affected matrix rows:** none. No jCli row, no wire-protocol row and no Redis
  golden row changes; the feature is invisible unless an operator enables it for
  a specific user.
- **Legacy behavior and evidence:** legacy Jasmin reports whatever the upstream
  returned. It has no notion of a per-user receipt policy at all. The closest
  legacy concept is the fake SMSC's activation window (`fake_smsc.py:227-228`),
  which reads `dlr:block:{digits}` and synthesizes `DELIVRD`/`000` on presence
  and `REJECTD`/`008` on absence — but only for traffic that emulator terminates,
  never for a real upstream, and never per submitting user.
- **New behavior and reason:** `users[].dlr_gate` makes the terminal receipt a
  chosen user is told depend on whether the destination has an open activation
  window. The reason is operational: a partner's traffic must be confirmed only
  for destinations a legitimate flow registered, which is an anti-AIT control the
  gateway could previously apply only inside a termination connector. The
  keyspace, the normalization (`termination.NormalizeDestination`) and the
  default status pairing are deliberately the existing ones, so the two gates
  share entries rather than drifting apart.
- **Security/operational impact:** each gated user gets a scoped credential of
  its own (`dlr_gate.key_id` + `token_sha256`, minted on enable, plaintext shown
  once) serving `POST /dlr-registry/<key_id>` on the public front door. That
  token authorizes nothing but its own endpoint, so the partner whose traffic is
  gated can open windows without ever holding the admin token. Windows are owned
  by the user that opened them and count only for that user; an unowned window —
  what the operator API and the legacy keyspace produce — counts for everyone.
  Beyond that, this is the one place where what a partner is
  told and what happened differ on purpose, so the boundary is enforced in code
  and in tests. The override is applied *after* `cdr.RecordFinalDLR`, so the
  commercial record always carries the real upstream status
  (`TestGateOverride_CDRKeepsTheUpstreamTruth`), and every override is logged
  with both statuses. Routing and billing are untouched — the message is still
  sent and still charged. `dlvrd` is overridden alongside the status so a partner
  never receives `stat:REJECTD` beside `dlvrd:001`. An unreadable registry fails
  open to the hit receipt, matching the termination gate: an infrastructure blip
  must not reject live traffic.
- **Migration and rollback:** rollback is removing `dlr_gate` from the affected
  users, which the admin API and console both do; the gate then never fires and
  the record loses its `gate_stat`/`gate_err` fields. Those two fields are only
  written when a gate decided something, so a deployment that never enables it
  produces `dlr:<msgid>` records byte-identical to before
  (`TestDLRRecordOmitsGateFieldsWhenUnset`). No schema and no wire format change.
- **Differential fixture/test proving the boundary:**
  `TestRegistryEntryIsReadableByTerminationGate` asserts that an entry written by
  the new registry API produces an accept from the pre-existing
  `termination` `redis-window` source, and that an absent number produces a
  reject — the compatibility claim the shared keyspace rests on. The Redis golden
  differential (`TestGoldenRedisState`) is unchanged and still passes.
- **Owner approval:** requested 2026-08-03 with the plan in
  `docs/plans/026-dlr-registry-gate.md`.

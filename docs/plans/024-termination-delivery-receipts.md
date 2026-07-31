# Plan 024 — Terminated messages must produce a delivery receipt

- **Date:** 2026-07-31
- **Status:** draft — blocked on one owner decision (see below)
- **Summary:** A partner whose traffic reaches a termination connector gets `submit_sm_resp` = OK and then never receives a DLR, because the synthesized receipt is refused by the CDR state machine. Fixing it means deciding what a terminated message means to billing.
- **Related:** [plan 021](021-mt-termination-connector.md), [ADR-006](../adr/006-durable-cdr-lifecycle.md), [ADR-008](../adr/008-mt-termination-connector.md)

## The defect

Reproduced on the demo gateway with the partner simulator: bind as transceiver,
submit with `registered_delivery`, `submit_sm_resp` returns `ESME_ROK` and a
message id, and **no receipt ever arrives**. `submitted=1 accepted=1 receipted=0`.

The gateway log says why:

```
ERROR DLR lookup failed: cdr "5b98122f-…/000001" cannot accept final DLR
      from submission state ADMITTED
```

`internal/infra/storage/postgres_cdr.go:235` (and the SQLite twin at `:300`):

```go
if submissionState != cdr.StateSMSCAccepted {
	return fmt.Errorf("cdr %q cannot accept final DLR from submission state %s", id, submissionState)
}
```

A CDR reaches `SMSC_ACCEPTED` only when a submit transaction commits
`ResultSuccess` (`postgres_submittransaction.go:369`) — which happens when an
**upstream SMSC** answers `submit_sm_resp`. A termination connector never talks
to an SMSC; it consumes the queue and spools. So the CDR stays `ADMITTED`
forever and the receipt the connector correctly synthesizes is rejected by our
own state machine, silently, on a path that logs the failure as a DLR-lookup
error rather than as a termination problem.

This matters more than a missing feature: plan 021 exists to decommission
`dlr-smpp-python`, **whose entire job was generating these receipts**. Until this
is fixed the replacement cannot replace it.

## The decision this is blocked on

The natural fix is for the termination connector to record the accepted
transition — semantically it *is* the SMSC here, and it did accept the message.
But that same transition is what publishes the late-billing intent
(`internal/core/smppc/response_publish.go:161`), so marking terminated messages
accepted **also settles their billing**.

Three readings, and they are genuinely different commercially:

1. **Terminated traffic bills exactly like relayed traffic.** The partner
   submitted, we accepted, we delivered to the application; the late share
   settles. Simplest and most consistent, and probably right if partners are
   charged for terminated messages at all.
2. **Terminated traffic settles on the verdict, not on acceptance.** A message
   the activation gate rejected (`REJECTD`) arguably should not incur the late
   share even though it was "accepted" by the connector.
3. **Terminated traffic is not billed here.** If the commercial relationship for
   terminating traffic is settled outside this platform, the CDR should reach a
   receipt-admitting state **without** emitting a billing intent.

**Nothing should be implemented until this is answered**, because each reading
produces a different transition and reversing a billing decision after it has
run against real traffic means reconciling money, not editing code.

## Approach, once decided

Add a terminal state the receipt guard admits, rather than reusing
`SMSC_ACCEPTED` and inheriting its billing side effect by accident.

- `StateTerminatedLocally` joins `SMSCAccepted`/`SMSCRejected`/`TerminalTimeout`
  in `cdr.State.Terminal()` and in the final-DLR guard in both stores.
- The termination worker commits that transition when it spools a message.
- Whether that transition also publishes a late-billing intent is the switch the
  decision above sets, and it is one call site rather than a semantic overload.

This keeps the state machine honest: "the message stopped here" and "an upstream
SMSC accepted it" are different facts and should not share a state, however
convenient the shortcut is today.

## Steps

### Step 1: The state and the guard

- **Files:** `internal/core/cdr/model.go`, `internal/infra/storage/postgres_cdr.go`, `internal/infra/storage/sqlite_cdr.go`, plus their tests
- **Changes:** Add `StateTerminatedLocally` and `EventTerminatedLocally`; admit it in `Terminal()` and in the final-DLR guard alongside `StateSMSCAccepted`.
- **Verify:** a CDR in the new state accepts a final DLR; one in `ADMITTED` still does not — the guard must not simply be removed.

### Step 2: The termination worker records it

- **Files:** `internal/core/termination/connector.go`, `internal/app/gateway/termination.go` (dependency wiring), tests
- **Changes:** On spooling a message, commit the terminated transition for its part key. Idempotent on redelivery: the spool already dedupes, and the CDR event key must too.
- **Verify:** an AMQP redelivery of the same submit produces exactly one CDR event.

### Step 3: Billing, per the decision

- **Files:** whichever of `internal/core/smppc/response_publish.go` / the termination commit path the decision implies
- **Changes:** Emit or do not emit the late-billing intent on the new transition.
- **Verify:** a terminated message produces exactly the ledger entries the decision calls for — and, critically, **not two** if it also went through early billing at admission.

### Step 4: End to end

- **Files:** `internal/app/gateway/*_test.go`
- **Changes:** Partner-simulator test: bind, submit with `registered_delivery`, assert a receipt arrives and carries the connector's verdict.
- **Verify:** `receipted=1` from `cmd/synevyr-partner-sim`, which today reports 0.

## Risks

- **Billing is the risk, not the receipt.** The receipt half is small and
  well-bounded. The transition it needs is entangled with money, and that is the
  only reason this is a plan rather than a patch.
- **Double-charging.** Terminated messages already take the early share at
  admission. Whatever Step 3 decides must be checked against that, not just
  against the relayed path.
- **A removed guard would be worse than the bug.** Deleting the
  `!= StateSMSCAccepted` check would make receipts flow immediately and let a
  final DLR land on a message that was never accepted by anything. The new state
  exists precisely so the guard keeps meaning something.

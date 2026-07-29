# Billing mismatch or balance drift

## Symptom

`JasminBillingMismatch` fires, logs contain `CDR reconciliation mismatch`, or
a customer balance differs from the durable commercial record. Early charges
are applied at admission; late charges are projected through
`submit_billing_intents` and `cdr_records.billing_outcome`. Live quotas persist
to `billing_quotas` every `outbound.quota_persist_interval_seconds` (10 seconds
when unset).

## Confirm

Preserve the alert labels and gateway logs:

```console
docker compose -f docker-compose.prod.yml logs --since=2h gateway | \
  grep -E 'CDR reconciliation mismatch|Billing quota persistence failed|Charging user failed'
curl -fsS http://127.0.0.1:${GATEWAY_ADMIN_API_PORT:-8405}/metrics/prometheus | \
  grep -E 'jasmin_billing_(charges|refusals|mismatches)'
```

Inspect aggregates without message content:

```console
docker compose -f docker-compose.prod.yml exec postgres \
  psql -U "${POSTGRES_USER:-jasmin}" -d "${POSTGRES_DB:-jasmin}" -c \
  "select scope,principal_key,balance,submit_sm_count,updated_at from billing_quotas order by scope,principal_key;"
docker compose -f docker-compose.prod.yml exec postgres \
  psql -U "${POSTGRES_USER:-jasmin}" -d "${POSTGRES_DB:-jasmin}" -c \
  "select billing_outcome,count(*),sum(early_amount),sum(actual_late_amount) from cdr_records group by billing_outcome order by billing_outcome;"
```

The reconciliation codes include `CDR_WITHOUT_SUBMIT_PART`,
`SUBMIT_PART_WITHOUT_CDR`, `FINAL_RESULT_STATE_MISMATCH`,
`ACCEPTED_LATE_INTENT_MISSING`, `BILLING_LEDGER_PROJECTION_MISMATCH`, and
`FINAL_DLR_EVENT_PROJECTION_MISMATCH`.

## Fix

- Stop account provisioning or manual balance changes while evidence is being
  collected; do not edit `billing_quotas`, `cdr_records`, or
  `submit_billing_intents` in place.
- Restore Postgres first if quota persistence is failing.
- Compare the provisioned balance/count with the durable quota and the
  content-free CDR events for the affected principal and time window.
- Treat a reconciliation mismatch as a data repair requiring peer review and
  a database backup. Use `scripts/deploy/backup.sh` before any approved repair.
- If the issue is a legitimate billing refusal, correct the user's balance,
  submit count, group ceiling, or route rate through the admin UI/jCli; do not
  suppress the refusal metric.

## Verify recovery

After the reviewed correction, wait longer than
`quota_persist_interval_seconds`, then confirm `billing_quotas.updated_at`
advances after a controlled charged submit and its CDR has the expected
`early_amount`, `actual_late_amount`, and `billing_outcome`.

No new `jasmin_billing_mismatches_total` increment or
`CDR reconciliation mismatch` line may occur during the next maintenance run.

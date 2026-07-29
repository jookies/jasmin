# PostgreSQL down or slow

## Symptom

`/ready` returns 503 with a `"postgres"` check of `probe timeout` or
`failed: ...`. Startup may fail at migration, submit recovery, durable quota
load, or active/passive fencing. Runtime logs may contain
`Billing quota persistence failed:`, `CDR reconciliation failed`, or
`CDR retention prune failed`.

## Confirm

```console
docker compose -f docker-compose.prod.yml ps postgres gateway
docker compose -f docker-compose.prod.yml exec postgres \
  pg_isready -U "${POSTGRES_USER:-jasmin}" -d "${POSTGRES_DB:-jasmin}"
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
docker compose -f docker-compose.prod.yml logs --since=30m postgres gateway
```

Use the database container to inspect active and waiting statements:

```console
docker compose -f docker-compose.prod.yml exec postgres \
  psql -U "${POSTGRES_USER:-jasmin}" -d "${POSTGRES_DB:-jasmin}" -c \
  "select pid,state,wait_event_type,wait_event,now()-query_start as age,left(query,120) from pg_stat_activity where datname=current_database() order by query_start;"
```

Check disk space and connection count:

```console
docker compose -f docker-compose.prod.yml exec postgres df -h /var/lib/postgresql/data
docker compose -f docker-compose.prod.yml exec postgres \
  psql -U "${POSTGRES_USER:-jasmin}" -d "${POSTGRES_DB:-jasmin}" -c \
  "select count(*),state from pg_stat_activity group by state;"
```

The gateway DSN is `outbound.postgres_dsn`, normally `env:POSTGRES_DSN`.

## Fix

- Restore the Postgres process, volume space, DNS, TLS policy, or credentials.
- Identify and terminate only a statement proven to be blocking gateway work;
  preserve the query and incident evidence first.
- In HA, do not force both gateways active. The Postgres session advisory lock
  is the active/passive fence; restoring the database permits one node to
  acquire it.
- Do not edit `billing_quotas`, `cdr_records`, `submit_parts`, or
  `submit_outbox` as an availability fix. Use a reviewed data-repair plan.
- If recovery requires restore, follow `deploy/BACKUP.md`; it drops and
  recreates the database and loses writes newer than the backup.

## Verify recovery

```console
docker compose -f docker-compose.prod.yml exec postgres \
  pg_isready -U "${POSTGRES_USER:-jasmin}" -d "${POSTGRES_DB:-jasmin}"
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
docker compose -f docker-compose.prod.yml logs --since=5m gateway | \
  grep -E 'Billing quota persistence failed|CDR reconciliation failed'
```

`/ready` must show `"postgres":"ok"`. Confirm statement latency remains normal
for 15 minutes and no new persistence/reconciliation error is logged.

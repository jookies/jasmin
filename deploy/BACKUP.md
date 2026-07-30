# Backup, restore, and teardown

## What actually needs backing up

| Store | Volume | Holds | Loss impact |
|---|---|---|---|
| Postgres | `pgdata` | Submit transactions, CDRs/billing, HA advisory-lock state | The durability boundary. Losing this loses billing history. |
| admin.db (SQLite) | `admindata` | Connectors/routes/users managed via the admin web UI or jCli | Losing it loses admin-plane config not also present in `configs/gateway.json`. |
| RabbitMQ | `rabbitmqdata` | In-flight (not-yet-delivered-to-SMSC) queued messages | Losing it loses messages currently in flight, not history. Not covered by the backup script — see below if you need it. |
| Redis | `redisdata` | DLR-tracking / quota cache | A fast cache, not a source of truth. Safe to lose; the gateway rebuilds what it needs. |

`scripts/deploy/backup.sh` covers Postgres and admin.db — the two stores where
loss is not just an availability blip but permanent data loss.

## Backup

```console
$ scripts/deploy/backup.sh
==> Backing up postgres (database: jasmin)...
  -> /home/you/synevyr-gateway-backups/postgres-20260729T120000Z.sql.gz (1.2M)
==> Backing up admin.db...
  -> /home/you/synevyr-gateway-backups/admin-20260729T120000Z.db
```

Writes to `$HOME/synevyr-gateway-backups` by default — outside the repo
checkout on purpose, so a backup never ends up in `git status`. Override with
`BACKUP_DIR=/somewhere/else scripts/deploy/backup.sh`, or pass it as the first
argument. Point `BACKUP_DIR` at an off-host location (rsync target, object
storage mount, etc.) for real disaster-recovery coverage — a backup that lives
on the same disk as the thing it backs up isn't one.

Run it from cron / systemd timer for a schedule, e.g.:

```
0 * * * * BACKUP_DIR=/mnt/backups/jasmin /opt/jasmin/scripts/deploy/backup.sh >> /var/log/synevyr-backup.log 2>&1
```

`pg_dump` runs against the live database (no downtime, no need to stop the
gateway) and is transactionally consistent as of the moment it starts.

## Restore

```console
$ scripts/deploy/restore.sh /home/you/synevyr-gateway-backups/postgres-20260729T120000Z.sql.gz \
    /home/you/synevyr-gateway-backups/admin-20260729T120000Z.db
```

This is destructive: it stops the gateway, **drops and recreates** the
Postgres database, replays the dump into it, and (if you passed a second
argument) overwrites `admin.db`. It prompts for confirmation unless you pass
`--yes-i-am-sure` (for scripted DR runbooks). Everything written to Postgres
after the backup was taken is gone after a restore — there is no partial/
merge restore.

After it finishes, verify:

```console
$ curl http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/health
```

## RabbitMQ (optional, if you need in-flight messages preserved across a
disaster too, not just a restart)

The `rabbitmqdata` volume already persists queue state across container
restarts and `docker compose down` (without `-v`). It is not covered by
`backup.sh` because RabbitMQ backup is an operational trade-off most
deployments don't need: in-flight messages are, by definition, messages that
haven't reached the SMSC yet, and Jasmin's queues are meant to drain quickly.
If your traffic pattern means large queue depths matter, back up the volume
directly:

```console
$ docker run --rm -v synevyr-prod_rabbitmqdata:/data -v "$PWD":/backup alpine \
    tar czf /backup/rabbitmq-$(date -u +%Y%m%dT%H%M%SZ).tar.gz -C /data .
```

(Volume name may have a different compose-project prefix than `synevyr-prod_` —
check with `docker volume ls`.)

## Teardown

Stop the stack, keep the data:

```console
$ docker compose -f docker-compose.prod.yml down
```

Stop the stack **and delete all volumes** (Postgres, RabbitMQ, Redis, admin
state — everything not backed up externally is gone):

```console
$ docker compose -f docker-compose.prod.yml down -v
```

Take a backup first if there is any chance you'll want this data again.

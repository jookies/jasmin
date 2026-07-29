# Credential compromise or rotation

## Symptom

A gateway, SMSC, SMPPS, HTTP sender, admin UI, jCli, RabbitMQ, Postgres, or
Redis credential is exposed or scheduled for rotation. Treat unexpected admin
mutations, binds, or sender authentication as compromise until disproven.

## Confirm

Identify the credential scope without printing its value:

- `ADMIN_TOKEN`, `ADMIN_WEB_PASSWORD`, and `JCLI_PASSWORD` protect separate
  management surfaces on ports 8405, 8404, and 8990.
- `SMSC_PASSWORD` is the outbound `connectors[].password`.
- `SMPPS_USER_PASSWORD` is an inbound `smpps.users[].password`.
- HTTP senders use `outbound.users[].password_sha256`.
- `RABBITMQ_DEFAULT_PASS`, `POSTGRES_PASSWORD`, and `REDIS_URL` protect
  infrastructure connections.

Review recent changes and access logs, but never paste `.env`, resolved config,
Authorization headers, or password hashes into the incident channel:

```console
git status --short
docker compose -f docker-compose.prod.yml logs --since=2h gateway
```

## Fix

1. Revoke or rotate at the credential issuer first.
2. Update the corresponding secret in `.env` or its `env:`/`file:` source.
   Secret references are resolved at startup, so restart `gateway` for
   config-owned credentials.
3. For `ADMIN_TOKEN`, rotate the `.env` value and restart before issuing more
   admin API calls. Keep port 8405 loopback-only.
4. For an HTTP sender, replace `password_sha256` in `configs/gateway.json` and
   restart, or use the admin web UI for an admin-managed user.
5. For an admin-managed SMSC or SMPPS user, update the password in the admin
   web UI. Unbind the old SMPPS session from the UI so it must authenticate
   again.
6. Rotate RabbitMQ/Postgres server credentials and the matching gateway DSN in
   one maintenance window. A mismatch makes the gateway unready.

If compromise is suspected, rotate related credentials rather than assuming
one leaked secret was isolated. Preserve logs and the Postgres/admin-store
backup for audit.

## Verify recovery

```console
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
docker compose -f docker-compose.prod.yml logs --since=5m gateway
```

Test one authorized action per rotated surface and confirm the old credential
is rejected. Verify the SMSC reconnects and binds, the intended SMPPS account
can rebind, a controlled HTTP sender can authenticate, and no secret appears
in logs or API responses.

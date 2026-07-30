# Gateway will not start

## Symptom

The `gateway` container exits or restart-loops and `/live`, `/health`, and
`/ready` never become available. Startup validates config, acquires the HA
Postgres fence, migrates and recovers durable state, opens Redis/RabbitMQ,
builds workers, starts connectors, then waits up to `bind_timeout_seconds` for
every connector in `required_connector_ids` (or every configured connector
when that list is empty).

## Confirm

```console
docker compose -f docker-compose.prod.yml ps -a gateway postgres rabbitmq redis
docker compose -f docker-compose.prod.yml logs --tail=300 gateway
docker compose -f docker-compose.prod.yml config --quiet
docker compose -f docker-compose.prod.yml run --rm --no-deps gateway \
  --check-config --config /etc/synevyr/gateway.json
```

Classify the first fatal line, not the final restart:

- `invalid gateway configuration` or `decode config` means JSON/schema;
- `acquire active-passive gateway fence` means Postgres/HA;
- `migrate submit transaction store` or `recover unresolved submit attempts`
  means Postgres state;
- `connect RabbitMQ`, `declare RabbitMQ`, or AMQP 406 means broker/topology;
- `open DLR request store` means `dlr_lookup.redis_url`;
- `start interceptor runner` means configured interception/Python;
- `required SMPPc connectors not bound` means SMSC reachability or bind.

Check infrastructure independently:

```console
docker compose -f docker-compose.prod.yml exec postgres \
  pg_isready -U "${POSTGRES_USER:-jasmin}" -d "${POSTGRES_DB:-jasmin}"
docker compose -f docker-compose.prod.yml exec rabbitmq rabbitmq-diagnostics -q ping
docker compose -f docker-compose.prod.yml exec redis redis-cli ping
```

## Fix

- Correct only the first failing layer and rerun `--check-config`.
- Ensure every referenced `env:NAME` has a non-empty value in `.env`; do not
  replace it with a committed literal secret.
- For connector failure, validate `host`, `port`, `system_id`, password, bind
  mode, and remote allow-list. Increasing `bind_timeout_seconds` does not fix a
  rejected bind.
- For AMQP 406, make `amqp_durable_topology` match the vhost or use a separate
  vhost. Do not delete production queues.
- For HA fencing, confirm only one active node owns the namespace; do not
  remove the advisory-lock safety to force startup.
- Never modify `jasmin/` or baseline fixtures during recovery.

## Verify recovery

```console
docker compose -f docker-compose.prod.yml up -d gateway
docker compose -f docker-compose.prod.yml logs --since=5m gateway
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/live
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
python3 scripts/compat/verify_baseline_tree.py
```

The final command must print `oracle_tree=frozen`. `/live` must report active
and `/ready` must report `"status":"ok"` with Postgres, AMQP, bridge, and every
required connector healthy.

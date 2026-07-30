# RabbitMQ down or queues backing up

## Symptom

`/ready` returns 503 with `"amqp":"connection closed"`, or
`SynevyrQueueBacklogGrowing` fires. Submits may be durably admitted to Postgres
but wait in `submit_outbox`; workers log `DLR lookup worker failed:`,
`DLR thrower worker failed:`, `deliver_sm thrower worker failed:`, or
`MO dispatch worker failed:`.

The queues used by this gateway are:

- `submit.sm.<cid>` for outbound connector work;
- `DLRLookup-main` for `dlr.*` correlation;
- `dlr_thrower` for HTTP/SMPPS receipt delivery;
- `RouterPB_deliver_sm_all` and `deliver_sm_thrower` for MO routing/delivery;
- `RouterPB_bill_request_submit_sm_resp_all` for late billing.

## Confirm

```console
docker compose -f docker-compose.prod.yml ps rabbitmq gateway
docker compose -f docker-compose.prod.yml exec rabbitmq rabbitmq-diagnostics -q ping
docker compose -f docker-compose.prod.yml exec rabbitmq \
  rabbitmqctl list_queues name messages_ready messages_unacknowledged consumers
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
curl -fsS http://127.0.0.1:${GATEWAY_ADMIN_API_PORT:-8405}/metrics/prometheus | grep synevyr_queue_depth
```

Check for resource alarms and connection churn:

```console
docker compose -f docker-compose.prod.yml exec rabbitmq rabbitmq-diagnostics -q check_running
docker compose -f docker-compose.prod.yml exec rabbitmq rabbitmqctl list_alarms
docker compose -f docker-compose.prod.yml logs --since=30m rabbitmq gateway
```

An AMQP `PRECONDITION_FAILED` 406 at startup usually means
`amqp_durable_topology` does not match existing exchanges/queues. Do not delete
live queues to clear it.

## Fix

- Restore the RabbitMQ container, disk space, memory, DNS, or credentials
  referenced by `outbound.amqp_url`/`AMQP_URL`.
- If a single `submit.sm.<cid>` queue grows while RabbitMQ is healthy, repair
  that connector; it owns the queue consumer.
- If `DLRLookup-main`, `dlr_thrower`, or `deliver_sm_thrower` has no consumer,
  inspect the matching worker error before restarting the gateway.
- Keep `amqp_durable_topology` uniform for the entire vhost. Move the Go
  gateway to a separate vhost if a legacy non-durable Jasmin topology exists.
- Do not purge a production queue unless the business owner explicitly accepts
  losing those messages. A restart with durable topology should preserve them.

## Verify recovery

```console
docker compose -f docker-compose.prod.yml exec rabbitmq rabbitmq-diagnostics -q ping
docker compose -f docker-compose.prod.yml exec rabbitmq \
  rabbitmqctl list_queues name messages_ready messages_unacknowledged consumers
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
```

The gateway logs `AMQP topology and outbound workers are ready.` after startup.
Verify every growing queue has a consumer and that `messages_ready` and
`synevyr_queue_depth` decrease over two successive five-minute observations.

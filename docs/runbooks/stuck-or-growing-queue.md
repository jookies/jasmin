# Stuck or growing queue

## Symptom

`JasminQueueBacklogGrowing` fires or a RabbitMQ queue has rising
`messages_ready`. A `submit.sm.<cid>` backlog delays MT delivery; a
`DLRLookup-main` or `dlr_thrower` backlog delays receipts; a
`RouterPB_deliver_sm_all` or `deliver_sm_thrower` backlog delays MO delivery.

## Confirm

Take two snapshots five minutes apart:

```console
docker compose -f docker-compose.prod.yml exec rabbitmq \
  rabbitmqctl list_queues name messages_ready messages_unacknowledged consumers
curl -fsS http://127.0.0.1:${GATEWAY_ADMIN_API_PORT:-8405}/metrics/prometheus | grep jasmin_queue_depth
```

Interpret the fields:

- rising `messages_ready` with zero consumers means the owning connector or
  worker is absent;
- rising `messages_ready` with consumers means work arrives faster than it
  completes;
- high `messages_unacknowledged` means consumers hold work and may be blocked
  on the SMSC, callback, codec, or database.

For a connector queue, correlate with bind state and submit latency:

```console
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
curl -fsS http://127.0.0.1:${GATEWAY_ADMIN_API_PORT:-8405}/metrics/prometheus | \
  grep -E 'jasmin_connector_bound|jasmin_submit_round_trip_seconds'
```

## Fix

- Repair an unbound connector or dead worker before tuning capacity.
- For sustained SMSC throttling, honor the SMPP status and work with the SMSC;
  increasing `submit_sm_throughput` makes throttling worse.
- `connectors[].prefetch_count` controls concurrent broker deliveries and
  `connectors[].submit_sm_throughput` caps submits per second. Change them only
  after measuring downstream capacity, then restart a config-owned connector.
- Callback queues are serial by design. Fix a slow/unreachable MO or DLR HTTP
  target and review the configured thrower timeout/retry delay.
- Never purge `submit.sm.*` or a DLR/MO queue as routine recovery; purging is
  irreversible message loss.

## Verify recovery

```console
docker compose -f docker-compose.prod.yml exec rabbitmq \
  rabbitmqctl list_queues name messages_ready messages_unacknowledged consumers
curl -fsS http://127.0.0.1:${GATEWAY_ADMIN_API_PORT:-8405}/metrics/prometheus | grep jasmin_queue_depth
```

Verify the owning consumer stays present, `messages_unacknowledged` turns over,
and depth decreases in two successive samples. For `submit.sm.<cid>`, also
confirm submit successes increase and the connector remains bound.

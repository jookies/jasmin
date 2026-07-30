# DLRs not arriving

## Symptom

Customers receive submit acceptance but no requested delivery receipt, or
`JasminDLRCorrelationFailures` fires. The receipt path is SMSC `deliver_sm` →
`DLRLookup-main` → Redis correlation (`dlr:*` keys) → `dlr_thrower` → HTTP or
SMPPS destination.

## Confirm

Check the connector and both broker stages:

```console
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
docker compose -f docker-compose.prod.yml exec rabbitmq \
  rabbitmqctl list_queues name messages_ready messages_unacknowledged consumers
curl -fsS http://127.0.0.1:${GATEWAY_ADMIN_API_PORT:-8405}/metrics/prometheus | grep synevyr_dlr_total
docker compose -f docker-compose.prod.yml logs --since=30m gateway | \
  grep -E 'DLR lookup failed:|DLR throw failed:|DLRLookup configured and ready|DLRThrower configured and ready'
```

Validate Redis without dumping callback data:

```console
docker compose -f docker-compose.prod.yml exec redis redis-cli ping
docker compose -f docker-compose.prod.yml exec redis redis-cli --scan --pattern 'dlr:*' | head
```

The submit-side store uses `dlr_lookup.redis_url`; in the production JSON it is
`env:REDIS_URL`. `dlr_lookup.pid` defaults to `main`, which names
`DLRLookup-main`. `dlr_thrower.amqp_url` inherits the outbound AMQP URL when
empty.

Distinguish outcomes in `synevyr_dlr_total`: `correlation_failure` means the
receipt could not map to a submit; other failed outcomes mean forwarding to
the customer endpoint or SMPPS session failed.

## Fix

- Restore the SMSC bind if no receipts reach the gateway.
- Restore Redis before correlation TTLs expire. Do not fabricate or bulk-delete
  `dlr:*` mappings during an incident.
- Repair a missing consumer or broker connection for `DLRLookup-main` or
  `dlr_thrower`.
- For HTTP callbacks, fix DNS/TLS/status/acknowledgement at the configured DLR
  URL. The thrower retries according to `dlr_thrower` timeout, retry delay, and
  retry cap.
- Confirm the submit requested the intended DLR level: level 1 is the
  `submit_sm_resp`, level 2 is the terminal receipt, and level 3 requests both.

## Verify recovery

Submit a controlled level-3 message to a test destination, record its gateway
message ID, and verify both the SMSC acceptance and terminal state arrive.

```console
docker compose -f docker-compose.prod.yml exec rabbitmq \
  rabbitmqctl list_queues name messages_ready messages_unacknowledged consumers
curl -fsS http://127.0.0.1:${GATEWAY_ADMIN_API_PORT:-8405}/metrics/prometheus | grep synevyr_dlr_total
```

`DLRLookup-main` and `dlr_thrower` must drain, delivered outcomes must increase,
and no new `DLR lookup failed:` or `DLR throw failed:` line should appear.

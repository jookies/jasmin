# SMSC connection lost or flapping

## Symptom

`SynevyrConnectorUnbound` or `SynevyrConnectorFlapping` fires, `/ready` returns
503 with `connector:<cid>` set to `DISCONNECTED` or `CONNECTING`, and
`synevyr_connector_bound{connector="<cid>"}` is `0`. The connector logger is
named `smpp.client.<cid>` and emits `Connection lost. Reason:`,
`Connection failed. Reason:`, and `Reconnecting after ... seconds ...`.

## Confirm

Run these from the production checkout:

```console
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
curl -fsS http://127.0.0.1:${GATEWAY_ADMIN_API_PORT:-8405}/metrics/prometheus | grep 'synevyr_connector_.*connector="smsc-primary"'
docker compose -f docker-compose.prod.yml logs --since=30m gateway | grep -E 'Connection (lost|failed|made)|Reconnecting after'
```

Check the configured `connectors[].host`, `port`, `system_id`, `bind`, and
password reference in `configs/gateway.json`. Test TCP reachability from the
same container/network namespace, replacing the example host and port:

```console
docker compose -f docker-compose.prod.yml exec gateway \
  python3 -c 'import socket; socket.create_connection(("bootstrap-smsc",2775),5).close()'
```

`GET /admin/connectors` shows only connectors created by the admin plane; the
shipped `smsc-primary` is config-owned and is visible in `/ready`, the admin
web UI, and Prometheus instead.

## Fix

- Restore DNS, routing, firewall, or the remote SMSC first if the TCP probe
  fails.
- If the SMSC reports authentication or bind-mode rejection, update
  `connectors[].system_id`, `password`, or `bind`. `env:SMSC_PASSWORD` resolves
  from `.env`; restart the gateway after changing a config-owned connector.
- Keep `con_loss_retry` and `con_fail_retry` enabled for recoverable outages.
  The shipped `con_loss_delay` and `con_fail_delay` are 10 seconds. Do not
  shorten them to mask an unstable SMSC.
- For an admin-managed connector, correct it in the admin UI and start it
  there. Config-owned connectors cannot be mutated through `/admin/`.

Watch for `Connection made to <host>:<port>; connector [<cid>] is bound`.

## Verify recovery

```console
curl -fsS http://127.0.0.1:${GATEWAY_HTTP_PORT:-1401}/ready
curl -fsS http://127.0.0.1:${GATEWAY_ADMIN_API_PORT:-8405}/metrics/prometheus | \
  grep 'synevyr_connector_bound{connector="smsc-primary"} 1'
docker compose -f docker-compose.prod.yml logs --since=5m gateway | \
  grep 'connector \[smsc-primary\] is bound'
```

Confirm `synevyr_connector_uptime_seconds` rises for at least 15 minutes and
`changes(synevyr_connector_bound[15m])` stops increasing.

# Running the Synevyr Messaging Platform

This is the day-to-day operator handbook. It assumes the production example has
already been deployed and its required substitutions have been made. The shipped
listener defaults are HTTP on `1401`, customer SMPP on `2775`, the web console on
`8404`, the token admin API on `8405`, and jCli on `8990`
(`configs/gateway.production.example.json:20`,
`configs/gateway.production.example.json:114`,
`configs/gateway.production.example.json:127`).

Treat the platform as lab-validated, not carrier-proven. It has not carried
traffic over a real carrier link, has no completed sustained-load or 24-hour soak
test, and has incomplete instrumentation
(`docs/architecture.md:180`, `docs/operations/monitoring.md:12`). Qualify each
carrier, customer client, encoding, receipt mode, and failure policy before
production traffic.

The examples below use these local addresses:

```sh
export HTTP_URL='http://127.0.0.1:1401'
export ADMIN_URL='http://127.0.0.1:8405'
export WEB_URL='http://127.0.0.1:8404'
export ADMIN_TOKEN='the deployed admin token'
```

Keep the admin API, web console, and jCli on a private network. Every token API
request requires the configured bearer token
(`internal/app/admin/handler.go:54`). The production deployment guide exposes
them on loopback through an SSH tunnel rather than directly to the Internet
(`README.md:243`).

## 1. The model

The same in-process user, routing, connector, and billing services sit behind
the web console, jCli, and the token API
(`internal/app/gateway/runtime.go:500`, `internal/app/gateway/runtime.go:554`).
The surfaces are different views and do not represent separate configurations.

```text
                    MT: customer sends to a handset

 HTTP /send  ----\
                  +--> user --> group ceilings --> MT credential
 SMPP submit_sm --/                      |              |
                                         +------> filters
                                                     |
                              highest matching MT route
                                                     |
                                      live SMPPc connector
                                                     |
                                                carrier SMSC

                    Return traffic from a carrier

 carrier deliver_sm --> DLR correlation --> HTTP callback or bound ESME
                    \-> MO routing ------> HTTP webhook or bound ESME
```

A **user** is the sending and billing identity. It has a username, an HTTP
password verifier, enable state, optional group, balance and submit-count
ceilings, and an MT credential. The allowed identity syntax and complete stored
shape are defined in `internal/app/outbound/config.go:26` and
`internal/app/outbound/config.go:100`.

A **group** is optional in the Go configuration. When present, it supplies a
shared balance, submit-count ceiling, and enable state across its users
(`internal/app/outbound/config.go:41`, `internal/app/outbound/config.go:181`).
Both the user's finite ceilings and the group's finite ceilings must cover a
submit; both are decremented together
(`internal/core/billing/billing.go:496`). Use a group when several credentials
draw from one commercial allowance or must be suspended together.

An **MT credential** is a policy attached to a user. Its authorizations decide
which ingress and optional fields the customer may use. Its value filters fence
destination, source, priority, validity, and content; it also supplies the
default source address and the per-ingress throughput ceilings
(`internal/app/outbound/config.go:145`). This policy is checked before routing
and billing.

A **filter** is a condition on a routable message. MT routes support user, group,
source, destination, content, tag, date, and time conditions
(`internal/app/outbound/filters.go:58`). All filters on one route must match.
Saved filters in the web console and jCli are templates copied into a route; a
later edit or deletion of the template does not rewrite an existing route
(`internal/app/jcli/managers_filter.go:10`,
`internal/app/jcli/managers_routes.go:105`).

An **MT route** chooses one or more outbound connector IDs and a per-segment
rate. Positive orders are considered from highest to lowest; order `0` is the
unfiltered default. The first route whose filters all match wins
(`internal/core/routingtable/table.go:172`,
`internal/core/routingtable/table.go:214`). A route with several connector IDs
uses the first currently available candidate in the configured order
(`internal/app/outbound/runtime.go:1043`).

An **SMPPc connector** is Synevyr acting as an ESME toward a partner SMSC. Its
configuration contains the remote host, bind credentials and role, TON/NPI,
timeouts, pacing, DLR ID conversion, TLS, and logging
(`internal/core/smppc/config.go:20`). Only a transmitter or transceiver
connector can carry MT traffic; only a receiver or transceiver can receive MO
and DLR traffic (`internal/core/smppc/config.go:394`).

An **SMPPs bind user** is Synevyr acting as an SMSC toward a customer ESME. It is
separate from the outbound user. The bind record authenticates `system_id` and
the wire password and limits IPs and sessions; a matching outbound username
provides the sending identity and commercial policy
(`internal/app/smppsserver/directory.go:18`,
`internal/app/smppssubmit/handler.go:85`).

For an MT message, the message therefore touches the ingress credential, its
user and possibly group ceilings, the first matching route and all of that
route's filters, and finally one live SMPPc connector. For an MO, the source
SMPPc connector is matched against the MO route table and the destination is an
HTTP webhook or an eligible SMPPs session
(`internal/app/modispatch/service.go:389`).

## 2. Onboard an HTTP customer

Use the token API for repeatable onboarding. It exposes the raw user model and
does not return password material in list or get responses
(`internal/app/admin/handler.go:240`). Use the web console for later human edits,
where password fields are write-only and a blank password preserves the current
hash (`internal/app/adminweb/handlers_users.go:15`,
`internal/app/adminweb/handlers_users.go:326`).

Before creating the account, confirm that an MT route exists and its connector
is `BOUND`. A successful `/send` means durable admission to the submit pipeline,
not handset delivery (`docs/architecture.md:49`).

### Generate and hash the password

The HTTP compatibility endpoint accepts a password of at most 16 characters
(`internal/transport/httpcompat/handler.go:32`). Generate exactly 16 hexadecimal
characters, keep the plaintext in the customer secret store, and calculate the
SHA-256 over the exact bytes without a newline:

```sh
export HTTP_PASSWORD="$(openssl rand -hex 8)"
export HTTP_PASSWORD_SHA256="$(
  printf '%s' "$HTTP_PASSWORD" | shasum -a 256 | awk '{print $1}'
)"
printf 'Give this once to the customer: %s\n' "$HTTP_PASSWORD"
```

New users should set exactly one password verifier. `password_sha256` must be 64
hexadecimal characters; `password_md5` exists only for migrated legacy users
(`internal/app/outbound/config.go:519`).

### Create the user

This account may call `/send` and `/balance`, may request DLRs, cannot override
its source address, is restricted to the example destination range, and is
limited to ten accepted HTTP submits per second. Its balance is finite and its
submit count is unlimited. Replace the `1555` documentation range and source
address with the customer's contracted values before running the request.

```sh
python3 - <<'PY' |
import json
import os

print(json.dumps({
    "username": "acme_http",
    "external_id": "acme_http",
    "password_sha256": os.environ["HTTP_PASSWORD_SHA256"],
    "balance": 50.0,
    "submit_sm_count": None,
    "early_decrement_balance_percent": None,
    "disabled": False,
    "mt_credential": {
        "http_send": True,
        "http_bulk": False,
        "http_balance": True,
        "http_rate": False,
        "smpps_send": False,
        "http_long_content": False,
        "set_dlr_level": True,
        "http_set_dlr_method": True,
        "set_source_address": False,
        "set_priority": False,
        "set_validity_period": False,
        "set_hex_content": False,
        "set_schedule_delivery_time": False,
        "filter_destination_address": "^1555[0-9]{7}$",
        "default_source_address": "ACME",
        "http_throughput": 10
    }
}))
PY
curl --fail-with-body --connect-timeout 5 --max-time 15 \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @- \
  "$ADMIN_URL/admin/users"
```

The token API accepts that raw `UserConfig` document and keys it by `username`
(`internal/app/admin/handler.go:240`). If the customer belongs to a shared
commercial account, first create the group under **Groups** in the web console,
then set `group_id` on the user. A user referencing a missing group is rejected
so a group ceiling cannot disappear silently
(`internal/app/outbound/config.go:484`).

Authorizations default to permissive except `http_bulk` when fields are omitted
(`internal/core/mtcredential/credential.go:61`). Set every material permission
explicitly during onboarding. A denied optional field is rejected rather than
silently stripped (`internal/core/mtcredential/validate.go:76`).

### Understand the three kinds of quota

Balance and submit count are independent finite ceilings. A rated message
decrements balance by route rate multiplied by segment count. A finite
`submit_sm_count` is also decremented by segment count
(`internal/core/billing/billing.go:552`). If both are finite, both must have
enough remaining capacity. A value of `0` for either one means exhausted, not
unlimited (`internal/core/billing/billing.go:496`). JSON `null` is unlimited for
balance or submit count (`internal/core/billing/quota_store.go:20`).

Throughput is different. `http_throughput: 0` and
`smpps_throughput: 0` mean **unlimited**, not blocked. That inherited behavior
is Q-022, and applies because the limiter only runs for a positive value
(`docs/reference/legacy-behaviours.md:43`,
`internal/core/throughput/limiter.go:35`). A positive value is minimum spacing
between accepted submissions, with no burst credit or queue.

To use a count allowance instead of money, change the example to:

```json
{
  "balance": null,
  "submit_sm_count": 1000
}
```

To make both commercial ceilings unlimited, set both to `null`. Do not copy the
production template's bootstrap values of `0.0` and `0`; those are deliberately
closed until replaced (`configs/gateway.production.example.json:29`).

### Verify the account

Check the effective live ceilings before sending:

```sh
curl --fail-with-body --connect-timeout 5 --max-time 15 \
  --get "$HTTP_URL/balance" \
  --data-urlencode 'username=acme_http' \
  --data-urlencode "password=$HTTP_PASSWORD"
```

The expected shape is:

```json
{"balance": "50", "sms_count": "ND"}
```

`ND` is the compatibility representation of an unlimited balance or count
(`internal/transport/httpcompat/handler.go:219`).

Send one billable, real message to a test handset. Omitting `from` exercises the
provisioned default source:

```sh
curl --fail-with-body --connect-timeout 5 --max-time 30 \
  --get "$HTTP_URL/send" \
  --data-urlencode 'username=acme_http' \
  --data-urlencode "password=$HTTP_PASSWORD" \
  --data-urlencode 'to=15551234567' \
  --data-urlencode 'content=Synevyr onboarding test'
```

Record the returned queue message ID:

```text
Success "86d15f1a-f75b-43f0-ab2e-a9bbde7acdc8"
```

That exact response form is defined by the compatibility handler
(`internal/transport/httpcompat/handler.go:352`). Repeat `/balance` and verify the
expected balance or count decrement. Multipart messages are charged per emitted
part (`docs/api/http.md:169`).

If the call fails, use the status and body rather than retrying blindly. A 403
is authentication or throughput; a 400 is a request or credential policy
failure; a 500 may mean no live route or insufficient quota
(`docs/api/http.md:236`). Remove `HTTP_PASSWORD` and
`HTTP_PASSWORD_SHA256` from the shell after handing the credential over:

```sh
unset HTTP_PASSWORD HTTP_PASSWORD_SHA256
```

## 3. Onboard an SMPP customer

Use jCli for the initial provision because its `user` manager writes the
outbound user and mirrors the complete SMPP bind policy, including value
filters, into a bind record with the same name
(`internal/app/jcli/managers_user.go:296`). Review the resulting two records in
the web console's separate **Users** and **SMPPs Binds** pages
(`internal/app/adminweb/server.go:163`,
`internal/app/adminweb/server.go:180`).

Generate the bind secret:

```sh
export SMPPS_BIND_PASSWORD="$(openssl rand -hex 4)"
printf 'ESME bind password: %s\n' "$SMPPS_BIND_PASSWORD"
```

The bind password is deliberately only eight ASCII characters. SMPP 3.4 permits
at most eight password octets plus the terminating NUL
(`docs/api/smpp.md:133`). Current config validation does not reject a longer
value, so a longer value can be saved even though a conforming ESME cannot send
it (`internal/app/smppsserver/directory.go:137`). Eight hex characters have only
32 bits of entropy; combine the password with an exact IP/CIDR allowlist and a
private network or verified TLS.

Connect to jCli on the private listener. Create the group only if it does not
already exist:

```sh
telnet 127.0.0.1 8990
```

```text
group -a
gid acme
ok
```

Then create the user. The `password` line below is illustrative; paste the value
generated in `SMPPS_BIND_PASSWORD`. Replace the example IP with the customer's
exact address or CIDR.

```text
user -a
uid acme_smpp
gid acme
username acme_smpp
password 9f2a6c1e
mt_messaging_cred authorization http_send no
mt_messaging_cred authorization http_balance no
mt_messaging_cred authorization http_rate no
mt_messaging_cred authorization http_bulk no
mt_messaging_cred authorization smpps_send yes
mt_messaging_cred authorization http_long_content no
mt_messaging_cred authorization dlr_level yes
mt_messaging_cred authorization http_dlr_method no
mt_messaging_cred authorization src_addr no
mt_messaging_cred authorization priority no
mt_messaging_cred authorization validity_period no
mt_messaging_cred authorization schedule_delivery_time no
mt_messaging_cred authorization hex_content no
mt_messaging_cred valuefilter dst_addr ^1555[0-9]{7}$
mt_messaging_cred defaultvalue src_addr ACME
mt_messaging_cred quota balance 50
mt_messaging_cred quota smpps_throughput 10
smpps_cred authorization bind yes
smpps_cred authorization ip 203.0.113.10/32
smpps_cred quota max_bindings 2
ok
```

jCli requires `gid`, password, UID, and username for a new user
(`internal/app/jcli/managers_user.go:26`). Its credential grammar accepts the
authorization, value-filter, default, quota, bind, IP, and binding-count lines
shown above (`internal/app/jcli/managers_user.go:74`). It hashes the plaintext
into the outbound user's `password_sha256`, then stores the plaintext in the
separate bind record because the bind server computes its verifier itself
(`internal/app/jcli/managers_user.go:278`,
`internal/app/jcli/managers_user.go:301`). The user balance is finite, submit
count is omitted and therefore unlimited, and SMPP throughput is ten per second.

Verify that both web pages contain `acme_smpp` after jCli reports success. The
jCli save writes the outbound user before writing the mirrored bind account; if
the second write fails, it returns an error but does not roll back the first
write (`internal/app/jcli/managers_user.go:177`). This is an implementation bug,
so remove the partial outbound user or complete the bind record before testing.

An omitted bind IP becomes `0.0.0.0/0`, which admits any IPv4 address; always set
the peer IPs explicitly in production
(`internal/app/smppsserver/directory.go:151`). Bare IPs and CIDRs are supported
(`internal/core/smpps/ipmatch.go:27`).

The two records have different jobs:

* `smpps.users[].password`, represented by **SMPPs Binds**, is the password in
  the bind PDU.
* The `outbound.users[]` record, represented by **Users**, is found by
  `system_id == username` and supplies balance, submit count, routes, and
  `smpps_throughput`.

The server passes the bound `system_id` into the shared MT pipeline as the
username (`internal/app/smppssubmit/handler.go:85`). If the SMPPs bind record
exists but the matching outbound user does not, the ESME binds successfully and
then receives `ESME_RSYSERR` on `submit_sm`
(`internal/app/smppssubmit/handler.go:126`,
`internal/app/smppssubmit/handler.go:148`). Check this join before treating that
status as a platform fault.

Ask the customer to test a transceiver bind when they need both submits and
`deliver_sm`, or paired transmitter and receiver binds. A receiver bind cannot
submit, and a transmitter bind cannot receive DLRs or MO messages
(`internal/core/smpps/bindstate.go:78`,
`internal/core/smpps/session.go:390`). Verify:

1. the bind returns `ESME_ROK`;
2. the `smpp.server` log records the successful bind and role
   (`docs/operations/monitoring.md:189`);
3. an allowed `submit_sm` returns a Synevyr message ID;
4. an out-of-range destination returns `ESME_RSUBMITFAIL`; and
5. a burst over the ceiling returns `ESME_RTHROTTLED` without dropping the bind.

The current status mappings are explicit at
`internal/app/smppssubmit/handler.go:133`. For a real receipt test, set
`registered_delivery` low bits to `1`, keep a receiver-capable bind open, and
acknowledge each `deliver_sm` with `deliver_sm_resp`. Low-bit value `2` requests
failure receipts only; avoid value `3`, which is admitted initially but does not
create the expected correlation behavior (`docs/api/smpp.md:372`).

```sh
unset SMPPS_BIND_PASSWORD
```

## 4. Onboard a carrier or messaging partner

> For the shape of a **multi-carrier** setup — how several carriers coexist, how
> traffic is split between them, and why they all share one delivery-receipt path
> — see [`multi-carrier-and-dlr-flow.md`](multi-carrier-and-dlr-flow.md), which
> has the end-to-end diagram.

Use the token API for a reviewed, repeatable connector payload, then use the web
console to watch it. The API create operation validates, applies, optionally
starts, and then persists the connector
(`internal/app/admin/service.go:109`). Admin-created connector start/stop state
survives restart (`internal/app/admin/service.go:183`).

The SMSC supplies the host, port, system ID, bind password, system type, bind
role, address range, TON/NPI, TLS requirements, maximum throughput, window,
receipt ID format, and retry policy. Do not infer these from another carrier.
The example below is a transceiver with international ISDN destination
addressing and a 25 submit/second ceiling. Replace the `.example` host and every
contract value before sending the request.

Read the carrier password without putting it in shell history:

```sh
read -r -s SMSC_PASSWORD
export SMSC_PASSWORD
printf '\n'
```

Create and start the connector. The response is passed through Python so the
token API's echoed password is not printed:

```sh
set -o pipefail
python3 - <<'PY' |
import json
import os

print(json.dumps({
    "start": True,
    "config": {
        "cid": "carrier-a",
        "host": "smpp.carrier.example",
        "port": 2775,
        "system_id": "synevyr-prod",
        "password": os.environ["SMSC_PASSWORD"],
        "system_type": "",
        "bind": "transceiver",
        "addr_ton": 0,
        "addr_npi": 0,
        "address_range": "",
        "src_ton": 2,
        "src_npi": 1,
        "dst_ton": 1,
        "dst_npi": 1,
        "trx_to": 30,
        "res_to": 60,
        "pdu_to": 30,
        "con_loss_retry": True,
        "con_fail_retry": True,
        "con_loss_delay": 10,
        "con_fail_delay": 10,
        "tls_enabled": False,
        "priority": 0,
        "log_level": "INFO",
        "submit_sm_throughput": 25,
        "prefetch_count": 25,
        "window_size": 25,
        "dlr_msg_id_bases": 0,
        "dlr_expiry": 86400
    }
}))
PY
curl --fail-with-body --connect-timeout 5 --max-time 15 \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary @- \
  "$ADMIN_URL/admin/connectors" |
python3 -c '
import json
import sys
value = json.load(sys.stdin)
value.get("config", {})["password"] = "REDACTED"
print(json.dumps(value, indent=2))
'
unset SMSC_PASSWORD
```

`src_ton`, `src_npi`, `dst_ton`, and `dst_npi` are the wire values used for
front-door submits that do not supply them
(`internal/core/smppc/config.go:34`,
`internal/core/smppc/config.go:379`). `addr_ton`, `addr_npi`, and
`address_range` belong to the bind. Confirm all seven with the partner.

`submit_sm_throughput` paces the carrier side. If omitted it defaults to one
submit per second; an explicit zero disables pacing
(`internal/core/smppc/config.go:407`,
`internal/core/smppc/pacer.go:55`). `prefetch_count` controls fetched AMQP work,
while `window_size` bounds outstanding `submit_sm` PDUs
(`internal/core/smppc/config.go:102`). Set them deliberately rather than using a
high carrier TPS with a one-message window.

`dlr_msg_id_bases` is `0` when submit and receipt IDs use the same base, `1` when
the receipt is decimal and submit response hexadecimal, and `2` for the reverse
(`internal/core/smppc/config.go:112`). A wrong value presents as missing DLR
correlation, not a bind failure.

### Confirm `BOUND`

The observed states are `DISCONNECTED`, `CONNECTING`, `BOUND`, and `UNBINDING`
(`internal/core/smppc/connector.go:26`). Poll without displaying the secret:

```sh
set -o pipefail
for attempt in $(seq 1 12); do
  state="$(
    curl --fail-with-body --silent --show-error \
      --connect-timeout 5 --max-time 10 \
      -H "Authorization: Bearer $ADMIN_TOKEN" \
      "$ADMIN_URL/admin/connectors/carrier-a" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["observed"])'
  )" || break
  printf '%s\n' "$state"
  [ "$state" = BOUND ] && break
  sleep 5
done
```

Do not route paid traffic until the final line is `BOUND`. The connector logs
the target before dialing, the reason on failure, the reconnect delay, and a
positive bound message (`internal/core/smppc/connector.go:414`). With the
production Compose file:

```sh
docker compose -f docker-compose.prod.yml logs --since=10m gateway |
  grep -F 'carrier-a'
```

If it does not bind, check in this order:

1. Resolve the configured name and test TCP reachability with a bounded call:

   ```sh
   python3 - <<'PY'
   import socket

   with socket.create_connection(
       ("smpp.carrier.example", 2775),
       timeout=5,
   ):
       print("TCP reachable")
   PY
   ```

2. Compare system ID, password, system type, and bind role byte-for-byte with
   the carrier. A receiver-only connector cannot carry MT even when it binds
   (`internal/core/smppc/config.go:394`).
3. Confirm the carrier has allowlisted Synevyr's actual egress IP.
4. Confirm TLS mode, server name, and CA file. Certificate verification cannot
   be disabled by connector configuration
   (`internal/core/smppc/config.go:265`).
5. Check whether the bind response arrived inside `bind_to`, and whether retry
   is enabled. Failed attempts back off to the configured cap with jitter
   (`internal/core/smppc/connector.go:424`).

After correcting an admin-managed connector in the web console, leave
**Desired started** on. Updates replace its config live
(`internal/app/admin/service.go:142`). For an explicit retry:

```sh
set -o pipefail
curl --fail-with-body --connect-timeout 5 --max-time 15 \
  -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$ADMIN_URL/admin/connectors/carrier-a/start" |
python3 -c '
import json
import sys
value = json.load(sys.stdin)
value.get("config", {})["password"] = "REDACTED"
print(json.dumps(value, indent=2))
'
```

Security defect: the token API returns the complete connector config, including
the password, on create, get, list, update, start, and stop
(`internal/app/admin/handler.go:360`). Keep its output out of tickets and logs.
The web BFF deliberately blanks the password
(`internal/app/adminweb/handlers_connectors.go:23`). Also note that `env:NAME`
secret expansion applies to file-configured connectors, not admin-store
connector JSON (`internal/app/gateway/secrets.go:52`); do not enter an
`env:` reference in this API and expect it to resolve.

## 5. Route traffic

### MT routes

![MT routes are evaluated from the highest order down and stop at the first match](../assets/diagrams/mt-route-priority.svg)

Evaluation stops at the first match, so a broad route placed above a specific one
takes its traffic and the specific route never fires. The order-0 default has no
filters by definition — anything that reaches it matches.

Use **MT Routes** in the web console for routine changes because it shows both
config-managed read-only routes and admin-managed editable routes
(`internal/app/adminweb/handlers_routes.go:48`). For automation, the token API
accepts the raw route JSON and uses `order` as its identity
(`internal/app/admin/handler.go:166`).

This route sends the example UK prefix for one customer to `carrier-a` at
`0.0125` per segment:

```sh
set -o pipefail
curl --fail-with-body --connect-timeout 5 --max-time 15 \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  --data-binary '{
    "connector_id": "carrier-a",
    "rate": 0.0125,
    "default": false,
    "order": 100,
    "filters": [
      {"type": "user", "username": "acme_http"},
      {"type": "destination_addr", "pattern": "^44"}
    ]
  }' \
  "$ADMIN_URL/admin/routes"
```

Orders are scanned from highest to lowest. All filters on one route must match,
and the first matching route wins
(`internal/core/routingtable/table.go:172`,
`internal/core/routingtable/table.go:214`). Use a user or group filter for
customer-specific commercial rates; a bare prefix route changes the rate and
carrier for every matching customer.

The default MT route must have `default: true`, `order: 0`, and no filters
(`internal/core/routingtable/table.go:150`). The production template owns order
`0`, so it is read-only to the admin plane. Change its JSON and restart when the
fallback carrier or rate changes. Config-owned identities are reserved and live
admin routes are additive (`docs/operations/configuration.md:435`).

Legacy regular expressions use `re.match` semantics: matching starts at
position zero even when the pattern has no `^`. Therefore:

| Intent | Correct pattern |
|---|---|
| Starts with `44` | `^44` or `44` |
| Contains `555` anywhere | `.*555` |
| Exactly 11 digits | `^[0-9]{11}$` |

A pattern of `555` does **not** mean substring match. The engine requires the
match location to start at zero
(`internal/core/routingfilter/filter.go:275`,
`internal/core/mtcredential/validate.go:146`). This applies to route filters and
user value filters. Patterns must also be valid Go RE2; Python lookaround and
backreferences are not supported
(`internal/core/mtcredential/credential.go:109`).

To build ordered carrier failover, set `connector_ids`:

```json
{
  "connector_ids": ["carrier-a", "carrier-b"],
  "rate": 0.0125,
  "default": false,
  "order": 100,
  "filters": [
    {"type": "destination_addr", "pattern": "^44"}
  ]
}
```

The runtime chooses the first currently available candidate in that order
(`internal/app/outbound/runtime.go:1043`). This is failover preference, not
traffic-weighted load distribution.

`/rate` and the web **Quote rate** tool now resolve the destination through the
live MT routing table and quote the rate of the route the message would actually
take, including routes added or repriced through the admin plane after start-up.

This was previously a bug worth knowing about if you are reading older notes or
running an older build: the rate reader discarded the destination and returned
one boot-built number taken from the **highest-order** configured route, so a
customer whose traffic fell through to a cheap order-0 default was quoted the
expensive filtered route's price. Fixed in plan 019; pinned by
`TestRateQuoteFollowsTheDestinationsRoute`.

One limit remains by construction: a quote carries no message content, so a
route filtered on `short_message` cannot match and the quote falls through to the
next route that does. When nothing matches at all — only possible in a table with
no default route — the answer is still the legacy fallback rate rather than an
error, because `/rate` answering a price is a customer-visible contract.

### MO routes

**Have an MO route at order 0.** An inbound message matching no MO route is
acknowledged to the carrier and then discarded, so the carrier records a delivery
and you have nothing — unlike the MT side, where an unmatched submit is refused
visibly at the front door. The gateway warns at startup when no default MO route
exists, the console's MO routes page shows a banner, and every drop increments
`synevyr_mo_total{outcome="dropped"}`.

MO traffic arrives as `deliver_sm` on a receiver-capable carrier connector. MO
routes also scan positive orders from highest to lowest, require every condition
to match, and fall back to order `0`
(`internal/app/modispatch/service.go:283`,
`internal/app/modispatch/service.go:389`).

For HTTP delivery, use **HTTP Destinations** to save the method and URL, then
create an **MO Route**. Selecting a saved destination copies it into the route;
editing the saved destination later does not change existing routes
(`web/src/pages/mo-routes/form.tsx:22`). For a customer-specific path, enter:

```json
{
  "order": 100,
  "default": false,
  "filter_connector_id": "carrier-a",
  "connector": {
    "type": "http",
    "cid": "acme-mo",
    "url": "https://messaging.acme.example/synevyr/mo",
    "method": "POST"
  }
}
```

The web form exposes exactly those source-connector and destination fields
(`web/src/pages/mo-routes/form.tsx:74`). A blank
`filter_connector_id` means any inbound carrier. Add source, destination,
content, tag, date, or time filters only when needed; all must match
(`internal/app/modispatch/service.go:45`).

To deliver MO messages to a customer ESME instead, use:

```json
{
  "order": 100,
  "default": false,
  "filter_connector_id": "carrier-a",
  "connector": {
    "type": "smpps",
    "system_id": "acme_smpp"
  }
}
```

The target must have an eligible receiver or transceiver session at delivery
time. HTTP and SMPPs are the only supported MO destination types, and each
requires the fields shown above
(`internal/app/modispatch/service.go:160`).

The shipped production template owns an HTTP default MO route at order `0`
(`configs/gateway.production.example.json:98`). Replace its placeholder URL
before receiving MO traffic. An admin route cannot replace the same
config-owned order (`internal/app/modispatch/service.go:319`).

## 6. Operate delivery receipts

For HTTP customers, a `/send` request enables callbacks when `dlr=yes`, or when
`dlr-url` or `dlr-level` is supplied. A callback record is created only when a
nonempty `dlr-url` is present (`docs/api/http.md:129`). A production test asking
for both submit and terminal events is below. Replace the `.example` URL with
the customer's live receiver.

```sh
curl --fail-with-body --connect-timeout 5 --max-time 30 \
  --get "$HTTP_URL/send" \
  --data-urlencode 'username=acme_http' \
  --data-urlencode "password=$HTTP_PASSWORD" \
  --data-urlencode 'to=15551234567' \
  --data-urlencode 'content=Synevyr DLR test' \
  --data-urlencode 'dlr=yes' \
  --data-urlencode 'dlr-level=3' \
  --data-urlencode 'dlr-method=POST' \
  --data-urlencode \
    'dlr-url=https://messaging.acme.example/synevyr/dlr'
```

The levels mean:

| Requested | What the customer receives |
|---:|---|
| `1` | One level-1 callback with the upstream `submit_sm_resp` status. |
| `2` | A level-2 callback after a successful submit and carrier receipt. |
| `3` | A level-1 callback, then a level-2 callback if a terminal receipt follows. |

These sequences are implemented in the DLR correlator
(`docs/api/callbacks.md:83`). Level `1` with `message_status=ESME_ROK` means the
SMSC accepted the submit; it does not mean the handset received it. A terminal
level `2` with `message_status=DELIVRD` is the positive delivery evidence
supplied by the carrier.

The customer's HTTP receiver must accept form-encoded GET or POST fields and
return:

```http
HTTP/1.1 200 OK
Content-Type: text/plain

ACK/Jasmin
```

The status must be 2xx and the body, after surrounding whitespace is removed,
must be exactly `ACK/Jasmin`
(`docs/api/callbacks.md:10`). An empty 200, `OK`, JSON, wrong capitalization, or
a redirect is not a valid integration response. The current thrower
incorrectly accepts a 3xx with that body because it rejects only status 400 and
above (`internal/core/dlr/http_thrower.go:117`); customers must not depend on
that defect.

Every callback includes `id`, actual `level`, `message_status`, and `connector`.
Level 2 also includes `id_smsc`, submitted and delivered counts, dates, error,
and text (`internal/core/dlr/http_thrower.go:65`). The receiver must make
processing idempotent by `id` plus actual `level`: failed acknowledgements cause
redelivery. The default is the initial attempt plus three retries, 30 seconds
apart, then purge (`docs/api/callbacks.md:231`).

For SMPP customers, `registered_delivery` controls receipt selection and the
receipt returns as `deliver_sm` to the original `system_id`. It has
`esm_class=0x04`, Appendix-B receipt text, and the
`receipted_message_id` and `message_state` TLVs
(`docs/api/smpp.md:372`). The ESME must respond with `deliver_sm_resp`; otherwise
the server-side delivery window cannot complete.

To decide whether an HTTP receipt was delivered, correlate three places:

1. the customer's receiver log shows the exact message `id`, level, and a 2xx
   `ACK/Jasmin` response;
2. the `dlr-thrower` log has no retry or final throw error for that delivery; and
3. for terminal delivery, the level-2 status is `DELIVRD`.

Not verified: there is no operator-facing REST, web, or jCli query for DLR
callback acknowledgement history. A successful throw is acknowledged to AMQP
without a success log (`internal/core/dlr/thrower_consumer.go:101`), while only
throw failures are attached to the component logger
(`internal/app/gateway/runtime.go:693`). The CDR repository records a final
carrier receipt before correlation cleanup
(`internal/app/gateway/runtime.go:618`,
`internal/core/dlr/correlation.go:333`), but the CDR service is not mounted on
any admin route in `internal/app/adminweb/server.go:123` or
`internal/app/admin/handler.go:38`. Use the receiver access log as the
authoritative callback-ACK evidence until a management query is added.

## 7. Day-two operations

### Suspend a customer who has not paid

For an HTTP-only customer, open **Users**, edit the account, set **Disabled**,
and save. New authentication attempts immediately fail. Disabling a group also
blocks every member (`internal/app/outbound/config.go:968`). The jCli equivalent
is:

```text
user -d acme_http
```

For an SMPP customer, disabling the outbound user blocks submits but does not
terminate an already authenticated bind. In **SMPPs Binds**, use **Ban**; it
sets the bind account disabled and explicitly unbinds current sessions
(`internal/app/adminweb/handlers_smpps_users.go:207`). The jCli equivalent is:

```text
user --smpp-ban=acme_smpp
```

Editing or deleting a bind account alone affects new binds only; existing
sessions are unaffected until explicitly unbound
(`internal/app/smppsserver/directory.go:104`).

### Change a rate or fund an account

![One rate split between submit time and SMSC acceptance](../assets/diagrams/charging-split.svg)

On a prepaid account the whole rate is taken when the message is admitted. With
`early_decrement_balance_percent` set, only that share is taken at submit and the
remainder is applied after the SMSC accepts. The early share is **not refunded**
when the SMSC rejects the message.

Edit the admin-managed route in **MT Routes** and save its complete rate and
filter contract. The replacement is live. A route rate applies per emitted
segment (`internal/core/billing/billing.go:552`). A config-managed route is
read-only in the admin plane; edit JSON and restart.

To top up a user, edit its finite balance or submit count in **Users**. Changing
the provisioned quota is the explicit reset/top-up gesture; changing unrelated
fields preserves the already spent-down live value
(`internal/app/outbound/config.go:580`). Confirm the effective value with
`/balance` after the save. If the user and group both have finite ceilings,
check and top up both.

### Rotate a credential

For HTTP, edit **Users**, enter a new plaintext password, save, test one request
with it, then revoke the old value at the customer's secret store. Leaving the
web password blank preserves the current hash
(`internal/app/adminweb/handlers_users.go:326`). The new hash is used by the
next HTTP request.

For SMPP, edit the record in **SMPPs Binds**, enter an eight-character
replacement, save, and use **Unbind**. Existing sessions do not reauthenticate
when the directory snapshot changes
(`internal/app/smppsserver/directory.go:109`). Coordinate the forced reconnect
window with the customer. Do not rotate only the outbound user's HTTP password:
the SMPP bind reads the separate bind-record password.

For a carrier, edit **Connectors** and enter the new partner password. The web
form keeps the stored password only when the field is blank
(`internal/app/adminweb/handlers_connectors.go:109`). Keep the old connection in
maintenance until the partner activates the new credential, then confirm the
connector returns to `BOUND`.

### Move traffic between carriers

Provision and start the destination carrier first. Wait for `BOUND`, add it to
the route's `connector_ids` ahead of the old carrier, then send a controlled test
and inspect its `cid` in the submit audit line. Because candidate selection uses
the first available connector, the new carrier takes new traffic immediately
after the live route replacement (`internal/app/outbound/runtime.go:1043`).

Keep the old connector running as the second candidate during observation.
After the cutover is accepted, remove it from the route and stop it:

```sh
curl --fail-with-body --connect-timeout 5 --max-time 15 \
  -X POST \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$ADMIN_URL/admin/connectors/carrier-a/stop" |
python3 -c '
import json
import sys
value = json.load(sys.stdin)
value.get("config", {})["password"] = "REDACTED"
print(json.dumps(value, indent=2))
'
```

Changing the config-owned default route still requires a JSON change and
restart; use a higher-order live route for a reversible incident override.

### Answer “was my message delivered?”

Start with the exact queue message ID returned to the customer. Search the
gateway submit audit:

```sh
export MESSAGE_ID='86d15f1a-f75b-43f0-ab2e-a9bbde7acdc8'
docker compose -f docker-compose.prod.yml logs --since=24h gateway |
  grep -F "[queue-msgid:$MESSAGE_ID]"
```

The success line contains connector ID, queue ID, SMSC ID, status, addressing,
DLR request, and content or its privacy-protected size
(`internal/core/smppc/mtlog.go:14`). It is written after the durable final
`submit_sm_resp` commit (`internal/core/smppc/session.go:891`).

Interpret it narrowly:

| Evidence | What it proves |
|---|---|
| `/send` returned `Success` | Synevyr durably admitted the logical message. |
| Audit status is `CommandStatus.ESME_ROK` | The routed SMSC accepted the submit. |
| Audit status is `ERROR/...` | The SMSC rejected it; inspect status and retry flag. |
| HTTP level-2 `DELIVRD` was ACKed | Carrier reported delivery; callback accepted. |
| SMPP `stat:DELIVRD` was ACKed | Carrier reported delivery; ESME accepted it. |

The web **Operations** message lookup is not a delivery log. Its
`RESULT_COMMITTED` state means every part has a durable submit response
(`internal/core/submittransaction/model.go:95`), even though the UI currently
renders it with a positive badge (`web/src/pages/operations.tsx:441`). That
presentation can be mistaken for handset delivery and should be treated as a UI
defect.

**The direct answer now exists.** Look the gateway message ID up against the
durable commercial record, which distinguishes SMSC acceptance from final
delivery and carries what was charged:

```sh
# Web console: Billing → Usage, "Message ID" field. Or over the admin API:
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$ADMIN_URL/admin/cdrs?message=$MESSAGE_ID" | jq '.records[]'

# The full lifecycle of one part, admission through terminal receipt:
curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$ADMIN_URL/admin/cdrs/$MESSAGE_ID/000001/events" | jq
```

A message ID returns every part of that message, each with its own
`state` (submission), `delivery_state` (handset outcome, empty while pending)
and `charged_total`. Multipart messages are charged per part, so a two-part send
returns two rows.

Both surfaces read through the audited CDR service, so each lookup is recorded
in `cdr_access_audit` — console reads under the operator's username, admin API
reads under `admin-api`.

The submit audit log above remains useful for what the CDR deliberately does not
store: destination, source and content.

The modern submit and DLR Prometheus series are still inert
(`docs/operations/monitoring.md:52`), so do not read their zero values as "no
failures" -- read them as "not measured". The per-connector SMPPc legacy counters
ARE live: bind, disconnect, submit request/accept/throttle/other-failure, inbound
deliver_sm and data_sm, and enquire_link all count. The per-connector *clocks*
(`connected_at`, `bound_at`, `last_*_pdu_at`) remain untracked and render `ND`. Connector state populated by readiness probes, live HTTP
front-door counters, SMPPs session counters, and component logs are the useful
current signals.

## 8. Choose the control surface

Use the web console for normal human changes. It covers the widest resource set,
shows config-managed records as read-only, and keeps user, bind, and connector
passwords write-only
(`internal/app/adminweb/server.go:140`,
`internal/app/adminweb/handlers_connectors.go:10`). Avoid the **Partner
Onboarding** page for provisioning: its current submit action only constructs
local UI state and does not call a server resource
(`web/src/pages/partner-onboarding.tsx:216`).

Use jCli for legacy runbooks and an emergency terminal workflow. Connect to the
private listener:

```text
telnet 127.0.0.1 8990
```

The console supports `group`, `user`, `smppccm`, `mtrouter`, `morouter`,
`filter`, and `httpccm` managers
(`internal/app/jcli/help_text.go:27`). Each service call has a ten-second
context timeout (`internal/app/jcli/managers.go:22`). Be careful with
`smppccm -s`: the show output includes the connector password in plaintext
(`internal/app/jcli/managers_smppccm.go:333`).

Use the token REST API for reviewed automation of connectors, MT routes, and
users only. It has no endpoints for groups, SMPPs bind users, MO routes, saved
filters, HTTP destinations, or profiles
(`internal/app/admin/handler.go:38`). PUT operations take complete raw objects;
the token user GET response does not contain enough state to reconstruct a safe
user PUT (`internal/app/admin/handler.go:278`). Keep a source-controlled,
secret-free desired-state payload for automation.

| Task | Recommended surface | Live? |
|---|---|---:|
| Create/edit users and groups | Web | Yes |
| Fund balance or count | Web, then `/balance` | Yes |
| Create/ban/unbind SMPPs users | Web | Yes; explicit unbind for existing sessions |
| Create/edit/start/stop SMPPc connectors | Web or token API | Yes |
| Create/edit positive-order MT routes | Web or token API | Yes |
| Create/edit MO routes | Web | Yes |
| Saved filters and HTTP destinations | Web | Yes; existing routes keep copies |
| Legacy scripted operation | jCli | Yes |
| Listener addresses, TLS listeners, DSNs, HA, logs, workers | JSON/environment | No |
| Config-owned users, routes, groups, connectors, binds | JSON/environment | No |

Admin-managed state is persisted and replayed after restart, while static JSON
keeps ownership of its configured names and orders
(`docs/operations/configuration.md:435`). Listener addresses, listener TLS,
database and broker URLs, HA, logging, worker policy, and every static
config-owned entity have no runtime setter and require restart
(`docs/operations/configuration.md:451`).

Before and after a restart-bound change, check readiness and inspect individual
checks rather than treating every 503 alike:

```sh
curl --fail-with-body --connect-timeout 5 --max-time 10 \
  'http://127.0.0.1:8405/ready'
```

`ok` admits traffic; `starting`, `degraded`, and `broken` are not ready. The
health response identifies PostgreSQL, AMQP, codec, and required connector
checks (`docs/operations/monitoring.md:161`). A readiness scrape also refreshes
the connector gauges; scraping Prometheus alone does not
(`docs/operations/monitoring.md:43`).

After any live change, verify the effective state through a different path:
check a user with `/balance`, a route with a controlled test submit, a connector
by observed `BOUND`, an HTTP DLR in the receiver log, and an SMPPs change with a
fresh bind. Saving a form is not the end of the operation.

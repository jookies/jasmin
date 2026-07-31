# Getting started

- **Date:** 2026-07-30
- **Status:** active
- **Summary:** Your first hour with Synevyr, hands on. Start the stack, send your
  first message upstream, receive your first message inbound, receive a delivery
  receipt, and terminate traffic to your own application — all on one machine,
  with no carrier account and no real SMSC.

This is a tutorial: type the commands, see the output. Everything runs locally
against a bundled SMSC simulator, so nothing you do here can reach a real
network or cost money.

If you want the concepts first — what MT, MO, a bind and a transceiver actually
are — read [learning-path.md](learning-path.md). This guide names them as it goes
but does not explain them from scratch.

**Time:** about an hour, most of it waiting for Docker.

---

## What you need

- **Docker** with the Compose v2 plugin (`docker compose version`). Docker
  Desktop on macOS/Windows, Docker Engine on Linux.
- **curl** and **python3** — both already present on macOS and most Linux.
- Roughly **4 GB** of free RAM and 10 GB of disk for the images.

You do **not** need Go, Node, a carrier account, or an SMPP client.

---

## Step 1 — Start the stack

```console
$ scripts/dev.sh up
```

This builds the gateway image and starts five containers: the gateway,
PostgreSQL, RabbitMQ, Redis, and a fake SMSC that accepts any bind and answers
`ESME_ROK` to everything. First run takes a few minutes; later runs are seconds.

When it finishes you get an endpoint summary:

```
  HTTP API      http://127.0.0.1:1401      user smppuser / password
  REST daemon   http://127.0.0.1:8080
  Admin web UI  http://127.0.0.1:8404      admin / Welcome1!
  jCli console  nc 127.0.0.1 8990          jcliadmin / dev-jcli-password
  SMPPs bind    127.0.0.1:2775             shortcode-app / shortcodepw
  Fake SMSC     http://127.0.0.1:8288/inject/mo  (and /inject/dlr)
```

### Verify it is actually ready

```console
$ curl http://127.0.0.1:1401/health
{"status":"ok","ready":true,"checks":{"amqp":"ok","bridge":"ok","connector:smsc-primary":"bound","postgres":"ok"}}
```

Read that payload rather than trusting the container status. `ready:true` means
every dependency answered **and** every required connector is bound. In
particular `"connector:smsc-primary":"bound"` is your first SMPP session: the
gateway connected out to the fake SMSC as an ESME and bound as a transceiver.
That is the connection you would otherwise be making to a carrier.

If it never becomes ready: `scripts/dev.sh logs gateway`.

---

## Step 2 — Send your first message upstream

The HTTP front door is the fastest way to submit. This is an **MT** message —
one travelling toward a handset.

```console
$ curl "http://127.0.0.1:1401/send?username=smppuser&password=password&to=15551234567&from=1111&content=hello"
Success "3ccd9de7-a5f3-4411-b812-6bb3d48c1b73"
```

The UUID is the **gateway message id**. Keep it — it is how you correlate
everything that happens to this message afterwards.

Now watch it arrive at the other end:

```console
$ docker logs synevyr-dev-smppsim-1 --tail 3
2026/07/31 03:24:56 172.27.0.5:50370: bind 0x9 system_id="smppclient1" accepted
2026/07/31 03:26:38 172.27.0.5:50370: submit_sm seq=5 -> ESME_ROK message_id=fake-10
```

That is the whole outbound path working: HTTP front door → authentication →
MT routing → the `smsc-primary` connector → `submit_sm` on the wire → the SMSC
accepting it.

`scripts/dev.sh smoke` does exactly this call if you want to repeat it.

> **`ESME_ROK` means accepted, not delivered.** The SMSC took the message. See
> [learning-path.md §1.3](learning-path.md#13-mt-mo-and-delivery-receipts) for
> why that distinction ends up mattering more than anything else in this domain.

### Where did it go, and why?

It matched the **default MT route** (order 0) in `configs/gateway.example.json`,
which points at `smsc-primary`. A destination starting with `9` would have
matched a higher-priority route instead — you will use that in Step 5.

---

## Step 3 — Receive an inbound message (MO)

Traffic in the other direction. An **MO** is a message *from* a handset.

The dev config's default MO route forwards inbound messages to an HTTP webhook
at `http://host.docker.internal:8899/mo`, so start something that listens there
first — otherwise the gateway logs a connection refused and retries.

Save this as `mo_listener.py` and run it in a second terminal:

```python
import http.server, urllib.parse

class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        print(f"MO from={q.get('from',[''])[0]} to={q.get('to',[''])[0]} "
              f"content={q.get('content',[''])[0]}", flush=True)
        # Exactly this body, or the gateway treats it as a failure and retries.
        body = b"ACK/Jasmin"
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    do_POST = do_GET
    def log_message(self, *a): pass

http.server.HTTPServer(("0.0.0.0", 8899), H).serve_forever()
```

```console
$ python3 mo_listener.py
```

Now create inbound traffic. The fake SMSC has an inject trigger, so you do not
need a handset:

```console
$ curl -X POST "http://127.0.0.1:8288/inject/mo?from=380671234567&to=1111&text=first%20inbound"
deliver_sm seq=3 sent
```

Your listener prints:

```
MO from=380671234567 to=1111 content=first inbound
```

and the gateway logs the message on its way through:

```console
$ docker logs synevyr-dev-gateway-1 --tail 5 | grep SMS-MO
2026-07-31 05:54:51 INFO 1 SMS-MO [cid:smsc-primary] [queue-msgid:745517e6-…] [status:CommandStatus.ESME_ROK] [from:b'380671234567'] [to:b'1111'] [content:b'first inbound']
```

That is the inbound path: the simulator sent a `deliver_sm` over the bound
session, the gateway classified it as MO (not a receipt — see
[learning-path.md §1.3](learning-path.md#13-mt-mo-and-delivery-receipts)),
matched it against the MO route table, and threw it at your webhook.

> **`ACK/Jasmin` is not optional.** Your endpoint must return HTTP 2xx **and** a
> body that is exactly `ACK/Jasmin` after trimming. A 2xx with an empty body,
> `OK`, or JSON is a *failure* and will be retried. This is inherited
> compatibility behaviour, not a preference — see
> [api/callbacks.md](api/callbacks.md).

### Now watch an interceptor reject one

The dev config also ships an **MO interceptor** matching `.*STOP`. Send that:

```console
$ curl -X POST "http://127.0.0.1:8288/inject/mo?from=380671234567&to=1111&text=STOP"
```

Your listener prints nothing, and the gateway says:

```
INFO 1 MO message [queue-msgid:b8f443ae-…] rejected by interceptor, not routed
```

The message was stopped *before* routing. That is what an interceptor is for —
opt-out handling, content policy, rewriting — and it is also the first thing to
suspect when inbound traffic silently does not arrive. See §2.12 of the
[learning path](learning-path.md#212-interceptors).

---

## Step 4 — Receive a delivery receipt

A **DLR** tells you what became of an MT message. Trigger one:

```console
$ curl -X POST "http://127.0.0.1:8288/inject/dlr?id=fake-10&stat=DELIVRD&from=1111&to=380671234567"
```

The gateway correlates the receipt back to the original submit using the SMSC's
message id (`fake-10` above — the one `submit_sm_resp` returned), and forwards it
to the DLR URL that was registered at submit time.

> **If you did not ask for a receipt, you will not get one**, and the gateway
> will log `no DLR map for msgid` because there is nothing to correlate against.
> That log line during this tutorial is expected, not a fault. To request one,
> pass `dlr-url` and `dlr-level` on `/send` — see [api/http.md](api/http.md).

---

## Step 5 — Terminate traffic to your own application

So far the gateway has been a **forwarder**: it relays to an upstream SMSC. The
other mode is being the **destination** — a partner submits to you, and your own
application is what the message is for. That is a *termination connector*.

![How a downstream application receives terminated messages](assets/diagrams/termination-sinks.svg)

The dev config already has one (`terminate-local`) and an MT route that sends any
destination starting with `9` to it.

```console
$ scripts/dev.sh spool
==> GET http://127.0.0.1:1401/send?username=smppuser&password=password&to=99991234567&from=1111&content=your%20code%20is%2063125
Success "f1c48d95-00a1-439e-b0fc-43385d750208"
==> Terminated locally. Read it back with: scripts/dev.sh messages
```

Nothing left the machine. The message was decoded, reassembled, given a verdict,
written to the 24-hour spool, and its receipt synthesised.

Read it back the way a downstream application would:

```console
$ scripts/dev.sh messages
--------------------------------------------------------------------
     message_id: f1c48d95-00a1-439e-b0fc-43385d750208
    received_at: 2026-07-31T03:25:06.942Z
      connector: terminate-local
        partner: smppuser
           from: 1111
             to: 99991234567
       encoding: utf-8
          parts: 1
        verdict: delivrd
 delivery_state: pending
           text: your code is 63125
--------------------------------------------------------------------
1 message(s), next_cursor=eyJ2IjoxLCJzZXEiOjR9
```

That command mints a scoped read token and calls `GET /messages` — the **pull**
path. `delivery_state: pending` is correct here: `terminate-local` has no push
endpoint, so nothing is being pushed and the row waits to be pulled.

### Push it to a real endpoint instead

To see the **push** path, give a termination connector a delivery endpoint. The
quickest way is a throwaway webhook:

1. Open <https://webhook.site> and copy the unique URL it gives you.
2. In the console (<http://127.0.0.1:8404>) go to **Termination → Termination
   connectors**, create one, and paste that URL as its delivery endpoint.
3. Add an MT route pointing at it (**Messaging → MT routes**).
4. Send a message matching that route.

Each POST arrives at webhook.site with the full body and the four
`X-Synevyr-*` headers. **Treat that URL as a secret** — anyone holding it can
read your message content — and point only test traffic at it.

Full contract: [api/messages.md](api/messages.md).

---

## Step 6 — Bind an ESME, like a customer would

Everything above used HTTP. Now play the *other* role: your customer connecting
in over SMPP, with this gateway acting as the SMSC.

```console
$ scripts/dev.sh partner
```

This runs the bundled partner simulator: it binds to `127.0.0.1:2775` as
`shortcode-app`, submits, waits for receipts, prints them and unbinds. It needs
Go (`go run`), so skip it if you have no Go toolchain — nothing later depends on
it.

To bind your own client instead, the details are in
[api/smpp.md](api/smpp.md). One thing to know before you try:

> **SMPP 3.4 caps the bind password at 8 characters.** A longer one cannot be
> encoded by a conformant ESME, so the bind fails before authentication is
> attempted, with an error that will not mention password length.

---

## Step 7 — Look around the console

<http://127.0.0.1:8404>, `admin` / `Welcome1!`.

| Where | What it answers |
| --- | --- |
| **Overview → Control room** | is the gateway healthy, are connectors bound |
| **Messaging → Connectors** | the outbound sessions to carriers |
| **Messaging → MT routes** | which connector an outbound message goes to |
| **Messaging → MO routes** | where inbound messages are delivered |
| **Termination → Messages** | the spool — what terminated here, with content |
| **Termination → Read tokens** | credentials your application uses to pull |
| **Access → Gateway users** | who may submit |
| **Billing** | balances, usage, statements |
| **Learn** (top bar) | the full curriculum, with figures |

There is also a jCli console (`nc 127.0.0.1 8990`, `jcliadmin` /
`dev-jcli-password`) which mirrors the legacy Jasmin console commands.

---

## Stopping and cleaning up

```console
$ scripts/dev.sh stop     # stop containers, keep everything
$ scripts/dev.sh down     # remove containers, KEEP data volumes
$ scripts/dev.sh reset    # remove containers AND volumes — destroys local data
```

`reset` asks for confirmation. It deletes the PostgreSQL volume (submits, CDRs)
and the admin SQLite database (users, connectors and routes you created in the
console).

---

## What this tutorial did not do

Being clear about the gap between this and production:

- **The fake SMSC is not a carrier.** It accepts every bind and `ESME_ROK`s every
  submit, so a healthy-looking local stack proves your configuration parses and
  routes — not that anything would be delivered.
- **No TLS anywhere.** Every listener here is plain HTTP/TCP on loopback.
- **The credentials are published in this repository.** They are dev fixtures.
- **No throughput limit is being enforced meaningfully**, because the simulator
  never pushes back.

## Next

| You want to | Read |
| --- | --- |
| Understand the protocol and the domain | [learning-path.md](learning-path.md) |
| Deploy this for real | [operations/new-deployment.md](operations/new-deployment.md) |
| Run the published image | [../README.md](../README.md) |
| Every HTTP parameter | [api/http.md](api/http.md) |
| Receive terminated messages | [api/messages.md](api/messages.md) |
| Fix something broken | [runbooks/](runbooks/) |

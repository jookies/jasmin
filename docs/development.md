# Development

For people changing the code. Read [`architecture.md`](architecture.md) first —
it explains the layering this document assumes.

## Getting a build

```console
$ go build ./...
$ go vet ./...
$ go test ./...          # 198 test files, 897 tests, no external dependencies
$ go test -race ./...
```

That is the whole loop. The test suite needs no broker, database or network,
which is deliberate: domain logic lives in `internal/core` and is written to be
testable without infrastructure. If a change makes a `core` package need a
socket, that is a signal the logic drifted into the wrong layer.

Go 1.26.5 is what CI uses (`.github/workflows/ci.yml`).

## Tests that need services

Thirteen test files reach for real infrastructure and **skip cleanly** when it is
absent, so a bare `go test ./...` stays green. To actually run them:

```console
$ docker run -d -p 5672:5672 rabbitmq:3-alpine
$ docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=synevyr postgres:16-alpine

$ AMQP_URL=amqp://guest:guest@localhost:5672/ \
  TEST_POSTGRES_DSN=postgres://postgres:postgres@localhost:5432/synevyr?sslmode=disable \
  go test -count=1 -run TestGatewayHTTPToDurableSMPPResponse ./internal/app/gateway/
```

That one test is the highest-value check in the repository: HTTP submit → routing
→ billing → queue → SMPPc → SMSC → `submit_sm_resp` → DLR, through real RabbitMQ
and PostgreSQL.

Two gotchas that cost real time:

- **The E2E asserts on accumulated state.** Run it twice against the same
  database and the balance assertion fails, because the first run already
  charged. Recreate the database, or use a fresh vhost and DB per run.
- **`docker compose up -d` does not reload a bind-mounted config.** After editing
  `configs/gateway.json` you need `restart`; after editing `.env` you need
  `up -d` to recreate with the new environment.

## Interop testing — the gate that matters most

Every other test drives our server with our own client helpers. That proves the
two halves agree with each other; it **cannot** catch a misreading of SMPP 3.4
held on both ends of the wire. Seven of the fourteen protocol defects found
during hardening were invisible to a green suite for exactly that reason.

`scripts/interop/` removes our code from one end:

```console
$ pip install smpp-pdu3 twisted
$ PYTHON_PATH=python go test -count=1 -run TestThirdPartyESME ./internal/core/smpps/
```

That drives the gateway with `smpp.twisted` — an independent library sharing no
code with this project. **If you change anything in `internal/core/smpps` or
`internal/transport/smppwire`, run this.** Unit tests will not tell you that you
broke conformance.

The other direction, `scripts/interop/smsc_probe.py`, is an independent SMPP
*server* and doubles as a **carrier emulator**: throttling, delayed and
out-of-order receipts, injected error statuses, mid-session disconnects. Use it
for behaviour a well-mannered simulator will never produce.

```console
$ python scripts/interop/smsc_probe.py --port 2775 --system-id u --password p \
    --throttle-after 10 --dlr-delay 2 --drop-after 50
```

## Fuzzing

Twelve fuzz targets guard the parsers and anything doing arithmetic on
attacker-influenced input:

```console
$ go test -fuzz FuzzDecodeNeverPanics -fuzztime 60s ./internal/transport/smppwire/
```

`FuzzDecodeNeverPanics` is the important one — it fuzzes the SMPP PDU decoder,
which is the only code reachable by an unauthenticated peer. It has survived tens
of millions of executions. Add a target rather than a table test when you write
new parsing.

## Conventions

**Comments explain why, not what.** The codebase is dense with inherited
behaviour that looks wrong until you know the reason, so a comment that says
"this reproduces the reference's off-by-one because customers depend on it" is
worth more than one describing the loop.

**Errors wrap with context** — `fmt.Errorf("doing x: %w", err)` — and are matched
with `errors.Is`/`errors.As`. One instance of this mattering: `smppwire.Read`
originally formatted its I/O error with `%v`, so callers could not distinguish a
read timeout from a dead socket, which silently broke keepalive handling.

**Never weaken a test to make a change pass.** If a test encodes wrong
behaviour, change it *and* say why in the commit message. Several tests in here
deliberately assert odd behaviour, and the comment above them explains which
customer depends on it.

**Prefer explicit over clever.** `internal/app/gateway/runtime.go` is long
because all the wiring is in one readable place. That is a choice, not neglect.

## Before you change protocol behaviour

Check [`reference/legacy-behaviours.md`](reference/legacy-behaviours.md) first.
It documents 22 inherited behaviours, several of which are bugs matched on
purpose. A throughput quota of `0` meaning *unlimited*, `ACK/Jasmin` as the
required callback body, `ND` for unlimited values — each looks like a defect and
each is load-bearing for someone's integration.

If you decide to diverge anyway, record it in
[`reference/deviations.md`](reference/deviations.md) with the reason. The point is
that the next person finds a decision rather than a mystery.

## What CI enforces

`.github/workflows/ci.yml`, three jobs:

1. **`go`** — build, vet, `go test`, `go test -race`, and the gateway E2E against
   real RabbitMQ and PostgreSQL service containers.
2. **`smpp-interop`** — the third-party SMPP conformance gate.
3. **`adminweb-bundle-freshness`** — fails if `internal/app/adminweb/dist` is
   stale relative to `web/src`.

That last one catches a real trap: the admin console is embedded with `go:embed`,
so **editing `web/src` without rebuilding ships the old UI**. Run
`cd web && npm run build` and commit the result. The check compares a source hash
rather than the bundle bytes, because the minifier renames identifiers between
environments — a byte comparison would fail on an untouched bundle and pass on a
stale one.

CodeQL analyses Go on a schedule and on pushes to `master`.

## Repository layout

```
cmd/synevyr-gateway/      the gateway binary
cmd/synevyr-fake-smsc/    SMSC simulator, used by tests and first boot
internal/core/            protocol and domain logic (no infrastructure)
internal/app/             wiring; runtime.go is the composition root
internal/transport/       codecs and clients: SMPP wire, AMQP, pickle, Redis
internal/infra/storage/   PostgreSQL repositories
internal/config/          legacy INI config parsing (--legacy-cfg)
web/                      React admin console, embedded via go:embed
scripts/interop/          third-party SMPP probes and the carrier emulator
scripts/deploy/           setup, backup, restore
docs/                     this documentation
deploy/                   alert rules, backup runbook
```

## Adding a plan or a decision

Non-trivial work gets a plan in [`plans/`](plans/) before implementation;
architectural choices get an ADR in [`adr/`](adr/) after. Both are numbered and
append-only — a superseded document gets its status changed and a link to its
replacement rather than a rewrite, so the reasoning behind a past decision
survives it.

Plans may lag the code. When they disagree, the code wins and the plan should be
corrected.

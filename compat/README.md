# Executable compatibility harness

This directory freezes observable behavior from Python Jasmin baseline
`0aac58e466d583d0f0436df7b8afa3dc96191263` before Go implementation begins.

## Verify the frozen oracle tree

```bash
python3 scripts/compat/verify_baseline_tree.py
```

The runner and CI refuse to execute if any file under `jasmin/`, `tests/`,
`misc/config/`, or the pinned dependency metadata differs from the recorded
baseline aggregate hash.

## Run the upstream regression suite

```bash
scripts/compat/run_baseline_tests.sh
```

Every invocation uses an isolated Compose project. The Python oracle and
compatibility tools are copied into the image; the live checkout is not mounted
as executable source. Only `compat/fixtures/` is mounted for recorder output.
Python, Redis and RabbitMQ images are digest-pinned, Python dependencies are
installed with `--require-hashes` from `requirements-baseline.lock`, and broker
data uses tmpfs. Regenerate locks from the reviewed `requirements-*.in` inputs.
Set
`KEEP_COMPAT_SERVICES=1` only when debugging.

## Regenerate all oracle fixtures

```bash
scripts/compat/capture_all.sh
```

Generated contracts:

| Surface | Fixture | Cases |
|---|---|---:|
| HTTP | `fixtures/http/baseline.json` | 9 |
| SMPP wire | `fixtures/smpp/baseline.json` | 7 |
| AMQP broker round-trip content | `fixtures/amqp/baseline.json` | 7 |
| Redis state | `fixtures/redis/baseline.json` | 4 |

Coverage is tracked in `../spec/compatibility/FIXTURE_COVERAGE.csv`; partial cases are not treated as parity completion.

## Verify committed fixtures

```bash
python3 scripts/compat/verify_fixtures.py
```

Full JSON Schema validation uses the pinned tooling dependency:

```bash
python3 -m pip install --require-hashes -r compat/requirements-tools.txt
python3 scripts/compat/validate_json_schemas.py
```

## Compare a Go implementation result

The Go recorder must emit the same schema and stable case ordering:

```bash
python3 scripts/compat/diff_golden.py \
  compat/fixtures/http/baseline.json \
  build/go-http-actual.json
```

Exit `0` means exact match. Exit `1` prints JSON-pointer mismatches.

## Safety boundary

AMQP fixtures are captured after a real Jasmin publish/RabbitMQ/consume round
trip. AMQP and Redis fixtures intentionally preserve legacy pickle bytes for
the trusted migration bridge. The standard-library validator never unpickles
fixture input: it checks safe pickle opcodes and trusted whole-case fingerprints.
Production Go code must not decode arbitrary pickle. The bridge is allowlisted,
isolated and temporary as specified by ADR-0001.

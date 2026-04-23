# `interceptor_hash_tlv.py` — Per-user TLV profile interceptor

An MT interceptor that applies a per-user "TLV profile" to every outbound
`submit_sm`. The profile (TAG, VALUE, optional TYPE) lives in a simple CSV
file. On each submit the interceptor:

- **adds** any profile TLV missing from the caller's PDU
- **replaces** any PDU TLV whose tag is in the profile with the profile's value
- **validates** the caller's value against the profile, recording mismatches
- **caches** the parsed profile in memory and **hot-reloads** on file mtime change
- looks up the user's row in **O(1)** (dict keyed by username)
- is **thread-safe** (guarded by an RLock injected by the interceptor engine)

TLVs whose tag is *not* in the profile are passed through untouched.

Files:
- [`interceptor_hash_tlv.py`](./interceptor_hash_tlv.py) — the interceptor script
- [`tlv_profiles.csv`](./tlv_profiles.csv) — sample profile file

---

## 1. How it works

The interceptor engine loads the script via `eval()` with a restricted
namespace. On every submit, the engine injects these names into the script:

| name | what it is |
|---|---|
| `routable` | `RoutableSubmitSm` — the message being submitted |
| `smpp_status` / `http_status` | set to an int to reject |
| `extra` | dict — keys surface in the interceptor log |
| `hashlib`, `re`, `json`, `datetime`, `math`, `struct`, `os` | pre-imported stdlib |
| `cache` | dict that persists across script runs (shared across all scripts) |
| `cache_lock` | `threading.RLock` for safe cache access |

The script reads `routable.user.username` and looks it up in the parsed
profile dict. For each profile TLV, it either keeps, replaces, or adds it
on `routable.pdu.custom_tlvs`. Any caller-supplied TLVs with tags not in
the profile are untouched.

The `listeners.py` dispatch-time logic then runs `resolve_tlv_types()` to
stamp the wire encoding type on each TLV (from the smppcc `custom_tlvs`
config if the profile didn't specify one), and `encodeRawParams()` puts
them on the SMPP wire.

---

## 2. Install and wire up

### 2.1. Deploy the script and sample profile

Bind-mount setup (recommended — survives container rebuilds):
```bash
mkdir -p ~/jasmin-data/config/resource

cp misc/scripts/interceptor_hash_tlv.py ~/jasmin-data/config/resource/
cp misc/scripts/tlv_profiles.csv        ~/jasmin-data/config/resource/

docker run -d --name jasmin-tlv --network jasmin-tlv-net \
  -e REDIS_CLIENT_HOST=jasmin-redis -e AMQP_BROKER_HOST=jasmin-rabbit \
  -v ~/jasmin-data/config:/etc/jasmin \
  -v ~/jasmin-data/store:/etc/jasmin/store \
  -v ~/jasmin-data/logs:/var/log/jasmin \
  -p 2775:2775 -p 8990:8990 -p 1401:1401 \
  jasmin:0.12-tlv
```

Or for an already-running container without bind mounts:
```bash
docker cp misc/scripts/interceptor_hash_tlv.py jasmin-tlv:/etc/jasmin/resource/
docker cp misc/scripts/tlv_profiles.csv        jasmin-tlv:/etc/jasmin/resource/
```

### 2.2. Register the interceptor in jCli

```
telnet localhost 8990                        # jcliadmin / jclipwd
jcli : mtinterceptor -a
> type DefaultInterceptor
> script /etc/jasmin/resource/interceptor_hash_tlv.py
> order 100
> ok
jcli : persist
```

Verify:
```
jcli : mtinterceptor -l
```
You should see the interceptor registered with `order 100`.

### 2.3. Declare the TLV encoding rules on the connector

The interceptor only supplies *values*. The **types and max lengths** used on
the wire still come from the SMPP connector's `custom_tlvs` rule set, unless
the profile specifies a type inline (`TAG:VALUE:TYPE`).

```
jcli : smppccm -u smalert
> custom_tlvs 0x1400,OctetString,20,required;0x1401,OctetString,20,required;0x1402,OctetString,64,optional
> ok
jcli : smppccm -0 smalert
jcli : smppccm -1 smalert
jcli : persist
```

`required` here means "dispatch-time validation will reject the submit if
the PDU does not carry this tag" — which won't happen for profile users,
because the interceptor adds whatever's missing.

---

## 3. Profile file format

Location: **`/etc/jasmin/resource/tlv_profiles.csv`** (inside the container).
If you bind-mount, edit it on your Mac — the interceptor picks up changes on
the next submit (no restart).

Row grammar:
```
<username> ; TAG:VALUE[:TYPE] ; TAG:VALUE[:TYPE] ; ...
```

- Lines beginning with `#` and blank lines are ignored.
- `TYPE` is optional. When present it must be one of
  `Int1 Int2 Int4 Int8 OctetString COctetString`. Either order works —
  `TAG:VALUE:TYPE` and `TAG:TYPE:VALUE` both parse, because the parser
  detects the type token wherever it sits.
- Values can contain `:` — only the type token is pulled out; the rest is the
  value.
- Numeric tags accept hex (`0x1400`) or decimal (`5120`).

See [`tlv_profiles.csv`](./tlv_profiles.csv) for ready-to-copy samples.

---

## 4. Per-submit behaviour

For user `U` with profile `P`:

| Tag state on the PDU (from caller) | Result |
|---|---|
| Not present, in `P` | Profile TLV **added** → `extra['tlv_added']` records the tag |
| Present, matches `P`'s value | **Kept** as-is → `extra['tlv_kept']` records the tag |
| Present, value differs from `P` | **Replaced** with profile value → `extra['tlv_mismatch']` records `0xTAG:caller='X'/profile='Y'` |
| Present, tag **not** in `P` | **Passed through** unchanged |

If the user is not found in the profile file:
- `extra['tlv_profile'] = 'miss:<username>'`
- PDU left exactly as received — no mutation

The `extra` dict is logged by `interceptord` — useful for ops visibility.

---

## 5. Caching

- Profiles are parsed on first use and stored in the `cache` dict injected
  by `jasmin/interceptor/interceptor.py`.
- Every subsequent submit does `os.path.getmtime()` and hits the dict
  directly — O(1) username lookup.
- When the file mtime changes (e.g. you save the CSV on your Mac), the
  next submit re-parses and refreshes the cache.
- Parsing happens **outside** the lock, so concurrent first-time callers
  don't serialise on disk I/O. A double-check inside the lock prevents
  lost updates on racing reloads.

Net effect: steady-state lookups cost roughly one stat + one dict read.

---

## 6. Verifying end-to-end

### 6.1. Quick interceptor smoke test

From your Mac, send one message through the REST API:

```bash
python3 misc/scripts/rest_load_test.py \
  --url http://localhost:8080 \
  --username anshu --password anshuman \
  --count 1 -v \
  --to '+919216217231' --from ABXOTP \
  --content 'interceptor TLV test'
```

No `--tlv` flag needed — the interceptor supplies them from the profile.

### 6.2. Watch the log

```bash
docker logs --tail 40 jasmin-tlv 2>&1 | grep -v LD_PRELOAD
```

Expect to see:
```
SMS-MT [cid:smalert] ... [tlvs:0x1400:b'1707167205648943173',0x1401:b'1401778070000018542',0x1402:b'9c70f816...']
```

And the interceptor run log:
```
Running with a pdu.command.submit_sm (from:b'ABXOTP', to:b'+919216217231').
```

### 6.3. Confirm the wire bytes

With `JASMIN_TLV_WIRE_LOG=1` set on the container, the wire logger prints:
```
OUT pdu=submit_sm seq=N len=L tlvs=0x1400,0x1401,0x1402 hex=0000...14000013...14010013...14020040...
```

The three `1400/1401/1402` TLV sections should be visible in the hex.

### 6.4. Unit-test the script in isolation

Run the 4-case simulator inside the container to prove each branch:
- replace stale value
- add missing TLVs
- skip untouched passthrough tags
- miss for an unknown user

```bash
docker exec jasmin-tlv python - <<'PY'
from jasmin.routing.Routables import RoutableSubmitSm
from jasmin.routing.jasminApi import User, Group, MtMessagingCredential
from smpp.pdu.operations import SubmitSM
import threading, os as _os

def sim(username, initial):
    pdu = SubmitSM(seqNum=1, service_type='')
    pdu.custom_tlvs = list(initial)
    u = User(uid='u', group=Group('g'), username=username,
             password='x', mt_credential=MtMessagingCredential())
    r = RoutableSubmitSm(pdu, u)
    ns = {'routable': r, 'smpp_status': None, 'http_status': None,
          'extra': {}, 'cache': {}, 'cache_lock': threading.RLock(),
          'os': _os}
    exec(open('/etc/jasmin/resource/interceptor_hash_tlv.py').read(), ns)
    return r.pdu.custom_tlvs, ns['extra']

tlvs, extra = sim('anshu', [(0x1400, None, 'OctetString', b'STALE')])
print('result:')
for t in tlvs: print(' ', '0x%04X' % t[0], t[2], t[3])
print('extra :', extra)
PY
```

Expect: `0x1400` replaced, `0x1401/0x1402` added,
`extra['tlv_mismatch']` mentions 0x1400 with old and new values.

---

## 7. Editing profiles live

With the bind-mount setup:
```bash
vim ~/jasmin-data/config/resource/tlv_profiles.csv
```

The **next** submit picks up the new values automatically. No process
restart, no `docker cp`, no cache flush — the mtime check does it for you.

---

## 8. Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `extra['tlv_profile'] = miss:<user>` | Username not in the CSV. Add a row. |
| Caller TLVs get replaced when you didn't expect it | The profile is authoritative. Remove that tag from the CSV row if the caller should be free to send arbitrary values. |
| Log shows `NameError: name 'os' is not defined` (or similar) | The interceptor engine version in your container doesn't inject `os` / `cache` / `cache_lock`. Rebuild `jasmin:0.12-tlv` so `jasmin/interceptor/interceptor.py` has the cache + module injections. |
| Wire bytes look right but SMSC still NACKs with `0xC4` | TLV encoding is correct but the SMSC rejects the *value*. Typically a vendor policy — check their TLV spec and adjust either the profile VALUE or the smppcc TYPE declaration. |
| Profile edit not taking effect | The mtime-based cache needs a **content** change (not just `touch`). If you hit FS precision quirks on macOS, edit a character and save. |

---

## 9. Related files

| file | role |
|---|---|
| [`interceptor_hash_tlv.py`](./interceptor_hash_tlv.py) | The interceptor script itself |
| [`tlv_profiles.csv`](./tlv_profiles.csv) | Sample profile file |
| [`../../jasmin/interceptor/interceptor.py`](../../jasmin/interceptor/interceptor.py) | Injects `os`, `cache`, `cache_lock` into script namespace |
| [`../../jasmin/routing/Routables.py`](../../jasmin/routing/Routables.py) | `routable.getCustomTlvs()` / `addCustomTlv()` API |
| [`../../jasmin/tools/tlv_encoder.py`](../../jasmin/tools/tlv_encoder.py) | `resolve_tlv_types()` at dispatch time, wire encode/decode patches |
| [`../../jasmin/managers/listeners.py`](../../jasmin/managers/listeners.py) | Calls `resolve_tlv_types()` and `validate_custom_tlvs()` just before `sendDataRequest` |
| [`rest_load_test.md`](./rest_load_test.md) | REST load test script + TLV architecture reference |
| [`../../misc/doc/sources/apis/smpp-server/custom-tlv.rst`](../../misc/doc/sources/apis/smpp-server/custom-tlv.rst) | Canonical TLV documentation (Sphinx) |

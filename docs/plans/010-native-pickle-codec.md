# Native Go pickle codec — retire the Python bridge

- **Date:** 2026-07-27
- **Status:** done — all 10 bridge actions ported to `internal/transport/gopickle` + `picklecompat.NativeCodec` (machine #93; connectors+response #94; submit encode #95; submit decode #96; repickle #97; deliver encode #98; routed decode #99; SubmitSmBill #100), each proven `pickle.loads`-equal to the live bridge. **The three deferred edges are now ported and differential-proven** (schedule/validity absolute-time codec; custom-TLV `[tag,length,type,value]` tuples; the full DataCoding surface — DEFAULT + RAW-int + GSM_MESSAGE_CLASS across all 256 bytes, lossy-matching `DataCodingEncoder`). **`pickle_codec` now defaults to `native`** (`bridge` is an opt-in fallback), the gateway integration test runs E2E on the default (no subprocess), and **the pickle-bridge layer is retired from `docker/Dockerfile.gateway`** (`scripts/pickle_bridge.py` + `smpp-pdu3` + the `jasmin/` source tree removed; Python stays only for the stdlib-only interceptor runner). The `*Bridge` code + all differentials are kept for regression under `PYTHON_PATH`.
- **Summary:** Replace `scripts/pickle_bridge.py` (a hot-path Python subprocess) with a native Go protocol-2 pickle codec, so the gateway is Go-only on the AMQP encode/decode path — the last blocker to "actually a Go rewrite."
- **Related:** [prod-testing-readiness](../../) backlog item #2. Consumes the bridge seam `internal/transport/picklecompat`.

## Context

Every AMQP body the gateway produces/consumes is a Python protocol-2 pickle, produced today by execing `scripts/pickle_bridge.py` over JSON-lines (`picklecompat.Bridge`). This couples the "Go" runtime to Python + Twisted/smpp.pdu/jasmin at runtime. The bridge exposes 10 actions: `decode`, `decode_submit_sm`, `decode_routed_deliver_sm`, `encode`, `encode_submit_sm_resp`, `encode_submit_sm`, `encode_routable_deliver_sm`, `repickle_routable_pdu`, `encode_connector_list`, `ping`.

**Key feasibility finding:** the encode differentials are **semantic, not byte-exact** — they `pickle.loads()` the Go output and compare the reconstructed *object* (e.g. `deliver_encoder_differential_test.go`: "the unpickled object must equal the one the legacy listener would build — rather than byte-exact"; the front-door tests compare the decoded *wire* body). So the native codec must emit pickles that Python's restricted unpicklers **load into the correct objects**, not byte-identical opcode streams. This grants two simplifications: (1) we emit **memo-free** valid protocol-2 (no BINPUT/BINGET — Python's memo reuse just becomes a repeated literal that loads identically); (2) we choose the simplest opcodes per construct. The **decoder** must still handle Python's full memo/opcode set, since legacy-produced pickles use them.

Object graphs (from `pickletools.dis`): a class instance = `GLOBAL cls`, `EMPTY_TUPLE`, `NEWOBJ`, then `EMPTY_DICT` + `MARK` + key/value pairs + `SETITEMS` + `BUILD`. smpp.pdu enums = `GLOBAL smpp.pdu.pdu_types.<Enum>` + `<int>` + `TUPLE1` + `REDUCE`. Non-empty bytes = `GLOBAL _codecs.encode` + `BINUNICODE <latin1-str>` + `BINUNICODE 'latin1'` + `TUPLE2` + `REDUCE`; empty bytes = `GLOBAL builtins.bytes` + `EMPTY_TUPLE` + `REDUCE`.

Desired outcome: a `picklecompat.NativeCodec` implementing the same methods as `Bridge`, differential-proven against the bridge for every action, then wired into the gateway with the Python bridge removed.

## Approach

A `internal/transport/gopickle` package with a small **value IR** (`None`/`Bool`/`Int`/`Str`/`Bytes`/`List`/`Tuple`/`Dict`/`Global`/`Reduce`/`Object`) plus a protocol-2 **Pickler** (IR → bytes, memo-free) and **Unpickler** (bytes → IR, full opcode VM with a reduce/class registry). Object codecs (smpp.pdu PDUs, jasmin Routables/Connectors/Bill, enums, datetime) convert between the IR and the existing Go domain structs (`smppwire`, `picklecompat` request/result types). Finally a `NativeCodec` facade implements the bridge methods over the codecs, is proven `==` to the bridge under `PYTHON_PATH`, and replaces it in the gateway.

Trade-off: the IR indirection over a direct byte-emitter costs a small allocation per value but makes each object codec a readable IR-tree builder and gives one place to prove the opcode machine. Worth it for correctness on a format this fiddly.

## Steps (one PR each)

### Step 1: The pickle machine (Pickler + Unpickler + IR)

- **Files:** `internal/transport/gopickle/value.go` (IR), `pickler.go` (IR→bytes), `unpickler.go` (bytes→IR VM), tests.
- **Changes:** IR types; Pickler emitting `PROTO2, NONE, NEWTRUE/NEWFALSE, BININT1/BININT2/BININT/LONG1 (int sizing), BINUNICODE, EMPTY_LIST/APPENDS, EMPTY_DICT/SETITEMS, EMPTY_TUPLE/TUPLE1/2/3/MARK+TUPLE, GLOBAL, REDUCE, NEWOBJ, BUILD, STOP`, memo-free; Unpickler handling all of those **plus** memo (`BINPUT/LONG_BINPUT/BINGET/LONG_BINGET`), `SHORT_BINUNICODE/BINBYTES/SHORT_BINBYTES`, and a callable registry for `REDUCE`/`NEWOBJ`.
- **Verify:** round-trip unit tests (Go IR → bytes → Go IR); **Python-parity differential** (`PYTHON_PATH`): Python `pickle.loads` accepts Go-emitted primitives and returns equal values, and Go `Unpickler` reads Python-emitted primitives (ints across the BININT1/2/4/LONG boundaries, unicode, bytes, nested list/dict/tuple, None/bool).

### Step 2: Enum + datetime + bytes value codecs

- **Files:** `gopickle/smppenum.go` (or under picklecompat), tests.
- **Changes:** IR builders/readers for the smpp.pdu enums used (`CommandId`, `CommandStatus`, `DataCoding`, `AddrTon`, `AddrNpi`, `PriorityFlag`, `EsmClass`, `RegisteredDelivery`, `ReplaceIfPresentFlag`), `SMPPRelativeTime`, and `datetime.datetime` — each as `Reduce{Global, args}` matching the dis output.
- **Verify:** each enum/value round-trips through Python (`Enum(x)` reconstructs) and Go; boundary values.

### Step 3: `encode_connector_list` + `encode_submit_sm_resp` codecs (first end-to-end)

- **Files:** `picklecompat/native_router.go`, `native_response.go` (or a `nativecodec` subpkg), tests.
- **Changes:** build the `HttpConnector`/`SmppServerSystemIdConnector` list Object IR (incl. the computed `_str`/`_repr` fields, reproduced from the legacy formats) and the `SubmitSMResp` Object IR.
- **Verify:** differential — Python `ConnectorListUnpickler`/stock loader loads the Go pickle into connectors/`SubmitSMResp` equal (field-wise) to the bridge's output for the same input.

### Step 4: `decode_submit_sm` (+ chain) + `encode_submit_sm`

- **Files:** `native_submit.go`, tests.
- **Changes:** decode a `SubmitSM` pickle (params dict → `smppwire.SubmitSMBody` + TLVs, the nextPdu chain) via the Unpickler; encode a `SubmitSM` (+ optional `SubmitSmBill`) Object IR from `SubmitSMEncodeRequest`.
- **Verify:** the existing front-door + chain differentials pass with the native codec swapped in (wire body byte-identical); decode of bridge-encoded submit == native decode.

### Step 5: `encode_routable_deliver_sm` + `repickle_routable_pdu` + `decode_routed_deliver_sm`

- **Files:** `native_deliver.go`, tests.
- **Changes:** build `RoutableDeliverSm(DeliverSM|DataSM, Connector)` Object IR from wire; repickle (decode routable → re-emit bare pdu + the decoded fields from #89); decode routed deliver.
- **Verify:** the deliver/repickle/routed differentials pass with the native codec.

### Step 6: `decode` / `encode` generic + `NativeCodec` facade + parity gate

- **Files:** `picklecompat/native_codec.go` (implements the `Bridge` method set), a parity differential covering all 10 actions.
- **Changes:** the facade dispatches to the Step 3-5 codecs; the generic `decode`/`encode` cover the remaining AMQP fixtures.
- **Verify:** one differential per action asserting native-output `pickle.loads`-equals bridge-output (and Go-decode of a Python pickle equals expected) across a fixture matrix.

### Step 7: Swap the gateway + retire the bridge

- **Files:** `internal/app/gateway/runtime.go` (construct `NativeCodec` instead of `NewBridge`), `/health` (drop the bridge ping or point it at a no-op), `docker/Dockerfile.gateway` (drop the Python runtime layer once nothing else needs it — interceptor runner still does, gate accordingly), docs.
- **Changes:** feature-flag `pickle_codec: native|bridge` (default bridge → flip to native after soak), then remove the bridge. Keep the interceptor runner (separate Python contract) unless also ported.
- **Verify:** full `PYTHON_PATH` suite green with native; compose E2E (send/receive/DLR) green; `--check-config`; frozen tree unchanged (scripts/ not fingerprinted, but the bridge file removal is a separate concern).

## End-to-end verification

Per-action parity differential (native == bridge, semantic) + the full existing differential suite passing with the native codec swapped in + a compose send/receive/DLR drill on the native path. The bridge stays available behind the flag until the native path has soaked.

## Rollback

Everything is additive until Step 7; the `pickle_codec` flag defaults to `bridge`. Flip back to `bridge` or revert the Step 7 branch to restore the subprocess path.

## Risks

- **Object `__dict__` fidelity** — computed fields like `HttpConnector._str/_repr` must match the legacy format or a strict consumer/`==` diverges. Mitigation: reproduce the exact legacy string formats; assert field-wise in the differential.
- **Int/long boundaries** — Python picks BININT1/2/4/LONG1 by magnitude; the decoder must accept all, the encoder must stay within what loads equal. Mitigation: boundary tests in Step 1.
- **Unicode vs bytes** — smpp.pdu addr fields are latin1-decoded str in params but bytes on our side; keep the `_codecs.encode(...,'latin1')` bytes shape exactly. Mitigation: Step 2 bytes tests.
- **Interceptor runner still Python** — retiring `pickle_bridge.py` does not remove Python from the image while `scripts/interceptor_runner.py` remains. Mitigation: Step 7 gates the Dockerfile change on that; a native interceptor is out of scope here.

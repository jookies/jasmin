# Front-door connector-config PDU parameters (TON/NPI + service_type/protocol_id/...)

- **Date:** 2026-07-26
- **Status:** done — GAP 4 plumbing landed earlier; the deferred Step 5 full front-door byte-differential is now closed by `submit_encoder_sendpath_differential_test.go` (drives the real `update_submit_sm_pdu`+`preSubmitSm` send path, default + non-default connectors across a shape matrix; byte-identical). Tracked under [009-core-gateway-completeness.md](009-core-gateway-completeness.md) Step 1.
- **Summary:** Apply the routed connector's default submit_sm PDU parameters (TON/NPI, service_type, protocol_id, replace_if_present_flag, sm_default_msg_id, source_addr) on the Go front-door submit path, with the submitter's explicit values winning (MT-path audit GAP 4).
- **Related:** MT-path parity audit backlog (memory, GAP 4). This is the **full-stack-Go** edge (Go HTTP API → Go smppc), distinct from the bridge/legacy-publisher gaps (1/2/3/5/6).

## Context

A Go-front-door submit currently emits `source_addr_ton/npi = 00/00`, `dest_addr_ton/npi = 00/00`, and empty `service_type`/`protocol_id`/`sm_default_msg_id`, because `submit_envelope_builder.go` and `picklecompat.SubmitSMEncodeRequest` carry none of these and the bridge `encode_submit_sm` sets no such kwargs. Jasmin instead fills every unset mandatory param from the routed connector's `SMPPClientConfig` (`operations.py:_setConfigParamsInPDU`), then re-applies the connector's values in `send.py:update_submit_sm_pdu`. Defaults: `source_addr_ton=NATIONAL(2)`, `source_addr_npi=ISDN(1)`, `dest_addr_ton=INTERNATIONAL(1)`, `dest_addr_npi=ISDN(1)`, `service_type=None`, `protocol_id=None`, `source_addr=None`, `sm_default_msg_id=0`, `replace_if_present_flag=<default>`. So every Go-front-door message differs from Jasmin on 4+ high-signal bytes; strict SMSCs reject `dest_addr_ton=UNKNOWN(0)`.

Precedence to reproduce: **submitter-supplied value > routed-connector config value > SMPPClientConfig default**. (`_setConfigParamsInPDU` only fills params NOT in the submit kwargs; `update_submit_sm_pdu` re-applies the connector list but the HTTP front door does not expose these as user params, so in practice the connector value wins for the send.py list, and TON/NPI come from the connector config.)

## Approach

Thread the routed connector's PDU-default config to `submit_service` (which already has the routed `connectorID` and builds `SubmitEnvelopeRequest`), apply the precedence there, and carry the resolved values through `SubmitEnvelopeRequest` → `SubmitSMEncodeRequest` → the bridge `encode_submit_sm`. No durable-model or session change; the smppc **send** path already emits whatever the encoded pickle contains.

Trade-off: a connector-config lookup must reach `submit_service`. Reuse the existing connector registry the gateway already holds (the same `smppc.Config` set used to start connectors); expose a read-only `PDUDefaults(connectorID)` provider rather than passing the whole registry.

## Steps

### Step 1: Connector PDU-default config

- **Files:** `internal/core/smppc/config.go`.
- **Changes:** Confirm/parse `SrcTON/SrcNPI/DstTON/DstNPI` with the legacy defaults (2/1/1/1). Add `ServiceType string`, `ProtocolID *uint8`, `ReplaceIfPresentFlag *uint8`, `SmDefaultMsgID uint8` (default 0), `SourceAddr string` with the legacy defaults (None/0). Expose a small `PDUDefaults` view struct.
- **Verify:** `go build/vet`; a config unit test asserting the legacy defaults; a PYTHON_PATH differential against `SMPPClientConfig` for the eight fields.

### Step 2: Connector-config provider into submit_service

- **Files:** `internal/core/submit_service.go` (dependencies), gateway/outbound wiring where `submit_service` is constructed.
- **Changes:** Add a `ConnectorPDUDefaults func(connectorID string) (smppc.PDUDefaults, bool)` dependency; the gateway supplies it from the connector registry. `submit_service` looks up the routed connector's defaults.
- **Verify:** `go build/vet`; unit test with a fake provider.

### Step 3: Apply precedence into SubmitEnvelopeRequest

- **Files:** `internal/core/submit_service.go`, `internal/core/submit_service.go` `SubmitEnvelopeRequest` struct.
- **Changes:** Add `SourceAddrTON/NPI`, `DestAddrTON/NPI uint8`, `ServiceType string`, `ProtocolID/ReplaceIfPresent *uint8`, `SmDefaultMsgID uint8` to `SubmitEnvelopeRequest`. In the builder-request assembly, resolve each: submitter value if the HTTP request set it, else the connector default. Apply the connector `source_addr` when the request source is empty (`protocol.py:262-263`).
- **Verify:** unit test over the precedence matrix (submitter set vs unset, connector default).

### Step 4: Carry through the encoder + bridge

- **Files:** `internal/app/outbound/submit_envelope_builder.go`, `internal/transport/picklecompat` `SubmitSMEncodeRequest` + the encode path, `scripts/pickle_bridge.py` `encode_submit_sm`.
- **Changes:** Add the fields to `SubmitSMEncodeRequest`; the builder passes them; `encode_submit_sm` sets `source_addr_ton/npi`, `dest_addr_ton/npi`, `service_type`, `protocol_id`, `replace_if_present_flag`, `sm_default_msg_id` on the `SubmitSM` (encoding TON/NPI via the `AddrTon`/`AddrNpi` enums like the existing byte fields).
- **Verify:** the existing single-part decode differentials still pass; `py_compile`.

### Step 5: Verification (done) + full front-door differential (deferred)

- **Done:** a config-defaults unit test (`config_pdu_defaults_test.go`: unset connector resolves TON/NPI 2/1/1/1; explicit values survive) and a PYTHON_PATH encode round-trip (`submit_encoder_pdu_config_test.go`: TON/NPI, service_type and protocol_id flow through the bridge encode → pickle → decode into the wire body). Full `go test ./...` shows no new failures (the provider is nil in existing tests, so the pre-GAP-4 zero defaults are unchanged there).
- **Done (2026-07-27):** the full front-door differential now exists — `submit_encoder_sendpath_differential_test.go` asserts the Go-encoded submit body equals the legacy `SMPPOperationFactory(config).SubmitSM` + `update_submit_sm_pdu` + `preSubmitSm` (data_coding int→object, default source_addr) + `PDUEncoder` bytes for the DEFAULT connector (2/1/1/1) and non-default connectors, across a shape matrix (service_type, protocol_id, sm_default_msg_id, empty-source fallback, UCS2). The Go side derives its params from `smppc.Config.Validate()+PDUDefaults()` — the resolved defaults, not hand-picked constants. Gotcha proven en route: AddrTon/AddrNpi enums are 1-indexed (NATIONAL=3) and the encoder maps enum→wire as value−1 (NATIONAL→wire 2), so the connector config carries **wire** bytes, decoded via the encoder — not enum-by-value.

## End-to-end verification

PYTHON_PATH differential: Go front-door submit wire == legacy for default and non-default connector configs. Full `go test ./...` shows no new failures. Regression: the legacy-publisher → Go-smppc path (the bridge decode) is untouched.

## Rollback

Additive; revert the branch. New config fields default to the legacy values, so an unset config behaves as today plus the correct TON/NPI defaults.

## Risks

- **Wiring the connector-config provider** into `submit_service` is the main surface; keep it a narrow read-only lookup, not the whole registry.
- **Precedence subtlety:** the HTTP front door does not currently expose TON/NPI as user params, so the connector value effectively always wins — verify against `send.py` rather than assuming a submitter override path exists.
- **TON/NPI enum values** must match the legacy `AddrTon`/`AddrNpi` (NATIONAL=2, INTERNATIONAL=1, ISDN=1) — differential-verified.

# SMPP 3.4 ESME integration

This guide is for an application binding as an ESME to Synevyr's
customer-facing SMPP server. In this direction Synevyr is the SMPP server:
`submit_sm` enters the MT routing and billing path, while MO messages and
delivery receipts leave Synevyr as `deliver_sm`
(`internal/core/smpps/server.go:27`, `internal/app/smppsdelivery/mo_sink.go:25`,
`internal/app/smppsdelivery/sink.go:37`).

## Readiness warning

This interface has passed an independent `smpp.twisted` bind, submit, delivery,
acknowledgement, and unbind test. It has not run on a real carrier link. Long
messages, delivery receipts, and error statuses have not yet passed through the
third-party client; sustained-load and 24-hour soak tests have not been run
(`docs/plans/017-smpp-production-readiness.md:129`,
`docs/plans/017-smpp-production-readiness.md:137`,
`docs/plans/017-smpp-production-readiness.md:142`). Prometheus instrumentation
is also incomplete: some defined series have no call sites and therefore show a
misleading zero (`docs/plans/017-smpp-production-readiness.md:101`).

Treat the interface as lab-validated, not carrier-proven. Before carrying
production traffic, test the exact ESME library, message encodings, DLR modes,
and failure/retry policy that will be deployed.

## Connection and bind

Synevyr accepts all three SMPP 3.4 bind types. A transmitter may submit MT
messages but cannot receive `deliver_sm`; a receiver may receive MO messages and
receipts but cannot submit; a transceiver may do both. The state gate admits
`submit_sm` only on transmitter and transceiver binds, and the delivery path
selects only receiver and transceiver sessions
(`internal/core/smpps/bindstate.go:78`,
`internal/core/smpps/session.go:375`).

| Bind | May send `submit_sm` | May receive `deliver_sm` | Typical use |
|---|---:|---:|---|
| `bind_transmitter` | Yes | No | A send-only integration |
| `bind_receiver` | No | Yes | MO and DLR delivery alongside a separate TX bind |
| `bind_transceiver` | Yes | Yes | One bidirectional session |

Multiple sessions may bind with the same `system_id`. `max_bindings`, when set,
is one total across RX, TX, and TRX binds, not a limit per bind type
(`internal/core/smpps/bindauth.go:31`). When delivering to several eligible
sessions for one `system_id`, Synevyr selects them round-robin
(`internal/core/smpps/server.go:231`).

### Two records make one sending identity

Binding and sending use related but separate configuration records:

* `smpps.users[].system_id` and `smpps.users[].password` authenticate the SMPP
  bind. The password is stored as an MD5 verifier in memory
  (`internal/app/smppsserver/directory.go:18`,
  `internal/core/smpps/bindauth.go:12`).
* The same `system_id` is passed into the MT pipeline as its username
  (`internal/app/smppssubmit/handler.go:85`). A matching
  `outbound.users[].username` supplies the billing identity, balance, quota,
  and per-ingress throughput setting
  (`internal/core/submit_service.go:230`,
  `internal/app/outbound/config.go:682`,
  `internal/app/outbound/config.go:782`).

The names must therefore match:

```json
{
  "outbound": {
    "users": [
      {
        "username": "smppuser",
        "external_id": "smppuser",
        "password_sha256": "5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8",
        "balance": 100.0,
        "submit_sm_count": 10000,
        "mt_credential": {
          "smpps_send": true,
          "smpps_throughput": 10
        }
      }
    ]
  },
  "smpps": {
    "bind_addr": "0.0.0.0:2775",
    "enquire_link_timeout": 30,
    "inactivity_timeout": 300,
    "deliver_sm_window_size": 10,
    "deliver_sm_response_timeout": 30,
    "users": [
      {
        "system_id": "smppuser",
        "password": "smpppw1",
        "bind": true,
        "smpps_send": true,
        "ip_whitelist": "0.0.0.0/0",
        "max_bindings": 2
      }
    ]
  }
}
```

This is a relevant-fields fragment, not a complete gateway file. The displayed
outbound password hash is the sample file's SHA-256 for `password`; it is an
HTTP credential and is not the SMPP bind password
(`configs/gateway.example.json:29`). Start with
`configs/gateway.example.json`, which contains the remaining route, connector,
database, broker, and Redis configuration.

There are two send checks. `smpps.users[].smpps_send` and that record's value
filters reject an individual PDU before it reaches the MT service
(`internal/app/smppsserver/directory.go:184`,
`internal/app/smppssubmit/handler.go:67`). The matching outbound user then
supplies the commercial identity used by the MT service. A system ID present
only under `smpps.users[]` can bind successfully, but its first `submit_sm`
fails outbound identity lookup and returns `ESME_RSYSERR`
(`internal/core/submit_service.go:230`,
`internal/app/smppssubmit/handler.go:148`). That status looks like an internal
server fault even though the operational cause is a missing
`outbound.users[]` entry.

Bind authentication also checks whether the user and group are enabled, the
peer IP, bind authorization, and the total binding quota. A missing user, wrong
password, disabled user, or disabled group is deliberately collapsed to
`ESME_RINVPASWD`; a disallowed IP, disabled bind authorization, or exhausted
binding quota returns `ESME_RBINDFAIL`
(`internal/core/smpps/bindauth.go:31`). If `ip_whitelist` is omitted, the
effective value is `0.0.0.0/0`, which admits IPv4 only, not IPv6
(`internal/app/smppsserver/directory.go:151`,
`internal/core/smpps/ipmatch.go:71`). Restrict it to known peer addresses in
production.

### The eight-character password ceiling

SMPP 3.4 encodes a bind password as a C-octet string of at most nine octets:
eight password octets plus the terminating NUL. A conforming ESME cannot put a
longer password on the wire. Use at most eight ASCII characters
(`scripts/deploy/setup.sh:68`).

Eight hexadecimal characters provide only 32 bits of secret space. Do not rely
on that password as the network perimeter; use the IP whitelist, a private
network, or TLS and restrict listener exposure
(`scripts/deploy/setup.sh:71`). Current configuration validation does **not**
reject a password longer than eight characters: it validates the system ID,
whitelist, and binding count but not password length
(`internal/app/smppsserver/directory.go:137`). Such a configuration is
effectively unusable by a conforming client.

## Timers, keepalive, and delivery window

`enquire_link` keepalive and inactivity are independent. After a quiet read
period, Synevyr sends `enquire_link`; that action does not close the session.
The inactivity clock is measured from the last PDU received, so an
`enquire_link_resp` proves activity, while a peer that never responds is
eventually disconnected when inactivity is enabled
(`internal/core/smpps/session.go:75`,
`internal/core/smpps/session.go:127`).

| JSON field | Effective JSON behavior when omitted/zero | Recommended explicit starting value |
|---|---|---:|
| `enquire_link_timeout` | Disabled | 30 seconds |
| `inactivity_timeout` | Disabled | 300 seconds |
| `deliver_sm_window_size` | 10 outstanding per session | 10 |
| `deliver_sm_response_timeout` | 30 seconds | 30 seconds |

The effective zero behavior follows the service's direct conversion of JSON
values and the core's zero-disable rules
(`internal/app/smppsserver/service.go:115`,
`internal/core/smpps/server.go:44`). The 30/300 values are applied only when a
legacy `jasmin.cfg` is loaded through `--legacy-cfg`
(`internal/config/sections.go:164`,
`internal/app/gateway/jasmin_config.go:54`). This differs from comments that call
30/300 the defaults, so set both values explicitly. The delivery window and
response timeout really do default to 10 and 30 seconds in the core
(`internal/core/smpps/server.go:62`,
`internal/core/smpps/server.go:98`).

The window bounds `deliver_sm` requests awaiting a correlated
`deliver_sm_resp` on each session. Synevyr assigns a nonzero sequence number,
occupies a window slot, writes with a deadline, and releases the slot only after
an acknowledgement, cancellation, disconnect, or timeout
(`internal/core/smpps/session.go:352`). A matching `deliver_sm_resp` with
`ESME_ROK` succeeds; a nonzero response or `generic_nack` is a delivery failure
(`internal/core/smpps/session.go:224`). The MO or DLR thrower treats delivery
failure as retryable according to its own policy
(`internal/app/smppsdelivery/mo_sink.go:10`,
`internal/app/smppsdelivery/sink.go:20`).

A larger window permits more concurrent deliveries but increases the number
whose outcome is uncertain when a session fails. A smaller window applies
backpressure sooner. A short response timeout releases stuck slots sooner but
can cause a slow ESME's successful processing to race a retry; a long timeout
lets a non-acknowledging ESME occupy the window longer. Always acknowledge each
`deliver_sm` with its exact `sequence_number`.

## Supported PDUs

The wire codec recognizes bind, submit, deliver, data, unbind, enquire-link,
their responses, and `generic_nack`
(`internal/transport/smppwire/types.go:8`,
`internal/transport/smppwire/codec.go:124`). Recognition does not mean every PDU
is a valid ESME request.

| Direction | PDU | Behavior |
|---|---|---|
| ESME → Synevyr | `bind_receiver`, `bind_transmitter`, `bind_transceiver` | Accepted only before a successful bind |
| ESME → Synevyr | `submit_sm` | Accepted only on TX/TRX; enters MT auth, routing, billing, and publication |
| Either | `enquire_link` | Answered with `enquire_link_resp`; Synevyr also originates it |
| ESME → Synevyr | `unbind` | Answered with `unbind_resp`, then the session closes |
| Synevyr → ESME | `deliver_sm` | Carries an MO or DLR on RX/TRX; ESME must answer `deliver_sm_resp` |
| ESME → Synevyr | `generic_nack` | A matching sequence negatively acknowledges an outstanding `deliver_sm` |
| ESME → Synevyr | other response PDU except `unbind_resp` | Consumed without a response; only `deliver_sm_resp` and `generic_nack` settle delivery |
| ESME → Synevyr | `data_sm` | Deliberately refused; see below |

The request state rules are encoded in
`internal/core/smpps/bindstate.go:62`; response handling is in
`internal/core/smpps/session.go:172`. Commands that the codec does not recognize,
including `query_sm`, `cancel_sm`, `replace_sm`, and `alert_notification`, get
`generic_nack` with `ESME_RINVCMDID`. A decodable but unsupported request such
as ESME-originated `deliver_sm` reaches the state gate, gets
`generic_nack`/`ESME_RSYSERR`, and closes the session
(`internal/transport/smppwire/codec.go:155`,
`internal/core/smpps/session.go:183`).

Known defect: `unbind_resp` is excluded from the general response path and has
no case in the request dispatcher, despite being admitted by the state gate. If
one reaches the dispatcher, the default branch answers it with
`generic_nack`/`ESME_RSYSERR`
(`internal/core/smpps/session.go:172`,
`internal/core/smpps/session.go:213`). An operator-initiated unbind currently
writes `unbind` and immediately closes the session rather than waiting for that
response (`internal/core/smpps/server.go:272`). ESMEs should still send
`unbind_resp` as required by SMPP, but must tolerate the connection closing
first.

### Why `data_sm` is refused

On a transmit-capable bind, Synevyr answers `data_sm` with
`data_sm_resp`/`ESME_RSYSERR` and leaves the bind open
(`internal/core/smpps/session.go:207`). On a receiver or unbound session it is
rejected first with `ESME_RINVBNDSTS`. This is deliberate: the customer
submission adapter defines validation, routing, DLR registration, and billing
only for `submit_sm`, and production-readiness records `data_sm` refusal
without billing as the intended behavior
(`internal/app/smppssubmit/handler.go:55`,
`docs/plans/017-smpp-production-readiness.md:65`). Sending `data_sm` is not a
fallback for a long or binary message; use `submit_sm` with
`message_payload`, SAR, or UDH as appropriate.

## `submit_sm` fields, encoding, and concatenation

Synevyr preserves the ESME's `data_coding` byte and raw `short_message` bytes.
It does not transcode a real SMPP submission or reinterpret coding zero as
UTF-8; all 256 `data_coding` byte values can cross the compatibility encoder
(`internal/app/smppssubmit/handler.go:85`,
`internal/transport/picklecompat/native_submit.go:434`). The ESME is responsible
for making the bytes agree with `data_coding`.

The gateway's GSM 03.38 replacement behavior applies to text entering through
non-SMPP ingress with coding zero, not to an ESME's raw PDU
(`internal/core/submit_service.go:254`). In that path extension-table
characters use the GSM escape sequence, `ç` and a defined set of Greek capitals
map to GSM lookalikes, and unsupported runes become byte `0x3f` (`?`)
(`internal/core/submit_service.go:630`). This distinction matters when comparing
an HTTP-originated message with an otherwise similar SMPP submission: the HTTP
path may replace characters, while the SMPP path preserves the supplied bytes.

For customer SMPP input, Synevyr does not split a long `submit_sm` into new
parts. A PDU with the UDHI bit is passed through as one pre-encoded UDH part,
and any SMPP PDU, including one carrying SAR TLVs, is preserved as one part to
avoid double segmentation (`internal/core/segmentation/segmentation.go:44`,
`internal/core/segmentation/segmentation.go:145`,
`internal/core/submit_service.go:375`). The ESME must send each concatenated
segment as its own `submit_sm`. UDH bytes, the UDHI bit, and the complete SAR
triple are preserved toward the selected SMSC.

In the reverse direction, carrier `deliver_sm` messages are recognized as
multipart when they have the complete SAR triple, or the UDHI bit plus the
six-byte 8-bit concatenation header `05 00 03 ref total sequence`
(`internal/core/smppc/deliver.go:155`,
`internal/core/smppc/deliver.go:321`). Synevyr stores and publishes each segment,
then builds a reassembled whole when all sequences exist
(`internal/core/smppc/deliver.go:169`). SMPPS routes receive the original marked
segments; HTTP routes receive the reassembled whole
(`internal/app/modispatch/service.go:463`). Consequently an ESME should
reassemble inbound UDH or SAR segments itself.

Not verified: inbound carrier reassembly of the 16-bit UDH concatenation
information element. The detector implements only the `05 00 03` 8-bit form;
inspect `internal/core/smppc/deliver.go:321` before depending on another UDH
form.

## Optional parameters (TLVs)

The following known TLVs are accepted on customer `submit_sm` and carried into
the outbound PDU. The list is the codec's exact allowlist
(`internal/transport/smppwire/codec.go:1471`).

| Tag | Parameter | Tag | Parameter |
|---:|---|---:|---|
| `0x0005` | `dest_addr_subunit` | `0x000d` | `source_addr_subunit` |
| `0x0019` | `payload_type` | `0x0201` | `privacy_indicator` |
| `0x0202` | `source_subaddress` | `0x0203` | `dest_subaddress` |
| `0x0204` | `user_message_reference` | `0x0205` | `user_response_code` |
| `0x020a` | `source_port` | `0x020b` | `destination_port` |
| `0x020c` | `sar_msg_ref_num` | `0x020d` | `language_indicator` |
| `0x020e` | `sar_total_segments` | `0x020f` | `sar_segment_seqnum` |
| `0x0304` | `number_of_messages` | `0x0381` | `callback_num` |
| `0x0424` | `message_payload` | `0x0426` | `more_messages_to_send` |
| `0x1201` | `display_time` | `0x1203` | `sms_signal` |

The three SAR parameters must be present together. Total and sequence must be
nonzero, and sequence cannot exceed total. `message_payload` and a nonempty
`short_message` are mutually exclusive
(`internal/transport/smppwire/codec.go:786`). Known parameter lengths and enum
values are validated before the PDU is admitted. Unknown vendor tags are
captured with their original value and forwarded through the custom-TLV lane
(`internal/transport/smppwire/types.go:139`,
`internal/app/smppssubmit/handler.go:99`).

Current limitation: the SMPPs adapter builds credential and routing content from
`short_message`, not `message_payload`
(`internal/app/smppssubmit/handler.go:67`,
`internal/app/smppssubmit/handler.go:85`). It marks that empty short-message
field present in the routable, so short-message route and interceptor filters do
not fall back to the payload TLV
(`internal/core/submit_service.go:277`,
`internal/core/routingfilter/filter.go:275`). The raw `message_payload` is still
retained for the outbound PDU
(`internal/app/outbound/submit_envelope_builder.go:229`). Until that gap is
fixed, do not use `filter_content` or a `short_message` route/interceptor filter
as a security boundary for customers allowed to send `message_payload`.

For carrier-originated `deliver_sm`, these known TLVs can be decoded and
delivered to an ESME:

| Tag | Parameter | Tag | Parameter |
|---:|---|---:|---|
| `0x0019` | `payload_type` | `0x001e` | `receipted_message_id` |
| `0x0201` | `privacy_indicator` | `0x0202` | `source_subaddress` |
| `0x0203` | `dest_subaddress` | `0x0204` | `user_message_reference` |
| `0x0205` | `user_response_code` | `0x020a` | `source_port` |
| `0x020b` | `destination_port` | `0x020c` | `sar_msg_ref_num` |
| `0x020d` | `language_indicator` | `0x020e` | `sar_total_segments` |
| `0x020f` | `sar_segment_seqnum` | `0x0381` | `callback_num` |
| `0x0423` | `network_error_code` | `0x0424` | `message_payload` |
| `0x0426` | `more_messages_to_send` | `0x0427` | `message_state` |

That list comes from the carrier-side `deliver_sm` allowlist and delivery
encoder (`internal/transport/smppwire/codec.go:1499`,
`internal/transport/smppwire/codec.go:928`). Unknown carrier TLVs are captured
for MO/DLR forwarding; known-but-unhandled standard TLVs are not silently
promoted to vendor TLVs (`internal/transport/smppwire/codec.go:760`).

### Strict customer input, liberal carrier input

Customer `submit_sm` is decoded strictly. A malformed, disallowed, incomplete,
or invalid optional parameter produces a specific error status, so the ESME can
correct its PDU. Carrier-originated `deliver_sm` is deliberately more tolerant:
known parameters that are disallowed for that PDU or have a bad value are
skipped so one optional carrier quirk does not create a reconnect loop and an
MO outage (`internal/transport/smppwire/codec.go:318`,
`internal/transport/smppwire/codec.go:517`,
`docs/reference/deviations.md:14`).

This is not unlimited tolerance. A truncated TLV stream is fatal because the
next parameter boundary cannot be recovered, and certain known parameters for
which the compatibility codec has no encoder are still refused
(`internal/transport/smppwire/codec.go:528`,
`internal/transport/smppwire/codec.go:767`).

## Delivery receipts

Any nonzero value in the low two bits of `registered_delivery` makes the SMPPs
submission path store receipt correlation state
(`internal/app/smppssubmit/handler.go:27`,
`internal/app/smppssubmit/handler.go:111`). A later receipt is sent to the
original `system_id` as `deliver_sm`. Its source and destination addressing are
swapped from the submit, `esm_class` is `0x04`, and it contains both Appendix-B
receipt text and the `receipted_message_id` and `message_state` TLVs
(`internal/core/dlr/smpps_receipt.go:104`).

Use low-bit value 1 to request success and failure receipts, or value 2 to
request failures only. The correlation record recognizes those two values;
value 3 falls through to “no receipt requested”
(`internal/core/dlr/amqp_projection.go:227`,
`internal/core/dlr/correlation.go:18`). Avoid value 3 even though the front
door's initial nonzero check admits it.

The receipt text has this exact byte format:

```text
id:<message-id> submit date:<YYMMDDhhmm> done date:<YYMMDDhhmm> stat:<state> err:<error>
```

The formatter is defined in `internal/core/dlr/smpps_receipt.go:82`. Do not
assume the initial successful `submit_sm_resp` means handset delivery; it means
the gateway accepted the submission. Terminal state arrives later, if it was
requested and the upstream SMSC supplies a correlatable receipt.

HTTP's numeric DLR levels do not configure an SMPP ESME. For reference, level 1
is the SMSC submit response, level 2 is the terminal receipt, and level 3 is
both; those values drive HTTP callback correlation
(`internal/core/dlr/correlation.go:174`,
`internal/core/dlr/correlation.go:363`). SMPP customers use the
`registered_delivery` field instead, and Synevyr applies its success/failure
receipt rules from that value (`internal/core/dlr/correlation.go:212`,
`internal/core/dlr/correlation.go:389`).

## Throughput and throttling

Set the SMPP ingress ceiling with
`outbound.users[].mt_credential.smpps_throughput`. A missing, zero, or negative
value means unlimited (`internal/app/outbound/config.go:170`,
`internal/core/throughput/limiter.go:35`). A positive value is a minimum-spacing
limiter, not a token bucket: there is no burst credit and no queue. At 10
submits/second, accepted submits for that user must be at least 100 ms apart
(`internal/core/throughput/limiter.go:1`).

The first submit passes. A rejection does not move the limiter's clock, and the
state is per process and resets on restart
(`internal/core/throughput/limiter.go:28`,
`internal/core/throughput/limiter.go:48`). The check occurs after route
selection but before billing and message-ID allocation, once per inbound
logical submit rather than per segment
(`internal/core/submit_service.go:342`). Exceeding throughput returns
`ESME_RTHROTTLED` without closing the bind; balance or submit-count quota
exhaustion maps to the same status
(`internal/app/smppssubmit/handler.go:133`). Back off before retrying and make
retry deduplication an application concern.

## Command statuses returned by the server

This is the complete set of `command_status` values emitted by the current
SMPPs session, bind, submit, and parser paths. Parser failures are returned in
`generic_nack`; command-specific failures use the matching response PDU
(`internal/core/smpps/session.go:87`,
`internal/transport/smppwire/codec.go:168`).

| Value | Name | Condition |
|---:|---|---|
| `0x00000000` | `ESME_ROK` | Successful bind, `submit_sm`, `enquire_link`, or `unbind` |
| `0x00000002` | `ESME_RINVCMDLEN` | Invalid command length, oversized frame, malformed mandatory body, or trailing bytes |
| `0x00000003` | `ESME_RINVCMDID` | The codec does not recognize `command_id` |
| `0x00000004` | `ESME_RINVBNDSTS` | Submit/data on a non-TX bind, or unbind while not bound |
| `0x00000005` | `ESME_RALYBND` | A second bind request on an already-bound session |
| `0x00000008` | `ESME_RSYSERR` | Deliberate `data_sm` refusal; decodable unsupported request; missing submit handler/body; missing outbound sending identity; or unmapped internal submit failure |
| `0x0000000b` | `ESME_RINVDSTADR` | Empty `destination_addr` on `submit_sm` |
| `0x0000000d` | `ESME_RBINDFAIL` | IP denied, bind authorization false, or `max_bindings` reached |
| `0x0000000e` | `ESME_RINVPASWD` | Unknown system ID, wrong password, disabled user, or disabled group |
| `0x00000045` | `ESME_RSUBMITFAIL` | SMPPs credential/filter rejection, no matching/live route, interceptor rejection, or invalid submit parameter |
| `0x00000058` | `ESME_RTHROTTLED` | Per-user throughput, balance, or submit-count quota exceeded |
| `0x000000c0` | `ESME_RINVOPTPARSTREAM` | Optional section has a truncated TLV header or value |
| `0x000000c1` | `ESME_ROPTPARNOTALLWD` | A known TLV is not allowed on customer `submit_sm`, or its compatibility encoder is unsupported |
| `0x000000c2` | `ESME_RINVPARLEN` | A TLV value has the wrong encoded length |
| `0x000000c3` | `ESME_RMISSINGOPTPARAM` | Only part of the required SAR TLV triple is present |
| `0x000000c4` | `ESME_RINVOPTPARAMVAL` | Invalid TLV enum/range, inconsistent SAR sequence, or both message fields present |

Status constants and parser mappings are defined at
`internal/core/smpps/bindauth.go:5`,
`internal/core/smpps/bindstate.go:12`,
`internal/app/smppssubmit/handler.go:20`, and
`internal/transport/smppwire/types.go:31`. Submit-error conditions are mapped at
`internal/app/smppssubmit/handler.go:133`; bind conditions are mapped at
`internal/core/smpps/bindauth.go:41`.

Most negative responses leave the bind open. Invalid command length or an
oversized frame closes it after `generic_nack`; a decodable unsupported request
that maps to `ESME_RSYSERR` also closes it. Parser errors with recoverable frame
boundaries and command-specific submit failures leave it open
(`internal/core/smpps/session.go:87`,
`internal/core/smpps/session.go:183`).

## TLS

Set both `smpps.tls_cert_file` and `smpps.tls_key_file` to terminate TLS on the
SMPP listener. Supplying only one is a configuration error. The certificate and
key are loaded when the service starts, and failure to bind the address or load
the pair prevents service construction
(`internal/app/smppsserver/service.go:42`,
`internal/app/smppsserver/service.go:132`). The listener requires TLS 1.2 or
newer (`internal/app/smppsserver/service.go:142`).

The configuration exposes a server certificate and private key only; it has no
client-CA or client-certificate-authentication field
(`internal/app/smppsserver/service.go:17`). Not verified: mutual TLS support.
Use bind credentials and the IP/network boundary for client authentication, and
restart the gateway after rotating the certificate because the pair is loaded
at service construction.

## Local interop probe

`scripts/interop/esme_probe.py` is a real third-party ESME built on
`smpp.twisted`; it is not Synevyr's own wire client
(`scripts/interop/esme_probe.py:1`). It has its own response timers and a hard
reactor stop, so a wedged peer cannot hang the command indefinitely
(`scripts/interop/esme_probe.py:84`,
`scripts/interop/esme_probe.py:180`).

Start the local example stack:

```console
$ docker compose -f docker-compose.gateway.yml up \
    --build -d --wait --wait-timeout 120
$ curl --fail --silent --show-error --max-time 8 \
    http://127.0.0.1:1401/ping
Jasmin/PONG
```

The compose file publishes SMPP only on host loopback at port 2775 and mounts
the example configuration
(`docker-compose.gateway.yml:84`,
`docker-compose.gateway.yml:96`). `/ping` returns the exact bytes
`Jasmin/PONG` (`internal/transport/httpcompat/handler.go:142`).

The example's `shortcode-app` account has no matching outbound user, so it is a
safe bind-only probe:

```console
$ python3 scripts/interop/esme_probe.py \
    --host 127.0.0.1 \
    --port 2775 \
    --system-id shortcode-app \
    --password shortcodepw \
    --bind transceiver \
    --timeout 15
{"bound": true, "bind_error": null, "submit_sent": false, "submit_status": null, "submit_message_id": null, "delivered": [], "unbound": true, "errors": []}
```

That single JSON line is the probe's complete stdout schema and success path
(`scripts/interop/esme_probe.py:45`,
`scripts/interop/esme_probe.py:209`). It imports Twisted and the `smpp` package;
the repository does not vendor an interpreter with those packages. The Go
interop test uses `PYTHON_PATH` to select one and skips when it is unavailable
(`internal/core/smpps/interop_test.go:18`). Use an already-provisioned
interpreter rather than assuming the system `python3` has them.

To exercise submission, first make the `smpps.users[].system_id` match an
`outbound.users[].username`, as in the configuration fragment above, then add:

```console
    --submit-text interop \
    --source 1111 \
    --destination 15551230000
```

A successful response has a `"submit_status"` containing `ESME_ROK` and a
nonempty `"submit_message_id"`; the exact message ID is generated per request
when the caller did not provide one (`internal/core/submit_service.go:407`).
The independent test asserts that status and the returned ID
(`internal/core/smpps/interop_test.go:102`).

For the opposite side of the gateway,
`scripts/interop/smsc_probe.py` is a third-party carrier emulator. It can
throttle, delay responses, send DLRs out of order, or drop a connection, and it
has an explicit lifetime timeout (`scripts/interop/smsc_probe.py:23`,
`scripts/interop/smsc_probe.py:253`). For example:

```console
$ python3 scripts/interop/smsc_probe.py \
    --port 2776 \
    --system-id carrier-test \
    --password smscpw1 \
    --lifetime 60
{"event": "listening", "port": 2776}
```

Point an SMPPc connector at `127.0.0.1:2776` while that process runs. This is a
lab tool, not evidence of carrier-specific compatibility; the script itself
lists undocumented TLVs, address normalization, receipt text, and real
throttling policy as things only a real carrier connection can prove
(`scripts/interop/smsc_probe.py:40`).

# Synevyr glossary

Telecom uses several overlapping vocabularies: SMPP describes a wire session,
mobile-network language describes message direction, and Synevyr adds routing,
billing, and administration terms. This glossary explains each term in the
context of this repository.

## Protocol and peers

**SMPP (Short Message Peer-to-Peer).** The binary session protocol Synevyr uses
for SMS exchange. Synevyr implements SMPP 3.4 in both roles: as a client toward
carrier SMSCs and as a server toward customer ESMEs (`README.md:3`,
`internal/transport/smppwire/types.go:8`). It matters because bind role, PDU
state, encoding bytes, sequence numbers, and response statuses are all part of
the integration contract, not HTTP-style implementation details.

**ESME (External Short Messaging Entity).** The application-side SMPP peer. In
the customer-facing flow an ESME connects to Synevyr's SMPP server, authenticates
as `smpps.users[].system_id`, submits MT traffic, and/or receives MO traffic and
receipts (`internal/app/smppsserver/directory.go:18`,
`internal/core/smpps/server.go:27`). It matters when describing direction:
“inbound SMPP” means a connection coming into Synevyr from an ESME, not
necessarily an inbound message to a handset.

**SMSC (Short Message Service Centre).** The upstream system that accepts,
delivers, stores, and reports SMS traffic for a mobile network. Synevyr connects
to it through an SMPP client connector described by `smppc.Config`, including
`host`, `port`, `system_id`, `password`, and `bind`
(`internal/core/smppc/config.go:12`). An SMSC accepting `submit_sm` is not proof
that the handset received the message; a later DLR records delivery outcome
(`internal/core/cdr/model.go:48`).

**A2P (application-to-person).** SMS initiated by software for a person, such as
an alert, verification code, or campaign message. In Synevyr it normally follows
the MT path from HTTP or an ESME, through an MT route, to an SMPPc carrier
connector (`README.md:31`, `internal/core/routingtable/table.go:233`). It matters
commercially because carrier policy, throughput, sender identity, filtering,
and per-part billing are usually attached to that path.

## Direction and sessions

**MT (mobile terminated).** A message travelling toward a mobile subscriber.
Synevyr names this routing direction `routingfilter.MT`, the string `"mt"`;
only an `smppc` connector can terminate an MT route
(`internal/core/routingfilter/filter.go:31`,
`internal/core/routingtable/table.go:233`). “Terminated” refers to the handset
side, even though the message originates at an application.

**MO (mobile originated).** A message that originated at a mobile subscriber
and arrived from an SMSC. Synevyr names it `routingfilter.MO`, the string
`"mo"`, and may route it to an HTTP connector or a bound SMPPs ESME
(`internal/core/routingfilter/filter.go:31`,
`internal/core/routingtable/table.go:233`). DLRs share much of this inbound
carrier path but are status reports, not subscriber-written MO text.

**Bind.** The authenticated SMPP handshake that changes a new connection from
OPEN into a bound role. Synevyr accepts receiver, transmitter, and transceiver
bind commands and tracks `StateBoundRX`, `StateBoundTX`, and `StateBoundTRX`
(`internal/core/smpps/bindstate.go:34`,
`internal/core/smpps/bindstate.go:111`). The role determines which operations
are legal:

* A **transmitter (TX)** sends `submit_sm` but receives no `deliver_sm`.
* A **receiver (RX)** receives `deliver_sm` but cannot send `submit_sm`.
* A **transceiver (TRX)** may do both.

The state gate enforces those permissions
(`internal/core/smpps/bindstate.go:78`). On the carrier-facing side, the same
role names are the `smppc.Config.bind` values `transmitter`, `receiver`, and
`transceiver` (`internal/core/smppc/config.go:12`).

**Enquire link.** `enquire_link` is the SMPP keepalive request, answered by
`enquire_link_resp`. On the server side,
`smpps.enquire_link_timeout` controls how long a quiet session waits before
Synevyr sends one; this timer is separate from
`smpps.inactivity_timeout`, which can disconnect a peer that sends nothing
(`internal/app/smppsserver/service.go:21`,
`internal/core/smpps/session.go:127`). It matters because a receiver bind can be
legitimately quiet for long periods; quiet is not the same as dead.

## PDUs and fields

**PDU (Protocol Data Unit).** One framed SMPP command or response. Its 16-byte
header contains `command_length`, `command_id`, `command_status`, and
`sequence_number`; command-specific fields follow
(`internal/transport/smppwire/types.go:27`,
`internal/transport/smppwire/types.go:67`). The PDU is the unit acknowledged on
the wire, while one human-visible long message may occupy several PDUs.

**`submit_sm`.** The request used to submit an MT message. A bound customer ESME
sends it to Synevyr; the SMPPs adapter validates its destination and credential,
then enters the shared routing, billing, and publication service
(`internal/app/smppssubmit/handler.go:55`). Its success response is
`submit_sm_resp`, carrying the gateway message ID
(`internal/core/smpps/session.go:470`).

**`deliver_sm`.** The request used to deliver an MO or DLR from an SMPP server
to an ESME. Synevyr receives it from carrier SMSCs and sends it to customer RX
or TRX binds (`internal/core/smppc/deliver.go:130`,
`internal/core/smpps/server.go:231`). The receiver must answer
`deliver_sm_resp` with the same sequence number; Synevyr holds an outstanding
window slot until that response or a timeout
(`internal/core/smpps/session.go:352`).

**`data_sm`.** An SMPP request whose content is carried principally through
optional parameters rather than the `submit_sm` body. The wire codec can parse
it, but the customer-facing server deliberately returns
`data_sm_resp`/`ESME_RSYSERR` because its submission, credential, routing, DLR,
and billing path is defined for `submit_sm`
(`internal/transport/smppwire/codec.go:87`,
`internal/core/smpps/session.go:207`). It is not a supported alternative for
binary or long customer messages.

**`generic_nack`.** A generic negative acknowledgement used when a request
cannot be handled as a normal command, such as an unknown `command_id` or
unparseable PDU. Synevyr maps parser failure categories to an SMPP status and
sends `generic_nack` using the offending PDU's sequence number
(`internal/transport/smppwire/codec.go:168`,
`internal/core/smpps/session.go:87`). From an ESME, a `generic_nack` with a
matching sequence negatively settles an outstanding Synevyr `deliver_sm`
(`internal/core/smpps/session.go:224`).

**Command status.** The `command_status` header field reports the result of an
SMPP request. Zero is `ESME_ROK`; other values identify state, authentication,
submission, throttling, or parse failures. Synevyr's bind statuses are defined
in `internal/core/smpps/bindauth.go:5`, submit statuses in
`internal/app/smppssubmit/handler.go:20`, and optional-parameter statuses in
`internal/transport/smppwire/types.go:31`. It matters because a retryable
`ESME_RTHROTTLED` is operationally different from a malformed-PDU error or bad
password.

**Sequence number.** The request/response correlation integer in every SMPP PDU
header (`internal/transport/smppwire/types.go:67`). Synevyr assigns
server-originated requests values from 1 through `0x7fffffff` and wraps to 1
(`internal/core/smpps/session.go:16`,
`internal/core/smpps/session.go:152`). An ESME must copy a received
`deliver_sm` sequence number into its `deliver_sm_resp`; otherwise the correct
window entry cannot be released.

**TON (Type of Number).** An SMPP byte describing how an address should be
interpreted, such as international or national. Synevyr keeps separate source
and destination values in `SMBody.SourceAddressTON` and
`SMBody.DestinationAddressTON`; carrier connector defaults are `src_ton` and
`dst_ton` (`internal/transport/smppwire/types.go:114`,
`internal/core/smppc/config.go:34`). The digit string alone may be ambiguous, so
TON is routing metadata, not decoration.

**NPI (Numbering Plan Indicator).** The companion SMPP byte identifying the
numbering plan. It maps to `SMBody.SourceAddressNPI` and
`SMBody.DestinationAddressNPI`, with carrier connector defaults `src_npi` and
`dst_npi` (`internal/transport/smppwire/types.go:114`,
`internal/core/smppc/config.go:34`). TON and NPI travel with the address and are
also saved for building a DLR back to the original SMPP sender
(`internal/app/smppssubmit/handler.go:117`).

**MSISDN (Mobile Station International Subscriber Directory Number).** The
subscriber's dialable mobile number, usually the MT destination and MO source.
Synevyr has no distinct MSISDN type: on the wire it is a byte string in
`source_addr` or `destination_addr`, interpreted with TON/NPI
(`internal/transport/smppwire/types.go:114`). This matters because Synevyr does
not infer or normalize a national/international format merely because an
address contains digits; an operator must align the address and metadata with
the carrier contract.

**`short_message`.** The mandatory length-prefixed user-data field in
`submit_sm` and `deliver_sm`, represented by `SMBody.ShortMessage`
(`internal/transport/smppwire/types.go:114`,
`internal/transport/smppwire/codec.go:311`). Its wire length is one byte, so the
codec refuses more than 255 octets when encoding
(`internal/transport/smppwire/codec.go:339`). Encoding and UDH bytes live inside
this field; it is not inherently UTF-8 text.

**`message_payload`.** Optional TLV `0x0424`, used to carry message bytes outside
`short_message`. Synevyr requires `message_payload` and a nonempty
`short_message` to be mutually exclusive and retains it in
`SMBody.Optional.MessagePayload`
(`internal/transport/smppwire/codec.go:786`,
`internal/transport/smppwire/types.go:160`). It matters for payloads that do not
fit the short field, but the current SMPPs content credential checks only
`short_message`, not this TLV (`internal/core/mtcredential/submit.go:51`).
The MT routable also marks the empty short-message field present, preventing
short-message route or interceptor filters from falling back to
`message_payload` (`internal/core/submit_service.go:277`,
`internal/core/routingfilter/filter.go:275`).

**TLV (tag-length-value).** An SMPP optional-parameter encoding: a two-byte tag,
two-byte length, and that many value octets. Synevyr parses the optional section
in `decodeTLVs`; truncated framing is fatal because the next boundary is
unknowable (`internal/transport/smppwire/codec.go:517`). Standard TLVs carry
features such as SAR and receipt state; unknown vendor TLVs are captured for
forwarding (`internal/transport/smppwire/types.go:139`).

**ESM class.** The `esm_class` byte classifies an SMPP message and carries GSM
feature bits. In this code, bit `0x40` is the UDHI flag saying
`short_message` starts with a User Data Header
(`internal/core/http.go:93`, `internal/core/http.go:131`). A generated delivery
receipt uses value `0x04` to mark the SMSC-delivery-receipt type
(`internal/core/dlr/smpps_receipt.go:24`). Misstating the class can make a UDH
look like message text or make an ordinary MO look like a DLR.

**`registered_delivery`.** The `submit_sm` byte that asks for delivery reporting.
For SMPPs input, Synevyr treats any nonzero value in the low two bits as a
receipt request and records enough origin data to route a later receipt back to
the same system ID (`internal/app/smppssubmit/handler.go:27`,
`internal/app/smppssubmit/handler.go:111`). Correlation recognizes low-bit value
1 as success/failure receipts and 2 as failure-only; value 3 normalizes to no
request (`internal/core/dlr/amqp_projection.go:227`). It is the SMPP control;
numeric HTTP DLR levels are a different API concept.

**Validity period.** The `submit_sm.validity_period` C-octet string tells the
SMSC when an undelivered message should expire. Synevyr carries the raw field in
`SMBody.ValidityPeriod` and preserves it in the outbound raw PDU
(`internal/transport/smppwire/types.go:125`,
`internal/app/outbound/submit_envelope_builder.go:211`). The carrier connector's
fallback is `smppc.validity_period` (`internal/core/smppc/config.go:91`).
It matters because a short validity reduces stale delivery attempts but gives
the network less time to reach an unavailable handset.

## Encoding and multipart messages

**GSM 03.38.** The GSM default alphabet used for compact SMS text. On the
non-SMPP text path with coding zero, Synevyr encodes characters into GSM 03.38,
uses an escape plus a second septet for extension-table characters, maps `ç`
and selected Greek capitals to GSM lookalikes, and replaces other unsupported
runes with `?` (`internal/core/submit_service.go:630`). A real ESME submission
is byte-preserving and skips this conversion
(`internal/core/submit_service.go:254`), so the ESME must encode its own content.

**`data_coding`.** The SMPP byte declaring how user data is encoded. It maps to
`SMBody.DataCoding`; Synevyr's compatibility encoder classifies every byte from
0 through 255 (`internal/transport/smppwire/types.go:129`,
`internal/transport/picklecompat/native_submit.go:434`). It matters both for
decoding at the handset and for length accounting. The ESME-facing path does
not verify that message bytes actually match the declared coding.

**UDH (User Data Header).** Binary metadata at the start of
`short_message`, signalled by the UDHI bit in `esm_class`. A concatenation UDH
can contain a reference, total part count, and sequence number. Synevyr
preserves an ESME's pre-encoded UDH PDU instead of prepending another header
(`internal/core/segmentation/segmentation.go:44`). On inbound carrier traffic,
the implemented long-message detector recognizes the six-byte 8-bit form
`05 00 03 ref total sequence` (`internal/core/smppc/deliver.go:155`).

**SAR (segmentation and reassembly).** SMPP's TLV-based concatenation mechanism,
using `sar_msg_ref_num`, `sar_total_segments`, and `sar_segment_seqnum`. Synevyr
requires all three together and validates nonzero, consistent sequence values
(`internal/transport/smppwire/codec.go:794`). SAR avoids consuming bytes inside
`short_message`, but every segment still travels as a separate PDU.

**Concatenation.** Presenting several SMS segments as one logical message. UDH
and SAR are two ways to identify and order the parts. Synevyr preserves
customer-supplied SMPP parts rather than segmenting them again
(`internal/core/submit_service.go:375`); for carrier MO traffic it stores parts
and reassembles a whole after all sequences arrive
(`internal/core/smppc/deliver.go:169`). An SMPP customer receives the marked
segments, while an HTTP MO destination receives the reassembled whole
(`internal/app/modispatch/service.go:451`).

## Delivery, flow control, and charging

**Delivery receipt (DLR).** An asynchronous status report for an earlier
submission. It can report states such as delivered, expired, deleted,
undeliverable, or rejected (`internal/core/cdr/model.go:48`). For an SMPP
customer, Synevyr sends the DLR as `deliver_sm` with `esm_class` `0x04`,
receipt text, and `receipted_message_id` and `message_state` TLVs
(`internal/core/dlr/smpps_receipt.go:104`). A DLR is distinct from a successful
`submit_sm_resp`, which only acknowledges submission.

**DLR levels.** Synevyr's HTTP callback API uses level 1 for the SMSC submit
response, level 2 for a later terminal receipt, and level 3 for both. The
correlator publishes levels 1/3 at submit response and tracks levels 2/3 for
terminal delivery (`internal/core/dlr/correlation.go:174`,
`internal/core/dlr/correlation.go:363`). SMPP ESMEs do not send this numeric
level; they use `registered_delivery`.

**Throttling.** Refusing traffic temporarily because it exceeds an allowed
rate. The customer SMPP setting is
`outbound.users[].mt_credential.smpps_throughput`; Synevyr implements it as a
minimum interval between accepted logical submits, with no burst bucket or
queue (`internal/app/outbound/config.go:170`,
`internal/core/throughput/limiter.go:1`). Rejection returns
`ESME_RTHROTTLED` and leaves the bind alive
(`internal/app/smppssubmit/handler.go:139`), so a client should slow down and
retry deliberately.

**Window.** The maximum number of SMPP requests sent but not yet answered.
For customer delivery,
`smpps.deliver_sm_window_size` bounds outstanding `deliver_sm` per session and
`smpps.deliver_sm_response_timeout` bounds each wait
(`internal/app/smppsserver/service.go:28`). The core defaults are 10 and 30
seconds (`internal/core/smpps/server.go:62`). A window prevents an
unacknowledging peer from causing unbounded in-flight work; increasing it trades
more concurrency for more uncertain work on failure.

**Prepaid.** Charging the configured early amount before durable message
admission. A CDR whose amount is entirely early is classified `PREPAID`
(`internal/core/cdr/model.go:102`,
`internal/core/cdr/model.go:111`). It matters because insufficient balance can
reject the submit before it is sent.

**Postpaid.** Charging the configured late amount only after SMSC acceptance. A
CDR whose amount is entirely late is classified `POSTPAID`; a mixture of early
and late amounts is `SPLIT` (`internal/core/cdr/model.go:111`,
`internal/core/cdr/model.go:159`). It reduces upfront charging but makes
acceptance correlation and idempotent late billing commercially critical.

**CDR (call detail record, used here for SMS).** The durable commercial record
for one admitted message part. Synevyr's `cdr.Record` stores routing identity,
rate, currency, early/late amounts, SMSC submission result, delivery result, and
billing outcome (`internal/core/cdr/model.go:138`,
`internal/core/cdr/model.go:159`). It deliberately excludes message content,
addresses, credentials, and callback URLs (`internal/core/cdr/model.go:1`).
Use CDRs for reconciliation and audit, not as a message archive.

## Routing and administration

**Connector.** A named destination or source endpoint used by routing. The
routing model recognizes `smppc` for carrier-facing SMPP, `smpps` for a bound
customer ESME, and `http` for a webhook
(`internal/core/routingtable/table.go:25`). MT routes may target only `smppc`;
MO routes may target `http` or `smpps`
(`internal/core/routingtable/table.go:233`). Carrier connection settings live
under SMPPc connector configuration, while an SMPPs MO connector identifies a
customer `system_id`.

**Route.** A rule that chooses a connector after its filters match. A route
holds direction, order, connector or connector pool, rate, filters, and whether
it is the default (`internal/core/routingtable/table.go:41`). Tables evaluate
higher orders first and select the first matching route; order zero is reserved
for the default (`internal/core/routingtable/table.go:141`,
`internal/core/routingtable/table.go:198`). It matters because the selected MT
route determines both carrier and price.

**Filter.** A predicate over a routable message. Built-in kinds include
connector, user, group, source address, destination address, short message,
date interval, time interval, tag, and Python evaluation
(`internal/core/routingfilter/filter.go:38`). Route and interceptor filters are
embedded in their owner rather than referenced as reusable live objects
(`docs/reference/deviations.md:27`). A filter decides whether a rule applies; it
does not itself select the next rule or connector.

**Interceptor.** An ordered Python hook that runs before route selection and
may return a changed routable message or reject it. Each interceptor has a
script and optional filters; tables run from highest order downward and stop on
reject (`internal/core/interceptor/interceptor.go:23`,
`internal/core/interceptor/interceptor.go:103`). Config fields are
`outbound.mt_interceptors` and `outbound.mo_interceptors`
(`internal/app/outbound/config.go:46`). These scripts execute on the gateway
host, so admin editing is disabled by default and enabling it is equivalent to
granting shell-level trust (`internal/app/gateway/config.go:132`).

**jCli.** Synevyr's Jasmin-compatible, telnet-style management console. It is a
third interface over the same admin services used by the JSON API and web UI;
commands manage connectors, routes, users, groups, filters, interceptors,
profiles, and statistics (`internal/app/jcli/server.go:1`,
`internal/app/jcli/server.go:33`). Configure it with
`admin.jcli_listen_address`, `jcli_username`, `jcli_password`, and
`jcli_idle_timeout`; an empty listen address disables it
(`internal/app/gateway/config.go:138`). Its transcript is deliberately
compatibility-sensitive because operator automation parses it, and it should be
bound to an internal interface.

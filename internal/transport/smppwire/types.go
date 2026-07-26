package smppwire

import (
	"errors"
	"fmt"
)

const (
	CommandSubmitSM            uint32 = 0x00000004
	CommandDeliverSM           uint32 = 0x00000005
	CommandDataSM              uint32 = 0x00000103
	CommandBindReceiver        uint32 = 0x00000001
	CommandBindTransmitter     uint32 = 0x00000002
	CommandUnbind              uint32 = 0x00000006
	CommandBindTransceiver     uint32 = 0x00000009
	CommandSubmitSMResp        uint32 = 0x80000004
	CommandDeliverSMResp       uint32 = 0x80000005
	CommandDataSMResp          uint32 = 0x80000103
	CommandBindReceiverResp    uint32 = 0x80000001
	CommandBindTransmitterResp uint32 = 0x80000002
	CommandUnbindResp          uint32 = 0x80000006
	CommandBindTransceiverResp uint32 = 0x80000009
	CommandEnquireLink         uint32 = 0x00000015
	CommandEnquireLinkResp     uint32 = 0x80000015

	HeaderSize     uint32 = 16
	DefaultMaxSize uint32 = 1 << 20
)

const (
	tagReceiptedMessageID uint16 = 0x001e
	tagSARMessageRef      uint16 = 0x020c
	tagSARTotalSegments   uint16 = 0x020e
	tagSARSegmentSequence uint16 = 0x020f
	tagMessagePayload     uint16 = 0x0424
	tagMoreMessagesToSend uint16 = 0x0426
	tagMessageState       uint16 = 0x0427
)

var (
	ErrInvalidCommandLength          = errors.New("invalid SMPP command length")
	ErrFrameTooLarge                 = errors.New("SMPP frame exceeds configured maximum")
	ErrTruncatedFrame                = errors.New("truncated SMPP frame")
	ErrUnsupportedCommand            = errors.New("unsupported SMPP command")
	ErrMalformedCString              = errors.New("malformed SMPP C-octet string")
	ErrMalformedTLV                  = errors.New("malformed SMPP TLV")
	ErrLegacyMessagePayloadRoundTrip = errors.New("legacy message_payload round-trip failure")
)

type Header struct {
	CommandLength  uint32
	CommandID      uint32
	CommandStatus  uint32
	SequenceNumber uint32
}

type PDU struct {
	Header         Header
	Bind           *BindBody
	BindResponse   *BindResponseBody
	SM             *SMBody
	SubmitResponse *SubmitResponseBody

	decodedMessagePayload bool
}

type BindBody struct {
	SystemID         []byte
	Password         []byte
	SystemType       []byte
	InterfaceVersion byte
	AddressTON       byte
	AddressNPI       byte
	AddressRange     []byte
}

type BindResponseBody struct {
	SystemID []byte
}

type SMBody struct {
	ServiceType           []byte
	SourceAddressTON      byte
	SourceAddressNPI      byte
	SourceAddress         []byte
	DestinationAddressTON byte
	DestinationAddressNPI byte
	DestinationAddress    []byte
	ESMClass              byte
	ProtocolID            byte
	PriorityFlag          byte
	ScheduleDeliveryTime  []byte
	ValidityPeriod        []byte
	RegisteredDelivery    byte
	ReplaceIfPresentFlag  byte
	DataCoding            byte
	SMDefaultMessageID    byte
	ShortMessage          []byte
	Optional              OptionalParameters
	// VendorTLVs is a pre-encoded vendor custom-TLV section appended verbatim
	// after the known optional parameters on encode (the position the legacy
	// patched encoder emits pdu.custom_tlvs). Encode-only: decode never
	// populates it — unknown wire TLVs stay unretained per KNOWN_QUIRKS Q-016,
	// so an encode/decode round trip does not preserve this field.
	VendorTLVs []byte
	// CapturedVendorTLVs are wire TLVs outside the legacy library's known-tag
	// set, captured on decode exactly like the fork's decoder patch
	// (install_pdu_decoder_patch): raw octets, wire order, duplicates kept.
	// Decode-only: encode does NOT re-emit them, so re-encoding drops unknown
	// TLVs per KNOWN_QUIRKS Q-016 — capture is for MO/DLR forwarding, which
	// projects them as custom_tlvs rather than round-tripping the wire.
	CapturedVendorTLVs []CapturedVendorTLV
}

// CapturedVendorTLV is one captured vendor-range wire TLV. The legacy capture
// shape is (tag, length, 'OctetString', bytes); length is always len(Value)
// because the wire framing supplies exactly the declared byte count.
type CapturedVendorTLV struct {
	Tag   uint16
	Value []byte
}

// SubmitSMBody names the canonical outbound submit_sm body while preserving
// SMBody compatibility for deliver_sm and existing callers.
type SubmitSMBody = SMBody

// OptionalParameters carries decoded optional TLVs. encodeTLVs re-emits every
// present field in the frozen library's deliver_sm optionalParams order, so a
// decode -> encode round trip is byte-identical to the patched Python encoder.
// Validation mirrors the legacy library: fixed lengths, enum value tables, and
// callback_num structure errors fail the whole PDU decode exactly where
// smpp.pdu3 raises.
type OptionalParameters struct {
	SARMessageReference *uint16
	SARTotalSegments    *byte
	SARSegmentSequence  *byte
	MoreMessagesToSend  *byte
	MessagePayload      []byte
	ReceiptedMessageID  []byte
	MessageState        *byte

	UserMessageReference *uint16
	SourcePort           *uint16
	DestinationPort      *uint16
	PayloadType          *byte
	PrivacyIndicator     *byte
	LanguageIndicator    *byte
	CallbackNum          *CallbackNumber
	// NetworkErrorCode is verbatim octets of any length (the legacy encoder
	// reads exactly the declared length); non-nil-empty means present-empty.
	NetworkErrorCode []byte
}

// CallbackNumber is the decoded callback_num structure: digit mode, TON, NPI,
// then the remaining octets as digits — validated against the legacy value
// tables on decode.
type CallbackNumber struct {
	DigitMode byte
	TON       byte
	NPI       byte
	Digits    []byte
}

type SubmitResponseBody struct {
	MessageID []byte
}

type LegacyMessagePayloadError struct {
	Size int
}

func (e *LegacyMessagePayloadError) Error() string {
	return fmt.Sprintf("legacy message_payload size %d does not match expected 1", e.Size)
}

func (e *LegacyMessagePayloadError) Unwrap() error {
	return ErrLegacyMessagePayloadRoundTrip
}

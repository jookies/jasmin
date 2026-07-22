package smppwire

import (
	"errors"
	"fmt"
)

const (
	CommandSubmitSM            uint32 = 0x00000004
	CommandDeliverSM           uint32 = 0x00000005
	CommandUnbind              uint32 = 0x00000006
	CommandBindTransceiver     uint32 = 0x00000009
	CommandSubmitSMResp        uint32 = 0x80000004
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
}

// SubmitSMBody names the canonical outbound submit_sm body while preserving
// SMBody compatibility for deliver_sm and existing callers.
type SubmitSMBody = SMBody

type OptionalParameters struct {
	SARMessageReference *uint16
	SARTotalSegments    *byte
	SARSegmentSequence  *byte
	MessagePayload      []byte
	ReceiptedMessageID  []byte
	MessageState        *byte
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

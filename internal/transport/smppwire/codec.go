package smppwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

func Decode(frame []byte) (PDU, error) {
	if len(frame) < int(HeaderSize) {
		return PDU{}, ErrTruncatedFrame
	}
	header := decodeHeader(frame[:HeaderSize])
	if header.CommandLength < HeaderSize {
		return PDU{}, fmt.Errorf("%w: %d", ErrInvalidCommandLength, header.CommandLength)
	}
	if header.CommandLength > DefaultMaxSize {
		return PDU{}, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, header.CommandLength, DefaultMaxSize)
	}
	if uint32(len(frame)) != header.CommandLength {
		return PDU{}, fmt.Errorf("%w: header=%d bytes=%d", ErrTruncatedFrame, header.CommandLength, len(frame))
	}
	return decodeBody(header, frame[HeaderSize:])
}

func Read(r io.Reader, maximum uint32) (PDU, error) {
	if maximum == 0 {
		maximum = DefaultMaxSize
	}
	headerBytes := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, headerBytes); err != nil {
		return PDU{}, fmt.Errorf("%w: header: %v", ErrTruncatedFrame, err)
	}
	header := decodeHeader(headerBytes)
	if header.CommandLength < HeaderSize {
		return PDU{}, fmt.Errorf("%w: %d", ErrInvalidCommandLength, header.CommandLength)
	}
	if header.CommandLength > maximum {
		return PDU{}, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, header.CommandLength, maximum)
	}
	body := make([]byte, header.CommandLength-HeaderSize)
	if _, err := io.ReadFull(r, body); err != nil {
		return PDU{}, fmt.Errorf("%w: body: %v", ErrTruncatedFrame, err)
	}
	return decodeBody(header, body)
}

func Encode(pdu PDU) ([]byte, error) {
	var body []byte
	var err error
	switch pdu.Header.CommandID {
	case CommandBindTransceiver:
		body, err = encodeBind(pdu.Bind)
	case CommandBindTransceiverResp:
		if pdu.BindResponse == nil && pdu.Header.CommandStatus != 0 {
			body = nil
		} else {
			body, err = encodeBindResponse(pdu.BindResponse)
		}
	case CommandEnquireLink, CommandEnquireLinkResp, CommandUnbind, CommandUnbindResp:
		body = nil
	case CommandSubmitSM, CommandDeliverSM:
		if pdu.decodedMessagePayload {
			return nil, &LegacyMessagePayloadError{Size: len(pdu.SM.Optional.MessagePayload)}
		}
		body, err = encodeSM(pdu.SM)
	case CommandSubmitSMResp:
		body, err = encodeSubmitResponse(pdu.SubmitResponse)
	default:
		return nil, fmt.Errorf("%w: %#x", ErrUnsupportedCommand, pdu.Header.CommandID)
	}
	if err != nil {
		return nil, err
	}
	length := HeaderSize + uint32(len(body))
	if length > DefaultMaxSize {
		return nil, fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, length, DefaultMaxSize)
	}
	wire := make([]byte, length)
	binary.BigEndian.PutUint32(wire[0:4], length)
	binary.BigEndian.PutUint32(wire[4:8], pdu.Header.CommandID)
	binary.BigEndian.PutUint32(wire[8:12], pdu.Header.CommandStatus)
	binary.BigEndian.PutUint32(wire[12:16], pdu.Header.SequenceNumber)
	copy(wire[HeaderSize:], body)
	return wire, nil
}

func decodeHeader(data []byte) Header {
	return Header{
		CommandLength:  binary.BigEndian.Uint32(data[0:4]),
		CommandID:      binary.BigEndian.Uint32(data[4:8]),
		CommandStatus:  binary.BigEndian.Uint32(data[8:12]),
		SequenceNumber: binary.BigEndian.Uint32(data[12:16]),
	}
}

func decodeBody(header Header, body []byte) (PDU, error) {
	pdu := PDU{Header: header}
	cursor := newCursor(body)
	var err error
	switch header.CommandID {
	case CommandBindTransceiver:
		pdu.Bind, err = decodeBind(cursor)
	case CommandBindTransceiverResp:
		if cursor.remaining() == 0 && header.CommandStatus != 0 {
			// Error bind responses may be header-only. Preserve the absence of the
			// optional system_id so Decode -> Encode remains byte-exact.
			pdu.BindResponse = nil
		} else {
			pdu.BindResponse, err = decodeBindResponse(cursor)
		}
	case CommandEnquireLink, CommandEnquireLinkResp, CommandUnbind, CommandUnbindResp:
		// Header-only control PDUs.
	case CommandSubmitSM, CommandDeliverSM:
		pdu.SM, pdu.decodedMessagePayload, err = decodeSM(cursor)
	case CommandSubmitSMResp:
		pdu.SubmitResponse, err = decodeSubmitResponse(cursor)
	default:
		err = fmt.Errorf("%w: %#x", ErrUnsupportedCommand, header.CommandID)
	}
	if err != nil {
		return PDU{}, err
	}
	if cursor.remaining() != 0 {
		return PDU{}, fmt.Errorf("unexpected trailing mandatory bytes: %d", cursor.remaining())
	}
	return pdu, nil
}

func decodeBind(c *cursor) (*BindBody, error) {
	body := &BindBody{}
	var err error
	if body.SystemID, err = c.cstring(); err != nil {
		return nil, fieldError("system_id", err)
	}
	if body.Password, err = c.cstring(); err != nil {
		return nil, fieldError("password", err)
	}
	if body.SystemType, err = c.cstring(); err != nil {
		return nil, fieldError("system_type", err)
	}
	if body.InterfaceVersion, err = c.byte(); err != nil {
		return nil, fieldError("interface_version", err)
	}
	if body.AddressTON, err = c.byte(); err != nil {
		return nil, fieldError("addr_ton", err)
	}
	if body.AddressNPI, err = c.byte(); err != nil {
		return nil, fieldError("addr_npi", err)
	}
	if body.AddressRange, err = c.cstring(); err != nil {
		return nil, fieldError("address_range", err)
	}
	return body, nil
}

func encodeBind(body *BindBody) ([]byte, error) {
	if body == nil {
		return nil, errors.New("bind body is required")
	}
	size := cstringWireSize(body.SystemID) + cstringWireSize(body.Password) +
		cstringWireSize(body.SystemType) + 3 + cstringWireSize(body.AddressRange)
	if err := ensureBodySize(size); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	for _, field := range []struct {
		name  string
		value []byte
	}{{"system_id", body.SystemID}, {"password", body.Password}, {"system_type", body.SystemType}} {
		if err := writeCString(&output, field.name, field.value); err != nil {
			return nil, err
		}
	}
	output.WriteByte(body.InterfaceVersion)
	output.WriteByte(body.AddressTON)
	output.WriteByte(body.AddressNPI)
	if err := writeCString(&output, "address_range", body.AddressRange); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func decodeBindResponse(c *cursor) (*BindResponseBody, error) {
	systemID, err := c.cstring()
	if err != nil {
		return nil, fieldError("system_id", err)
	}
	return &BindResponseBody{SystemID: systemID}, nil
}

func encodeBindResponse(body *BindResponseBody) ([]byte, error) {
	if body == nil {
		return nil, errors.New("bind response body is required")
	}
	var output bytes.Buffer
	if err := writeCString(&output, "system_id", body.SystemID); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func decodeSM(c *cursor) (*SMBody, bool, error) {
	body := &SMBody{}
	var err error
	if body.ServiceType, err = c.cstring(); err != nil {
		return nil, false, fieldError("service_type", err)
	}
	if body.SourceAddressTON, err = c.byte(); err != nil {
		return nil, false, fieldError("source_addr_ton", err)
	}
	if body.SourceAddressNPI, err = c.byte(); err != nil {
		return nil, false, fieldError("source_addr_npi", err)
	}
	if body.SourceAddress, err = c.cstring(); err != nil {
		return nil, false, fieldError("source_addr", err)
	}
	if body.DestinationAddressTON, err = c.byte(); err != nil {
		return nil, false, fieldError("dest_addr_ton", err)
	}
	if body.DestinationAddressNPI, err = c.byte(); err != nil {
		return nil, false, fieldError("dest_addr_npi", err)
	}
	if body.DestinationAddress, err = c.cstring(); err != nil {
		return nil, false, fieldError("destination_addr", err)
	}
	if body.ESMClass, err = c.byte(); err != nil {
		return nil, false, fieldError("esm_class", err)
	}
	if body.ProtocolID, err = c.byte(); err != nil {
		return nil, false, fieldError("protocol_id", err)
	}
	if body.PriorityFlag, err = c.byte(); err != nil {
		return nil, false, fieldError("priority_flag", err)
	}
	if body.ScheduleDeliveryTime, err = c.cstring(); err != nil {
		return nil, false, fieldError("schedule_delivery_time", err)
	}
	if body.ValidityPeriod, err = c.cstring(); err != nil {
		return nil, false, fieldError("validity_period", err)
	}
	if body.RegisteredDelivery, err = c.byte(); err != nil {
		return nil, false, fieldError("registered_delivery", err)
	}
	if body.ReplaceIfPresentFlag, err = c.byte(); err != nil {
		return nil, false, fieldError("replace_if_present_flag", err)
	}
	if body.DataCoding, err = c.byte(); err != nil {
		return nil, false, fieldError("data_coding", err)
	}
	if body.SMDefaultMessageID, err = c.byte(); err != nil {
		return nil, false, fieldError("sm_default_msg_id", err)
	}
	messageLength, err := c.byte()
	if err != nil {
		return nil, false, fieldError("sm_length", err)
	}
	if body.ShortMessage, err = c.take(int(messageLength)); err != nil {
		return nil, false, fieldError("short_message", err)
	}
	messagePayload, err := decodeTLVs(c, &body.Optional)
	if err != nil {
		return nil, false, err
	}
	return body, messagePayload, nil
}

func encodeSM(body *SMBody) ([]byte, error) {
	if body == nil {
		return nil, errors.New("submit/deliver body is required")
	}
	if len(body.ShortMessage) > 255 {
		return nil, fmt.Errorf("short_message length %d exceeds 255", len(body.ShortMessage))
	}
	size := cstringWireSize(body.ServiceType) + cstringWireSize(body.SourceAddress) +
		cstringWireSize(body.DestinationAddress) + cstringWireSize(body.ScheduleDeliveryTime) +
		cstringWireSize(body.ValidityPeriod) + 12 + uint64(len(body.ShortMessage)) +
		optionalWireSize(body.Optional) + uint64(len(body.VendorTLVs))
	if err := ensureBodySize(size); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := writeCString(&output, "service_type", body.ServiceType); err != nil {
		return nil, err
	}
	output.WriteByte(body.SourceAddressTON)
	output.WriteByte(body.SourceAddressNPI)
	if err := writeCString(&output, "source_addr", body.SourceAddress); err != nil {
		return nil, err
	}
	output.WriteByte(body.DestinationAddressTON)
	output.WriteByte(body.DestinationAddressNPI)
	if err := writeCString(&output, "destination_addr", body.DestinationAddress); err != nil {
		return nil, err
	}
	output.WriteByte(body.ESMClass)
	output.WriteByte(body.ProtocolID)
	output.WriteByte(body.PriorityFlag)
	if err := writeCString(&output, "schedule_delivery_time", body.ScheduleDeliveryTime); err != nil {
		return nil, err
	}
	if err := writeCString(&output, "validity_period", body.ValidityPeriod); err != nil {
		return nil, err
	}
	output.WriteByte(body.RegisteredDelivery)
	output.WriteByte(body.ReplaceIfPresentFlag)
	output.WriteByte(body.DataCoding)
	output.WriteByte(body.SMDefaultMessageID)
	output.WriteByte(byte(len(body.ShortMessage)))
	output.Write(body.ShortMessage)
	if err := encodeTLVs(&output, body.Optional); err != nil {
		return nil, err
	}
	output.Write(body.VendorTLVs)
	return output.Bytes(), nil
}

func decodeSubmitResponse(c *cursor) (*SubmitResponseBody, error) {
	messageID, err := c.cstring()
	if err != nil {
		return nil, fieldError("message_id", err)
	}
	return &SubmitResponseBody{MessageID: messageID}, nil
}

func encodeSubmitResponse(body *SubmitResponseBody) ([]byte, error) {
	if body == nil {
		return nil, errors.New("submit_sm_resp body is required")
	}
	if err := ensureBodySize(cstringWireSize(body.MessageID)); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := writeCString(&output, "message_id", body.MessageID); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func decodeTLVs(c *cursor, optional *OptionalParameters) (bool, error) {
	messagePayload := false
	for c.remaining() != 0 {
		if c.remaining() < 4 {
			return false, fmt.Errorf("%w: truncated TLV header", ErrMalformedTLV)
		}
		tag, _ := c.uint16()
		length, _ := c.uint16()
		value, err := c.take(int(length))
		if err != nil {
			return false, fmt.Errorf("%w: tag %#04x length %d", ErrMalformedTLV, tag, length)
		}
		switch tag {
		case tagSARMessageRef:
			if len(value) != 2 {
				return false, fixedTLVLengthError(tag, len(value), 2)
			}
			v := binary.BigEndian.Uint16(value)
			optional.SARMessageReference = &v
		case tagSARTotalSegments:
			if len(value) != 1 {
				return false, fixedTLVLengthError(tag, len(value), 1)
			}
			v := value[0]
			optional.SARTotalSegments = &v
		case tagSARSegmentSequence:
			if len(value) != 1 {
				return false, fixedTLVLengthError(tag, len(value), 1)
			}
			v := value[0]
			optional.SARSegmentSequence = &v
		case tagMessagePayload:
			optional.MessagePayload = append([]byte(nil), value...)
			messagePayload = true
		case tagReceiptedMessageID:
			if len(value) == 0 || value[len(value)-1] != 0 {
				return false, fmt.Errorf("%w: receipted_message_id is not NUL terminated", ErrMalformedTLV)
			}
			optional.ReceiptedMessageID = append([]byte(nil), value[:len(value)-1]...)
		case tagMessageState:
			if len(value) != 1 {
				return false, fixedTLVLengthError(tag, len(value), 1)
			}
			v := value[0]
			optional.MessageState = &v
		default:
			// Frozen compatibility behavior accepts unknown optionals but does not
			// retain them for re-encoding (KNOWN_QUIRKS Q-016).
		}
	}
	return messagePayload, nil
}

func encodeTLVs(output *bytes.Buffer, optional OptionalParameters) error {
	if optional.SARMessageReference != nil {
		value := make([]byte, 2)
		binary.BigEndian.PutUint16(value, *optional.SARMessageReference)
		if err := writeTLV(output, tagSARMessageRef, value); err != nil {
			return err
		}
	}
	if optional.SARTotalSegments != nil {
		if err := writeTLV(output, tagSARTotalSegments, []byte{*optional.SARTotalSegments}); err != nil {
			return err
		}
	}
	if optional.SARSegmentSequence != nil {
		if err := writeTLV(output, tagSARSegmentSequence, []byte{*optional.SARSegmentSequence}); err != nil {
			return err
		}
	}
	if optional.MessagePayload != nil {
		if err := writeTLV(output, tagMessagePayload, optional.MessagePayload); err != nil {
			return err
		}
	}
	if optional.ReceiptedMessageID != nil {
		value := append(append([]byte(nil), optional.ReceiptedMessageID...), 0)
		if err := writeTLV(output, tagReceiptedMessageID, value); err != nil {
			return err
		}
	}
	if optional.MessageState != nil {
		if err := writeTLV(output, tagMessageState, []byte{*optional.MessageState}); err != nil {
			return err
		}
	}
	return nil
}

func writeTLV(output *bytes.Buffer, tag uint16, value []byte) error {
	if len(value) > int(^uint16(0)) {
		return fmt.Errorf("%w: tag %#04x length %d exceeds 65535", ErrMalformedTLV, tag, len(value))
	}
	_ = binary.Write(output, binary.BigEndian, tag)
	_ = binary.Write(output, binary.BigEndian, uint16(len(value)))
	output.Write(value)
	return nil
}

func writeCString(output *bytes.Buffer, name string, value []byte) error {
	if bytes.IndexByte(value, 0) >= 0 {
		return fmt.Errorf("%w: %s contains NUL", ErrMalformedCString, name)
	}
	output.Write(value)
	output.WriteByte(0)
	return nil
}

func fixedTLVLengthError(tag uint16, got, want int) error {
	return fmt.Errorf("%w: tag %#04x length %d, want %d", ErrMalformedTLV, tag, got, want)
}

func fieldError(name string, err error) error {
	return fmt.Errorf("%s: %w", name, err)
}

func cstringWireSize(value []byte) uint64 {
	return uint64(len(value)) + 1
}

func optionalWireSize(optional OptionalParameters) uint64 {
	var size uint64
	if optional.SARMessageReference != nil {
		size += 6
	}
	if optional.SARTotalSegments != nil {
		size += 5
	}
	if optional.SARSegmentSequence != nil {
		size += 5
	}
	if optional.MessagePayload != nil {
		size += 4 + uint64(len(optional.MessagePayload))
	}
	if optional.ReceiptedMessageID != nil {
		size += 5 + uint64(len(optional.ReceiptedMessageID))
	}
	if optional.MessageState != nil {
		size += 5
	}
	return size
}

func ensureBodySize(size uint64) error {
	maximumBody := uint64(DefaultMaxSize - HeaderSize)
	if size > maximumBody {
		return fmt.Errorf("%w: body %d > %d", ErrFrameTooLarge, size, maximumBody)
	}
	return nil
}

type cursor struct {
	data   []byte
	offset int
}

func newCursor(data []byte) *cursor { return &cursor{data: data} }
func (c *cursor) remaining() int    { return len(c.data) - c.offset }

func (c *cursor) take(length int) ([]byte, error) {
	if length < 0 || length > c.remaining() {
		return nil, ErrTruncatedFrame
	}
	value := append([]byte(nil), c.data[c.offset:c.offset+length]...)
	c.offset += length
	return value, nil
}

func (c *cursor) byte() (byte, error) {
	value, err := c.take(1)
	if err != nil {
		return 0, err
	}
	return value[0], nil
}

func (c *cursor) uint16() (uint16, error) {
	value, err := c.take(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(value), nil
}

func (c *cursor) cstring() ([]byte, error) {
	remaining := c.data[c.offset:]
	terminator := bytes.IndexByte(remaining, 0)
	if terminator < 0 {
		return nil, ErrMalformedCString
	}
	value := append([]byte(nil), remaining[:terminator]...)
	c.offset += terminator + 1
	return value, nil
}

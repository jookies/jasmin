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
		return PDU{}, newParseError(header,
			fmt.Errorf("%w: %d", ErrInvalidCommandLength, header.CommandLength))
	}
	if header.CommandLength > DefaultMaxSize {
		return PDU{}, newParseError(header,
			fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, header.CommandLength, DefaultMaxSize))
	}
	if uint32(len(frame)) != header.CommandLength {
		return PDU{}, newParseError(header,
			fmt.Errorf("%w: header=%d bytes=%d", ErrTruncatedFrame, header.CommandLength, len(frame)))
	}
	pdu, err := decodeBody(header, frame[HeaderSize:])
	if err != nil {
		return PDU{}, newParseError(header, err)
	}
	return pdu, nil
}

func Read(r io.Reader, maximum uint32) (PDU, error) {
	if maximum == 0 {
		maximum = DefaultMaxSize
	}
	headerBytes := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, headerBytes); err != nil {
		return PDU{}, fmt.Errorf("%w: header: %w", ErrTruncatedFrame, err)
	}
	header := decodeHeader(headerBytes)
	if header.CommandLength < HeaderSize {
		return PDU{}, newParseError(header,
			fmt.Errorf("%w: %d", ErrInvalidCommandLength, header.CommandLength))
	}
	if header.CommandLength > maximum {
		return PDU{}, newParseError(header,
			fmt.Errorf("%w: %d > %d", ErrFrameTooLarge, header.CommandLength, maximum))
	}
	body := make([]byte, header.CommandLength-HeaderSize)
	if _, err := io.ReadFull(r, body); err != nil {
		return PDU{}, fmt.Errorf("%w: body: %w", ErrTruncatedFrame, err)
	}
	pdu, err := decodeBody(header, body)
	if err != nil {
		return PDU{}, newParseError(header, err)
	}
	return pdu, nil
}

func Encode(pdu PDU) ([]byte, error) {
	var body []byte
	var err error
	switch pdu.Header.CommandID {
	case CommandBindTransceiver, CommandBindReceiver, CommandBindTransmitter:
		body, err = encodeBind(pdu.Bind)
	case CommandBindTransceiverResp, CommandBindReceiverResp, CommandBindTransmitterResp:
		if pdu.BindResponse == nil && pdu.Header.CommandStatus != 0 {
			body = nil
		} else {
			body, err = encodeBindResponse(pdu.BindResponse)
		}
	case CommandEnquireLink, CommandEnquireLinkResp, CommandUnbind, CommandUnbindResp, CommandGenericNACK:
		body = nil
	case CommandSubmitSM:
		if pdu.decodedMessagePayload {
			return nil, &LegacyMessagePayloadError{Size: len(pdu.SM.Optional.MessagePayload)}
		}
		body, err = encodeSM(pdu.SM, true)
	case CommandDeliverSM:
		if pdu.decodedMessagePayload {
			return nil, &LegacyMessagePayloadError{Size: len(pdu.SM.Optional.MessagePayload)}
		}
		body, err = encodeSM(pdu.SM, false)
	case CommandDataSM:
		body, err = encodeDataSM(pdu.SM)
	case CommandSubmitSMResp, CommandDataSMResp, CommandDeliverSMResp:
		if pdu.SubmitResponse == nil && pdu.Header.CommandStatus != 0 {
			// Error responses are header-only (SMPP noBodyOnError).
			body = nil
		} else {
			body, err = encodeSubmitResponse(pdu.SubmitResponse)
		}
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
	case CommandBindTransceiver, CommandBindReceiver, CommandBindTransmitter:
		pdu.Bind, err = decodeBind(cursor)
	case CommandBindTransceiverResp, CommandBindReceiverResp, CommandBindTransmitterResp:
		if header.CommandStatus != 0 {
			if cursor.remaining() != 0 {
				err = fmt.Errorf("%w: error response has a body", ErrInvalidCommandLength)
			}
			pdu.BindResponse = nil
		} else {
			pdu.BindResponse, err = decodeBindResponse(cursor)
		}
	case CommandEnquireLink, CommandEnquireLinkResp, CommandUnbind, CommandUnbindResp, CommandGenericNACK:
		// Header-only control PDUs.
	case CommandSubmitSM, CommandDeliverSM:
		pdu.SM, pdu.decodedMessagePayload, err = decodeSM(cursor, header.CommandID)
	case CommandDataSM:
		pdu.SM, pdu.decodedMessagePayload, err = decodeDataSM(cursor)
	case CommandSubmitSMResp, CommandDataSMResp, CommandDeliverSMResp:
		if header.CommandStatus != 0 {
			if cursor.remaining() != 0 {
				err = fmt.Errorf("%w: error response has a body", ErrInvalidCommandLength)
			}
			pdu.SubmitResponse = nil
		} else {
			pdu.SubmitResponse, err = decodeSubmitResponse(cursor)
		}
	default:
		err = fmt.Errorf("%w: %#x", ErrUnsupportedCommand, header.CommandID)
	}
	if err != nil {
		return PDU{}, err
	}
	if cursor.remaining() != 0 {
		return PDU{}, fmt.Errorf("%w: unexpected trailing bytes: %d",
			ErrInvalidCommandLength, cursor.remaining())
	}
	return pdu, nil
}

func newParseError(header Header, err error) error {
	status := StatusInvalidCommandLength
	switch {
	case errors.Is(err, ErrUnsupportedCommand):
		status = StatusInvalidCommandID
	case errors.Is(err, ErrInvalidOptionalStream):
		status = StatusInvalidOptionalParameterStream
	case errors.Is(err, ErrOptionalParameterNotAllowed):
		status = StatusOptionalParameterNotAllowed
	case errors.Is(err, ErrInvalidOptionalParameterLength):
		status = StatusInvalidParameterLength
	case errors.Is(err, ErrMissingOptionalParameter):
		status = StatusMissingOptionalParameter
	case errors.Is(err, ErrInvalidOptionalParameterValue):
		status = StatusInvalidOptionalParameterValue
	}
	return &ParseError{Header: header, CommandStatus: status, Err: err}
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

func decodeSM(c *cursor, commandID uint32) (*SMBody, bool, error) {
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
	var allowedKnown map[uint16]struct{}
	if commandID == CommandSubmitSM {
		allowedKnown = submitSMAllowedKnownTLVs
	} else {
		allowedKnown = deliverSMAllowedKnownTLVs
	}
	messagePayload, err := decodeTLVs(c, body, allowedKnown)
	if err != nil {
		return nil, false, err
	}
	if messagePayload && len(body.ShortMessage) != 0 {
		return nil, false, optionalParameterError(ErrInvalidOptionalParameterValue,
			"message_payload and short_message are mutually exclusive")
	}
	return body, messagePayload, nil
}

func encodeSM(body *SMBody, submit bool) ([]byte, error) {
	if body == nil {
		return nil, errors.New("submit/deliver body is required")
	}
	if err := validateOptionalCombinations(body); err != nil {
		return nil, err
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
	encodeOptionals := encodeTLVs
	if submit {
		encodeOptionals = encodeSubmitTLVs
	}
	if err := encodeOptionals(&output, body.Optional); err != nil {
		return nil, err
	}
	output.Write(body.VendorTLVs)
	return output.Bytes(), nil
}

// decodeDataSM decodes a data_sm body. Per SMPP 3.4 §4.7.1 its mandatory
// parameters are a strict subset of submit_sm/deliver_sm: no protocol_id,
// priority_flag, schedule_delivery_time, validity_period, replace_if_present_
// flag, sm_default_msg_id, or sm_length/short_message — the message rides the
// message_payload TLV. Reuses the shared TLV decoder.
func decodeDataSM(c *cursor) (*SMBody, bool, error) {
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
	if body.RegisteredDelivery, err = c.byte(); err != nil {
		return nil, false, fieldError("registered_delivery", err)
	}
	if body.DataCoding, err = c.byte(); err != nil {
		return nil, false, fieldError("data_coding", err)
	}
	messagePayload, err := decodeTLVs(c, body, dataSMAllowedKnownTLVs)
	if err != nil {
		return nil, false, err
	}
	return body, messagePayload, nil
}

// encodeDataSM encodes a data_sm body with the data_sm mandatory subset.
func encodeDataSM(body *SMBody) ([]byte, error) {
	if body == nil {
		return nil, errors.New("data_sm body is required")
	}
	if err := validateOptionalCombinations(body); err != nil {
		return nil, err
	}
	size := cstringWireSize(body.ServiceType) + cstringWireSize(body.SourceAddress) +
		cstringWireSize(body.DestinationAddress) + 7 + optionalWireSize(body.Optional) + uint64(len(body.VendorTLVs))
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
	output.WriteByte(body.RegisteredDelivery)
	output.WriteByte(body.DataCoding)
	if err := encodeDataSMTLVs(&output, body.Optional); err != nil {
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

// DecodeOptionalSection parses a raw optional-TLV section into body using the
// codec's typed standard optionals, vendor capture, and SMPP validation. It
// exists for projections that rebuild a body from re-encoded parameters rather
// than a full wire frame.
func DecodeOptionalSection(data []byte, body *SMBody) error {
	c := &cursor{data: data}
	_, err := decodeTLVs(c, body, nil)
	return err
}

func decodeTLVs(c *cursor, body *SMBody, allowedKnown map[uint16]struct{}) (bool, error) {
	optional := &body.Optional
	messagePayload := false
	for c.remaining() != 0 {
		if c.remaining() < 4 {
			return false, optionalParameterError(ErrInvalidOptionalStream, "truncated TLV header")
		}
		tag, _ := c.uint16()
		length, _ := c.uint16()
		value, err := c.take(int(length))
		if err != nil {
			return false, optionalParameterError(ErrInvalidOptionalStream,
				"tag %#04x length %d", tag, length)
		}
		if allowedKnown != nil && legacyKnownWireTag(tag) {
			if _, allowed := allowedKnown[tag]; !allowed {
				return false, optionalParameterError(ErrOptionalParameterNotAllowed,
					"optional parameter %#04x not allowed", tag)
			}
		}
		if err := validateOptionalValue(tag, value); err != nil {
			return false, err
		}
		switch tag {
		case tagSourceAddrSubunit:
			v, err := enumByte(tag, value, "source_addr_subunit", 4)
			if err != nil {
				return false, err
			}
			optional.SourceAddrSubunit = &v
		case tagDestAddrSubunit:
			v, err := enumByte(tag, value, "dest_addr_subunit", 4)
			if err != nil {
				return false, err
			}
			optional.DestAddrSubunit = &v
		case tagSourceNetworkType:
			v, err := enumByte(tag, value, "source_network_type", 8)
			if err != nil {
				return false, err
			}
			optional.SourceNetworkType = &v
		case tagDestNetworkType:
			v, err := enumByte(tag, value, "dest_network_type", 8)
			if err != nil {
				return false, err
			}
			optional.DestNetworkType = &v
		case tagSourceBearerType:
			v, err := enumByte(tag, value, "source_bearer_type", 8)
			if err != nil {
				return false, err
			}
			optional.SourceBearerType = &v
		case tagDestBearerType:
			v, err := enumByte(tag, value, "dest_bearer_type", 8)
			if err != nil {
				return false, err
			}
			optional.DestBearerType = &v
		case tagSourceTelematicsID:
			v, err := fixedUint16(tag, value)
			if err != nil {
				return false, err
			}
			optional.SourceTelematicsID = &v
		case tagDestTelematicsID:
			v, err := fixedUint16(tag, value)
			if err != nil {
				return false, err
			}
			optional.DestTelematicsID = &v
		case tagQoSTimeToLive:
			if len(value) != 4 {
				return false, fixedTLVLengthError(tag, len(value), 4)
			}
			v := binary.BigEndian.Uint32(value)
			optional.QoSTimeToLive = &v
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
			if value[0] == 0 {
				return false, optionalParameterError(ErrInvalidOptionalParameterValue,
					"sar_total_segments must be non-zero")
			}
			v := value[0]
			optional.SARTotalSegments = &v
		case tagSARSegmentSequence:
			if len(value) != 1 {
				return false, fixedTLVLengthError(tag, len(value), 1)
			}
			if value[0] == 0 {
				return false, optionalParameterError(ErrInvalidOptionalParameterValue,
					"sar_segment_seqnum must be non-zero")
			}
			v := value[0]
			optional.SARSegmentSequence = &v
		case tagMoreMessagesToSend:
			if len(value) != 1 {
				return false, fixedTLVLengthError(tag, len(value), 1)
			}
			if value[0] > 1 {
				return false, optionalParameterError(ErrInvalidOptionalParameterValue,
					"more_messages_to_send value %#x", value[0])
			}
			v := value[0]
			optional.MoreMessagesToSend = &v
		case tagMessagePayload:
			optional.MessagePayload = append([]byte(nil), value...)
			messagePayload = true
		case tagReceiptedMessageID:
			if len(value) == 0 || value[len(value)-1] != 0 {
				return false, optionalParameterError(ErrInvalidOptionalParameterLength,
					"receipted_message_id is not NUL terminated")
			}
			if len(value) > 65 { // the legacy COctetStringEncoder maxSize
				return false, optionalParameterError(ErrInvalidOptionalParameterLength,
					"receipted_message_id longer than 65")
			}
			optional.ReceiptedMessageID = append([]byte(nil), value[:len(value)-1]...)
		case tagMessageState:
			if len(value) != 1 {
				return false, fixedTLVLengthError(tag, len(value), 1)
			}
			if value[0] < 1 || value[0] > 8 {
				return false, optionalParameterError(ErrInvalidOptionalParameterValue,
					"unknown message_state value %#x", value[0])
			}
			v := value[0]
			optional.MessageState = &v
		case tagUserMessageReference:
			v, err := fixedUint16(tag, value)
			if err != nil {
				return false, err
			}
			optional.UserMessageReference = &v
		case tagSourcePort:
			v, err := fixedUint16(tag, value)
			if err != nil {
				return false, err
			}
			optional.SourcePort = &v
		case tagDestinationPort:
			v, err := fixedUint16(tag, value)
			if err != nil {
				return false, err
			}
			optional.DestinationPort = &v
		case tagSourceSubaddress:
			optional.SourceSubaddress, err = decodeSubaddress(value)
			if err != nil {
				return false, optionalParameterError(ErrInvalidOptionalParameterLength,
					"tag %#04x: %v", tag, err)
			}
		case tagDestSubaddress:
			optional.DestSubaddress, err = decodeSubaddress(value)
			if err != nil {
				return false, optionalParameterError(ErrInvalidOptionalParameterLength,
					"tag %#04x: %v", tag, err)
			}
		case tagUserResponseCode:
			if len(value) != 1 {
				return false, fixedTLVLengthError(tag, len(value), 1)
			}
			v := value[0]
			optional.UserResponseCode = &v
		case tagPayloadType:
			v, err := enumByte(tag, value, "payload_type", 1)
			if err != nil {
				return false, err
			}
			optional.PayloadType = &v
		case tagPrivacyIndicator:
			v, err := enumByte(tag, value, "privacy_indicator", 3)
			if err != nil {
				return false, err
			}
			optional.PrivacyIndicator = &v
		case tagLanguageIndicator:
			v, err := enumByte(tag, value, "language_indicator", 5)
			if err != nil {
				return false, err
			}
			optional.LanguageIndicator = &v
		case tagDisplayTime:
			v, err := enumByte(tag, value, "display_time", 2)
			if err != nil {
				return false, err
			}
			optional.DisplayTime = &v
		case tagSMSSignal:
			if len(value) != 2 {
				return false, fixedTLVLengthError(tag, len(value), 2)
			}
			optional.SMSSignal = append([]byte{}, value...)
		case tagNumberOfMessages:
			if len(value) != 1 {
				return false, fixedTLVLengthError(tag, len(value), 1)
			}
			if value[0] > 99 {
				return false, optionalParameterError(ErrInvalidOptionalParameterValue,
					"number_of_messages value %#x", value[0])
			}
			v := value[0]
			optional.NumberOfMessages = &v
		case tagCallbackNum:
			number, err := decodeCallbackNumber(value)
			if err != nil {
				return false, err
			}
			optional.CallbackNum = number
		case tagNetworkErrorCode:
			if len(value) != 3 {
				return false, fixedTLVLengthError(tag, len(value), 3)
			}
			optional.NetworkErrorCode = append([]byte{}, value...)
		default:
			// Frozen compatibility behavior accepts unknown optionals but does not
			// retain them for re-encoding (KNOWN_QUIRKS Q-016). Tags outside the
			// legacy library's known set are additionally captured for MO/DLR
			// forwarding, mirroring the fork's decoder patch; known-but-unhandled
			// standard optionals stay dropped (standard tlv_params forwarding is
			// a separate slice).
			if legacyUnsupportedWireTag(tag) {
				// The legacy library knows these tags but has no option
				// encoder: decode raises "Optional Parameter not allowed"
				// and the whole PDU fails.
				return false, optionalParameterError(ErrOptionalParameterNotAllowed,
					"optional parameter %#04x not allowed", tag)
			}
			if !legacyKnownWireTag(tag) {
				body.CapturedVendorTLVs = append(body.CapturedVendorTLVs,
					CapturedVendorTLV{Tag: tag, Value: append([]byte(nil), value...)})
			}
		}
	}
	if err := validateSARGroup(*optional); err != nil {
		return false, err
	}
	return messagePayload, nil
}

func validateOptionalCombinations(body *SMBody) error {
	if body.Optional.MessagePayload != nil && len(body.ShortMessage) != 0 {
		return optionalParameterError(ErrInvalidOptionalParameterValue,
			"message_payload and short_message are mutually exclusive")
	}
	return validateSARGroup(body.Optional)
}

func validateSARGroup(optional OptionalParameters) error {
	present := 0
	for _, parameterPresent := range []bool{
		optional.SARMessageReference != nil,
		optional.SARTotalSegments != nil,
		optional.SARSegmentSequence != nil,
	} {
		if parameterPresent {
			present++
		}
	}
	if present != 0 && present != 3 {
		return optionalParameterError(ErrMissingOptionalParameter,
			"sar_msg_ref_num, sar_total_segments, and sar_segment_seqnum must be supplied together")
	}
	if present == 3 && *optional.SARSegmentSequence > *optional.SARTotalSegments {
		return optionalParameterError(ErrInvalidOptionalParameterValue,
			"sar_segment_seqnum %d exceeds sar_total_segments %d",
			*optional.SARSegmentSequence, *optional.SARTotalSegments)
	}
	return nil
}

// encodeSubmitTLVs emits retained optionals in SubmitSM.optionalParams order.
// The frozen encoder iterates that declaration rather than preserving arrival
// order, so the order here is part of byte fidelity.
func encodeSubmitTLVs(output *bytes.Buffer, optional OptionalParameters) error {
	if optional.UserMessageReference != nil {
		if err := writeTLV(output, tagUserMessageReference, uint16Bytes(*optional.UserMessageReference)); err != nil {
			return err
		}
	}
	if optional.SourcePort != nil {
		if err := writeTLV(output, tagSourcePort, uint16Bytes(*optional.SourcePort)); err != nil {
			return err
		}
	}
	if optional.SourceAddrSubunit != nil {
		if err := writeTLV(output, tagSourceAddrSubunit, []byte{*optional.SourceAddrSubunit}); err != nil {
			return err
		}
	}
	if optional.DestinationPort != nil {
		if err := writeTLV(output, tagDestinationPort, uint16Bytes(*optional.DestinationPort)); err != nil {
			return err
		}
	}
	if optional.DestAddrSubunit != nil {
		if err := writeTLV(output, tagDestAddrSubunit, []byte{*optional.DestAddrSubunit}); err != nil {
			return err
		}
	}
	if optional.SARMessageReference != nil {
		if err := writeTLV(output, tagSARMessageRef, uint16Bytes(*optional.SARMessageReference)); err != nil {
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
	if optional.MoreMessagesToSend != nil {
		if err := writeTLV(output, tagMoreMessagesToSend, []byte{*optional.MoreMessagesToSend}); err != nil {
			return err
		}
	}
	if optional.PayloadType != nil {
		if err := writeTLV(output, tagPayloadType, []byte{*optional.PayloadType}); err != nil {
			return err
		}
	}
	if optional.MessagePayload != nil {
		if err := writeTLV(output, tagMessagePayload, optional.MessagePayload); err != nil {
			return err
		}
	}
	if optional.PrivacyIndicator != nil {
		if err := writeTLV(output, tagPrivacyIndicator, []byte{*optional.PrivacyIndicator}); err != nil {
			return err
		}
	}
	if optional.CallbackNum != nil {
		value := append([]byte{optional.CallbackNum.DigitMode, optional.CallbackNum.TON, optional.CallbackNum.NPI},
			optional.CallbackNum.Digits...)
		if err := writeTLV(output, tagCallbackNum, value); err != nil {
			return err
		}
	}
	if optional.SourceSubaddress != nil {
		value := append([]byte{optional.SourceSubaddress.TypeTag}, optional.SourceSubaddress.Value...)
		if err := writeTLV(output, tagSourceSubaddress, value); err != nil {
			return err
		}
	}
	if optional.DestSubaddress != nil {
		value := append([]byte{optional.DestSubaddress.TypeTag}, optional.DestSubaddress.Value...)
		if err := writeTLV(output, tagDestSubaddress, value); err != nil {
			return err
		}
	}
	if optional.UserResponseCode != nil {
		if err := writeTLV(output, tagUserResponseCode, []byte{*optional.UserResponseCode}); err != nil {
			return err
		}
	}
	if optional.DisplayTime != nil {
		if err := writeTLV(output, tagDisplayTime, []byte{*optional.DisplayTime}); err != nil {
			return err
		}
	}
	if optional.SMSSignal != nil {
		if err := writeTLV(output, tagSMSSignal, optional.SMSSignal); err != nil {
			return err
		}
	}
	if optional.NumberOfMessages != nil {
		if err := writeTLV(output, tagNumberOfMessages, []byte{*optional.NumberOfMessages}); err != nil {
			return err
		}
	}
	if optional.LanguageIndicator != nil {
		if err := writeTLV(output, tagLanguageIndicator, []byte{*optional.LanguageIndicator}); err != nil {
			return err
		}
	}
	return nil
}

// encodeTLVs emits the deliver_sm retained subset in the frozen declaration
// order. Captured vendor TLVs are appended separately through VendorTLVs.
func encodeTLVs(output *bytes.Buffer, optional OptionalParameters) error {
	if optional.MoreMessagesToSend != nil {
		if err := writeTLV(output, tagMoreMessagesToSend, []byte{*optional.MoreMessagesToSend}); err != nil {
			return err
		}
	}
	if optional.UserMessageReference != nil {
		if err := writeTLV(output, tagUserMessageReference, uint16Bytes(*optional.UserMessageReference)); err != nil {
			return err
		}
	}
	if optional.SourcePort != nil {
		if err := writeTLV(output, tagSourcePort, uint16Bytes(*optional.SourcePort)); err != nil {
			return err
		}
	}
	if optional.DestinationPort != nil {
		if err := writeTLV(output, tagDestinationPort, uint16Bytes(*optional.DestinationPort)); err != nil {
			return err
		}
	}
	if optional.SARMessageReference != nil {
		if err := writeTLV(output, tagSARMessageRef, uint16Bytes(*optional.SARMessageReference)); err != nil {
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
	if optional.UserResponseCode != nil {
		if err := writeTLV(output, tagUserResponseCode, []byte{*optional.UserResponseCode}); err != nil {
			return err
		}
	}
	if optional.PrivacyIndicator != nil {
		if err := writeTLV(output, tagPrivacyIndicator, []byte{*optional.PrivacyIndicator}); err != nil {
			return err
		}
	}
	if optional.PayloadType != nil {
		if err := writeTLV(output, tagPayloadType, []byte{*optional.PayloadType}); err != nil {
			return err
		}
	}
	if optional.MessagePayload != nil {
		if err := writeTLV(output, tagMessagePayload, optional.MessagePayload); err != nil {
			return err
		}
	}
	if optional.CallbackNum != nil {
		value := append([]byte{optional.CallbackNum.DigitMode, optional.CallbackNum.TON, optional.CallbackNum.NPI},
			optional.CallbackNum.Digits...)
		if err := writeTLV(output, tagCallbackNum, value); err != nil {
			return err
		}
	}
	if optional.SourceSubaddress != nil {
		value := append([]byte{optional.SourceSubaddress.TypeTag}, optional.SourceSubaddress.Value...)
		if err := writeTLV(output, tagSourceSubaddress, value); err != nil {
			return err
		}
	}
	if optional.DestSubaddress != nil {
		value := append([]byte{optional.DestSubaddress.TypeTag}, optional.DestSubaddress.Value...)
		if err := writeTLV(output, tagDestSubaddress, value); err != nil {
			return err
		}
	}
	if optional.LanguageIndicator != nil {
		if err := writeTLV(output, tagLanguageIndicator, []byte{*optional.LanguageIndicator}); err != nil {
			return err
		}
	}
	if optional.NetworkErrorCode != nil {
		if err := writeTLV(output, tagNetworkErrorCode, optional.NetworkErrorCode); err != nil {
			return err
		}
	}
	if optional.MessageState != nil {
		if err := writeTLV(output, tagMessageState, []byte{*optional.MessageState}); err != nil {
			return err
		}
	}
	if optional.ReceiptedMessageID != nil {
		value := append(append([]byte(nil), optional.ReceiptedMessageID...), 0)
		if err := writeTLV(output, tagReceiptedMessageID, value); err != nil {
			return err
		}
	}
	return nil
}

// encodeDataSMTLVs emits every DataSM optional parameter supported by the
// frozen OptionEncoder, in DataSM.optionalParams declaration order. Listed
// parameters whose frozen encoders are absent are rejected during decode and
// therefore have no representation here.
func encodeDataSMTLVs(output *bytes.Buffer, optional OptionalParameters) error {
	if optional.SourcePort != nil {
		if err := writeTLV(output, tagSourcePort, uint16Bytes(*optional.SourcePort)); err != nil {
			return err
		}
	}
	if optional.SourceAddrSubunit != nil {
		if err := writeTLV(output, tagSourceAddrSubunit, []byte{*optional.SourceAddrSubunit}); err != nil {
			return err
		}
	}
	if optional.SourceNetworkType != nil {
		if err := writeTLV(output, tagSourceNetworkType, []byte{*optional.SourceNetworkType}); err != nil {
			return err
		}
	}
	if optional.SourceBearerType != nil {
		if err := writeTLV(output, tagSourceBearerType, []byte{*optional.SourceBearerType}); err != nil {
			return err
		}
	}
	if optional.SourceTelematicsID != nil {
		if err := writeTLV(output, tagSourceTelematicsID, uint16Bytes(*optional.SourceTelematicsID)); err != nil {
			return err
		}
	}
	if optional.DestinationPort != nil {
		if err := writeTLV(output, tagDestinationPort, uint16Bytes(*optional.DestinationPort)); err != nil {
			return err
		}
	}
	if optional.DestAddrSubunit != nil {
		if err := writeTLV(output, tagDestAddrSubunit, []byte{*optional.DestAddrSubunit}); err != nil {
			return err
		}
	}
	if optional.DestNetworkType != nil {
		if err := writeTLV(output, tagDestNetworkType, []byte{*optional.DestNetworkType}); err != nil {
			return err
		}
	}
	if optional.DestBearerType != nil {
		if err := writeTLV(output, tagDestBearerType, []byte{*optional.DestBearerType}); err != nil {
			return err
		}
	}
	if optional.DestTelematicsID != nil {
		if err := writeTLV(output, tagDestTelematicsID, uint16Bytes(*optional.DestTelematicsID)); err != nil {
			return err
		}
	}
	if optional.SARMessageReference != nil {
		if err := writeTLV(output, tagSARMessageRef, uint16Bytes(*optional.SARMessageReference)); err != nil {
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
	if optional.MoreMessagesToSend != nil {
		if err := writeTLV(output, tagMoreMessagesToSend, []byte{*optional.MoreMessagesToSend}); err != nil {
			return err
		}
	}
	if optional.QoSTimeToLive != nil {
		if err := writeTLV(output, tagQoSTimeToLive, uint32Bytes(*optional.QoSTimeToLive)); err != nil {
			return err
		}
	}
	if optional.PayloadType != nil {
		if err := writeTLV(output, tagPayloadType, []byte{*optional.PayloadType}); err != nil {
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
	if optional.NetworkErrorCode != nil {
		if err := writeTLV(output, tagNetworkErrorCode, optional.NetworkErrorCode); err != nil {
			return err
		}
	}
	if optional.UserMessageReference != nil {
		if err := writeTLV(output, tagUserMessageReference, uint16Bytes(*optional.UserMessageReference)); err != nil {
			return err
		}
	}
	if optional.PrivacyIndicator != nil {
		if err := writeTLV(output, tagPrivacyIndicator, []byte{*optional.PrivacyIndicator}); err != nil {
			return err
		}
	}
	if optional.CallbackNum != nil {
		value := append([]byte{optional.CallbackNum.DigitMode, optional.CallbackNum.TON, optional.CallbackNum.NPI},
			optional.CallbackNum.Digits...)
		if err := writeTLV(output, tagCallbackNum, value); err != nil {
			return err
		}
	}
	if optional.SourceSubaddress != nil {
		value := append([]byte{optional.SourceSubaddress.TypeTag}, optional.SourceSubaddress.Value...)
		if err := writeTLV(output, tagSourceSubaddress, value); err != nil {
			return err
		}
	}
	if optional.DestSubaddress != nil {
		value := append([]byte{optional.DestSubaddress.TypeTag}, optional.DestSubaddress.Value...)
		if err := writeTLV(output, tagDestSubaddress, value); err != nil {
			return err
		}
	}
	if optional.UserResponseCode != nil {
		if err := writeTLV(output, tagUserResponseCode, []byte{*optional.UserResponseCode}); err != nil {
			return err
		}
	}
	if optional.DisplayTime != nil {
		if err := writeTLV(output, tagDisplayTime, []byte{*optional.DisplayTime}); err != nil {
			return err
		}
	}
	if optional.SMSSignal != nil {
		if err := writeTLV(output, tagSMSSignal, optional.SMSSignal); err != nil {
			return err
		}
	}
	if optional.NumberOfMessages != nil {
		if err := writeTLV(output, tagNumberOfMessages, []byte{*optional.NumberOfMessages}); err != nil {
			return err
		}
	}
	if optional.LanguageIndicator != nil {
		if err := writeTLV(output, tagLanguageIndicator, []byte{*optional.LanguageIndicator}); err != nil {
			return err
		}
	}
	return nil
}

func uint16Bytes(value uint16) []byte {
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, value)
	return out
}

func uint32Bytes(value uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, value)
	return out
}

func writeTLV(output *bytes.Buffer, tag uint16, value []byte) error {
	if len(value) > int(^uint16(0)) {
		return fmt.Errorf("%w: tag %#04x length %d exceeds 65535", ErrMalformedTLV, tag, len(value))
	}
	if err := validateOptionalValue(tag, value); err != nil {
		return err
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
	return optionalParameterError(ErrInvalidOptionalParameterLength,
		"tag %#04x length %d, want %d", tag, got, want)
}

func optionalParameterError(kind error, format string, args ...any) error {
	return fmt.Errorf("%w: %w: %s", ErrMalformedTLV, kind, fmt.Sprintf(format, args...))
}

func validateOptionalValue(tag uint16, value []byte) error {
	switch tag {
	case tagUserMessageReference, tagSourcePort, tagDestinationPort,
		tagSARMessageRef, tagSourceTelematicsID, tagDestTelematicsID, tagSMSSignal:
		if len(value) != 2 {
			return fixedTLVLengthError(tag, len(value), 2)
		}
	case tagQoSTimeToLive:
		if len(value) != 4 {
			return fixedTLVLengthError(tag, len(value), 4)
		}
	case tagSourceAddrSubunit, tagDestAddrSubunit, tagSourceNetworkType,
		tagDestNetworkType, tagSourceBearerType, tagDestBearerType,
		tagSARTotalSegments, tagSARSegmentSequence, tagMoreMessagesToSend,
		tagMessageState, tagUserResponseCode, tagPayloadType, tagPrivacyIndicator,
		tagLanguageIndicator, tagDisplayTime, tagNumberOfMessages:
		if len(value) != 1 {
			return fixedTLVLengthError(tag, len(value), 1)
		}
	}

	switch tag {
	case tagSourceAddrSubunit, tagDestAddrSubunit:
		return optionalEnumValue(tag, value[0], 4)
	case tagSourceNetworkType, tagDestNetworkType, tagSourceBearerType, tagDestBearerType:
		return optionalEnumValue(tag, value[0], 8)
	case tagPayloadType:
		return optionalEnumValue(tag, value[0], 1)
	case tagPrivacyIndicator:
		return optionalEnumValue(tag, value[0], 3)
	case tagLanguageIndicator:
		return optionalEnumValue(tag, value[0], 5)
	case tagDisplayTime:
		return optionalEnumValue(tag, value[0], 2)
	case tagSARTotalSegments:
		if value[0] == 0 {
			return optionalParameterError(ErrInvalidOptionalParameterValue,
				"sar_total_segments must be non-zero")
		}
	case tagSARSegmentSequence:
		if value[0] == 0 {
			return optionalParameterError(ErrInvalidOptionalParameterValue,
				"sar_segment_seqnum must be non-zero")
		}
	case tagMoreMessagesToSend:
		return optionalEnumValue(tag, value[0], 1)
	case tagMessageState:
		if value[0] < 1 || value[0] > 8 {
			return optionalParameterError(ErrInvalidOptionalParameterValue,
				"tag %#04x value %#x", tag, value[0])
		}
	case tagNumberOfMessages:
		if value[0] > 99 {
			return optionalParameterError(ErrInvalidOptionalParameterValue,
				"tag %#04x value %#x", tag, value[0])
		}
	case tagSourceSubaddress, tagDestSubaddress:
		if len(value) < 2 || len(value) > 23 {
			return optionalParameterError(ErrInvalidOptionalParameterLength,
				"tag %#04x length %d, want 2..23", tag, len(value))
		}
	case tagCallbackNum:
		if _, err := decodeCallbackNumber(value); err != nil {
			return err
		}
	case tagNetworkErrorCode:
		if len(value) != 3 {
			return fixedTLVLengthError(tag, len(value), 3)
		}
	case tagReceiptedMessageID:
		if len(value) == 0 || len(value) > 65 || value[len(value)-1] != 0 {
			return optionalParameterError(ErrInvalidOptionalParameterLength,
				"receipted_message_id must be a 1..65 byte C-octet string")
		}
	}
	return nil
}

func optionalEnumValue(tag uint16, value, maximum byte) error {
	if value > maximum {
		return optionalParameterError(ErrInvalidOptionalParameterValue,
			"tag %#04x value %#x", tag, value)
	}
	return nil
}

func fieldError(name string, err error) error {
	if errors.Is(err, ErrTruncatedFrame) {
		return fmt.Errorf("%s: %w: %w", name, ErrMissingMandatoryParameter, err)
	}
	return fmt.Errorf("%s: %w", name, err)
}

func cstringWireSize(value []byte) uint64 {
	return uint64(len(value)) + 1
}

func optionalWireSize(optional OptionalParameters) uint64 {
	var size uint64
	// 2-byte-body optionals: 4 header + 2 body.
	for _, present := range []bool{
		optional.UserMessageReference != nil,
		optional.SourcePort != nil,
		optional.DestinationPort != nil,
		optional.SARMessageReference != nil,
		optional.SourceTelematicsID != nil,
		optional.DestTelematicsID != nil,
	} {
		if present {
			size += 6
		}
	}
	// 1-byte-body optionals: 4 header + 1 body.
	for _, present := range []bool{
		optional.SARTotalSegments != nil,
		optional.SARSegmentSequence != nil,
		optional.MoreMessagesToSend != nil,
		optional.PrivacyIndicator != nil,
		optional.PayloadType != nil,
		optional.LanguageIndicator != nil,
		optional.MessageState != nil,
		optional.SourceAddrSubunit != nil,
		optional.DestAddrSubunit != nil,
		optional.UserResponseCode != nil,
		optional.DisplayTime != nil,
		optional.NumberOfMessages != nil,
		optional.SourceNetworkType != nil,
		optional.DestNetworkType != nil,
		optional.SourceBearerType != nil,
		optional.DestBearerType != nil,
	} {
		if present {
			size += 5
		}
	}
	if optional.MessagePayload != nil {
		size += 4 + uint64(len(optional.MessagePayload))
	}
	if optional.QoSTimeToLive != nil {
		size += 8 // 4-byte TLV header + uint32 body
	}
	if optional.CallbackNum != nil {
		size += 4 + 3 + uint64(len(optional.CallbackNum.Digits))
	}
	if optional.SourceSubaddress != nil {
		size += 5 + uint64(len(optional.SourceSubaddress.Value))
	}
	if optional.DestSubaddress != nil {
		size += 5 + uint64(len(optional.DestSubaddress.Value))
	}
	if optional.SMSSignal != nil {
		size += 4 + uint64(len(optional.SMSSignal))
	}
	if optional.NetworkErrorCode != nil {
		size += 4 + uint64(len(optional.NetworkErrorCode))
	}
	if optional.ReceiptedMessageID != nil {
		size += 5 + uint64(len(optional.ReceiptedMessageID)) // 4 header + value + NUL
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
	if len(remaining) == 0 {
		return nil, ErrTruncatedFrame
	}
	terminator := bytes.IndexByte(remaining, 0)
	if terminator < 0 {
		return nil, ErrMalformedCString
	}
	value := append([]byte(nil), remaining[:terminator]...)
	c.offset += terminator + 1
	return value, nil
}

// legacyKnownWireTags is smpp.pdu3's tag_name_map wire values (minus the
// synthetic vendor_specific_bypass). Any wire tag outside this set is what the
// legacy decoder maps to vendor_specific_bypass — the fork's capture boundary.
var legacyKnownWireTags = map[uint16]struct{}{
	0x0005: {}, 0x0006: {}, 0x0007: {}, 0x0008: {}, 0x000D: {}, 0x000E: {},
	0x000F: {}, 0x0010: {}, 0x0017: {}, 0x0019: {}, 0x001D: {}, 0x001E: {},
	0x0030: {}, 0x0201: {}, 0x0202: {}, 0x0203: {}, 0x0204: {}, 0x0205: {},
	0x020A: {}, 0x020B: {}, 0x020C: {}, 0x020D: {}, 0x020E: {}, 0x020F: {},
	0x0210: {}, 0x0302: {}, 0x0303: {}, 0x0304: {}, 0x0381: {}, 0x0420: {},
	0x0421: {}, 0x0422: {}, 0x0423: {}, 0x0424: {}, 0x0425: {}, 0x0426: {},
	0x0427: {}, 0x0501: {}, 0x1201: {}, 0x1203: {}, 0x1204: {}, 0x130C: {},
	0x1380: {}, 0x1383: {},
}

func legacyKnownWireTag(tag uint16) bool {
	_, known := legacyKnownWireTags[tag]
	return known
}

// submitSMAllowedKnownTLVs is the intersection of SubmitSM.optionalParams and
// the frozen OptionEncoder table. Listed-but-unimplemented tags (for example
// callback_num_atag) fail in OptionEncoder; known tags belonging only to
// deliver_sm/data_sm (for example message_state) fail the PDU-specific allow
// check. Unknown vendor tags remain accepted and captured by the Jasmin patch.
var submitSMAllowedKnownTLVs = map[uint16]struct{}{
	0x0005: {}, // dest_addr_subunit
	0x000D: {}, // source_addr_subunit
	0x0019: {}, // payload_type
	0x0201: {}, // privacy_indicator
	0x0202: {}, // source_subaddress
	0x0203: {}, // dest_subaddress
	0x0204: {}, // user_message_reference
	0x0205: {}, // user_response_code
	0x020A: {}, // source_port
	0x020B: {}, // destination_port
	0x020C: {}, // sar_msg_ref_num
	0x020D: {}, // language_indicator
	0x020E: {}, // sar_total_segments
	0x020F: {}, // sar_segment_seqnum
	0x0304: {}, // number_of_messages
	0x0381: {}, // callback_num
	0x0424: {}, // message_payload
	0x0426: {}, // more_messages_to_send
	0x1201: {}, // display_time
	0x1203: {}, // sms_signal
}

var deliverSMAllowedKnownTLVs = map[uint16]struct{}{
	0x0019: {}, // payload_type
	0x001E: {}, // receipted_message_id
	0x0201: {}, // privacy_indicator
	0x0202: {}, // source_subaddress
	0x0203: {}, // dest_subaddress
	0x0204: {}, // user_message_reference
	0x0205: {}, // user_response_code
	0x020A: {}, // source_port
	0x020B: {}, // destination_port
	0x020C: {}, // sar_msg_ref_num
	0x020D: {}, // language_indicator
	0x020E: {}, // sar_total_segments
	0x020F: {}, // sar_segment_seqnum
	0x0381: {}, // callback_num
	0x0423: {}, // network_error_code
	0x0424: {}, // message_payload
	0x0426: {}, // more_messages_to_send
	0x0427: {}, // message_state
}

// dataSMAllowedKnownTLVs is the exact intersection of DataSM.optionalParams
// and the frozen OptionEncoder table. This prevents a known standard optional
// belonging to another command from being accepted and silently discarded,
// while retaining the Jasmin vendor-specific bypass for unknown tags.
var dataSMAllowedKnownTLVs = map[uint16]struct{}{
	0x0005: {}, // dest_addr_subunit
	0x0006: {}, // dest_network_type
	0x0007: {}, // dest_bearer_type
	0x0008: {}, // dest_telematics_id
	0x000D: {}, // source_addr_subunit
	0x000E: {}, // source_network_type
	0x000F: {}, // source_bearer_type
	0x0010: {}, // source_telematics_id
	0x0017: {}, // qos_time_to_live
	0x0019: {}, // payload_type
	0x001E: {}, // receipted_message_id
	0x0201: {}, // privacy_indicator
	0x0202: {}, // source_subaddress
	0x0203: {}, // dest_subaddress
	0x0204: {}, // user_message_reference
	0x0205: {}, // user_response_code
	0x020A: {}, // source_port
	0x020B: {}, // destination_port
	0x020C: {}, // sar_msg_ref_num
	0x020D: {}, // language_indicator
	0x020E: {}, // sar_total_segments
	0x020F: {}, // sar_segment_seqnum
	0x0304: {}, // number_of_messages
	0x0381: {}, // callback_num
	0x0423: {}, // network_error_code
	0x0424: {}, // message_payload
	0x0426: {}, // more_messages_to_send
	0x0427: {}, // message_state
	0x1201: {}, // display_time
	0x1203: {}, // sms_signal
}

// The forwarded standard-optional tags decoded beyond the original six.
const (
	tagDestAddrSubunit      uint16 = 0x0005
	tagDestNetworkType      uint16 = 0x0006
	tagDestBearerType       uint16 = 0x0007
	tagDestTelematicsID     uint16 = 0x0008
	tagSourceAddrSubunit    uint16 = 0x000D
	tagSourceNetworkType    uint16 = 0x000E
	tagSourceBearerType     uint16 = 0x000F
	tagSourceTelematicsID   uint16 = 0x0010
	tagQoSTimeToLive        uint16 = 0x0017
	tagUserMessageReference uint16 = 0x0204
	tagSourcePort           uint16 = 0x020A
	tagDestinationPort      uint16 = 0x020B
	tagSourceSubaddress     uint16 = 0x0202
	tagDestSubaddress       uint16 = 0x0203
	tagUserResponseCode     uint16 = 0x0205
	tagPayloadType          uint16 = 0x0019
	tagPrivacyIndicator     uint16 = 0x0201
	tagLanguageIndicator    uint16 = 0x020D
	tagDisplayTime          uint16 = 0x1201
	tagSMSSignal            uint16 = 0x1203
	tagNumberOfMessages     uint16 = 0x0304
	tagCallbackNum          uint16 = 0x0381
	tagNetworkErrorCode     uint16 = 0x0423
)

// legacyUnsupportedWireTags are in the legacy tag_name_map but have no option
// encoder in OptionEncoder.options: the legacy decode raises "Optional
// Parameter not allowed" and rejects the PDU.
var legacyUnsupportedWireTags = map[uint16]struct{}{
	0x0030: {}, 0x0302: {}, 0x0303: {}, 0x0420: {}, 0x0421: {},
	0x0501: {}, 0x1204: {}, 0x130C: {}, 0x1380: {}, 0x1383: {},
}

func legacyUnsupportedWireTag(tag uint16) bool {
	_, unsupported := legacyUnsupportedWireTags[tag]
	return unsupported
}

func fixedUint16(tag uint16, value []byte) (uint16, error) {
	if len(value) != 2 {
		return 0, fixedTLVLengthError(tag, len(value), 2)
	}
	return binary.BigEndian.Uint16(value), nil
}

func decodeSubaddress(value []byte) (*Subaddress, error) {
	if len(value) < 2 || len(value) > 23 {
		return nil, fmt.Errorf("subaddress length %d, want 2..23", len(value))
	}
	typeTag := value[0]
	switch typeTag {
	case 0x80, 0x88, 0xa0:
	default:
		// The frozen SubaddressEncoder catches an unknown type-tag enum and
		// normalizes it to its Jasmin-added RESERVED member.
		typeTag = 0
	}
	return &Subaddress{TypeTag: typeTag, Value: append([]byte(nil), value[1:]...)}, nil
}

// enumByte validates a one-byte enum against the legacy contiguous value
// table [0..maxValue]; out-of-table values fail the PDU decode like the
// legacy "Unknown <name> value" errors.
func enumByte(tag uint16, value []byte, name string, maxValue byte) (byte, error) {
	if len(value) != 1 {
		return 0, fixedTLVLengthError(tag, len(value), 1)
	}
	if value[0] > maxValue {
		return 0, optionalParameterError(ErrInvalidOptionalParameterValue,
			"unknown %s value %#x", name, value[0])
	}
	return value[0], nil
}

// callbackNPIValues is the legacy addr_npi table — non-contiguous.
var callbackNPIValues = map[byte]struct{}{
	0: {}, 1: {}, 3: {}, 4: {}, 6: {}, 8: {}, 9: {}, 10: {}, 14: {}, 18: {},
}

// decodeCallbackNumber validates the SMPP 4..19 octet callback_num structure:
// digit mode, TON, NPI, then 1..16 digit octets.
func decodeCallbackNumber(value []byte) (*CallbackNumber, error) {
	if len(value) < 4 || len(value) > 19 {
		return nil, optionalParameterError(ErrInvalidOptionalParameterLength,
			"callback_num length %d, want 4..19", len(value))
	}
	digitMode, ton, npi := value[0], value[1], value[2]
	if digitMode > 1 {
		return nil, optionalParameterError(ErrInvalidOptionalParameterValue,
			"unknown callback_num_digit_mode_indicator value %#x", digitMode)
	}
	if ton > 6 {
		return nil, optionalParameterError(ErrInvalidOptionalParameterValue,
			"unknown addr_ton value %#x", ton)
	}
	if _, ok := callbackNPIValues[npi]; !ok {
		return nil, optionalParameterError(ErrInvalidOptionalParameterValue,
			"unknown addr_npi value %#x", npi)
	}
	return &CallbackNumber{
		DigitMode: digitMode,
		TON:       ton,
		NPI:       npi,
		Digits:    append([]byte(nil), value[3:]...),
	}, nil
}

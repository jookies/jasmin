package picklecompat

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

var ErrInvalidSubmitSM = errors.New("invalid legacy SubmitSM envelope")
var ErrSubmitSMPoison = errors.New("poison legacy SubmitSM envelope")
var ErrSubmitSMTransient = errors.New("transient SubmitSM bridge failure")

func poisonSubmitError(format string, args ...any) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidSubmitSM, ErrSubmitSMPoison, fmt.Sprintf(format, args...))
}

func transientSubmitError(format string, args ...any) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidSubmitSM, ErrSubmitSMTransient, fmt.Sprintf(format, args...))
}

type submitSMWire struct {
	ServiceType          Bytes                 `json:"service_type"`
	SourceAddrTON        uint8                 `json:"source_addr_ton"`
	SourceAddrNPI        uint8                 `json:"source_addr_npi"`
	SourceAddr           Bytes                 `json:"source_addr"`
	DestAddrTON          uint8                 `json:"dest_addr_ton"`
	DestAddrNPI          uint8                 `json:"dest_addr_npi"`
	DestinationAddr      Bytes                 `json:"destination_addr"`
	ESMClass             uint8                 `json:"esm_class"`
	ProtocolID           uint8                 `json:"protocol_id"`
	PriorityFlag         uint8                 `json:"priority_flag"`
	ScheduleDeliveryTime Bytes                 `json:"schedule_delivery_time"`
	ValidityPeriod       Bytes                 `json:"validity_period"`
	RegisteredDelivery   uint8                 `json:"registered_delivery"`
	ReplaceIfPresentFlag uint8                 `json:"replace_if_present_flag"`
	DataCoding           uint8                 `json:"data_coding"`
	SMDefaultMessageID   uint8                 `json:"sm_default_msg_id"`
	ShortMessage         Bytes                 `json:"short_message"`
	OptionalTLVs         []submitSMOptionalTLV `json:"optional_tlvs"`
	CustomTLVs           []json.RawMessage     `json:"custom_tlvs"`
}

type submitSMOptionalTLV struct {
	Tag   uint16 `json:"tag"`
	Value Bytes  `json:"value"`
}

// SubmitSMChainPart is one wire body of a (possibly multipart) submit, plus its
// vendor custom-TLV tuples. A long message pickles a nextPdu chain of these; a
// non-chained submit yields exactly one part.
type SubmitSMChainPart struct {
	Body       smppwire.SubmitSMBody
	CustomTLVs []tlv.TLV
}

// submitSMEnvelope is the bridge's chain projection: one entry per nextPdu node.
type submitSMEnvelope struct {
	Parts []submitSMWire `json:"parts"`
}

// DecodeSubmitSMChain invokes the bridge's restricted SubmitSM-only unpickler and
// projects the pickled SubmitSM and its nextPdu chain into the canonical wire
// bodies plus each PDU's vendor custom-TLV tuples, verbatim and unresolved — the
// session applies connector rules and wire encoding, mirroring the legacy
// listener sending every part of a LongSubmitSm. Unknown TLVs decoded off actual
// wire bytes remain absent (KNOWN_QUIRKS Q-016); these tuples come from the
// pickled pdu.custom_tlvs attribute, which legacy preserves.
func (b *Bridge) DecodeSubmitSMChain(ctx context.Context, data []byte) ([]SubmitSMChainPart, error) {
	if b == nil {
		return nil, transientSubmitError("nil bridge")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) == 0 || len(data) > int(smppwire.DefaultMaxSize) {
		return nil, poisonSubmitError("pickle size %d", len(data))
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	request := bridgeRequest{Action: "decode_submit_sm", Data: base64.StdEncoding.EncodeToString(data)}
	if err := json.NewEncoder(b.stdin).Encode(request); err != nil {
		return nil, transientSubmitError("send bridge request: %v", err)
	}
	var response bridgeResponse
	if err := b.decodeResponse(ctx, &response); err != nil {
		return nil, transientSubmitError("read bridge response: %v", err)
	}
	if response.Status != "ok" {
		return nil, poisonSubmitError("%s", response.Message)
	}
	var envelope submitSMEnvelope
	if err := json.Unmarshal(response.Result, &envelope); err != nil {
		return nil, transientSubmitError("decode projection: %v", err)
	}
	if len(envelope.Parts) == 0 {
		return nil, poisonSubmitError("bridge returned no submit parts")
	}
	parts := make([]SubmitSMChainPart, 0, len(envelope.Parts))
	for _, wire := range envelope.Parts {
		body, tuples, err := buildSubmitPart(wire)
		if err != nil {
			return nil, err
		}
		parts = append(parts, SubmitSMChainPart{Body: body, CustomTLVs: tuples})
	}
	return parts, nil
}

// DecodeSubmitSM decodes a single-part submit (the first chain part), for the
// callers and tests that expect exactly one body.
func (b *Bridge) DecodeSubmitSM(ctx context.Context, data []byte) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	parts, err := b.DecodeSubmitSMChain(ctx, data)
	if err != nil {
		return smppwire.SubmitSMBody{}, nil, err
	}
	return parts[0].Body, parts[0].CustomTLVs, nil
}

// buildSubmitPart projects one bridge-decoded part into a wire body plus its
// custom-TLV tuples, enforcing the legacy optional-TLV allowlist.
func buildSubmitPart(wire submitSMWire) (smppwire.SubmitSMBody, []tlv.TLV, error) {
	body := smppwire.SubmitSMBody{
		ServiceType:           cloneBytes(wire.ServiceType),
		SourceAddressTON:      wire.SourceAddrTON,
		SourceAddressNPI:      wire.SourceAddrNPI,
		SourceAddress:         cloneBytes(wire.SourceAddr),
		DestinationAddressTON: wire.DestAddrTON,
		DestinationAddressNPI: wire.DestAddrNPI,
		DestinationAddress:    cloneBytes(wire.DestinationAddr),
		ESMClass:              wire.ESMClass,
		ProtocolID:            wire.ProtocolID,
		PriorityFlag:          wire.PriorityFlag,
		ScheduleDeliveryTime:  cloneBytes(wire.ScheduleDeliveryTime),
		ValidityPeriod:        cloneBytes(wire.ValidityPeriod),
		RegisteredDelivery:    wire.RegisteredDelivery,
		ReplaceIfPresentFlag:  wire.ReplaceIfPresentFlag,
		DataCoding:            wire.DataCoding,
		SMDefaultMessageID:    wire.SMDefaultMessageID,
		ShortMessage:          cloneBytes(wire.ShortMessage),
	}
	seen := make(map[uint16]struct{}, len(wire.OptionalTLVs))
	for _, option := range wire.OptionalTLVs {
		if _, duplicate := seen[option.Tag]; duplicate {
			return smppwire.SubmitSMBody{}, nil, fmt.Errorf("%w: duplicate TLV %#04x", ErrInvalidSubmitSM, option.Tag)
		}
		seen[option.Tag] = struct{}{}
		switch option.Tag {
		case 0x000d:
			if len(option.Value) != 1 || option.Value[0] > 4 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.SourceAddrSubunit = &value
		case 0x0005:
			if len(option.Value) != 1 || option.Value[0] > 4 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.DestAddrSubunit = &value
		case 0x0204:
			if len(option.Value) != 2 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := binary.BigEndian.Uint16(option.Value)
			body.Optional.UserMessageReference = &value
		case 0x020a:
			if len(option.Value) != 2 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := binary.BigEndian.Uint16(option.Value)
			body.Optional.SourcePort = &value
		case 0x020b:
			if len(option.Value) != 2 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := binary.BigEndian.Uint16(option.Value)
			body.Optional.DestinationPort = &value
		case 0x0202, 0x0203:
			if len(option.Value) < 2 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			typeTag := option.Value[0]
			switch typeTag {
			case 0, 0x80, 0x88, 0xa0:
			default:
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			subaddress := &smppwire.Subaddress{
				TypeTag: typeTag,
				Value:   cloneBytes(option.Value[1:]),
			}
			if option.Tag == 0x0202 {
				body.Optional.SourceSubaddress = subaddress
			} else {
				body.Optional.DestSubaddress = subaddress
			}
		case 0x0205:
			if len(option.Value) != 1 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.UserResponseCode = &value
		case 0x020c:
			if len(option.Value) != 2 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := binary.BigEndian.Uint16(option.Value)
			body.Optional.SARMessageReference = &value
		case 0x020e:
			if len(option.Value) != 1 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.SARTotalSegments = &value
		case 0x020f:
			if len(option.Value) != 1 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.SARSegmentSequence = &value
		case 0x0426:
			if len(option.Value) != 1 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.MoreMessagesToSend = &value
		case 0x0019:
			if len(option.Value) != 1 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.PayloadType = &value
		case 0x0424:
			body.Optional.MessagePayload = cloneBytes(option.Value)
		case 0x0201:
			if len(option.Value) != 1 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.PrivacyIndicator = &value
		case 0x020d:
			if len(option.Value) != 1 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.LanguageIndicator = &value
		case 0x1201:
			if len(option.Value) != 1 || option.Value[0] > 2 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.DisplayTime = &value
		case 0x1203:
			body.Optional.SMSSignal = cloneBytes(option.Value)
		case 0x0304:
			if len(option.Value) != 1 || option.Value[0] > 99 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.NumberOfMessages = &value
		case 0x0381:
			if len(option.Value) < 3 {
				return smppwire.SubmitSMBody{}, nil, invalidTLV(option.Tag, len(option.Value))
			}
			body.Optional.CallbackNum = &smppwire.CallbackNumber{
				DigitMode: option.Value[0],
				TON:       option.Value[1],
				NPI:       option.Value[2],
				Digits:    cloneBytes(option.Value[3:]),
			}
		default:
			return smppwire.SubmitSMBody{}, nil, fmt.Errorf("%w: bridge returned unallowlisted TLV %#04x", ErrInvalidSubmitSM, option.Tag)
		}
	}
	if err := validateSubmitSMBody(body); err != nil {
		return smppwire.SubmitSMBody{}, nil, err
	}
	tuples, err := decodeWireCustomTLVs(wire.CustomTLVs)
	if err != nil {
		return smppwire.SubmitSMBody{}, nil, err
	}
	return body, tuples, nil
}

func validateSubmitSMBody(body smppwire.SubmitSMBody) error {
	for _, field := range []struct {
		name string
		data []byte
		max  int
	}{
		{"service_type", body.ServiceType, 5},
		{"source_addr", body.SourceAddress, 20},
		{"destination_addr", body.DestinationAddress, 20},
		{"schedule_delivery_time", body.ScheduleDeliveryTime, 16},
		{"validity_period", body.ValidityPeriod, 16},
		{"short_message", body.ShortMessage, 255},
	} {
		if len(field.data) > field.max {
			return fmt.Errorf("%w: %s length %d exceeds %d", ErrInvalidSubmitSM, field.name, len(field.data), field.max)
		}
		if field.name != "short_message" {
			for _, value := range field.data {
				if value == 0 {
					return fmt.Errorf("%w: %s contains NUL", ErrInvalidSubmitSM, field.name)
				}
			}
		}
	}
	sar := body.Optional
	count := 0
	if sar.SARMessageReference != nil {
		count++
	}
	if sar.SARTotalSegments != nil {
		count++
	}
	if sar.SARSegmentSequence != nil {
		count++
	}
	if count != 0 && count != 3 {
		return fmt.Errorf("%w: incomplete SAR option set", ErrInvalidSubmitSM)
	}
	if count == 3 {
		total := *sar.SARTotalSegments
		sequence := *sar.SARSegmentSequence
		if total == 0 || sequence == 0 || sequence > total {
			return poisonSubmitError("invalid SAR total/sequence")
		}
	}
	return nil
}

func invalidTLV(tag uint16, size int) error {
	return fmt.Errorf("%w: invalid TLV %#04x length %d", ErrInvalidSubmitSM, tag, size)
}

func cloneBytes(value []byte) []byte { return append([]byte(nil), value...) }

// decodeWireCustomTLVs parses the bridge's [tag, length, type, value] entries
// into typed tuples. Structural violations are poison: every rejected shape is
// one the legacy listener or its wire encoder would crash on and reject —
// except falsy non-null type fields, which map to unresolved "" per the
// KNOWN_QUIRKS Q-017 decision.
func decodeWireCustomTLVs(entries []json.RawMessage) ([]tlv.TLV, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	tuples := make([]tlv.TLV, 0, len(entries))
	for index, entry := range entries {
		var fields []json.RawMessage
		if err := json.Unmarshal(entry, &fields); err != nil || len(fields) != 4 {
			return nil, poisonSubmitError("custom TLV %d is not a 4-element tuple", index)
		}
		tag, err := tupleTagField(fields[0])
		if err != nil {
			return nil, poisonSubmitError("custom TLV %d: %v", index, err)
		}
		length, err := tupleLengthField(fields[1])
		if err != nil {
			return nil, poisonSubmitError("custom TLV %d: %v", index, err)
		}
		typeName, err := tupleTypeName(fields[2])
		if err != nil {
			return nil, poisonSubmitError("custom TLV %d: %v", index, err)
		}
		value, err := tupleValueField(fields[3])
		if err != nil {
			return nil, poisonSubmitError("custom TLV %d: %v", index, err)
		}
		tuples = append(tuples, tlv.TLV{Tag: tag, Length: length, Type: typeName, Value: value})
	}
	return tuples, nil
}

func tupleNumber(raw json.RawMessage) (json.Number, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return "", false
	}
	return number, true
}

// tupleTagField requires an integral number: legacy tags reaching the wire
// encoder as strings or floats crash it, and the message is rejected.
func tupleTagField(raw json.RawMessage) (*big.Int, error) {
	number, ok := tupleNumber(raw)
	if !ok {
		return nil, fmt.Errorf("tag is not a number (legacy wire encoder rejects)")
	}
	tag, ok := new(big.Int).SetString(string(number), 10)
	if !ok {
		return nil, fmt.Errorf("tag %s is not an integer (legacy wire encoder rejects)", number)
	}
	return tag, nil
}

func tupleLengthField(raw json.RawMessage) (*int, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	number, ok := tupleNumber(raw)
	if !ok {
		return nil, fmt.Errorf("length hint is not a number or null (legacy length arithmetic rejects)")
	}
	value, err := number.Int64()
	if err != nil || int64(int(value)) != value {
		return nil, fmt.Errorf("length hint %s is not an integer (legacy length arithmetic rejects)", number)
	}
	length := int(value)
	return &length, nil
}

func tupleTypeName(raw json.RawMessage) (string, error) {
	if string(raw) == "null" {
		return "", nil
	}
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return name, nil
	}
	// Falsy non-null scalars map to unresolved "" (Q-017); everything else is
	// the legacy validation crash (`(type or '').strip()` on a non-string).
	switch string(raw) {
	case "false", "0", "0.0":
		return "", nil
	}
	return "", fmt.Errorf("type field %s is not a string (legacy validation rejects)", raw)
}

// tupleValueField carries scalar values with full precision: integral numbers
// as Python-int equivalents (int64, uint64, then canonical decimal string),
// non-integral as float64, the bridge bytes wrapper as []byte. Objects and
// arrays are poison — the legacy wire encoder crashes on them.
func tupleValueField(raw json.RawMessage) (any, error) {
	switch string(raw) {
	case "null":
		return nil, nil
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}
	if number, ok := tupleNumber(raw); ok {
		if integer, isInt := new(big.Int).SetString(string(number), 10); isInt {
			if integer.IsInt64() {
				return integer.Int64(), nil
			}
			if integer.IsUint64() {
				return integer.Uint64(), nil
			}
			return integer.String(), nil
		}
		if float, err := number.Float64(); err == nil {
			return float, nil
		}
	}
	var wrapped Bytes
	if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped != nil {
		return []byte(wrapped), nil
	}
	return nil, fmt.Errorf("value %s is not a supported scalar (legacy wire encoder rejects)", raw)
}

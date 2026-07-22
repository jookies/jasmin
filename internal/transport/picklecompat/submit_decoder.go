package picklecompat

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"

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
	DroppedUnknownTLVs   int                   `json:"dropped_unknown_tlvs"`
}

type submitSMOptionalTLV struct {
	Tag   uint16 `json:"tag"`
	Value Bytes  `json:"value"`
}

// DecodeSubmitSM invokes the bridge's restricted SubmitSM-only unpickler and
// projects the result into the canonical wire body. Unknown vendor TLVs are
// deliberately absent from the projection (KNOWN_QUIRKS Q-016).
func (b *Bridge) DecodeSubmitSM(ctx context.Context, data []byte) (smppwire.SubmitSMBody, error) {
	if b == nil {
		return smppwire.SubmitSMBody{}, transientSubmitError("nil bridge")
	}
	if err := ctx.Err(); err != nil {
		return smppwire.SubmitSMBody{}, err
	}
	if len(data) == 0 || len(data) > int(smppwire.DefaultMaxSize) {
		return smppwire.SubmitSMBody{}, poisonSubmitError("pickle size %d", len(data))
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	request := bridgeRequest{Action: "decode_submit_sm", Data: base64.StdEncoding.EncodeToString(data)}
	if err := json.NewEncoder(b.stdin).Encode(request); err != nil {
		return smppwire.SubmitSMBody{}, transientSubmitError("send bridge request: %v", err)
	}
	var response bridgeResponse
	if err := b.decodeResponse(ctx, &response); err != nil {
		return smppwire.SubmitSMBody{}, transientSubmitError("read bridge response: %v", err)
	}
	if response.Status != "ok" {
		return smppwire.SubmitSMBody{}, poisonSubmitError("%s", response.Message)
	}
	var wire submitSMWire
	if err := json.Unmarshal(response.Result, &wire); err != nil {
		return smppwire.SubmitSMBody{}, transientSubmitError("decode projection: %v", err)
	}
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
			return smppwire.SubmitSMBody{}, fmt.Errorf("%w: duplicate TLV %#04x", ErrInvalidSubmitSM, option.Tag)
		}
		seen[option.Tag] = struct{}{}
		switch option.Tag {
		case 0x020c:
			if len(option.Value) != 2 {
				return smppwire.SubmitSMBody{}, invalidTLV(option.Tag, len(option.Value))
			}
			value := binary.BigEndian.Uint16(option.Value)
			body.Optional.SARMessageReference = &value
		case 0x020e:
			if len(option.Value) != 1 {
				return smppwire.SubmitSMBody{}, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.SARTotalSegments = &value
		case 0x020f:
			if len(option.Value) != 1 {
				return smppwire.SubmitSMBody{}, invalidTLV(option.Tag, len(option.Value))
			}
			value := option.Value[0]
			body.Optional.SARSegmentSequence = &value
		case 0x0424:
			body.Optional.MessagePayload = cloneBytes(option.Value)
		default:
			return smppwire.SubmitSMBody{}, fmt.Errorf("%w: bridge returned unallowlisted TLV %#04x", ErrInvalidSubmitSM, option.Tag)
		}
	}
	if err := validateSubmitSMBody(body); err != nil {
		return smppwire.SubmitSMBody{}, err
	}
	return body, nil
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

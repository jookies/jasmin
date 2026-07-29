package outbound

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/pumpitspace/jasmin/internal/core"
	"github.com/pumpitspace/jasmin/internal/core/segmentation"
	"github.com/pumpitspace/jasmin/internal/core/tlv"
	"github.com/pumpitspace/jasmin/internal/transport/amqpcompat"
	"github.com/pumpitspace/jasmin/internal/transport/picklecompat"
	"github.com/pumpitspace/jasmin/internal/transport/smppwire"
)

var ErrInvalidSubmitEnvelope = errors.New("invalid production submit envelope")

type SubmitSMEncoder interface {
	EncodeSubmitSM(context.Context, picklecompat.SubmitSMEncodeRequest) (picklecompat.SubmitSMEncodeResult, error)
}

// SubmitEnvelopeBuilder converts the proven core orchestration request into
// the legacy protocol-2 SubmitSM body and exact AMQP properties expected by
// Python Jasmin consumers.
type SubmitEnvelopeBuilder struct {
	encoder SubmitSMEncoder
}

func NewSubmitEnvelopeBuilder(encoder SubmitSMEncoder) (*SubmitEnvelopeBuilder, error) {
	if encoder == nil {
		return nil, fmt.Errorf("%w: nil SubmitSM encoder", ErrInvalidSubmitEnvelope)
	}
	return &SubmitEnvelopeBuilder{encoder: encoder}, nil
}

func (builder *SubmitEnvelopeBuilder) BuildSubmitEnvelope(
	ctx context.Context,
	request core.SubmitEnvelopeRequest,
	part segmentation.Part,
) (amqpcompat.Envelope, error) {
	if request.MessageID == "" || request.BillID == "" || request.UserID == "" ||
		request.Username == "" || request.ConnectorID == "" || len(request.DestinationAddr) == 0 ||
		request.CreatedAt.IsZero() {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: missing identity or destination", ErrInvalidSubmitEnvelope)
	}
	if len(request.Parts) == 0 || part.Sequence() == 0 || int(part.Sequence()) > len(request.Parts) {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: invalid part sequence %d", ErrInvalidSubmitEnvelope, part.Sequence())
	}

	encodeRequest := picklecompat.SubmitSMEncodeRequest{
		Sequence:               int(part.Sequence()),
		SourceAddr:             picklecompat.Bytes(request.SourceAddr),
		DestinationAddr:        picklecompat.Bytes(request.DestinationAddr),
		ShortMessage:           picklecompat.Bytes(part.ShortMessage()),
		DataCoding:             request.DataCoding,
		Priority:               request.Priority,
		RegisteredDelivery:     request.DLR && int(part.Sequence()) == len(request.Parts),
		IncludeBill:            true,
		BillID:                 request.BillID,
		UserID:                 request.UserID,
		Username:               request.Username,
		SubmitSMAmount:         request.Bill.SubmitSmAmount,
		SubmitSMRespAmount:     request.Bill.SubmitSmRespAmount,
		DecrementSubmitSMCount: request.Bill.DecrementSubmitSmCount,
		SourceAddrTON:          request.SourceAddrTON,
		SourceAddrNPI:          request.SourceAddrNPI,
		DestAddrTON:            request.DestAddrTON,
		DestAddrNPI:            request.DestAddrNPI,
		ServiceType:            request.ServiceType,
		ProtocolID:             request.ProtocolID,
		ReplaceIfPresentFlag:   request.ReplaceIfPresentFlag,
		SmDefaultMsgID:         request.SmDefaultMsgID,
	}
	rawBody := request.SMPPSubmit
	if len(request.SMPPSubmits) > 0 {
		index := int(part.Sequence()) - 1
		if index < 0 || index >= len(request.SMPPSubmits) {
			return amqpcompat.Envelope{}, fmt.Errorf("%w: missing raw submit_sm chain part %d", ErrInvalidSubmitEnvelope, part.Sequence())
		}
		rawBody = request.SMPPSubmits[index]
	}
	if rawBody != nil {
		encodeRequest.RawPDU = rawSubmitSM(rawBody)
		// Routing/interception may deliberately rewrite these three fields; all
		// other raw PDU values remain exactly what the ESME supplied.
		encodeRequest.RawPDU.SourceAddr = picklecompat.Bytes(request.SourceAddr)
		encodeRequest.RawPDU.DestinationAddr = picklecompat.Bytes(request.DestinationAddr)
		encodeRequest.RawPDU.ShortMessage = picklecompat.Bytes(part.ShortMessage())
	}
	if request.ScheduleAt != nil {
		encodeRequest.ScheduleAt = request.ScheduleAt.Format(time.RFC3339Nano)
	}
	if request.ValidityPeriod != nil {
		if *request.ValidityPeriod < 0 {
			return amqpcompat.Envelope{}, fmt.Errorf("%w: negative validity period", ErrInvalidSubmitEnvelope)
		}
		encodeRequest.ValidityUntil = request.CreatedAt.Add(*request.ValidityPeriod).Format(time.RFC3339Nano)
	}
	if sar, ok := part.SAR(); ok {
		encodeRequest.SAR = &picklecompat.SubmitSMSAR{
			Reference: uint16(sar.Reference),
			Total:     sar.Total,
			Sequence:  sar.Sequence,
		}
	}
	if _, _, ok := part.UDH(); ok {
		encodeRequest.UDH = true
	}
	encodeRequest.CustomTLVs = tupleTLVs(part.CustomTLVs())

	encoded, err := builder.encoder.EncodeSubmitSM(ctx, encodeRequest)
	if err != nil {
		return amqpcompat.Envelope{}, fmt.Errorf("encode legacy SubmitSM: %w", err)
	}
	if len(encoded.Body) == 0 || len(encoded.Bill) == 0 {
		return amqpcompat.Envelope{}, fmt.Errorf("%w: encoder returned empty body or bill", ErrInvalidSubmitEnvelope)
	}

	headers := map[string]amqpcompat.Field{
		"created_at":           amqpcompat.StringField(legacyDateTime(request.CreatedAt)),
		"source_connector":     amqpcompat.StringField(sourceConnector(request.SourceConnector)),
		"submit_sm_bill":       amqpcompat.BytesField(encoded.Bill),
		"aggregate-message-id": amqpcompat.StringField(request.MessageID),
		"part-number":          amqpcompat.IntegerField(int64(part.Sequence())),
		"part-count":           amqpcompat.IntegerField(int64(len(request.Parts))),
		// Durable response metadata mirrors fields already present in the
		// allowlisted bill pickle, avoiding unsafe bill unpickling in Session.
		"user-id":          amqpcompat.StringField(request.UserID),
		"bill-id":          amqpcompat.StringField(request.BillID),
		"late-bill-amount": amqpcompat.StringField(pythonFloatString(request.Bill.SubmitSmRespAmount)),
	}
	if request.Expiration != "" {
		headers["expiration"] = amqpcompat.StringField(request.Expiration)
	} else if request.ValidityPeriod != nil {
		headers["expiration"] = amqpcompat.StringField(legacyDateTime(request.CreatedAt.Add(*request.ValidityPeriod)))
	}
	messageID := request.MessageID
	if len(request.Parts) > 1 {
		messageID = fmt.Sprintf("%s/%06d", request.MessageID, part.Sequence())
	}
	properties, err := amqpcompat.NewProperties(
		messageID,
		headers,
		amqpcompat.WithReplyTo("submit.sm.resp."+request.UserID),
		amqpcompat.WithPriority(request.Priority),
	)
	if err != nil {
		return amqpcompat.Envelope{}, err
	}
	return amqpcompat.NewEnvelope("submit.sm."+request.ConnectorID, properties, encoded.Body)
}

// pythonFloatString mirrors Python 3's str(float) formatting contour used by
// the legacy AMQP producer: shortest round-trippable digits, fixed notation
// for decimal exponents [-4, 15], and a fractional suffix for integral values.
func pythonFloatString(value float64) string {
	abs := math.Abs(value)
	format := byte('f')
	if abs != 0 && (abs < 1e-4 || abs >= 1e16) {
		format = 'g'
	}
	text := strconv.FormatFloat(value, format, -1, 64)
	if format == 'f' && !strings.Contains(text, ".") {
		text += ".0"
	}
	return text
}

// tupleTLVs projects the normalized tuples into the bridge's Python-tuple
// shape, preserving caller order — the legacy listener resolves, validates,
// and encodes them at submit time, so order here is wire order.
func tupleTLVs(values []tlv.TLV) []picklecompat.SubmitSMCustomTLV {
	if len(values) == 0 {
		return nil
	}
	result := make([]picklecompat.SubmitSMCustomTLV, 0, len(values))
	for _, value := range values {
		result = append(result, picklecompat.SubmitSMCustomTLV{
			Tag:    value.Tag,
			Length: value.Length,
			Type:   value.Type,
			Value:  value.Value,
		})
	}
	return result
}

func legacyDateTime(value time.Time) string {
	value = value.Round(0)
	if value.Nanosecond() == 0 {
		return value.Format("2006-01-02 15:04:05")
	}
	return value.Format("2006-01-02 15:04:05.000000")
}

func sourceConnector(value string) string {
	if value == "smppsapi" {
		return value
	}
	return "httpapi"
}

func rawSubmitSM(body *smppwire.SubmitSMBody) *picklecompat.SubmitSMRawPDU {
	if body == nil {
		return nil
	}
	raw := &picklecompat.SubmitSMRawPDU{
		ServiceType:          picklecompat.Bytes(body.ServiceType),
		SourceAddrTON:        body.SourceAddressTON,
		SourceAddrNPI:        body.SourceAddressNPI,
		SourceAddr:           picklecompat.Bytes(body.SourceAddress),
		DestAddrTON:          body.DestinationAddressTON,
		DestAddrNPI:          body.DestinationAddressNPI,
		DestinationAddr:      picklecompat.Bytes(body.DestinationAddress),
		ESMClass:             body.ESMClass,
		ProtocolID:           body.ProtocolID,
		PriorityFlag:         body.PriorityFlag,
		ScheduleDeliveryTime: picklecompat.Bytes(body.ScheduleDeliveryTime),
		ValidityPeriod:       picklecompat.Bytes(body.ValidityPeriod),
		RegisteredDelivery:   body.RegisteredDelivery,
		ReplaceIfPresentFlag: body.ReplaceIfPresentFlag,
		DataCoding:           body.DataCoding,
		SMDefaultMessageID:   body.SMDefaultMessageID,
		ShortMessage:         picklecompat.Bytes(body.ShortMessage),
		Optional: picklecompat.SubmitSMRawOptionalParameters{
			SARMessageReference:  body.Optional.SARMessageReference,
			SARTotalSegments:     body.Optional.SARTotalSegments,
			SARSegmentSequence:   body.Optional.SARSegmentSequence,
			MoreMessagesToSend:   body.Optional.MoreMessagesToSend,
			MessagePayload:       picklecompat.Bytes(body.Optional.MessagePayload),
			UserMessageReference: body.Optional.UserMessageReference,
			SourcePort:           body.Optional.SourcePort,
			DestinationPort:      body.Optional.DestinationPort,
			SourceAddrSubunit:    body.Optional.SourceAddrSubunit,
			DestAddrSubunit:      body.Optional.DestAddrSubunit,
			UserResponseCode:     body.Optional.UserResponseCode,
			PayloadType:          body.Optional.PayloadType,
			PrivacyIndicator:     body.Optional.PrivacyIndicator,
			LanguageIndicator:    body.Optional.LanguageIndicator,
			DisplayTime:          body.Optional.DisplayTime,
			SMSSignal:            picklecompat.Bytes(body.Optional.SMSSignal),
			NumberOfMessages:     body.Optional.NumberOfMessages,
		},
	}
	if body.Optional.SourceSubaddress != nil {
		raw.Optional.SourceSubaddress = &picklecompat.SubmitSMRawSubaddress{
			TypeTag: body.Optional.SourceSubaddress.TypeTag,
			Value:   picklecompat.Bytes(body.Optional.SourceSubaddress.Value),
		}
	}
	if body.Optional.DestSubaddress != nil {
		raw.Optional.DestSubaddress = &picklecompat.SubmitSMRawSubaddress{
			TypeTag: body.Optional.DestSubaddress.TypeTag,
			Value:   picklecompat.Bytes(body.Optional.DestSubaddress.Value),
		}
	}
	if body.Optional.CallbackNum != nil {
		raw.Optional.CallbackNum = &picklecompat.SubmitSMRawCallbackNumber{
			DigitMode: body.Optional.CallbackNum.DigitMode,
			TON:       body.Optional.CallbackNum.TON,
			NPI:       body.Optional.CallbackNum.NPI,
			Digits:    picklecompat.Bytes(body.Optional.CallbackNum.Digits),
		}
	}
	return raw
}

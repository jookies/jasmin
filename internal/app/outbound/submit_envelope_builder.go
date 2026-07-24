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
	encodeRequest.CustomTLVs = tupleTLVs(request.CustomTLVs)

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
	if request.ValidityPeriod != nil {
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
